package guide

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// BindingsPerOperator is K: the most field bindings bound mode drafts
// for one operator. Hints pin roles; the rest take schema order.
const BindingsPerOperator = 3

// listFieldCap is the most fields a list slot (regression predictors,
// matrix fields) takes from the schema beyond its hinted fields.
const listFieldCap = 5

// dfsBudget bounds the binding search per operator, so a wide schema
// whose slots cannot all be filled stays cheap.
const dfsBudget = 10000

// Bound is what cohort-bound mode knows about the cohort: its schema
// (header and schema block only) and Predict, which validates one
// draft against that cohort without executing it. Predict never reads
// a record; it is the caller's in-process predict, carrying the
// instance's options and the cohort's sidecar facts.
type Bound struct {
	Cohort  *types.Cohort
	Schema  *encoding.Schema
	Predict func(*types.Request) *descriptor.Envelope
	// Weight is the cohort's suggested row weight — the field Inspect
	// reports as suggested_weight (descx.SuggestWeight over the SPSS
	// sidecar's weight variable) — or "" when none. A weight-only
	// operator (AGG_WEIGHTED_MEAN) binds its weight to it; without one
	// it drafts with the weight as a need.
	Weight string
}

// slotLoc says where a bound value sits in the slot entry.
type slotLoc int

const (
	locTop     slotLoc = iota // entry[key] = value
	locOrderBy                // entry[key] = [{"field": value}]
	locParams                 // entry.params[key] = value
)

// bslot is one field-valued slot of a draft.
type bslot struct {
	key    string
	loc    slotLoc
	list   bool
	min    int
	accept func(*encoding.Field) bool
	// prefer orders candidates (lower first) before schema order; nil
	// keeps schema order.
	prefer func(*encoding.Field) int
	// narrow, when set, is the slot's preferred tier: if any candidate
	// passes it, bind drops the candidates that do not, except a hinted
	// one (a preference, never a refusal). nil keeps every candidate.
	narrow func(*encoding.Field) bool
	// vector marks a member-list slot that also takes ONE native vector
	// field (vec_f32 / vec_f64) by name, drafted as "vector": "<field>"
	// in place of the "fields" list (the slot's Vector / Fields pair).
	// accept judges the field's element type; min is its least dim.
	vector bool
	// kindOK, when set, admits a native vector by its schema kind
	// ("" = none declared); nil admits every kind.
	kindOK func(types.VectorKind) bool
}

// bneed is one value only the caller can choose.
type bneed struct {
	key  string
	loc  slotLoc
	list bool
	// alternatives are the other members of key's exactly_one_of
	// group: the caller may give one of them instead.
	alternatives []string
	// optional marks a need the request can run without when the
	// instance supplies it (a default weight): a probe predict accepts
	// with no error drops it.
	optional bool
}

// itemBattery admits the native vector kinds an item-battery operator
// suits: items answered on one scale (kind scale), yes/no items (kind
// binary), or a vector declaring no kind. A measure (magnitudes) or a
// composition (shares of a whole) is not a battery of items, so the
// binder never drafts one into a reliability check or a scale score.
func itemBattery(k types.VectorKind) bool {
	return k == "" || k == types.VectorKindScale || k == types.VectorKindBinary
}

// batteryKinds maps an operator to the native vector kinds its vector
// slot admits; an operator absent here admits every kind (the metric
// operators judge a kind through its metric default instead:
// nativeNeeds).
var batteryKinds = map[string]func(types.VectorKind) bool{
	string(types.MAT_RELIABILITY):  itemBattery,
	string(types.ATTR_SCALE_SCORE): itemBattery,
}

// nativeNeeds drops the needs a native vector binding answers from its
// schema: a comparison attribute's metric, when the vector's declared
// kind has a default for that attribute (vectors.KindDefault — never a
// poor fit), is the kind's to supply, as predict and the runtime
// resolve it. A kind-less native still needs the metric.
func nativeNeeds(op opInfo, needs []bneed, b binding, schema *encoding.Schema) []bneed {
	if op.category != catAttribute || !vectors.IsComparisonAttribute(types.AttributeType(op.name)) {
		return needs
	}
	c, f, _ := vectors.ComparisonConsumer(types.AttributeType(op.name))
	for i, isVec := range b.vector {
		if !isVec {
			continue
		}
		if _, ok := vectors.KindDefault(c, vectors.KindIn(nil, schema, b.fields[i][0]), f, nil); ok {
			return slices.DeleteFunc(slices.Clone(needs), func(n bneed) bool { return n.key == "metric" })
		}
	}
	return needs
}

// fieldNameParams are required params whose manifest type is a plain
// string but whose value names a schema field.
var fieldNameParams = map[string]map[string]bool{
	"AGG_DISTINCT_SUM": {"distinct_by": true},
}

// weightParams are the operators that compute nothing without a row
// weight, and the param that names one: a declared-optional param (the
// slot, request or instance weight may supply it instead), so bound
// mode binds it to the cohort's suggested weight (Bound.Weight) or else
// asks for it — dropping the ask when the probe shows a weight already
// resolves (an instance default weight).
var weightParams = map[string]string{
	string(types.AGG_WEIGHTED_MEAN): "weight_field",
}

// oneSampleNeeds are optional params a draft must still ask for: the
// one-sample TEST_T compares against mu, whose default of 0 is rarely
// the reference the question means.
var oneSampleNeeds = map[[2]string][]string{
	{"TEST_T", descx.IntentBenchmark}:            {"mu"},
	{"TEST_HOTELLING_T2", descx.IntentBenchmark}: {"mu"},
}

// needWhy says what to supply for a param only the caller can choose.
// Each sentence passes LintGuidanceText (TestRecommend_NeedsWhyLint).
var needWhy = map[string]string{
	"success":      "Name the outcome value that counts as a success.",
	"mu":           "Give the reference value the mean is compared with.",
	"family":       "Choose the model family that suits the outcome.",
	"percentile":   "Choose which percentile to report, between 0 and 100.",
	"ms_within":    "Give the within-group mean square from an ANOVA on the same groups.",
	"df_within":    "Give the within-group degrees of freedom from that same ANOVA.",
	"values":       "List the values the filter keeps, drops or matches.",
	"expression":   "Write the expression that picks the rows to keep.",
	"ranges":       "List the labelled ranges to use, each with a label, a start and an end.",
	"interval":     "Choose the bucket width.",
	"value":        "Name the value whose share to count.",
	"part":         "Choose the date part to extract.",
	"alpha":        "Choose the smoothing weight, between 0 and 1.",
	"degree":       "Choose the polynomial degree.",
	"codes":        "List the codes to look for.",
	"component":    "Choose which component to score, counting from 1 for the largest.",
	"factor":       "Choose which factor to score, counting from 1 for the one that accounts for the most variance.",
	"label":        "Name the option to look for.",
	"ratios":       "Give the split ratios.",
	"boundaries":   "List the cutpoints that split the values into buckets.",
	"frame":        "Choose the window frame: which rows around each row it covers.",
	"weight_field": "Name the field that holds each row's weight.",
}

// needWhyDefault covers a required param with no needWhy entry, such as
// an extension operator's.
const needWhyDefault = "Choose this value yourself; the schema cannot supply it."

func whyNeed(n bneed) string {
	w, ok := needWhy[n.key]
	if !ok {
		w = needWhyDefault
	}
	if len(n.alternatives) > 0 {
		w += " Or give " + strings.Join(n.alternatives, " or ") + " instead; give only one."
	}
	return w
}

// kindOf is the coarse kind of f; ok is false for an unknown type.
func kindOf(f *encoding.Field) descriptor.FieldKind {
	k, _ := descx.FieldKindOf(f.Type)
	return k
}

func kindIs(kinds ...descriptor.FieldKind) func(*encoding.Field) bool {
	return func(f *encoding.Field) bool { return slices.Contains(kinds, kindOf(f)) }
}

// typeIn accepts a field whose type name is in names; an empty list
// accepts every field (the operator declared none).
func typeIn(names []string) func(*encoding.Field) bool {
	return func(f *encoding.Field) bool {
		return len(names) == 0 || slices.Contains(names, f.Type.String())
	}
}

func both(a, b func(*encoding.Field) bool) func(*encoding.Field) bool {
	return func(f *encoding.Field) bool { return a(f) && b(f) }
}

// categoricalField is the level set, the acceptance table's
// categoricalValue class: a categorical (its dictionary labels are the
// levels) or a packed_bool (levels "0" / "1", fieldaccept.Levels). Every
// level slot the binder offers reads it, as the runtime does through
// Record.LevelValue.
func categoricalField(f *encoding.Field) bool {
	return f.Type.IsCategorical() || fieldaccept.Levels(f) != nil
}

// levelSlot is the predicate for a test's level slot: the acceptance
// table's own row when op's slot is tabled (TEST_ANOVA_RM's subject is a
// categorical alone there), else the level set (an extension test).
func levelSlot(op opInfo, slot fieldaccept.Slot) func(*encoding.Field) bool {
	r, ok := fieldaccept.Lookup(op.name, slot)
	if !ok {
		return categoricalField
	}
	return func(f *encoding.Field) bool { return !f.Type.IsVector() && r.Accepts(f.Type) }
}

// preferDate orders date fields before the rest.
func preferDate(f *encoding.Field) int {
	if kindOf(f) == descriptor.FieldKindDate {
		return 0
	}
	return 1
}

// intentKinds is every field kind some role of the intent takes.
func intentKinds(in descriptor.Intent) []descriptor.FieldKind {
	var out []descriptor.FieldKind
	for _, s := range in.Shapes {
		for _, r := range s.Roles {
			for _, k := range r.Kinds {
				if !slices.Contains(out, k) {
					out = append(out, k)
				}
			}
		}
	}
	return out
}

// testSlot is the field slot a test's required Go field maps to: the
// key, where it sits and which fields fit it. Predict refuses the
// rest (a categorical field, a numeric split), so the kinds here are
// predict's own rules.
func testSlot(op opInfo, key string) bslot {
	numeric := kindIs(descriptor.FieldKindNumeric)
	if len(op.accepts) > 0 {
		numeric = typeIn(op.accepts)
	}
	switch key {
	case "field":
		if op.name == string(types.TEST_PROP_Z) {
			return bslot{key: key, accept: levelSlot(op, fieldaccept.SlotField)}
		}
		return bslot{key: key, accept: numeric}
	case "field2":
		return bslot{key: key, accept: numeric}
	case "order_by":
		return bslot{key: key, loc: locOrderBy, accept: kindIs(descriptor.FieldKindDate, descriptor.FieldKindNumeric), prefer: preferDate}
	case "split_by":
		if op.name == string(types.TEST_HOTELLING_T2) {
			// The two-sample form refuses a split without exactly two
			// levels at run time (PULSE_TEST_SPLIT_GROUPS_LT_2), which
			// predict cannot see: the dictionary is the schema's word.
			return bslot{key: key, accept: both(levelSlot(op, fieldaccept.SlotSplitBy), twoLevels)}
		}
		if manyGroupTests[op.name] {
			accept := levelSlot(op, fieldaccept.SlotSplitBy)
			if op.name == string(types.TEST_TUKEY_HSD) {
				// Tukey HSD refuses fewer than three groups at run time
				// (PULSE_TEST_SPLIT_GROUPS_LT_2), and a field's declared
				// levels bound its observed groups from above.
				accept = both(accept, threeLevels)
			}
			return bslot{key: key, accept: accept, narrow: threeLevels}
		}
		return bslot{key: key, accept: levelSlot(op, fieldaccept.SlotSplitBy)}
	default: // subject_field, rows, cols
		return bslot{key: key, accept: levelSlot(op, fieldaccept.Slot(key))}
	}
}

// twoLevels accepts a level-set field with exactly two declared levels:
// a categorical whose dictionary holds two values, or a packed_bool.
func twoLevels(f *encoding.Field) bool {
	return len(fieldaccept.Levels(f)) == 2
}

// manyGroupTests are the k-group tests whose design is three or more
// groups (their Purpose: an ANOVA / Kruskal-Wallis / Brown-Forsythe /
// Tukey HSD across groups, with TEST_T / TEST_WELCH the two-group
// form). No registry declares a group minimum, so this table is the
// binder's word: their split_by prefers a field with at least three
// declared levels (bslot.narrow) and falls back to a two-level one —
// a categorical or a packed_bool — only when the cohort has none or a
// hint names it.
var manyGroupTests = map[string]bool{
	string(types.TEST_ANOVA_F):        true,
	string(types.TEST_ANOVA_WELCH):    true,
	string(types.TEST_KRUSKAL_WALLIS): true,
	string(types.TEST_BROWN_FORSYTHE): true,
	string(types.TEST_TUKEY_HSD):      true,
}

// threeLevels accepts a level-set field with at least three declared
// levels.
func threeLevels(f *encoding.Field) bool {
	return len(fieldaccept.Levels(f)) >= 3
}

// fieldFilterAccept turns a manifest field_filter into a predicate.
func fieldFilterAccept(filter string) func(*encoding.Field) bool {
	switch filter {
	case "numeric":
		return kindIs(descriptor.FieldKindNumeric)
	case "categorical":
		return categoricalField
	}
	return func(*encoding.Field) bool { return true }
}

// plan lists op's field slots and the values only the caller can
// choose, under intent in. ok is false when a required slot has no
// mapping (the unbound skeleton skips the same operators).
func plan(op opInfo, in descriptor.Intent) (slots []bslot, needs []bneed, ok bool) {
	fieldAccept := both(typeIn(op.accepts), kindIs(intentKinds(in)...))
	switch op.category {
	case catTest, catPostTest:
		req := append([]string(nil), op.requires...)
		req = append(req, optionalSlots[[2]string{op.name, in.ID}]...)
		for _, goName := range req {
			k, known := requireKey[goName]
			if !known {
				return nil, nil, false
			}
			slots = append(slots, testSlot(op, k))
		}
		// A vector-input test reads its p members from an inline fields
		// list (vectors.IsVectorTest), the matrix slot's shape.
		vector := vectors.IsVectorTest(types.TestType(op.name))
		if vector {
			slots = append([]bslot{{key: "fields", list: true, min: 2, vector: true, accept: kindIs(descriptor.FieldKindNumeric)}}, slots...)
		}
		for _, n := range oneSampleNeeds[[2]string{op.name, in.ID}] {
			needs = append(needs, bneed{key: n, loc: locParams, list: vector})
		}
	case catRegression:
		// target / predictors are declared params (below).
	case catMatrix:
		slots = append(slots, bslot{key: "fields", list: true, min: 2, vector: true, accept: kindIs(descriptor.FieldKindNumeric), kindOK: batteryKinds[op.name]})
	case catFilterer:
		shape, known := filtererShape[op.name]
		if !shape.noField {
			slots = append(slots, bslot{key: "field", accept: fieldAccept})
		}
		if known && shape.key != "" {
			loc := locTop
			if shape.inParam {
				loc = locParams
			}
			needs = append(needs, bneed{key: shape.key, loc: loc, list: listKeys[shape.key] || shape.inParam})
		}
	case catWindow:
		if !op.ignoresField && !descx.WindowFieldForbidden(types.WindowType(op.name)) {
			slots = append(slots, bslot{key: "field", accept: fieldAccept})
		}
		slots = append(slots, bslot{key: "order_by", loc: locOrderBy, accept: kindIs(descriptor.FieldKindDate, descriptor.FieldKindNumeric), prefer: preferDate})
		if descx.WindowFrameRequired(types.WindowType(op.name)) {
			needs = append(needs, bneed{key: "frame", loc: locTop})
		}
	default: // aggregator, attribute, grouper, feature
		// A fitted vector attribute reads its p members from an inline
		// fields list (vectors.IsVectorAttribute), like a vector test.
		if (op.category == catAttribute && vectors.IsVectorAttribute(types.AttributeType(op.name))) ||
			(op.category == catGrouper && vectors.IsVectorGroup(types.GroupType(op.name))) {
			slots = append(slots, bslot{key: "fields", list: true, min: 2, vector: true, accept: kindIs(descriptor.FieldKindNumeric), kindOK: batteryKinds[op.name]})
			break
		}
		// A vector aggregator (built-in, or an extension declared
		// VectorInput: opInfo.vectorInput) reads its members from an
		// inline fields list, never field; each member must be a type
		// the operator accepts.
		if op.vectorInput {
			slots = append(slots, bslot{key: "fields", list: true, min: 2, vector: true, accept: both(typeIn(op.accepts), kindIs(descriptor.FieldKindNumeric))})
			break
		}
		// A field-ignoring aggregator (AGG_RATIO) still names a field on
		// the wire: boundDraft fills it with its first bound param field.
		// A regression attribute reads target / predictors (below).
		if !op.ignoresField && !regressionShaped(op) {
			slots = append(slots, bslot{key: "field", accept: fieldAccept})
		}
	}
	for _, p := range op.params {
		loc := locParams
		if topLevelParam(op, p.name) {
			loc = locTop
		}
		switch {
		case p.typ == "field":
			accept := fieldFilterAccept(p.fieldFilter)
			if regressionShaped(op) {
				accept = both(accept, typeIn(op.accepts))
			}
			slots = append(slots, bslot{key: p.name, loc: loc, accept: accept})
		case regressionShaped(op) && p.name == "predictors":
			slots = append(slots, bslot{key: p.name, loc: loc, list: true, min: 1, accept: typeIn(op.accepts)})
		case fieldNameParams[op.name][p.name]:
			slots = append(slots, bslot{key: p.name, loc: loc, accept: func(*encoding.Field) bool { return true },
				prefer: func(f *encoding.Field) int {
					if categoricalField(f) {
						return 0
					}
					return 1
				}})
		default:
			needs = append(needs, bneed{key: p.name, loc: loc, list: p.list, alternatives: p.alternatives})
		}
	}
	return slots, needs, true
}

// binding is one assignment of fields to an operator's slots.
type binding struct {
	fields [][]string // per slot
	// vector marks a vector slot bound to ONE native vector field
	// (fields[i] is that field's name), drafted as "vector".
	vector []bool
	hints  int // hinted fields it uses
}

// elemType is the scalar type of a native vector field's elements.
func elemType(ft encoding.FieldType) encoding.FieldType {
	if ft == encoding.FieldTypeVecF32 {
		return encoding.FieldTypeF32
	}
	return encoding.FieldTypeF64
}

// fieldIdx is the logical index of field name in schema, -1 if absent.
func fieldIdx(schema *encoding.Schema, name string) int {
	return slices.IndexFunc(schema.Fields, func(f encoding.Field) bool { return f.Name == name })
}

// nativeFits reports whether native vector field f (logical index i of
// schema) fills vector slot s: its elements are a type s accepts and
// it has at least s.min of them.
func nativeFits(s bslot, schema *encoding.Schema, i int) bool {
	f := &schema.Fields[i]
	if !s.vector || !f.Type.IsVector() || schema.VectorDim(i) < max(s.min, 1) {
		return false
	}
	if s.kindOK != nil && !s.kindOK(vectors.KindIn(nil, schema, f.Name)) {
		return false
	}
	return s.accept(&encoding.Field{Name: f.Name, Type: elemType(f.Type)})
}

// bind finds up to k bindings of slots over schema. Each hint pins the
// first slot that takes it (a list slot takes every hint that fits);
// the other slots take schema fields in order (a slot's narrow tier,
// when any candidate is in it, keeps only that tier and the hints).
// With hints, an operator no hint fits gets no binding.
//
// A native vector field (vec_f32 / vec_f64) binds only a vector slot
// (bslot.vector), whole and by name; every other slot reads ONE value
// and refuses it (PULSE_VECTOR_FIELD_NOT_SCALAR) — judged here, not
// left to the predict probe, whose needs match is a substring test the
// refusal's prose could satisfy. A vector slot tries each fitting
// native field (schema order) BEFORE a list of numeric member columns:
// the cohort's own vector, with its schema kind, scale and labels, is
// the better draft. A hinted native pins the slot to itself alone.
func bind(slots []bslot, schema *encoding.Schema, hints []string, k int) []binding {
	cands := make([][]*encoding.Field, len(slots))
	natives := make([][]string, len(slots))
	for i, s := range slots {
		for j := range schema.Fields {
			f := &schema.Fields[j]
			if f.Type.IsVector() {
				if nativeFits(s, schema, j) {
					natives[i] = append(natives[i], f.Name)
				}
				continue
			}
			if s.accept(f) {
				cands[i] = append(cands[i], f)
			}
		}
		if s.narrow != nil && slices.ContainsFunc(cands[i], s.narrow) {
			cands[i] = slices.DeleteFunc(cands[i], func(f *encoding.Field) bool {
				return !s.narrow(f) && !slices.Contains(hints, f.Name)
			})
		}
		if s.prefer != nil {
			slices.SortStableFunc(cands[i], func(a, b *encoding.Field) int { return s.prefer(a) - s.prefer(b) })
		}
	}
	pinned := make([][]string, len(slots))
	pinnedNative := make([]string, len(slots))
	used := 0
	for _, h := range hints {
		for i, s := range slots {
			if pinnedNative[i] != "" {
				continue
			}
			if slices.Contains(natives[i], h) {
				if len(pinned[i]) > 0 {
					continue
				}
				pinnedNative[i] = h
				used++
				break
			}
			if (!s.list && len(pinned[i]) > 0) || !slices.ContainsFunc(cands[i], func(f *encoding.Field) bool { return f.Name == h }) {
				continue
			}
			pinned[i] = append(pinned[i], h)
			used++
			break
		}
	}
	if len(hints) > 0 && used == 0 {
		return nil
	}
	taken := map[string]bool{}
	for _, h := range hints {
		taken[h] = true // a hint never fills a slot it was not pinned to
	}

	// choices[i] is vector slot i's native options in draft order: the
	// pinned native alone, else each fitting native unhinted then ""
	// (the member list). A non-vector slot has the one choice "".
	choices := make([][]string, len(slots))
	for i := range slots {
		switch {
		case pinnedNative[i] != "":
			choices[i] = []string{pinnedNative[i]}
		case len(pinned[i]) > 0:
			choices[i] = []string{""}
		default:
			for _, n := range natives[i] {
				if !taken[n] {
					choices[i] = append(choices[i], n)
				}
			}
			choices[i] = append(choices[i], "")
		}
	}

	var out []binding
	cur := make([]string, len(slots))
	nat := make([]string, len(slots))
	budget := dfsBudget
	var dfs func(i int)
	dfs = func(i int) {
		if len(out) >= k || budget <= 0 {
			return
		}
		budget--
		if i == len(slots) {
			if b, ok := fillLists(slots, cands, pinned, nat, cur, taken); ok {
				b.hints = used
				out = append(out, b)
			}
			return
		}
		if slots[i].list {
			dfs(i + 1)
			return
		}
		if len(pinned[i]) > 0 {
			cur[i] = pinned[i][0]
			dfs(i + 1)
			return
		}
		for _, f := range cands[i] {
			if taken[f.Name] {
				continue
			}
			taken[f.Name] = true
			cur[i] = f.Name
			dfs(i + 1)
			delete(taken, f.Name)
			if len(out) >= k || budget <= 0 {
				return
			}
		}
	}
	// pick walks every vector slot's native choices (natives first),
	// then runs the scalar search under that choice.
	var pick func(i int)
	pick = func(i int) {
		if len(out) >= k || budget <= 0 {
			return
		}
		if i == len(slots) {
			dfs(0)
			return
		}
		for _, n := range choices[i] {
			nat[i] = n
			pick(i + 1)
			if len(out) >= k || budget <= 0 {
				return
			}
		}
		nat[i] = ""
	}
	pick(0)
	return out
}

// fillLists completes one scalar assignment: a vector slot bound to a
// native field (nat) takes that field alone; each other list slot takes
// its pinned hints, then free candidates up to listFieldCap.
func fillLists(slots []bslot, cands [][]*encoding.Field, pinned [][]string, nat, cur []string, taken map[string]bool) (binding, bool) {
	b := binding{fields: make([][]string, len(slots)), vector: make([]bool, len(slots))}
	local := map[string]bool{}
	for i, s := range slots {
		if !s.list {
			b.fields[i] = []string{cur[i]}
		}
	}
	for i, s := range slots {
		if !s.list {
			continue
		}
		if nat[i] != "" {
			b.fields[i] = []string{nat[i]}
			b.vector[i] = true
			continue
		}
		vals := append([]string(nil), pinned[i]...)
		for _, f := range cands[i] {
			if len(vals) >= len(pinned[i])+listFieldCap {
				break
			}
			if taken[f.Name] || local[f.Name] {
				continue
			}
			local[f.Name] = true
			vals = append(vals, f.Name)
		}
		if len(vals) < max(s.min, 1) {
			return binding{}, false
		}
		b.fields[i] = vals
	}
	return b, true
}

// boundDraft builds op's draft for one binding: the request as wire
// JSON (each need a "<key>" placeholder), the probe request predict
// runs (needs left out), and the needs.
func boundDraft(op opInfo, cohort *types.Cohort, slots []bslot, b binding, needs []bneed) (request json.RawMessage, probe *types.Request, ok bool) {
	build := func(withNeeds bool) map[string]any {
		entry := map[string]any{"type": op.name}
		params := map[string]any{}
		put := func(loc slotLoc, key string, v any) {
			switch loc {
			case locParams:
				params[key] = v
			case locOrderBy:
				entry[key] = []map[string]any{{"field": v}}
			default:
				entry[key] = v
			}
		}
		for i, s := range slots {
			if b.vector[i] {
				put(s.loc, "vector", b.fields[i][0])
				continue
			}
			var v any = b.fields[i][0]
			if s.list {
				v = b.fields[i]
			}
			put(s.loc, s.key, v)
		}
		// The wire requires an aggregation's field even when the
		// operator ignores it (AGG_RATIO, IgnoresField): name the first
		// field it does read, so the draft is the runtime's valid form.
		if op.category == catAggregator && op.ignoresField && len(slots) > 0 && !b.vector[0] {
			entry["field"] = b.fields[0][0]
		}
		if withNeeds {
			for _, n := range needs {
				var v any = placeholder(n.key)
				if n.list {
					v = []string{placeholder(n.key)}
				}
				put(n.loc, n.key, v)
			}
		}
		if len(params) > 0 {
			entry["params"] = params
		}
		return map[string]any{"cohort": cohort, slotKey[op.category]: []any{entry}}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(build(true)); err != nil {
		return nil, nil, false
	}
	raw, err := json.Marshal(build(false))
	if err != nil {
		return nil, nil, false
	}
	probe = &types.Request{}
	if err := json.Unmarshal(raw, probe); err != nil {
		return nil, nil, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), probe, true
}

// passes reports whether predict accepts a draft. A fully bound draft
// must be valid with no error. A draft with needs is probed without
// them, so predict may refuse exactly the missing values: it passes
// when every error names one of them, and fails on any other fault
// (a field predict rejects).
func passes(env *descriptor.Envelope, needs []bneed) bool {
	res, ok := env.Data.(*descriptor.PredictResult)
	if !ok || res == nil {
		return false
	}
	if len(needs) == 0 {
		return res.Valid && len(env.Errors) == 0
	}
	for _, e := range env.Errors {
		msg := strings.ToLower(e.Message)
		if !slices.ContainsFunc(needs, func(n bneed) bool { return strings.Contains(msg, n.key) }) {
			return false
		}
	}
	return true
}

// boundWhy is the deterministic bound template: the plain purpose,
// then the bound fields with their types (and a categorical field's
// group count), then the placeholder instruction when needs remain.
func boundWhy(plain string, schema *encoding.Schema, b binding, needs []bneed) string {
	var parts []string
	seen := map[string]bool{}
	for _, fs := range b.fields {
		for _, name := range fs {
			if seen[name] {
				continue
			}
			seen[name] = true
			f := schema.Field(name)
			if f == nil {
				continue
			}
			d := name + " (" + f.Type.String()
			if i := fieldIdx(schema, name); f.Type.IsVector() && i >= 0 {
				d += ", " + strconv.Itoa(schema.VectorDim(i)) + " elements"
			}
			if lv := fieldaccept.Levels(f); lv != nil {
				d += ", " + strconv.Itoa(len(lv)) + " groups"
			}
			parts = append(parts, d+")")
		}
	}
	out := strings.TrimSpace(plain)
	if len(parts) > 0 {
		out = strings.TrimSpace(out + " It uses " + joinAnd(parts) + ".")
	}
	if len(needs) > 0 {
		out = strings.TrimSpace(out + " " + unboundWhy)
	}
	return out
}

func joinAnd(parts []string) string {
	if len(parts) <= 1 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// validateHints holds every hint to the cohort: it must name a schema
// field whose kind some role of the intent takes — or, a native vector
// field, an intent some serving operator drafts a vector slot for
// (vectorSlots). A miss is SERVICE_VALIDATION naming the hint, never a
// silent drop.
func validateHints(hints []string, schema *encoding.Schema, in descriptor.Intent, vectorSlots bool) ([]string, error) {
	kinds := intentKinds(in)
	var out []string
	for _, h := range hints {
		if slices.Contains(out, h) {
			continue
		}
		f := schema.Field(h)
		if f == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"recommend: field hint "+quote(h)+" is not in the cohort schema",
				map[string]any{"field": "fields", "hint": h})
		}
		if k := kindOf(f); !slices.Contains(kinds, k) && (k != descriptor.FieldKindVector || !vectorSlots) {
			names := make([]string, len(kinds))
			for i, k := range kinds {
				names[i] = string(k)
			}
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"recommend: field hint "+quote(h)+" is a "+string(k)+" field; intent "+in.ID+" takes "+strings.Join(names, ", ")+" fields",
				map[string]any{"field": "fields", "hint": h, "kind": string(k), "kinds": names})
		}
		out = append(out, h)
	}
	return out, nil
}

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
}

// bneed is one value only the caller can choose.
type bneed struct {
	key  string
	loc  slotLoc
	list bool
}

// fieldNameParams are required params whose manifest type is a plain
// string but whose value names a schema field.
var fieldNameParams = map[string]map[string]bool{
	"AGG_DISTINCT_SUM": {"distinct_by": true},
}

// oneSampleNeeds are optional params a draft must still ask for: the
// one-sample TEST_T compares against mu, whose default of 0 is rarely
// the reference the question means.
var oneSampleNeeds = map[[2]string][]string{
	{"TEST_T", descx.IntentBenchmark}: {"mu"},
}

// needWhy says what to supply for a param only the caller can choose.
// Each sentence passes LintGuidanceText (TestRecommend_NeedsWhyLint).
var needWhy = map[string]string{
	"success":    "Name the outcome value that counts as a success.",
	"mu":         "Give the reference value the mean is compared with.",
	"family":     "Choose the model family that suits the outcome.",
	"percentile": "Choose which percentile to report, between 0 and 100.",
	"ms_within":  "Give the within-group mean square from an ANOVA on the same groups.",
	"df_within":  "Give the within-group degrees of freedom from that same ANOVA.",
	"values":     "List the values the filter keeps, drops or matches.",
	"expression": "Write the expression that picks the rows to keep.",
	"ranges":     "List the labelled date ranges to use.",
	"interval":   "Choose the bucket width.",
	"value":      "Name the value whose share to count.",
	"part":       "Choose the date part to extract.",
	"alpha":      "Choose the smoothing weight, between 0 and 1.",
	"degree":     "Choose the polynomial degree.",
	"codes":      "List the codes to look for.",
	"label":      "Name the option to look for.",
	"ratios":     "Give the split ratios.",
	"frame":      "Choose the window frame: which rows around each row it covers.",
}

// needWhyDefault covers a required param with no needWhy entry, such as
// an extension operator's.
const needWhyDefault = "Choose this value yourself; the schema cannot supply it."

func whyNeed(param string) string {
	if w, ok := needWhy[param]; ok {
		return w
	}
	return needWhyDefault
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

func categoricalField(f *encoding.Field) bool { return f.Type.IsCategorical() }

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
			return bslot{key: key, accept: categoricalField}
		}
		return bslot{key: key, accept: numeric}
	case "field2":
		return bslot{key: key, accept: numeric}
	case "order_by":
		return bslot{key: key, loc: locOrderBy, accept: kindIs(descriptor.FieldKindDate, descriptor.FieldKindNumeric), prefer: preferDate}
	default: // split_by, subject_field, rows, cols
		return bslot{key: key, accept: categoricalField}
	}
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
		for _, n := range oneSampleNeeds[[2]string{op.name, in.ID}] {
			needs = append(needs, bneed{key: n, loc: locParams})
		}
	case catRegression:
		// target / predictors are declared params (below).
	case catMatrix:
		slots = append(slots, bslot{key: "fields", list: true, min: 2, accept: kindIs(descriptor.FieldKindNumeric)})
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
		if !op.ignoresField {
			slots = append(slots, bslot{key: "field", accept: fieldAccept})
		}
		slots = append(slots, bslot{key: "order_by", loc: locOrderBy, accept: kindIs(descriptor.FieldKindDate, descriptor.FieldKindNumeric), prefer: preferDate})
		if descx.WindowFrameRequired(types.WindowType(op.name)) {
			needs = append(needs, bneed{key: "frame", loc: locTop})
		}
	default: // aggregator, attribute, grouper, feature
		if !op.ignoresField {
			slots = append(slots, bslot{key: "field", accept: fieldAccept})
		}
	}
	for _, p := range op.params {
		loc := locParams
		if op.category == catRegression || topLevelParams[op.category][p.name] {
			loc = locTop
		}
		switch {
		case p.typ == "field":
			accept := fieldFilterAccept(p.fieldFilter)
			if op.category == catRegression {
				accept = both(accept, typeIn(op.accepts))
			}
			slots = append(slots, bslot{key: p.name, loc: loc, accept: accept})
		case op.category == catRegression && p.name == "predictors":
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
			needs = append(needs, bneed{key: p.name, loc: loc, list: p.list})
		}
	}
	return slots, needs, true
}

// binding is one assignment of fields to an operator's slots.
type binding struct {
	fields [][]string // per slot
	hints  int        // hinted fields it uses
}

// bind finds up to k bindings of slots over schema. Each hint pins the
// first slot that takes it (a list slot takes every hint that fits);
// the other slots take schema fields in order. With hints, an operator
// no hint fits gets no binding.
func bind(slots []bslot, schema *encoding.Schema, hints []string, k int) []binding {
	cands := make([][]*encoding.Field, len(slots))
	for i, s := range slots {
		for j := range schema.Fields {
			f := &schema.Fields[j]
			if s.accept(f) {
				cands[i] = append(cands[i], f)
			}
		}
		if s.prefer != nil {
			slices.SortStableFunc(cands[i], func(a, b *encoding.Field) int { return s.prefer(a) - s.prefer(b) })
		}
	}
	pinned := make([][]string, len(slots))
	used := 0
	for _, h := range hints {
		for i, s := range slots {
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

	var out []binding
	cur := make([]string, len(slots))
	budget := dfsBudget
	var dfs func(i int)
	dfs = func(i int) {
		if len(out) >= k || budget <= 0 {
			return
		}
		budget--
		if i == len(slots) {
			if b, ok := fillLists(slots, cands, pinned, cur, taken); ok {
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
	dfs(0)
	return out
}

// fillLists completes one scalar assignment: each list slot takes its
// pinned hints, then free candidates up to listFieldCap.
func fillLists(slots []bslot, cands [][]*encoding.Field, pinned [][]string, cur []string, taken map[string]bool) (binding, bool) {
	b := binding{fields: make([][]string, len(slots))}
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
			var v any = b.fields[i][0]
			if s.list {
				v = b.fields[i]
			}
			put(s.loc, s.key, v)
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
			if f.Type.IsCategorical() && f.Dictionary != nil {
				d += ", " + strconv.Itoa(f.Dictionary.Count()) + " groups"
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
// field whose kind some role of the intent takes. A miss is
// SERVICE_VALIDATION naming the hint, never a silent drop.
func validateHints(hints []string, schema *encoding.Schema, in descriptor.Intent) ([]string, error) {
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
		if k := kindOf(f); !slices.Contains(kinds, k) {
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

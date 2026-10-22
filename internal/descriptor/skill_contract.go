package descriptor

import (
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Generated `## Contract` section (U38 E2-S1).
//
// An op skill may carry the marker line `<!-- generated: contract -->`
// (internal/skills/generated.go). Every rendered read replaces it with
// `## Contract` built HERE from the documented operator's capability
// declarations — never stored in the file:
//
//   - Types: per slot the admitted field types and the refusal code for
//     any other scalar type — the internal/fieldaccept table (the one
//     predict and the runtime both refuse from), else the declared
//     accepts_types (no code: the table judges nothing there); the
//     vector member types of a matrix operator (PULSE_VECTOR_MEMBER_TYPE);
//     the applies_to list of a synth distribution;
//   - Weight: the operator's weight class (internal/weighting, the
//     overlay class table, an extension's WeightAware declaration) —
//     the very facts ResolveWeights classes a slot by — or its refusal;
//   - Multiplicity: only for an operator that emits p-values (a test,
//     an inferential overlay kind, a matrix operator taking
//     params.inference or declaring PValues, REG_OLS / REG_GLM or an
//     extension model declaring PValues), with the families its surface
//     offers (multSurface.families);
//   - Streaming: Streamable (overlays: !Buffered), matrix Mergeable and
//     the ComponentSchema mergeability class, plus the streamable hint;
//   - Components: the ComponentSchema key names (optional ones apart).
//
// Instance- and profile-aware: with capability:weighting hidden the
// Weight line is absent and the components are the unweighted schema
// (no weighted floor keys, as the instance manifest lists them); with
// capability:multiplicity hidden the Multiplicity line is absent; the
// `compose` family is listed only with capability:compose offered; a
// hidden weighted twin is not named; every prose string (hints, refusal
// reasons) goes through the instance ProseScrub. The full-registry
// render (skills.Get) shows every line.
//
// Deterministic and cheap: the built-in facts are indexed once
// (contractIndex); an extension operator's are read off the instance's
// ExtensionsSnapshot per render. "" for a name no registry declares.

// contractVisibility is what one render may name: each flag reports
// whether the instance offers the feature.
type contractVisibility struct {
	weighting    bool
	multiplicity bool
	compose      bool
	// visible reports whether an operator / kind name is offered; nil
	// offers every name.
	visible func(name string) bool
	scrub   ProseScrub
}

// fullContractVisibility shows every line (the profile-free render).
var fullContractVisibility = contractVisibility{weighting: true, multiplicity: true, compose: true}

// contractTypes is one slot group's admission: the slots sharing it,
// the admitted types (already in render form) and the refusal codes
// for the rest — codes[0] covers "any other scalar type", each later
// one names its own types.
type contractTypes struct {
	slots  []string
	label  string // "Types" or "Member types" or "Applies to"
	admit  string
	codes  []contractCode
	suffix string // trailing note (packed_bool coercion)
}

type contractCode struct {
	code  errors.Code
	types []string // nil: any other scalar type
}

// contractWeight is the operator's weight fact.
type contractWeight struct {
	set    bool
	class  weighting.Class
	reason string // permanent refusal reason / kind refusal reason
	twin   string // a refused overlay's weighted twin
	// window: no slot weight (a request weight is the explicit one).
	window bool
	// extNotAware: an extension operator not declaring WeightAware;
	// extAgg marks an aggregator (an instance default is skipped).
	extNotAware bool
	extAgg      bool
}

// contractMult is the operator's multiplicity fact.
type contractMult struct {
	set     bool
	tukey   bool
	surface multSurface
	// matrixPayload: an overlay kind whose every shape is MATRIX (row /
	// column families offered).
	matrixPayload bool
	// pKey: a matrix operator's p-value auxiliary key; implies reports
	// that a spec block implies params.inference (built-ins).
	pKey    string
	implies bool
}

// contractFacts is everything one `## Contract` section renders.
type contractFacts struct {
	// category is the declaring registry: an Operator.Category, or
	// "test", "post_test", "regression", "matrix", "overlay", "synth".
	category string

	types []contractTypes

	streamSet    bool
	streamable   bool
	streamHint   string
	mergeSet     bool
	mergeable    bool
	mergeability descriptor.ComponentsMergeability

	// keys is the component schema with capability:weighting offered;
	// keysUnweighted the schema without it.
	keys           []descriptor.ComponentKey
	keysUnweighted []descriptor.ComponentKey

	weight contractWeight
	mult   contractMult
}

var (
	contractOnce sync.Once
	contractIdx  map[string]contractFacts
)

// contractIndex returns the built-in operators' contract facts, keyed
// by operator / kind / distribution name, built once from the
// capability tables.
func contractIndex() map[string]contractFacts {
	contractOnce.Do(func() { contractIdx = buildContractIndex() })
	return contractIdx
}

func buildContractIndex() map[string]contractFacts {
	idx := map[string]contractFacts{}
	unweightedAgg := map[string][]descriptor.ComponentKey{}
	for _, op := range aggregatorCapabilityTable() {
		unweightedAgg[op.Name] = op.ComponentSchema.Keys
	}
	addOps := func(ops []descriptor.Operator, unweighted map[string][]descriptor.ComponentKey) {
		for _, op := range ops {
			f := contractFacts{
				category:     op.Category,
				types:        operatorContractTypes(op),
				streamSet:    true,
				streamable:   op.Streamable,
				streamHint:   op.StreamableHint,
				mergeability: op.ComponentSchema.Mergeability,
				keys:         op.ComponentSchema.Keys,
			}
			f.keysUnweighted = f.keys
			if k, ok := unweighted[op.Name]; ok {
				f.keysUnweighted = k
			}
			f.weight = builtinWeight(op.Name, op.Category == "window")
			idx[op.Name] = f
		}
	}
	addOps(aggregatorCapabilities(), unweightedAgg)
	addOps(attributeCapabilities(), nil)
	addOps(filtererCapabilities(), nil)
	addOps(grouperCapabilities(), nil)
	addOps(windowCapabilities(), nil)
	addOps(featureCapabilities(), nil)

	for _, t := range testCapabilities() {
		tt := tabledContractTypes(t.Name)
		if isVectorInput(t.Name) {
			tt = append([]contractTypes{vectorMemberTypes()}, tt...)
		}
		idx[t.Name] = contractFacts{
			category:   "test",
			types:      tt,
			streamSet:  true,
			streamable: t.Streamable,
			weight:     builtinWeight(t.Family, false),
			mult:       contractMult{set: true, surface: surfaceTest, tukey: t.Family == string(types.TEST_TUKEY_HSD)},
		}
	}
	// A family offered only as a post-test (TEST_TUKEY_HSD, TEST_TREND,
	// …): it reads aggregated rows, so every weight is refused, and the
	// Tukey HSD is already corrected.
	for _, t := range postTestCapabilities() {
		if _, ok := idx[t.Family]; ok {
			continue
		}
		idx[t.Family] = contractFacts{
			category:   "post_test",
			types:      tabledContractTypes(t.Family),
			streamSet:  true,
			streamable: false,
			weight:     contractWeight{set: true, class: weighting.ClassRefuse, reason: reasonPostTest},
			mult:       contractMult{set: true, surface: surfaceTest, tukey: t.Family == string(types.TEST_TUKEY_HSD)},
		}
	}

	for _, r := range regressionCapabilities() {
		f := contractFacts{
			category:   "regression",
			types:      tabledContractTypes(r.Name),
			streamSet:  true,
			streamable: r.Streamable,
			streamHint: r.StreamableHint,
			weight:     builtinWeight(r.Name, false),
		}
		if len(f.types) == 0 && len(r.AcceptsTypes) > 0 {
			f.types = []contractTypes{declaredContractTypes([]string{"target", "predictor"}, r.AcceptsTypes)}
		}
		if r.Name != string(types.REG_BAYES_LINEAR) {
			f.mult = contractMult{set: true, surface: surfaceRegression}
		}
		idx[r.Name] = f
	}

	weightedMats := map[string][]descriptor.ComponentKey{}
	for _, m := range withMatrixWeightKeys(matrixCapabilities()) {
		weightedMats[m.Name] = m.ComponentSchema.Keys
	}
	inference := map[types.MatrixType]bool{}
	for _, k := range vectors.InferenceTypes() {
		inference[k] = true
	}
	for _, m := range matrixCapabilities() {
		f := contractFacts{
			category:       "matrix",
			types:          []contractTypes{vectorMemberTypes()},
			streamSet:      true,
			streamable:     m.Streamable,
			mergeSet:       true,
			mergeable:      m.Mergeable,
			mergeability:   m.ComponentSchema.Mergeability,
			keys:           weightedMats[m.Name],
			keysUnweighted: m.ComponentSchema.Keys,
			weight:         builtinWeight(m.Name, false),
		}
		if inference[types.MatrixType(m.Name)] {
			f.mult = contractMult{set: true, surface: surfaceMatrix, pKey: MatrixAuxP, implies: true}
		}
		idx[m.Name] = f
	}

	for _, o := range OverlayCapabilities() {
		f := contractFacts{
			category:   "overlay",
			streamSet:  true,
			streamable: !o.Buffered,
			weight:     overlayContractWeight(o.Kind),
		}
		if o.Inferential {
			matrix := len(o.Shapes) > 0
			for _, sh := range o.Shapes {
				if sh != types.OverlayShapeMatrix {
					matrix = false
				}
			}
			f.mult = contractMult{set: true, surface: surfaceRequestOverlay, matrixPayload: matrix}
		}
		idx[string(o.Kind)] = f
	}

	for _, d := range distributionCapabilities() {
		if len(d.AppliesTo) == 0 {
			continue
		}
		idx[d.Name] = contractFacts{category: "synth", types: []contractTypes{{label: "Applies to", admit: renderTypeList(d.AppliesTo) + " fields"}}}
	}
	return idx
}

// builtinWeight is a built-in operator's weight fact: its
// internal/weighting class, with the permanent refusal reason (or the
// frequency-only kind refusal reason) it states.
func builtinWeight(op string, window bool) contractWeight {
	c := weighting.ClassOf(op)
	w := contractWeight{set: true, class: c, window: window}
	switch c {
	case weighting.ClassRefuse:
		w.reason = weighting.RefusalReason(op)
	case weighting.ClassFrequencyOnly:
		w.reason = weighting.KindRefusalReason(op)
	}
	return w
}

// overlayContractWeight is an overlay kind's weight fact: its class
// (overlayWeightClass) with the table's reason and weighted twin.
func overlayContractWeight(kind types.OverlayKind) contractWeight {
	w := contractWeight{set: true, class: overlayWeightClass(string(kind))}
	if e, ok := overlayWeightClasses[kind]; ok {
		w.reason, w.twin = e.reason, string(e.twin)
	}
	return w
}

// contractSlots is the fieldaccept slot order a contract lists.
var contractSlots = []fieldaccept.Slot{
	fieldaccept.SlotField, fieldaccept.SlotField2, fieldaccept.SlotTarget, fieldaccept.SlotPredictor,
	fieldaccept.SlotSplitBy, fieldaccept.SlotSubject, fieldaccept.SlotOrder, fieldaccept.SlotRows,
	fieldaccept.SlotCols, fieldaccept.SlotStratify, fieldaccept.SlotPrediction, fieldaccept.SlotNumerator,
	fieldaccept.SlotDenominator, fieldaccept.SlotOutcome, fieldaccept.SlotBase,
}

// knownScalarTypes is every registered non-vector field type, in byte
// order.
var knownScalarTypes = func() []encoding.FieldType {
	var out []encoding.FieldType
	for b := 0; b < 256; b++ {
		if ft := encoding.FieldType(b); ft.IsKnown() && !ft.IsVector() {
			out = append(out, ft)
		}
	}
	return out
}()

// operatorContractTypes is an Operator's type admissions: a window
// that forbids a field says so; a vector-input operator lists its
// member types (vectorMemberTypes); the tabled slots come from the
// fieldaccept table; an operator with no tabled slot (and not
// IgnoresField) its declared accepts_types, slot unnamed — the
// manifest's own claim, with no refusal code.
func operatorContractTypes(op descriptor.Operator) []contractTypes {
	if op.ForbidsField {
		return []contractTypes{{label: "Types", admit: "reads no field; naming one is `" + string(errors.PULSE_WINDOW_INVALID) + "`"}}
	}
	out := tabledContractTypes(op.Name)
	switch {
	case isVectorInput(op.Name):
		out = append([]contractTypes{vectorMemberTypes()}, out...)
	case len(out) == 0 && !op.IgnoresField && len(op.AcceptsTypes) > 0:
		out = []contractTypes{declaredContractTypes(nil, op.AcceptsTypes)}
	}
	return out
}

// tabledContractTypes renders every fieldaccept slot of op: the
// admitted types and, per refusal code, the scalar types it refuses
// (the code refusing the most types reads "any other scalar type").
// Slots with an identical admission share one group.
func tabledContractTypes(op string) []contractTypes {
	var out []contractTypes
	for _, slot := range contractSlots {
		if _, ok := fieldaccept.Lookup(op, slot); !ok {
			continue
		}
		g := refusalGroup(fieldaccept.TypeNames(op, slot), func(ft encoding.FieldType) errors.Code {
			if ce := fieldaccept.Refusal(op, slot, "f", ft); ce != nil {
				return ce.Code
			}
			return ""
		})
		g.slots = []string{string(slot)}
		out = mergeContractTypes(out, g)
	}
	return out
}

// refusalGroup builds one admission from the admitted type names and a
// per-type refusal code ("" = admitted).
func refusalGroup(admitted []string, code func(encoding.FieldType) errors.Code) contractTypes {
	g := contractTypes{label: "Types", admit: renderTypeList(admitted)}
	byCode := map[errors.Code][]string{}
	var order []errors.Code
	for _, ft := range knownScalarTypes {
		c := code(ft)
		if c == "" {
			continue
		}
		if _, seen := byCode[c]; !seen {
			order = append(order, c)
		}
		byCode[c] = append(byCode[c], ft.String())
	}
	sort.SliceStable(order, func(i, j int) bool {
		if len(byCode[order[i]]) != len(byCode[order[j]]) {
			return len(byCode[order[i]]) > len(byCode[order[j]])
		}
		return order[i] < order[j]
	})
	for i, c := range order {
		cc := contractCode{code: c}
		if i > 0 {
			names := append([]string(nil), byCode[c]...)
			sort.Strings(names)
			cc.types = names
		}
		g.codes = append(g.codes, cc)
	}
	return g
}

// mergeContractTypes appends g, folding it into an earlier group with
// the same admission.
func mergeContractTypes(gs []contractTypes, g contractTypes) []contractTypes {
	for i := range gs {
		if gs[i].label == g.label && gs[i].admit == g.admit && slices.EqualFunc(gs[i].codes, g.codes, func(a, b contractCode) bool {
			return a.code == b.code && slices.Equal(a.types, b.types)
		}) {
			gs[i].slots = append(gs[i].slots, g.slots...)
			return gs
		}
	}
	return append(gs, g)
}

// declaredContractTypes is an untabled slot's admission: the declared
// type list, with no refusal code (no shared table judges it).
func declaredContractTypes(slots []string, accepts []string) contractTypes {
	return contractTypes{slots: slots, label: "Types", admit: renderAcceptList(accepts)}
}

// renderAcceptList renders a declared accepts list: "any type" when it
// covers every registered type, "any type but `vec_*`" when it covers
// every scalar one, else the list.
func renderAcceptList(accepts []string) string {
	have := map[string]bool{}
	for _, a := range accepts {
		have[a] = true
	}
	scalar := true
	for _, ft := range knownScalarTypes {
		if !have[ft.String()] {
			scalar = false
			break
		}
	}
	if scalar {
		if have[encoding.FieldTypeVecF32.String()] && have[encoding.FieldTypeVecF64.String()] {
			return "any type"
		}
		return "any type but `vec_*`"
	}
	return renderTypeList(accepts)
}

// typeFamilies are the field-type families a complete run of which
// renders as one `<prefix>*` token.
var typeFamilies = []string{"categorical_", "set_"}

// renderTypeList renders type names in backticks, a family whose every
// registered member is present collapsing to `<prefix>*`.
func renderTypeList(names []string) string {
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	full := map[string]bool{}
	for _, fam := range typeFamilies {
		complete, any := true, false
		for _, ft := range knownScalarTypes {
			if strings.HasPrefix(ft.String(), fam) {
				any = true
				complete = complete && have[ft.String()]
			}
		}
		full[fam] = any && complete
	}
	var out []string
	done := map[string]bool{}
	for _, n := range names {
		tok := "`" + n + "`"
		for _, fam := range typeFamilies {
			if full[fam] && strings.HasPrefix(n, fam) {
				tok = "`" + fam + "*`"
			}
		}
		if done[tok] {
			continue
		}
		done[tok] = true
		out = append(out, tok)
	}
	return strings.Join(out, ", ")
}

// vectorMemberTypes is a vector-input operator's member admission,
// read off the vector rule itself (vectors.MemberTypeAllowed, the check
// both predict and the runtime run): the integer and float types,
// packed_bool only under coerce "binary", a native vector's elements
// always; any other member type is PULSE_VECTOR_MEMBER_TYPE.
func vectorMemberTypes() contractTypes {
	var admit []string
	binaryOnly := false
	for _, ft := range knownScalarTypes {
		if vectors.MemberTypeAllowed(ft, types.VectorCoerceBinary) {
			admit = append(admit, ft.String())
			if !vectors.MemberTypeAllowed(ft, "") {
				binaryOnly = true
			}
		}
	}
	sort.Strings(admit)
	g := contractTypes{slots: []string{"vector", "fields"}, label: "Member types", admit: renderTypeList(admit),
		codes: []contractCode{{code: errors.PULSE_VECTOR_MEMBER_TYPE}}}
	notes := []string{"a `vec_*` field's elements are members"}
	if binaryOnly {
		notes = append([]string{"`packed_bool` only under `coerce: \"binary\"`"}, notes...)
	}
	g.suffix = strings.Join(notes, "; ")
	return g
}

// isVectorInput reports whether the built-in operator reads its input
// from a vector (Request.Vectors / inline fields) rather than a field.
func isVectorInput(op string) bool {
	return vectors.IsVectorAggregation(types.AggregationType(op)) ||
		vectors.IsVectorAttribute(types.AttributeType(op)) ||
		vectors.IsVectorGroup(types.GroupType(op)) ||
		vectors.IsVectorTest(types.TestType(op))
}

// extensionContract reads an extension operator's facts off the
// instance snapshot; false when ext registers no such name.
func extensionContract(name string, ext *ExtensionsSnapshot) (contractFacts, bool) {
	if ext == nil {
		return contractFacts{}, false
	}
	schema := func(m descriptor.OperatorMeta) contractFacts {
		f := contractFacts{streamSet: true, streamable: m.Streamable}
		if len(m.Accepts) > 0 {
			f.types = []contractTypes{declaredContractTypes(nil, m.Accepts)}
		}
		if cs, ok := ext.ComponentSchemas[name]; ok {
			f.mergeability = cs.Mergeability
			f.keys, f.keysUnweighted = cs.Keys, cs.Keys
		}
		return f
	}
	extWeight := func(m descriptor.OperatorMeta, agg bool) contractWeight {
		if m.WeightAware {
			return contractWeight{set: true, class: weighting.ClassAware}
		}
		return contractWeight{set: true, extNotAware: true, extAgg: agg}
	}
	if m, ok := findMeta(ext.Aggregators, name); ok {
		f := schema(m)
		f.mergeSet, f.mergeable = true, m.Mergeable
		f.weight = extWeight(m, true)
		return f, true
	}
	if m, ok := findMeta(ext.Attributes, name); ok {
		f := schema(m)
		f.mergeSet, f.mergeable = true, m.Mergeable
		f.weight = extWeight(m, false)
		return f, true
	}
	if m, ok := findMeta(ext.Groupers, name); ok {
		f := schema(m)
		f.mergeSet, f.mergeable = true, m.Mergeable
		f.weight = contractWeight{set: true, class: weighting.ClassNone}
		return f, true
	}
	for _, metas := range [][]descriptor.OperatorMeta{ext.Filterers, ext.Features} {
		if m, ok := findMeta(metas, name); ok {
			f := schema(m)
			f.weight = contractWeight{set: true, class: weighting.ClassNone}
			return f, true
		}
	}
	if m, ok := findMeta(ext.Windows, name); ok {
		f := schema(m)
		f.weight = contractWeight{set: true, class: weighting.ClassNone, window: true}
		return f, true
	}
	if m, ok := findMeta(ext.Tests, name); ok {
		f := schema(m)
		f.weight = extWeight(m, false)
		f.mult = contractMult{set: true, surface: surfaceTest}
		return f, true
	}
	if m, d, ok := ext.matrixOp(name); ok {
		f := contractFacts{streamSet: true, streamable: m.Streamable, mergeSet: true, mergeable: m.Streamable}
		if len(d.Accepts) > 0 {
			names := make([]string, len(d.Accepts))
			for i, a := range d.Accepts {
				names[i] = a.String()
			}
			sort.Strings(names)
			f.types = []contractTypes{{label: "Member types", admit: renderTypeList(names),
				codes: []contractCode{{code: errors.PULSE_VECTOR_MEMBER_TYPE}}}}
		}
		for _, weighted := range []bool{true, false} {
			for _, mm := range extensionMatrixMetas(ext, weighted) {
				if mm.Name == name {
					f.mergeability = mm.ComponentSchema.Mergeability
					if weighted {
						f.keys = mm.ComponentSchema.Keys
					} else {
						f.keysUnweighted = mm.ComponentSchema.Keys
					}
				}
			}
		}
		f.weight = extWeight(m, false)
		if d.PValues != "" {
			f.mult = contractMult{set: true, surface: surfaceMatrix, pKey: d.PValues}
		}
		return f, true
	}
	if m, d, ok := ext.regression(name); ok {
		f := contractFacts{streamSet: true, streamable: m.Streamable}
		if len(d.Accepts) > 0 {
			g := refusalGroup(fieldaccept.ExtensionRegressionTypeNames(d.Accepts), func(ft encoding.FieldType) errors.Code {
				if fieldaccept.ExtensionRegressionAccepts(d.Accepts, ft) {
					return ""
				}
				return errors.PULSE_FIELD_TYPE_UNSUPPORTED
			})
			g.slots = []string{string(fieldaccept.SlotTarget), string(fieldaccept.SlotPredictor)}
			f.types = []contractTypes{g}
		}
		f.weight = extWeight(m, false)
		if d.PValues {
			f.mult = contractMult{set: true, surface: surfaceRegression}
		}
		return f, true
	}
	return contractFacts{}, false
}

// contractFactsOf resolves operator's facts: the built-in index first,
// then the instance's extension registrations.
func contractFactsOf(operator string, ext *ExtensionsSnapshot) (contractFacts, bool) {
	if f, ok := contractIndex()[operator]; ok {
		return f, true
	}
	return extensionContract(operator, ext)
}

// RenderContractSection renders `## Contract` for operator on the
// full registry (every feature offered, no extension); "" for a name no
// registry declares.
func RenderContractSection(operator string) string {
	return renderContractSection(operator, nil, fullContractVisibility)
}

func renderContractSection(operator string, ext *ExtensionsSnapshot, v contractVisibility) string {
	f, ok := contractFactsOf(operator, ext)
	if !ok {
		return ""
	}
	var lines []string
	for _, g := range f.types {
		lines = append(lines, renderContractTypes(g))
	}
	if v.weighting && f.weight.set {
		lines = append(lines, renderContractWeight(f.weight, v))
	}
	if v.multiplicity && f.mult.set {
		lines = append(lines, renderContractMult(f.mult, v))
	}
	if s := renderContractStreaming(f, v); s != "" {
		lines = append(lines, s)
	}
	keys := f.keysUnweighted
	if v.weighting {
		keys = f.keys
	}
	if s := renderContractKeys(keys); s != "" {
		lines = append(lines, s)
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Contract\n\n" + strings.Join(lines, "\n")
}

func renderContractTypes(g contractTypes) string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(g.label)
	if len(g.slots) > 0 {
		b.WriteString(" (")
		for i, s := range g.slots {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("`" + s + "`")
		}
		b.WriteString(")")
	}
	b.WriteString(": ")
	if g.admit == "" {
		b.WriteString("none")
	} else {
		b.WriteString(g.admit)
	}
	if g.suffix != "" {
		b.WriteString(" (" + g.suffix + ")")
	}
	for _, c := range g.codes {
		b.WriteString("; ")
		if c.types == nil {
			b.WriteString("any other")
			if g.label == "Types" {
				b.WriteString(" scalar type")
			}
		} else {
			b.WriteString(renderTypeList(c.types))
		}
		b.WriteString(" is `" + string(c.code) + "`")
	}
	b.WriteString(".")
	return b.String()
}

func renderContractWeight(w contractWeight, v contractVisibility) string {
	reason := strings.TrimSuffix(strings.TrimSpace(v.scrub.Text(w.reason)), ".")
	switch {
	case w.extNotAware && w.extAgg:
		return "- Weight: not weight-aware; an explicit weight is `" + string(errors.PULSE_EXTENSION_NOT_WEIGHT_AWARE) + "`, an instance default is skipped."
	case w.extNotAware:
		return "- Weight: not weight-aware; any weight in force, an instance default included, is `" + string(errors.PULSE_EXTENSION_NOT_WEIGHT_AWARE) + "`; `weight: null` on the slot runs it unweighted."
	}
	switch w.class {
	case weighting.ClassAware:
		return "- Weight: weighted under `frequency` and `probability` weights."
	case weighting.ClassFrequencyOnly:
		s := "- Weight: weighted under `frequency` weights only; a `probability` weight is `" + string(errors.PULSE_WEIGHT_UNSUPPORTED) + "`"
		if reason != "" {
			s += " (" + reason + ")"
		}
		return s + "."
	case weighting.ClassRefuse:
		s := "- Weight: refused under any weight in force, an instance default included (`" + string(errors.PULSE_WEIGHT_UNSUPPORTED) + "`"
		if reason != "" {
			s += ": " + reason
		}
		s += "); `weight: null` on the slot runs it unweighted"
		if w.twin != "" && (v.visible == nil || v.visible(w.twin)) {
			s += "; the weighted twin is `" + w.twin + "`"
		}
		return s + "."
	case weighting.ClassNotWeightable:
		if w.window {
			return "- Weight: no weighted form and no slot weight; a request weight is `" + string(errors.PROCESSING_CONFIG) + "`, an instance default is skipped."
		}
		return "- Weight: no weighted form; an explicit weight is `" + string(errors.PROCESSING_CONFIG) + "`, an instance default is skipped."
	}
	return "- Weight: not read; a weight in force is skipped."
}

func renderContractMult(m contractMult, v contractVisibility) string {
	if m.tukey {
		return "- Multiplicity: already corrected for its own comparisons; a method on its own block is `" +
			string(errors.PULSE_MULTIPLICITY_INVALID) + "`, an inherited one skips it."
	}
	var lead string
	switch m.surface {
	case surfaceMatrix:
		adj, sig := MatrixAdjustedKeys(m.pKey)
		if m.implies {
			lead = "a spec `multiplicity` block (implying `params.inference`; an inherited method reaches only a spec under `params.inference`)"
		} else {
			lead = "a `multiplicity` method"
		}
		lead += " adds `" + adj + "` and `" + sig + "` beside `" + m.pKey + "`"
	case surfaceRegression:
		lead = "its own `multiplicity` block (an inherited one never reaches it) adds per-coefficient `p_adjusted` and `significant_adjusted` beside the raw p"
	default:
		lead = "a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p"
	}
	var fams []string
	def := m.surface.defaultFamily()
	for _, f := range m.surface.families(v.compose, m.matrixPayload) {
		s := "`" + string(f) + "`"
		switch {
		case f == def:
			s += " (default)"
		case f == types.MultiplicityFamilyCompose:
			s += " (inside Compose)"
		}
		fams = append(fams, s)
	}
	return "- Multiplicity: " + lead + "; families " + strings.Join(fams, ", ") + "."
}

func renderContractStreaming(f contractFacts, v contractVisibility) string {
	if !f.streamSet && f.mergeability == "" {
		return ""
	}
	var parts []string
	if f.streamSet {
		if f.streamable {
			parts = append(parts, "streamable")
		} else {
			parts = append(parts, "buffered only")
		}
	}
	if f.mergeSet {
		if f.mergeable {
			parts = append(parts, "partial states merge")
		} else {
			parts = append(parts, "partial states do not merge")
		}
	}
	if f.mergeability != "" {
		parts = append(parts, "components `"+string(f.mergeability)+"`")
	}
	s := "- Streaming: " + strings.Join(parts, "; ") + "."
	if hint := strings.TrimSpace(v.scrub.Text(f.streamHint)); hint != "" && !f.streamable {
		s += " " + sentenceEnd(hint)
	}
	return s
}

func renderContractKeys(keys []descriptor.ComponentKey) string {
	var req, opt []string
	for _, k := range keys {
		if k.Optional {
			opt = append(opt, "`"+k.Name+"`")
		} else {
			req = append(req, "`"+k.Name+"`")
		}
	}
	if len(req)+len(opt) == 0 {
		return ""
	}
	s := "- Components: "
	switch {
	case len(req) > 0 && len(opt) > 0:
		s += strings.Join(req, ", ") + "; optional " + strings.Join(opt, ", ")
	case len(req) > 0:
		s += strings.Join(req, ", ")
	default:
		s += "optional " + strings.Join(opt, ", ")
	}
	return s + "."
}

// contractVisibility is the Discovery's view: a feature is offered
// iff its node survived the instance prune (the pass-through view, no
// graph, offers everything).
func (d *Discovery) contractVisibility() contractVisibility {
	if d.graph == nil {
		return fullContractVisibility
	}
	has := func(name string) bool { return d.graph.Has(FenceFeatureID(name)) }
	return contractVisibility{
		weighting:    has(featWeighting),
		multiplicity: has(featMultiplicity),
		compose:      has(featCompose),
		visible:      has,
		scrub:        d.scrub,
	}
}

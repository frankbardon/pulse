package vectors

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// Vector aggregators — the types.Aggregation Vector / Fields slots,
// resolved by the one leaf predict (the field-reference walk), the
// runtime (Processor.prepare and the parallel reducers) and projection
// all call (the Attribute / Group Vector / Fields mirror).
//
// Rules (the contract), per aggregation slot ("aggregations[i]",
// "crosstab.cell", "crosstab.margin_aggregations[i]"):
//
//   - A built-in vector aggregator (VectorAggregationTypes) reads
//     exactly one of vector and fields; field is refused (it reads no
//     scalar column), and so are both or neither of vector and fields.
//   - A vector no Request.Vectors entry defines is PULSE_VECTOR_UNKNOWN
//     (`vector`, `slot`, `defined`); inline fields resolve by the vector
//     rules (ResolveSpec at "<slot>.fields"). Members are source columns
//     only, never an attribute's label.
//   - Params are strict (unknown keys refused): `labels` — one display
//     label per member, inline fields only (a named vector carries its
//     own labels: labels_with_vector); a count other than the member
//     count is PULSE_VECTOR_LABELS_MISMATCH. `expand` (bool) replaces
//     the array with one scalar column per member, "<label>[<element
//     label>]" (ExpandedColumn); two members sharing a display label
//     would write one column twice, so expand refuses them
//     (expand_duplicate_labels).
//   - A registered extension aggregator declared VectorInput
//     (pulse.AggregatorRegistration.VectorInput) reads its members by
//     the same rules; only `labels` and `expand` of its params are the
//     engine's (read leniently — the rest are the operator's own), and
//     `expand` needs a declared ArrayValued value
//     (expand_not_array_valued).
//   - Any OTHER aggregator — built-in or a registered extension not
//     declared VectorInput — refuses a vector input
//     (vector_not_accepted).
//   - The default output label is types.AggregationLabelOf:
//     "<TYPE>_<vector>", or "<TYPE>_<first fields entry as written>".
//
// Shape refusals are SERVICE_VALIDATION naming `aggregation` (the slot
// path) and `reason`. A vector aggregator's array value is refused
// downstream wherever one number is needed (AggregationKinds.
// IsArrayValued — types.AggregationType.ArrayValued or a declared
// ArrayValued extension): ArrayValuedCrosstabRefusal here, Sort and
// request overlays in the field-reference walk, a feeding chain stage
// in internal/mergegate (unless the slot expands: ChainStageSchema
// types its element columns for the next stage).

// ExtensionAggregation is what an instance declares about one
// aggregation type its extensions register: Registered (the name is an
// extension aggregator), VectorInput (it reads vector / fields),
// ArrayValued (its value is one figure per member), NonScalar (its
// value is not one number — pulse.AggregatorRegistration.NonScalar) and
// EmitsInputValue (its value is one of its input field's own encoded
// values — pulse.AggregatorRegistration.EmitsInputValue).
type ExtensionAggregation struct {
	Registered, VectorInput, ArrayValued, NonScalar, EmitsInputValue bool
}

// AggregationKinds is one instance's view of the aggregation types the
// vector rules judge. Route maps a type to the name it executes as (a
// hidden type routes to "", an unknown name); nil is the identity.
// Extension reports a routed, non-built-in name's extension
// declaration; nil means no extension is registered. The zero value is
// the built-in view. Predict (from the extensions snapshot) and the
// runtime (from the extension registry) build one each, over the same
// declarations, so they classify a slot identically.
type AggregationKinds struct {
	Route     func(types.AggregationType) types.AggregationType
	Extension func(name string) ExtensionAggregation
}

// Routed returns the name t executes as.
func (k AggregationKinds) Routed(t types.AggregationType) types.AggregationType {
	if k.Route == nil {
		return t
	}
	return k.Route(t)
}

// extension returns the extension declaration of the ROUTED name r (the
// zero value for a built-in, a hidden or an unregistered name).
func (k AggregationKinds) extension(r types.AggregationType) ExtensionAggregation {
	if k.Extension == nil || r == "" || slices.Contains(types.AllAggregationTypes(), r) {
		return ExtensionAggregation{}
	}
	e := k.Extension(string(r))
	if !e.Registered {
		return ExtensionAggregation{}
	}
	return e
}

// IsVector reports whether t reads a vector input: a built-in vector
// aggregator (VectorAggregationTypes) or an extension declared
// VectorInput.
func (k AggregationKinds) IsVector(t types.AggregationType) bool {
	r := k.Routed(t)
	return IsVectorAggregation(r) || k.extension(r).VectorInput
}

// IsArrayValued reports whether t's value is one figure per member:
// types.AggregationType.ArrayValued or an extension declared
// ArrayValued. Every consumer that needs one number refuses it.
func (k AggregationKinds) IsArrayValued(t types.AggregationType) bool {
	r := k.Routed(t)
	return r.ArrayValued() || k.extension(r).ArrayValued
}

// IsNonScalar reports whether t's value in a Response.Data row is not
// one number, so a consumer that reads one number per host row (an
// overlay's scored aggregation) has nothing to read: IsArrayValued, a
// built-in in NonScalarAggregationTypes, or an extension declared
// NonScalar. Declared metadata only — predict judges it from the
// request alone, as the runtime does.
func (k AggregationKinds) IsNonScalar(t types.AggregationType) bool {
	r := k.Routed(t)
	return k.IsArrayValued(t) || slices.Contains(NonScalarAggregationTypes(), r) || k.extension(r).NonScalar
}

// NonScalarAggregationTypes returns the built-in aggregation types
// whose Response.Data value is not one number — every built-in whose
// RichAggregator lift replaces the float64 (a moment struct, a label
// list, a per-label count map, one figure per vector member): the
// map-valued (types.AggregationType.MapValued), the array-valued
// (ArrayValued) and the two label-list set folds. Their float64 is a
// scalar FALLBACK (popcount, running mean, max-bin count) that never
// reaches the row. processing's TestNonScalarAggregationTypes_MatchRowValues
// holds the list to what each built-in actually writes.
func NonScalarAggregationTypes() []types.AggregationType {
	var out []types.AggregationType
	for _, t := range types.AllAggregationTypes() {
		if t.MapValued() || t.ArrayValued() || t == types.AGG_SET_UNION || t == types.AGG_SET_INTERSECTION {
			out = append(out, t)
		}
	}
	return out
}

// EmitsInputValueAggregationTypes returns the built-in aggregation
// types whose one number is one of the INPUT field's own encoded
// values — a value picked from the column, not a figure computed over
// it: AGG_MODE (the modal value). Over a numeric field that value is a
// magnitude; over a categorical_* field it is the dictionary INDEX and
// over a packed_bool field the 0/1 truth value (IsCodeInputType) — a
// code, so an overlay scoring it would compare codes as magnitudes.
// AGG_MODE_COUNT (a count), AGG_FREQUENCY (a count of a value matched
// by label) and AGG_DISTINCT_COUNT (a cardinality) are figures, not
// picks. processing's TestEmitsInputValueAggregationTypes_MatchRowValues
// holds the list to what every built-in writes.
func EmitsInputValueAggregationTypes() []types.AggregationType {
	return []types.AggregationType{types.AGG_MODE}
}

// IsCodeInputType reports whether a value picked from a field of type
// ft is a code rather than a magnitude: a categorical_* field's value
// is its dictionary index, a packed_bool field's its 0/1 truth value.
func IsCodeInputType(ft encoding.FieldType) bool {
	return ft.IsCategorical() || ft == encoding.FieldTypePackedBool
}

// EmitsInputValue reports whether t's value is one of its input
// field's own encoded values: a built-in in
// EmitsInputValueAggregationTypes or an extension declared
// EmitsInputValue. Declared metadata only.
func (k AggregationKinds) EmitsInputValue(t types.AggregationType) bool {
	r := k.Routed(t)
	return slices.Contains(EmitsInputValueAggregationTypes(), r) || k.extension(r).EmitsInputValue
}

// IsCode reports whether t over an input field of type input writes a
// code, not a magnitude, into its Response.Data row: EmitsInputValue
// over an IsCodeInputType field. It depends on the input field's
// schema type, so predict judges it from the request plus the schema
// (no records), as the runtime does.
func (k AggregationKinds) IsCode(t types.AggregationType, input encoding.FieldType) bool {
	return IsCodeInputType(input) && k.EmitsInputValue(t)
}

// VectorAggregationTypes returns the built-in aggregation types that
// read their columns from Vector / Fields, in AllAggregationTypes order.
func VectorAggregationTypes() []types.AggregationType {
	return []types.AggregationType{types.AGG_VEC_MEAN, types.AGG_VEC_SUM, types.AGG_VEC_SD}
}

// vectorAggregationList joins VectorAggregationTypes with sep, last
// for the final pair ("A, B or C" / "A / B / C") — refusal prose names
// the whole set, never a hand-kept subset.
func vectorAggregationList(sep, last string) string {
	ts := VectorAggregationTypes()
	out := ""
	for i, t := range ts {
		switch {
		case i == 0:
		case i == len(ts)-1:
			out += last
		default:
			out += sep
		}
		out += string(t)
	}
	return out
}

// IsVectorAggregation reports whether t is a built-in vector
// aggregation type.
func IsVectorAggregation(t types.AggregationType) bool {
	return slices.Contains(VectorAggregationTypes(), t)
}

// HasAggregationVectorInput reports whether a sets any vector-input
// slot.
func HasAggregationVectorInput(a *types.Aggregation) bool {
	return a != nil && (a.Vector != "" || len(a.Fields) > 0)
}

// AggregationInput is one aggregation slot's vector input after
// resolution. The zero value (Has false) reads no vector.
type AggregationInput struct {
	// Slot is the slot path ("aggregations[0]", "crosstab.cell", ...).
	Slot string
	// Members is the member set (axis order) with its display labels:
	// the named vector's, else params.labels, else the member names.
	Members Resolved
	// Expand is params.expand: the value is written as one column per
	// member (ExpandedColumn) instead of an array.
	Expand bool
	// Extension marks an extension vector aggregator's slot: its params
	// are the operator's, so AggregationSlotRewrite keeps every other
	// key.
	Extension bool
	// HasElement marks a slot whose params.element selects one member;
	// Element is that member's index in Members.
	HasElement bool
	Element    int
}

// Has reports whether the slot resolved a vector input.
func (in AggregationInput) Has() bool { return len(in.Members.Members) > 0 }

// AggregationSlot is one aggregation slot of a request with its path.
type AggregationSlot struct {
	Path string
	Agg  *types.Aggregation
}

// AggregationSlots lists req's aggregation slots in walk order:
// aggregations, then the crosstab cell, then its margin aggregations.
func AggregationSlots(req *types.Request) []AggregationSlot {
	if req == nil {
		return nil
	}
	var out []AggregationSlot
	for i, a := range req.Aggregations {
		if a != nil {
			out = append(out, AggregationSlot{Path: "aggregations[" + strconv.Itoa(i) + "]", Agg: a})
		}
	}
	if ct := req.Crosstab; ct != nil {
		for i, c := range ct.CellAggregations() {
			if c != nil {
				out = append(out, AggregationSlot{Path: ct.CellSlotPath(i), Agg: c})
			}
		}
		for i, a := range ct.MarginAggregations {
			if a != nil {
				out = append(out, AggregationSlot{Path: "crosstab.margin_aggregations[" + strconv.Itoa(i) + "]", Agg: a})
			}
		}
	}
	return out
}

// anyAggregationSlot reports whether pred holds for any aggregation
// slot of req (AggregationSlots' set, walk order) — without building
// the slot list, so the vector-free request every caller sees most
// pays no allocation for the check.
func anyAggregationSlot(req *types.Request, pred func(*types.Aggregation) bool) bool {
	if req == nil {
		return false
	}
	for _, a := range req.Aggregations {
		if a != nil && pred(a) {
			return true
		}
	}
	if ct := req.Crosstab; ct != nil {
		for _, c := range ct.CellAggregations() {
			if c != nil && pred(c) {
				return true
			}
		}
		for _, a := range ct.MarginAggregations {
			if a != nil && pred(a) {
				return true
			}
		}
	}
	return false
}

// AnyAggregationVectorInput reports whether any aggregation slot of req
// sets vector or fields (allocation-free).
func AnyAggregationVectorInput(req *types.Request) bool {
	return anyAggregationSlot(req, HasAggregationVectorInput)
}

// VecAggParams is a vector aggregator's decoded params.
type VecAggParams struct {
	// Labels is params.labels: one display label per member (inline
	// fields only).
	Labels []string `json:"labels,omitempty"`
	// Expand replaces the array value with one scalar column per member,
	// named ExpandedColumn(label, element label). Each is an ordinary
	// nullable f64 output column (Sort, a window, a post-test, a series
	// overlay's params.field and a later chain stage read it); the bare
	// label stays refused.
	Expand bool `json:"expand,omitempty"`
	// Element is params.element: the display label of the ONE member a
	// crosstab cell or margin aggregation reads (SelectsElement). The
	// slot is then a scalar over that member alone — its figure, floor
	// and components are the selected element's — so the crosstab
	// array_valued refusal does not apply. Built-in vector aggregators
	// only (for an extension the key is the operator's own); refused
	// outside a crosstab slot (element_outside_crosstab), beside expand
	// (element_with_expand), naming no member label (element_unknown)
	// or a label two members share (element_ambiguous).
	Element *string `json:"element,omitempty"`
}

// DecodeExtensionVecAggParams decodes the engine's keys of an extension
// vector aggregator's params — `labels` and `expand` — leniently: every
// other key is the operator's own and is left alone. A reserved key of
// the wrong JSON type is bad_params. at is the slot path.
func DecodeExtensionVecAggParams(at string, typ types.AggregationType, raw json.RawMessage) (VecAggParams, *errors.CodedError) {
	var p VecAggParams
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return p, nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return VecAggParams{}, aggregationInvalid(at, "bad_params",
			at+" "+string(typ)+" params: "+err.Error()+" (an object)", nil)
	}
	if v, ok := keys["labels"]; ok {
		if err := json.Unmarshal(v, &p.Labels); err != nil {
			return VecAggParams{}, aggregationInvalid(at, "bad_params",
				at+" "+string(typ)+" params.labels: "+err.Error()+" (a list of strings)", map[string]any{"param": "labels"})
		}
	}
	if v, ok := keys["expand"]; ok {
		if err := json.Unmarshal(v, &p.Expand); err != nil {
			return VecAggParams{}, aggregationInvalid(at, "bad_params",
				at+" "+string(typ)+" params.expand: "+err.Error()+" (a boolean)", map[string]any{"param": "expand"})
		}
	}
	return p, nil
}

// decodeVecAggParams is the slot's engine params: strict for a built-in,
// lenient (DecodeExtensionVecAggParams) for an extension.
func decodeVecAggParams(at string, typ types.AggregationType, raw json.RawMessage, extension bool) (VecAggParams, *errors.CodedError) {
	if extension {
		return DecodeExtensionVecAggParams(at, typ, raw)
	}
	return DecodeVecAggParams(at, typ, raw)
}

// DecodeVecAggParams decodes a vector aggregator's params strictly
// (unknown keys refused, bad_params). at is the slot path.
func DecodeVecAggParams(at string, typ types.AggregationType, raw json.RawMessage) (VecAggParams, *errors.CodedError) {
	var p VecAggParams
	paramread.Declare(paramread.Aggregator, string(typ), &p)
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return p, nil
	}
	if err := paramread.DecodeStrict(paramread.Aggregator, string(typ), raw, &p); err != nil {
		return VecAggParams{}, aggregationInvalid(at, "bad_params",
			at+" "+string(typ)+" params: "+err.Error()+" (keys: labels, expand, element)", nil)
	}
	return p, nil
}

// ResolveAggregations resolves every aggregation slot's vector input
// (AggregationSlots order) against schema (Request.Vectors first, like
// ResolveAttributes). kinds is the instance's view: its Route (a hidden
// type routes to an unknown name) and its extension declarations — an
// extension declared VectorInput resolves like a built-in vector
// aggregator, any other registered extension refuses a vector input.
// The result is keyed by the slot's *types.Aggregation and holds only
// the slots that resolved a vector input; the first refusal is
// returned. Like Resolve it reads only the schema.
// cat is the site's bound-model catalog, read by the windows-first
// decision (ResolveRequest; nil none).
func ResolveAggregations(req *types.Request, schema *encoding.Schema, cat model.Catalog, kinds AggregationKinds) (map[*types.Aggregation]AggregationInput, *errors.CodedError) {
	// Nothing to judge when no slot reads a vector or is a vector
	// aggregator: return before building the slot list (the common
	// request allocates nothing here).
	if !anyAggregationSlot(req, func(a *types.Aggregation) bool {
		return HasAggregationVectorInput(a) || kinds.IsVector(a.Type)
	}) {
		return nil, nil
	}
	slots := AggregationSlots(req)
	resolved, verr := ResolveRequest(req, schema, cat)
	if verr != nil {
		return nil, verr
	}
	var out map[*types.Aggregation]AggregationInput
	for _, s := range slots {
		typ := kinds.Routed(s.Agg.Type)
		builtin := slices.Contains(types.AllAggregationTypes(), typ)
		ext := kinds.extension(typ)
		if !IsVectorAggregation(typ) && !ext.VectorInput {
			if HasAggregationVectorInput(s.Agg) && (builtin || ext.Registered) {
				msg := s.Path + " (" + string(s.Agg.Type) + ") reads field, not a vector; drop vector / fields (" + vectorAggregationList(" / ", " / ") + " aggregate a vector)"
				if ext.Registered {
					msg = s.Path + " (" + string(s.Agg.Type) + ") reads field, not a vector: the extension is not registered with VectorInput; drop vector / fields"
				}
				return nil, aggregationInvalid(s.Path, "vector_not_accepted", msg, map[string]any{"type": string(s.Agg.Type)})
			}
			continue
		}
		in, err := resolveAggregation(s.Path, s.Agg, typ, req.Vectors, resolved, schema, ext)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = map[*types.Aggregation]AggregationInput{}
		}
		out[s.Agg] = in
	}
	return out, nil
}

// resolveAggregation resolves one vector aggregator slot; ext is the
// routed type's extension declaration (zero for a built-in).
func resolveAggregation(at string, a *types.Aggregation, typ types.AggregationType, specs []types.VectorSpec, resolved []Resolved, schema *encoding.Schema, ext ExtensionAggregation) (AggregationInput, *errors.CodedError) {
	name := string(a.Type)
	if a.Field != "" {
		return AggregationInput{}, aggregationInvalid(at, "field_on_vector_aggregation",
			at+" ("+name+") reads its columns from vector or fields, not field",
			map[string]any{"type": name, "field": a.Field})
	}
	switch {
	case a.Vector != "" && len(a.Fields) > 0:
		return AggregationInput{}, aggregationInvalid(at, "vector_and_fields", at+" sets both vector and fields; set exactly one", nil)
	case a.Vector == "" && len(a.Fields) == 0:
		return AggregationInput{}, aggregationInvalid(at, "no_vector_or_fields", at+" ("+name+") sets neither vector nor fields; set exactly one", nil)
	}
	p, err := decodeVecAggParams(at, typ, a.Params, ext.Registered)
	if err != nil {
		return AggregationInput{}, err
	}
	if p.Element != nil {
		if p.Expand {
			return AggregationInput{}, aggregationInvalid(at, "element_with_expand",
				at+" ("+name+") sets both params.element (one element as a scalar crosstab figure) and params.expand (one column per element); set one",
				map[string]any{"type": name, "param": "element"})
		}
		if !strings.HasPrefix(at, "crosstab.") {
			return AggregationInput{}, aggregationInvalid(at, "element_outside_crosstab",
				at+" ("+name+") params.element selects one element for a crosstab cell or margin aggregation; a grouped or ungrouped aggregation writes every element — use params.expand for one column per element",
				map[string]any{"type": name, "param": "element"})
		}
	}
	if p.Expand && ext.Registered && !ext.ArrayValued {
		return AggregationInput{}, aggregationInvalid(at, "expand_not_array_valued",
			at+" ("+name+") params.expand writes one column per element of an array value, and this extension aggregator returns one figure (it is not registered ArrayValued); drop expand",
			map[string]any{"type": name, "param": "expand"})
	}
	var members Resolved
	if a.Vector == "" {
		members, err = ResolveSpec(at+".fields", types.VectorSpec{Fields: a.Fields, Labels: p.Labels}, schema)
	} else {
		if len(p.Labels) > 0 {
			return AggregationInput{}, aggregationInvalid(at, "labels_with_vector",
				at+" params.labels is for inline fields; the vector "+strconv.Quote(a.Vector)+" carries its own labels (Request.Vectors labels)",
				map[string]any{"param": "labels", "vector": a.Vector})
		}
		members, err = findVector(at, "vector", a.Vector, specs, resolved)
	}
	if err != nil {
		return AggregationInput{}, err
	}
	element := -1
	if p.Element != nil {
		for i, l := range members.Labels {
			if l != *p.Element {
				continue
			}
			if element >= 0 {
				return AggregationInput{}, aggregationInvalid(at, "element_ambiguous",
					at+" ("+name+") params.element "+strconv.Quote(*p.Element)+" names members "+strconv.Itoa(element)+" and "+strconv.Itoa(i)+", which share that label; give the members distinct labels",
					map[string]any{"type": name, "param": "element", "element": *p.Element, "members": []int{element, i}})
			}
			element = i
		}
		if element < 0 {
			return AggregationInput{}, aggregationInvalid(at, "element_unknown",
				at+" ("+name+") params.element "+strconv.Quote(*p.Element)+" is not a member label; valid: "+strings.Join(members.Labels, ", "),
				map[string]any{"type": name, "param": "element", "element": *p.Element, "valid": append([]string(nil), members.Labels...)})
		}
	}
	if p.Expand {
		seen := make(map[string]int, len(members.Labels))
		for i, l := range members.Labels {
			if first, dup := seen[l]; dup {
				return AggregationInput{}, aggregationInvalid(at, "expand_duplicate_labels",
					at+" ("+name+") params.expand names one column per member by its display label, and members "+
						strconv.Itoa(first)+" and "+strconv.Itoa(i)+" share the label "+strconv.Quote(l)+"; give the members distinct labels",
					map[string]any{"type": name, "label": l, "members": []int{first, i}})
			}
			seen[l] = i
		}
	}
	return AggregationInput{Slot: at, Members: members, Expand: p.Expand, Extension: ext.Registered, HasElement: element >= 0, Element: max(element, 0)}, nil
}

// SelectsElement reports whether a is a built-in vector aggregation
// (routed under kinds; never an extension, whose params.element is its
// own key) whose params decode and set element: a crosstab slot reading
// ONE member as a scalar. Shape faults (an unknown element, element
// outside a crosstab) are the resolver's; this reads only the params.
func SelectsElement(a *types.Aggregation, kinds AggregationKinds) bool {
	if a == nil {
		return false
	}
	r := kinds.Routed(a.Type)
	if !IsVectorAggregation(r) {
		return false
	}
	p, err := DecodeVecAggParams("aggregation", a.Type, a.Params)
	return err == nil && p.Element != nil
}

// ElementMarginClass is the crosstab margin class of an
// element-selecting slot (SelectsElement): its scalar twin's — the
// selected element of AGG_VEC_MEAN is a weighted mean of one column
// (mean-reducible), of AGG_VEC_SUM a sum (summable). ok is false for
// any other slot, which keeps its type's class — an AGG_VEC_SD element
// included: a standard deviation recomputes its margin over the raw
// rows, like AGG_STDDEV (the type's MarginRecompute). Both crosstab arms'
// fusion facts and predict's normalize advisory read it.
func ElementMarginClass(a *types.Aggregation, kinds AggregationKinds) (types.MarginReducibility, bool) {
	if !SelectsElement(a, kinds) {
		return "", false
	}
	switch kinds.Routed(a.Type) {
	case types.AGG_VEC_MEAN:
		return types.MarginMeanReducible, true
	case types.AGG_VEC_SUM:
		return types.MarginSummable, true
	}
	return "", false
}

// ExpandedColumn is the Response.Data column an expanded vector
// aggregation writes element by element: "<label>[<element label>]".
func ExpandedColumn(label, element string) string {
	return label + "[" + element + "]"
}

// AggregationExpands reports whether a is an array-valued vector
// aggregation (a built-in, or an extension declared VectorInput and
// ArrayValued — kinds) whose params set expand (a params blob that does
// not decode never expands — the resolver refuses it).
func AggregationExpands(a *types.Aggregation, kinds AggregationKinds) bool {
	if a == nil || !kinds.IsVector(a.Type) || !kinds.IsArrayValued(a.Type) {
		return false
	}
	p, err := decodeVecAggParams("aggregation", a.Type, a.Params, kinds.extension(kinds.Routed(a.Type)).Registered)
	return err == nil && p.Expand
}

// AggregationOutputColumns returns the Response.Data columns slot a of
// req emits over schema: its label (types.AggregationLabelOf unless
// Label is set), or — on an expanding vector aggregation
// (AggregationExpands under kinds) — one ExpandedColumn per member in
// member order, the label itself absent.
// The second result is false when an expanding slot's members do not
// resolve (the resolver refuses the request); the label is returned
// then. Predict (return planning, chain stage columns, the per-slot
// columns) and the field-reference walk share it, so they name the
// columns the runtime writes. It reads no windows-first decision: every
// window label is admitted as a computed member (resolveListing), which
// resolves a valid request's vectors exactly as ResolveRequest does
// under any catalog (a schema field still wins), so the listing needs
// no model catalog — a vector naming a window label its windows do not
// put on the records is refused by ResolveRequest at its own site.
func AggregationOutputColumns(req *types.Request, a *types.Aggregation, schema *encoding.Schema, kinds AggregationKinds) ([]string, bool) {
	label := types.AggregationLabelOf(a)
	if !AggregationExpands(a, kinds) {
		return []string{label}, true
	}
	var specs []types.VectorSpec
	if req != nil {
		specs = req.Vectors
	}
	resolved, verr := resolveListing(req, schema)
	if verr != nil || schema == nil {
		return []string{label}, false
	}
	typ := kinds.Routed(a.Type)
	in, err := resolveAggregation("aggregation", a, typ, specs, resolved, schema, kinds.extension(typ))
	if err != nil {
		return []string{label}, false
	}
	out := make([]string, len(in.Members.Labels))
	for i, l := range in.Members.Labels {
		out[i] = ExpandedColumn(label, l)
	}
	return out, true
}

// AggregationColumns is the column lister a series overlay host hands
// seriesfield (seriesfield.Columns): for aggregation i of
// req.Aggregations, the Response.Data columns it writes —
// AggregationOutputColumns over the schema schema returns (called at
// most once, and only for an expanding slot; nil: none). With no
// schema an expanding slot's columns are still known when it carries
// its element labels inline (fields + params.labels — the prepared
// slot AggregationSlotRewrite hands the runtime, whose labels are the
// columns' element labels); otherwise known is false. A slot that does
// not expand writes its label (known). Predict and the runtime build
// it the same way, so an element column resolves identically.
func AggregationColumns(req *types.Request, schema func() *encoding.Schema, kinds AggregationKinds) func(i int) ([]string, bool) {
	var (
		loaded bool
		sch    *encoding.Schema
	)
	return func(i int) ([]string, bool) {
		if req == nil || i < 0 || i >= len(req.Aggregations) || req.Aggregations[i] == nil {
			return nil, true
		}
		a := req.Aggregations[i]
		if !AggregationExpands(a, kinds) {
			return []string{types.AggregationLabelOf(a)}, true
		}
		if !loaded && schema != nil {
			loaded, sch = true, schema()
		}
		if sch != nil {
			cols, ok := AggregationOutputColumns(req, a, sch, kinds)
			if !ok {
				return nil, false
			}
			return cols, true
		}
		if a.Vector != "" || len(a.Fields) == 0 {
			return nil, false
		}
		p, err := decodeVecAggParams("aggregation", a.Type, a.Params, kinds.extension(kinds.Routed(a.Type)).Registered)
		if err != nil || len(p.Labels) == 0 {
			return nil, false
		}
		label := types.AggregationLabelOf(a)
		out := make([]string, len(p.Labels))
		for k, l := range p.Labels {
			out[k] = ExpandedColumn(label, l)
		}
		return out, true
	}
}

// AggregationSlotRewrite is the slot a vector aggregator's factory
// receives: a copy with Label the default label when empty (computed
// from the slot as written), Vector cleared, Fields = the resolved
// literal members and params.labels = the resolved display labels — so
// a named vector's labels survive the literal-member rewrite. An
// extension's slot keeps every other params key (the operator's own).
// The caller's slot is never mutated.
func AggregationSlotRewrite(a *types.Aggregation, in AggregationInput) *types.Aggregation {
	cp := *a
	if cp.Label == "" {
		cp.Label = types.AggregationLabelOf(a)
	}
	cp.Vector = ""
	cp.Fields = append([]string(nil), in.Members.Members...)
	labels := in.Members.Labels
	if in.HasElement && in.Element < len(in.Members.Members) && !in.Extension {
		// An element-selecting crosstab slot reads its one member only:
		// the factory folds it alone and the floor (AggregationPresent)
		// counts the rows that answered it.
		cp.Fields = []string{in.Members.Members[in.Element]}
		if in.Element < len(labels) {
			labels = []string{labels[in.Element]}
		}
	}
	if in.Extension {
		keys := map[string]json.RawMessage{}
		if raw := bytes.TrimSpace(a.Params); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
			if json.Unmarshal(raw, &keys) != nil {
				return &cp
			}
		}
		if raw, err := json.Marshal(in.Members.Labels); err == nil {
			keys["labels"] = raw
		}
		if raw, err := json.Marshal(keys); err == nil {
			cp.Params = raw
		}
		return &cp
	}
	p, err := DecodeVecAggParams(in.Slot, a.Type, a.Params)
	if err == nil {
		p.Labels = append([]string(nil), labels...)
		if raw, merr := json.Marshal(p); merr == nil {
			cp.Params = raw
		}
	}
	return &cp
}

// AggregationInputMembers returns the union of every field req's
// aggregation slots read through inline fields (a named vector's
// members ride Members(req)), and whether every one resolved —
// projection decodes wide otherwise.
func AggregationInputMembers(req *types.Request, schema *encoding.Schema) (members []string, ok bool) {
	ok = true
	if !anyAggregationSlot(req, func(a *types.Aggregation) bool { return len(a.Fields) > 0 }) {
		return nil, ok
	}
	for _, s := range AggregationSlots(req) {
		if len(s.Agg.Fields) == 0 {
			continue
		}
		r, err := ResolveSpec(s.Path+".fields", types.VectorSpec{Fields: s.Agg.Fields}, schema)
		if err != nil {
			ok = false
			continue
		}
		members = append(members, r.SourceFields()...)
	}
	return members, ok
}

// ArrayValuedCrosstabRefusal is the crosstab gate for an array-valued
// aggregator (AggregationKinds.IsArrayValued — a built-in or a declared
// ArrayValued extension) as the cell or a margin aggregation:
// PROCESSING_CONFIG {aggregation, slot, reason: "array_valued"},
// whatever normalize says — a cell holds one number. The one exception
// is a slot selecting one element (SelectsElement: a built-in AGG_VEC_*
// with params.element), which is a scalar.
// kinds is the instance's view; nil when no slot is array-valued.
// processing.validateCrosstabSpec (both arms) and predict call it, so
// they refuse identically.
func ArrayValuedCrosstabRefusal(spec *types.CrosstabSpec, kinds AggregationKinds) *errors.CodedError {
	if spec == nil {
		return nil
	}
	check := func(slot string, a *types.Aggregation) *errors.CodedError {
		if a == nil || !kinds.IsArrayValued(a.Type) || SelectsElement(a, kinds) {
			return nil
		}
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"crosstab "+slot+" "+string(a.Type)+" is array-valued (one figure per vector member); a crosstab cell holds one number — select one element with params.element (built-in AGG_VEC_*), or run it as a grouped aggregation",
			map[string]any{"aggregation": string(a.Type), "slot": "crosstab." + slot, "reason": "array_valued"})
	}
	if spec.HasMeasures() {
		for i, a := range spec.Cells {
			if err := check("cells["+strconv.Itoa(i)+"]", a); err != nil {
				return err
			}
		}
	} else if err := check("cell", spec.Cell); err != nil {
		return err
	}
	for i, a := range spec.MarginAggregations {
		if err := check("margin_aggregations["+strconv.Itoa(i)+"]", a); err != nil {
			return err
		}
	}
	return nil
}

func aggregationInvalid(at, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"aggregation": at, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg, details)
}

package vectors

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
//     count is PULSE_VECTOR_LABELS_MISMATCH.
//   - Any OTHER aggregator — built-in or a registered extension —
//     refuses a vector input (vector_not_accepted): the extension
//     aggregator contract reads one field.
//   - The default output label is types.AggregationLabelOf:
//     "<TYPE>_<vector>", or "<TYPE>_<first fields entry as written>".
//
// Shape refusals are SERVICE_VALIDATION naming `aggregation` (the slot
// path) and `reason`. A vector aggregator's array value is refused
// downstream wherever one number is needed (types.AggregationType.
// ArrayValued): ArrayValuedCrosstabRefusal here, Sort and request
// overlays in the field-reference walk, a feeding chain stage in
// internal/mergegate.

// VectorAggregationTypes returns the built-in aggregation types that
// read their columns from Vector / Fields, in AllAggregationTypes order.
func VectorAggregationTypes() []types.AggregationType {
	return []types.AggregationType{types.AGG_VEC_MEAN, types.AGG_VEC_SUM}
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
		if ct.Cell != nil {
			out = append(out, AggregationSlot{Path: "crosstab.cell", Agg: ct.Cell})
		}
		for i, a := range ct.MarginAggregations {
			if a != nil {
				out = append(out, AggregationSlot{Path: "crosstab.margin_aggregations[" + strconv.Itoa(i) + "]", Agg: a})
			}
		}
	}
	return out
}

// VecAggParams is a vector aggregator's decoded params.
type VecAggParams struct {
	// Labels is params.labels: one display label per member (inline
	// fields only).
	Labels []string `json:"labels,omitempty"`
}

// DecodeVecAggParams decodes a vector aggregator's params strictly
// (unknown keys refused, bad_params). at is the slot path.
func DecodeVecAggParams(at string, typ types.AggregationType, raw json.RawMessage) (VecAggParams, *errors.CodedError) {
	var p VecAggParams
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return p, nil
	}
	if err := decodeStrict(raw, &p); err != nil {
		return VecAggParams{}, aggregationInvalid(at, "bad_params",
			at+" "+string(typ)+" params: "+err.Error()+" (keys: labels)", nil)
	}
	return p, nil
}

// ResolveAggregations resolves every aggregation slot's vector input
// (AggregationSlots order) against schema (Request.Vectors first, like
// ResolveAttributes). route maps an aggregation type to the built-in
// it executes as — the instance's view, where a hidden type routes to
// an unknown name; nil is the identity. ext reports whether a routed
// type that is not a built-in is a registered extension aggregator
// (its vector input is then refused); nil means none is. The result is
// keyed by the slot's *types.Aggregation and holds only the slots that
// resolved a vector input; the first refusal is returned. Like Resolve
// it reads only the schema.
func ResolveAggregations(req *types.Request, schema *encoding.Schema, route func(types.AggregationType) types.AggregationType, ext func(string) bool) (map[*types.Aggregation]AggregationInput, *errors.CodedError) {
	slots := AggregationSlots(req)
	if len(slots) == 0 {
		return nil, nil
	}
	if route == nil {
		route = func(t types.AggregationType) types.AggregationType { return t }
	}
	resolved, verr := Resolve(req.Vectors, schema)
	if verr != nil {
		return nil, verr
	}
	var out map[*types.Aggregation]AggregationInput
	for _, s := range slots {
		typ := route(s.Agg.Type)
		builtin := slices.Contains(types.AllAggregationTypes(), typ)
		if !IsVectorAggregation(typ) {
			if HasAggregationVectorInput(s.Agg) && (builtin || isExtension(ext, string(typ), builtin)) {
				return nil, aggregationInvalid(s.Path, "vector_not_accepted",
					s.Path+" ("+string(s.Agg.Type)+") reads field, not a vector; drop vector / fields (AGG_VEC_MEAN / AGG_VEC_SUM aggregate a vector)",
					map[string]any{"type": string(s.Agg.Type)})
			}
			continue
		}
		in, err := resolveAggregation(s.Path, s.Agg, typ, req.Vectors, resolved, schema)
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

// resolveAggregation resolves one vector aggregator slot.
func resolveAggregation(at string, a *types.Aggregation, typ types.AggregationType, specs []types.VectorSpec, resolved []Resolved, schema *encoding.Schema) (AggregationInput, *errors.CodedError) {
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
	p, err := DecodeVecAggParams(at, typ, a.Params)
	if err != nil {
		return AggregationInput{}, err
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
	return AggregationInput{Slot: at, Members: members}, nil
}

// AggregationSlotRewrite is the slot a vector aggregator's factory
// receives: a copy with Label the default label when empty (computed
// from the slot as written), Vector cleared, Fields = the resolved
// literal members and params.labels = the resolved display labels — so
// a named vector's labels survive the literal-member rewrite. The
// caller's slot is never mutated.
func AggregationSlotRewrite(a *types.Aggregation, in AggregationInput) *types.Aggregation {
	cp := *a
	if cp.Label == "" {
		cp.Label = types.AggregationLabelOf(a)
	}
	cp.Vector = ""
	cp.Fields = append([]string(nil), in.Members.Members...)
	p, err := DecodeVecAggParams(in.Slot, a.Type, a.Params)
	if err == nil {
		p.Labels = append([]string(nil), in.Members.Labels...)
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
	for _, s := range AggregationSlots(req) {
		if len(s.Agg.Fields) == 0 {
			continue
		}
		r, err := ResolveSpec(s.Path+".fields", types.VectorSpec{Fields: s.Agg.Fields}, schema)
		if err != nil {
			ok = false
			continue
		}
		members = append(members, r.Members...)
	}
	return members, ok
}

// ArrayValuedCrosstabRefusal is the unconditional crosstab gate for an
// array-valued aggregator (types.AggregationType.ArrayValued) as the
// cell or a margin aggregation: PROCESSING_CONFIG {aggregation, slot,
// reason: "array_valued"}, whatever normalize says — a cell holds one
// number. route is the instance's view (nil: identity); nil when no
// slot is array-valued. processing.validateCrosstabSpec (both arms) and
// predict call it, so they refuse identically.
func ArrayValuedCrosstabRefusal(spec *types.CrosstabSpec, route func(types.AggregationType) types.AggregationType) *errors.CodedError {
	if spec == nil {
		return nil
	}
	if route == nil {
		route = func(t types.AggregationType) types.AggregationType { return t }
	}
	check := func(slot string, a *types.Aggregation) *errors.CodedError {
		if a == nil || !route(a.Type).ArrayValued() {
			return nil
		}
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"crosstab "+slot+" "+string(a.Type)+" is array-valued (one figure per vector member); a crosstab cell holds one number — run it as a grouped aggregation instead",
			map[string]any{"aggregation": string(a.Type), "slot": "crosstab." + slot, "reason": "array_valued"})
	}
	if err := check("cell", spec.Cell); err != nil {
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

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

// Vector-input attributes — the types.Attribute Vector / Fields slots,
// resolved by the one leaf predict (the field-reference walk), the
// runtime and projection all call (the Test.Vector / Fields mirror).
// They are the FITTED attributes: a fit pass folds one co-moment over
// the filtered rows params.fit_on admits, then every filtered row is
// scored.
//
// Rules (the contract), per attribute slot "attributes[i]":
//
//   - A built-in vector-input type (VectorAttributeTypes) reads exactly
//     one of vector and fields; field is refused (it reads no scalar
//     column), and so are both or neither of vector and fields.
//   - A vector no Request.Vectors entry defines is PULSE_VECTOR_UNKNOWN
//     (`vector`, `slot`, `defined`); inline fields resolve by the vector
//     rules (ResolveSpec at "attributes[i].fields"). Members are source
//     columns only, never an earlier attribute's label.
//   - Params (strict: unknown keys refused) share fit_on and missing
//     (FittedCommon): fit_on is "filtered" (the default) or a non-empty
//     inline array of Filterer objects; missing is "listwise" (the only
//     mode; "pairwise" refused). An operator adds its own keys
//     (ATTR_MAHALANOBIS: MahalanobisParams — output "d2" | "p").
//   - Any OTHER built-in attribute refuses a vector input. An extension
//     attribute's slots are its own business and are not judged.
//
// Shape refusals are SERVICE_VALIDATION naming `attribute` (the slot
// path) and `reason`.

// VectorAttributeTypes returns the built-in attribute types that read
// their columns from Vector / Fields, in AllAttributeTypes order.
func VectorAttributeTypes() []types.AttributeType {
	return []types.AttributeType{types.ATTR_MAHALANOBIS}
}

// IsVectorAttribute reports whether t is a built-in vector-input
// attribute type.
func IsVectorAttribute(t types.AttributeType) bool {
	return slices.Contains(VectorAttributeTypes(), t)
}

// HasAttributeVectorInput reports whether a sets any vector-input slot.
func HasAttributeVectorInput(a *types.Attribute) bool {
	return a != nil && (a.Vector != "" || len(a.Fields) > 0)
}

// VectorAttributeLabel is the default output label of a vector-input
// attribute whose route is typ: "<TYPE>_<vector>", or
// "<TYPE>_<first fields entry as written>" for inline fields. ok is
// false for any other type (the caller's own label rule applies).
func VectorAttributeLabel(typ types.AttributeType, a *types.Attribute) (string, bool) {
	if a == nil || !IsVectorAttribute(typ) {
		return "", false
	}
	if a.Vector != "" {
		return string(a.Type) + "_" + a.Vector, true
	}
	first := ""
	if len(a.Fields) > 0 {
		first = a.Fields[0]
	}
	return string(a.Type) + "_" + first, true
}

// AttributeInput is one attribute slot's vector input after
// resolution. The zero value (Has false) is an attribute that reads no
// vector.
type AttributeInput struct {
	// Members is the member set (axis order).
	Members Resolved
	// FitOn is the decoded params.fit_on filterer list; nil fits on every
	// filtered row ("filtered").
	FitOn []*types.Filterer
}

// Has reports whether the slot resolved a vector input.
func (in AttributeInput) Has() bool { return len(in.Members.Members) > 0 }

// ResolveAttributes resolves every attribute's vector input against
// schema (Request.Vectors first, like ResolveTests). route maps an
// attribute type to the built-in it executes as — the instance's view,
// where a hidden type routes to an unknown name; nil is the identity.
// The result is index-aligned with req.Attributes (the zero
// AttributeInput for an attribute that reads no vector); the first
// refusal is returned. Like Resolve it reads only the schema.
func ResolveAttributes(req *types.Request, schema *encoding.Schema, route func(types.AttributeType) types.AttributeType) ([]AttributeInput, *errors.CodedError) {
	if req == nil || len(req.Attributes) == 0 {
		return nil, nil
	}
	if route == nil {
		route = func(t types.AttributeType) types.AttributeType { return t }
	}
	resolved, verr := Resolve(req.Vectors, schema)
	if verr != nil {
		return nil, verr
	}
	out := make([]AttributeInput, len(req.Attributes))
	for i, a := range req.Attributes {
		if a == nil {
			continue
		}
		in, err := resolveAttribute(attributePath(i), a, route(a.Type), req.Vectors, resolved, schema)
		if err != nil {
			return nil, err
		}
		out[i] = in
	}
	return out, nil
}

// resolveAttribute resolves one slot whose built-in route is typ.
func resolveAttribute(at string, a *types.Attribute, typ types.AttributeType, specs []types.VectorSpec, resolved []Resolved, schema *encoding.Schema) (AttributeInput, *errors.CodedError) {
	name := string(a.Type)
	if !IsVectorAttribute(typ) {
		if HasAttributeVectorInput(a) && slices.Contains(types.AllAttributeTypes(), typ) {
			return AttributeInput{}, attributeInvalid(at, "vector_not_accepted",
				at+" ("+name+") reads field columns, not a vector; drop vector / fields",
				map[string]any{"type": name})
		}
		return AttributeInput{}, nil
	}
	if a.Field != "" {
		return AttributeInput{}, attributeInvalid(at, "field_on_vector_attribute",
			at+" ("+name+") reads its columns from vector or fields, not field",
			map[string]any{"type": name, "field": a.Field})
	}
	switch {
	case a.Vector != "" && len(a.Fields) > 0:
		return AttributeInput{}, attributeInvalid(at, "vector_and_fields", at+" sets both vector and fields; set exactly one", nil)
	case a.Vector == "" && len(a.Fields) == 0:
		return AttributeInput{}, attributeInvalid(at, "no_vector_or_fields", at+" ("+name+") sets neither vector nor fields; set exactly one", nil)
	}
	fitOn, err := fittedParams(at, typ, a.Params)
	if err != nil {
		return AttributeInput{}, err
	}
	var members Resolved
	if a.Vector == "" {
		members, err = ResolveSpec(at+".fields", types.VectorSpec{Fields: a.Fields}, schema)
	} else {
		members, err = findVector(at, "vector", a.Vector, specs, resolved)
	}
	if err != nil {
		return AttributeInput{}, err
	}
	return AttributeInput{Members: members, FitOn: fitOn}, nil
}

// findVector looks up a named vector, PULSE_VECTOR_UNKNOWN when no
// Request.Vectors entry defines it.
func findVector(at, key, vector string, specs []types.VectorSpec, resolved []Resolved) (Resolved, *errors.CodedError) {
	r, ok := Find(resolved, vector)
	if ok {
		return r, nil
	}
	defined := make([]string, 0, len(specs))
	for _, v := range specs {
		defined = append(defined, v.Name)
	}
	return Resolved{}, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_UNKNOWN,
		at+" names vector "+strconv.Quote(vector)+", which no vectors entry defines",
		map[string]any{"vector": vector, "slot": at + "." + key, "defined": defined})
}

// fittedParams judges typ's params and returns its decoded fit_on.
func fittedParams(at string, typ types.AttributeType, raw json.RawMessage) ([]*types.Filterer, *errors.CodedError) {
	switch typ {
	case types.ATTR_MAHALANOBIS:
		cfg, err := MahalanobisParams(at, raw)
		return cfg.FitOn, err
	}
	return nil, nil
}

// FitOnFiltered is params.fit_on's default: fit on every filtered row.
const FitOnFiltered = "filtered"

// FittedCommon is the params every fitted vector attribute shares;
// an operator's params struct embeds it beside its own keys, so one
// strict decode refuses every unknown key.
type FittedCommon struct {
	// FitOn is "filtered" (default) or an inline Filterer array.
	FitOn json.RawMessage `json:"fit_on"`
	// Missing is "listwise" (the only mode).
	Missing *string `json:"missing"`
}

// resolve judges missing and decodes fit_on: nil for "filtered" or
// absent, else the non-empty filterer list.
func (c FittedCommon) resolve(at string) ([]*types.Filterer, *errors.CodedError) {
	if c.Missing != nil && *c.Missing != MissingListwise {
		reason, msg := "bad_params", at+" params.missing must be \"listwise\""
		if *c.Missing == MissingPairwise {
			reason = "pairwise_unsupported"
			msg = at + " params.missing \"pairwise\" is not supported on a fitted attribute: the fit needs one complete-case sample; use \"listwise\""
		}
		return nil, attributeInvalid(at, reason, msg,
			map[string]any{"param": "missing", "value": *c.Missing, "valid": []string{MissingListwise}})
	}
	raw := bytes.TrimSpace(c.FitOn)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s == FitOnFiltered {
			return nil, nil
		}
		return nil, attributeInvalid(at, "bad_fit_on",
			at+" params.fit_on must be \"filtered\" or an array of filterers",
			map[string]any{"param": "fit_on", "value": string(raw)})
	}
	var fs []*types.Filterer
	if err := decodeStrict(raw, &fs); err != nil {
		return nil, attributeInvalid(at, "bad_fit_on",
			at+" params.fit_on must be \"filtered\" or an array of filterers: "+err.Error(),
			map[string]any{"param": "fit_on"})
	}
	if len(fs) == 0 {
		return nil, attributeInvalid(at, "bad_fit_on",
			at+" params.fit_on is an empty filterer list; omit it (or use \"filtered\") to fit on every filtered row",
			map[string]any{"param": "fit_on"})
	}
	for j, f := range fs {
		if f == nil || f.Type == "" {
			return nil, attributeInvalid(at, "bad_fit_on",
				at+" params.fit_on["+strconv.Itoa(j)+"] needs a filterer type",
				map[string]any{"param": "fit_on", "index": j})
		}
	}
	return fs, nil
}

// ATTR_MAHALANOBIS params.output values.
const (
	// MahalanobisD2 is the default: the squared distance D².
	MahalanobisD2 = "d2"
	// MahalanobisP is the χ² upper tail of D² on p degrees of freedom.
	MahalanobisP = "p"
)

// MahalanobisConfig is ATTR_MAHALANOBIS's decoded params.
type MahalanobisConfig struct {
	// Output is MahalanobisD2 (default) or MahalanobisP.
	Output string
	// FitOn is the fit_on filterer list; nil fits on every filtered row.
	FitOn []*types.Filterer
}

// MahalanobisParams decodes ATTR_MAHALANOBIS's params: output ("d2"
// default, or "p"), fit_on and missing (FittedCommon). Unknown keys are
// refused. at is the slot path. Predict and the runtime factory both
// call it.
func MahalanobisParams(at string, raw json.RawMessage) (MahalanobisConfig, *errors.CodedError) {
	cfg := MahalanobisConfig{Output: MahalanobisD2}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return cfg, nil
	}
	var params struct {
		FittedCommon
		Output *string `json:"output"`
	}
	if err := decodeStrict(raw, &params); err != nil {
		return MahalanobisConfig{}, attributeInvalid(at, "bad_params",
			at+" ATTR_MAHALANOBIS params: "+err.Error()+" (keys: output, fit_on, missing)", nil)
	}
	fitOn, err := params.resolve(at)
	if err != nil {
		return MahalanobisConfig{}, err
	}
	cfg.FitOn = fitOn
	if params.Output != nil {
		switch v := *params.Output; v {
		case MahalanobisD2, MahalanobisP:
			cfg.Output = v
		default:
			return MahalanobisConfig{}, attributeInvalid(at, "bad_params", at+" params.output must be \"d2\" or \"p\"",
				map[string]any{"param": "output", "value": v, "valid": []string{MahalanobisD2, MahalanobisP}})
		}
	}
	return cfg, nil
}

// AttributeFitOn returns a's decoded params.fit_on filterers when a's
// route typ is a vector-input attribute (nil for "filtered", a non-vector
// type, or params that do not decode — those are refused elsewhere).
// Projection, the field-reference walk and the two-pass planner read
// it to learn which columns the fit predicate reads.
func AttributeFitOn(typ types.AttributeType, a *types.Attribute) []*types.Filterer {
	if a == nil || !IsVectorAttribute(typ) {
		return nil
	}
	fitOn, err := fittedParams(attributePath(0), typ, a.Params)
	if err != nil {
		return nil
	}
	return fitOn
}

// AttributeInputMembers returns the union of every field req's
// attributes read through inline fields (a named vector's members ride
// Members(req)), and whether every one resolved — projection decodes
// wide otherwise.
func AttributeInputMembers(req *types.Request, schema *encoding.Schema) (members []string, ok bool) {
	ok = true
	if req == nil {
		return nil, true
	}
	for i, a := range req.Attributes {
		if a == nil || len(a.Fields) == 0 {
			continue
		}
		r, err := ResolveSpec(attributePath(i)+".fields", types.VectorSpec{Fields: a.Fields}, schema)
		if err != nil {
			ok = false
			continue
		}
		members = append(members, r.Members...)
	}
	return members, ok
}

func attributePath(i int) string { return "attributes[" + strconv.Itoa(i) + "]" }

func attributeInvalid(at, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"attribute": at, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg, details)
}

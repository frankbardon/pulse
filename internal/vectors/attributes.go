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
// Most are FITTED attributes (FittedAttributeTypes): a fit pass folds
// one co-moment over the filtered rows params.fit_on admits, then every
// filtered row is scored. ATTR_SCALE_SCORE is row-local: each row's
// score reads only its own members (no fit, no fit_on). The
// reference-comparison pair (compare.go: ATTR_PROFILE_SIMILARITY,
// ATTR_VEC_DISTANCE) is row-local over a row-local metric; an
// ATTR_VEC_DISTANCE over std_euclidean / mahalanobis fits like
// ATTR_MAHALANOBIS (ComparisonFitted, per slot).
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
//   - ATTR_SCALE_SCORE's params (ScaleScoreParams: stat, min_valid,
//     reverse by member label, scale_min / scale_max with the kind
//     "scale" vector's bounds as fallback) are judged once the members
//     resolve, and so are the comparison pair's (ComparisonParams:
//     metric, min_valid, reference, labels, fit_on beside a fitted
//     metric; the table checks are ComparisonTableIssue's).
//   - A fitted type's params (strict: unknown keys refused) share
//     fit_on and missing (FittedCommon): fit_on is "filtered" (the default) or a non-empty
//     inline array of Filterer objects; missing is "listwise" (the only
//     mode; "pairwise" refused). An operator adds its own keys
//     (ATTR_MAHALANOBIS: MahalanobisParams — output "d2" | "p";
//     ATTR_PC_SCORE: PCScoreParams — component, on, rotation,
//     epsilon, components; ATTR_FACTOR_SCORE: FactorScoreParams —
//     factor, method and MAT_FACTOR's factors, extraction, rotation,
//     kappa, gamma, epsilon; the member-count bounds judged once the members resolve).
//   - Any OTHER built-in attribute refuses a vector input.
//   - A registered extension attribute (ext) that sets a vector input
//     has its member set judged by the same member rules: vector XOR
//     fields (vector_and_fields), the named vector (PULSE_VECTOR_UNKNOWN)
//     and the inline fields (ResolveSpec: existence, numeric type,
//     duplicates). field beside a vector and params are its own
//     business. Its factory, like a built-in's, receives the resolved
//     literal members.
//
// Shape refusals are SERVICE_VALIDATION naming `attribute` (the slot
// path) and `reason`.

// VectorAttributeTypes returns the built-in attribute types that read
// their columns from Vector / Fields, in AllAttributeTypes order.
func VectorAttributeTypes() []types.AttributeType {
	return []types.AttributeType{types.ATTR_FACTOR_SCORE, types.ATTR_MAHALANOBIS, types.ATTR_PC_SCORE,
		types.ATTR_PROFILE_SIMILARITY, types.ATTR_SCALE_SCORE, types.ATTR_VEC_DISTANCE}
}

// FittedAttributeTypes returns the vector-input attribute types that
// fit before they score (two-pass, params.fit_on), in AllAttributeTypes
// order: every VectorAttributeTypes entry but the row-local
// ATTR_SCALE_SCORE and the comparison pair (ATTR_VEC_DISTANCE fits per
// slot, by its metric: ComparisonFitted).
func FittedAttributeTypes() []types.AttributeType {
	return []types.AttributeType{types.ATTR_FACTOR_SCORE, types.ATTR_MAHALANOBIS, types.ATTR_PC_SCORE}
}

// IsVectorAttribute reports whether t is a built-in vector-input
// attribute type.
func IsVectorAttribute(t types.AttributeType) bool {
	return slices.Contains(VectorAttributeTypes(), t)
}

// IsFittedAttribute reports whether t is a built-in fitted vector
// attribute type (FittedAttributeTypes).
func IsFittedAttribute(t types.AttributeType) bool {
	return slices.Contains(FittedAttributeTypes(), t)
}

// HasAttributeVectorInput reports whether a sets any vector-input slot.
func HasAttributeVectorInput(a *types.Attribute) bool {
	return a != nil && (a.Vector != "" || len(a.Fields) > 0)
}

// VectorAttributeLabel is the default output label of a vector-input
// attribute whose route is typ: "<TYPE>_<vector>", or
// "<TYPE>_<first fields entry as written>" for inline fields, and on
// ATTR_PC_SCORE a "_PC<component>" suffix (when params.component
// decodes) and on ATTR_FACTOR_SCORE a "_F<factor>" suffix (when
// params.factor decodes) so two components or factors of one vector
// never share a label. ok is false for any other type (the caller's own
// label rule applies).
func VectorAttributeLabel(typ types.AttributeType, a *types.Attribute) (string, bool) {
	if a == nil || !IsVectorAttribute(typ) {
		return "", false
	}
	suffix := ""
	switch typ {
	case types.ATTR_PC_SCORE:
		if cfg, err := PCScoreParams("", a.Params); err == nil {
			suffix = "_PC" + strconv.Itoa(cfg.Component)
		}
	case types.ATTR_FACTOR_SCORE:
		if cfg, err := FactorScoreParams("", a.Params); err == nil {
			suffix = "_F" + strconv.Itoa(cfg.Factor)
		}
	}
	if a.Vector != "" {
		return string(a.Type) + "_" + a.Vector + suffix, true
	}
	first := ""
	if len(a.Fields) > 0 {
		first = a.Fields[0]
	}
	return string(a.Type) + "_" + first + suffix, true
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
// ext reports whether a routed type that is not a built-in is a
// registered extension attribute (its member set is then judged); nil
// means none is. The result is index-aligned with req.Attributes (the
// zero AttributeInput for an attribute that reads no vector); the first
// refusal is returned. Like Resolve it reads only the schema.
func ResolveAttributes(req *types.Request, schema *encoding.Schema, route func(types.AttributeType) types.AttributeType, ext func(string) bool) ([]AttributeInput, *errors.CodedError) {
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
		typ := route(a.Type)
		var in AttributeInput
		var err *errors.CodedError
		if isExtension(ext, string(typ), slices.Contains(types.AllAttributeTypes(), typ)) {
			var m Resolved
			m, err = resolveExtensionMembers(attributePath(i), a.Vector, a.Fields, req.Vectors, resolved, schema, attributeInvalid)
			in = AttributeInput{Members: m}
		} else {
			in, err = resolveAttribute(attributePath(i), a, typ, req.Vectors, resolved, schema)
		}
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
	if err := fittedMemberBounds(at, typ, a.Params, len(members.Members)); err != nil {
		return AttributeInput{}, err
	}
	if IsFittedAttribute(typ) {
		if _, err := FittedLabels(at, typ, a.Params, members); err != nil {
			return AttributeInput{}, err
		}
	}
	if typ == types.ATTR_SCALE_SCORE {
		if _, err := ScaleScoreParams(at, a.Params, members); err != nil {
			return AttributeInput{}, err
		}
	}
	if IsComparisonAttribute(typ) {
		cfg, err := ComparisonParams(at, typ, a.Params, members, schema)
		if err != nil {
			return AttributeInput{}, err
		}
		fitOn = cfg.FitOn
	}
	return AttributeInput{Members: members, FitOn: fitOn}, nil
}

// resolveExtensionMembers resolves a registered extension attribute's
// or grouper's member set by the built-in member rules: vector XOR
// fields, the named vector, the inline fields. A slot that sets neither
// reads none (the zero Resolved). invalid builds the category's shape
// refusal.
func resolveExtensionMembers(at, vector string, fields []string, specs []types.VectorSpec, resolved []Resolved, schema *encoding.Schema,
	invalid func(at, reason, msg string, extra map[string]any) *errors.CodedError) (Resolved, *errors.CodedError) {
	switch {
	case vector == "" && len(fields) == 0:
		return Resolved{}, nil
	case vector != "" && len(fields) > 0:
		return Resolved{}, invalid(at, "vector_and_fields", at+" sets both vector and fields; set exactly one", nil)
	case vector == "":
		return ResolveSpec(at+".fields", types.VectorSpec{Fields: fields}, schema)
	}
	return findVector(at, "vector", vector, specs, resolved)
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
	case types.ATTR_PC_SCORE:
		cfg, err := PCScoreParams(at, raw)
		return cfg.FitOn, err
	case types.ATTR_FACTOR_SCORE:
		cfg, err := FactorScoreParams(at, raw)
		return cfg.FitOn, err
	}
	return nil, nil
}

// comparisonFitOn decodes an ATTR_VEC_DISTANCE slot's params.fit_on
// alone (loose: the other keys are ComparisonParams' to judge); nil for
// any other type, "filtered" or absent.
func comparisonFitOn(typ types.AttributeType, raw json.RawMessage) []*types.Filterer {
	if typ != types.ATTR_VEC_DISTANCE {
		return nil
	}
	var c FittedCommon
	if json.Unmarshal(raw, &c) != nil {
		return nil
	}
	c.Missing = nil
	fitOn, err := c.resolve("")
	if err != nil {
		return nil
	}
	return fitOn
}

// fittedMemberBounds judges typ's member-count params against the p
// resolved members (fittedParams has accepted raw).
func fittedMemberBounds(at string, typ types.AttributeType, raw json.RawMessage, p int) *errors.CodedError {
	switch typ {
	case types.ATTR_PC_SCORE:
		cfg, err := PCScoreParams(at, raw)
		if err != nil {
			return err
		}
		return cfg.CheckMembers(at, p)
	case types.ATTR_FACTOR_SCORE:
		cfg, err := FactorScoreParams(at, raw)
		if err != nil {
			return err
		}
		return cfg.CheckMembers(at, p)
	}
	return nil
}

// FitOnFiltered is params.fit_on's default: fit on every filtered row.
const FitOnFiltered = "filtered"

// FittedCommon is the params every fitted vector attribute shares;
// an operator's params struct embeds it beside its own keys, so one
// strict decode refuses every unknown key.
type FittedCommon struct {
	// FitOn is "filtered" (default) or an inline Filterer array.
	FitOn json.RawMessage `json:"fit_on"`
	// Fit names a bound vector fit to score against instead of fitting
	// (mutually exclusive with FitOn).
	Fit *string `json:"fit"`
	// Missing is "listwise" (the only mode).
	Missing *string `json:"missing"`
}

// fitOnWritten reports whether params.fit_on is present (any value but
// JSON null).
func (c FittedCommon) fitOnWritten() bool {
	raw := bytes.TrimSpace(c.FitOn)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null"))
}

// resolveFit judges params.fit: "" when absent; a non-empty bound name
// otherwise. fit and fit_on are mutually exclusive — a bound fit is
// already fitted, so a fit population has nothing to select
// (reason fit_and_fit_on).
func (c FittedCommon) resolveFit(at string) (string, *errors.CodedError) {
	if c.Fit == nil {
		return "", nil
	}
	if *c.Fit == "" {
		return "", attributeInvalid(at, "bad_fit",
			at+" params.fit must name a bound vector fit",
			map[string]any{"param": "fit"})
	}
	if c.fitOnWritten() {
		return "", attributeInvalid(at, "fit_and_fit_on",
			at+" sets both params.fit and params.fit_on; set exactly one: fit scores against the bound vector fit "+strconv.Quote(*c.Fit)+", fit_on fits on this request's rows",
			map[string]any{"param": "fit", "fit": *c.Fit})
	}
	return *c.Fit, nil
}

// fittedLabels is the params.labels key the non-comparison fitted
// attributes take (a comparison slot's own params carry it): the labels
// a bound vector fit (params.fit) aligns to.
type fittedLabels struct {
	Labels []string `json:"labels"`
}

// FittedLabels judges a fitted attribute slot's params.labels against
// its resolved members (vector the named vector, "" for inline fields)
// and returns the labels a bound vector fit aligns to: params.labels
// (inline fields only, one unique non-empty label per member), else the
// members' labels (a named vector's own, the member names for inline
// fields). Comparison slots judge theirs in ComparisonParams.
func FittedLabels(at string, typ types.AttributeType, raw json.RawMessage, members Resolved) ([]string, *errors.CodedError) {
	var params fittedLabels
	if raw = bytes.TrimSpace(raw); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		_ = json.Unmarshal(raw, &params) // strictness is the operator decode's
	}
	return slotLabels(at, params.Labels, members)
}

// slotLabels is the one labels rule a comparison or fitted slot shares:
// explicit labels over inline fields (one unique non-empty label per
// member; refused beside a named vector), else the members' own labels
// (the member names when the members carry none).
func slotLabels(at string, explicit []string, members Resolved) ([]string, *errors.CodedError) {
	p := len(members.Members)
	labels := members.Labels
	if len(labels) != p {
		labels = members.Members
	}
	if explicit == nil {
		return labels, nil
	}
	if members.Name != "" {
		return nil, attributeInvalid(at, "labels_on_vector",
			at+" params.labels names inline fields' elements; vector "+strconv.Quote(members.Name)+" carries its own labels",
			map[string]any{"param": "labels", "vector": members.Name})
	}
	if len(explicit) != p {
		return nil, attributeInvalid(at, "bad_params",
			at+" params.labels must name each of the "+strconv.Itoa(p)+" members once",
			map[string]any{"param": "labels", "labels": len(explicit), "members": p})
	}
	seen := make(map[string]bool, p)
	for _, l := range explicit {
		if l == "" || seen[l] {
			return nil, attributeInvalid(at, "bad_params",
				at+" params.labels must be non-empty and unique",
				map[string]any{"param": "labels", "value": l})
		}
		seen[l] = true
	}
	return slices.Clone(explicit), nil
}

// FittedSlotParams returns a fitted (non-comparison) attribute slot's
// params with params.labels written out as members' labels, for the
// literal-member rewrite (Processor.prepare): the rewrite clears the
// named vector, and the slot's labels are what a bound vector fit
// (params.fit) aligns to and what a captured one (a Compose vector fit
// over this slot) is stored under. ok is false — keep the params as
// written — when the slot names no vector or its params do not decode.
func FittedSlotParams(a *types.Attribute, members Resolved) (json.RawMessage, bool) {
	if a == nil || a.Vector == "" || !IsFittedAttribute(a.Type) {
		return nil, false
	}
	obj := map[string]json.RawMessage{}
	if raw := bytes.TrimSpace(a.Params); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
			return nil, false
		}
	}
	labels := members.Labels
	if len(labels) != len(members.Members) {
		labels = members.Members
	}
	enc, err := json.Marshal(labels)
	if err != nil {
		return nil, false
	}
	obj["labels"] = enc
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}
	return out, true
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
	// Fit names the bound vector fit scored against ("" folds this
	// request's own fit); Labels are the labels it aligns to
	// (params.labels as written; nil: the members').
	Fit    string
	Labels []string
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
		fittedLabels
		Output *string `json:"output"`
	}
	if err := decodeStrict(raw, &params); err != nil {
		return MahalanobisConfig{}, attributeInvalid(at, "bad_params",
			at+" ATTR_MAHALANOBIS params: "+err.Error()+" (keys: output, fit_on, fit, labels, missing)", nil)
	}
	fitOn, err := params.resolve(at)
	if err != nil {
		return MahalanobisConfig{}, err
	}
	fit, err := params.resolveFit(at)
	if err != nil {
		return MahalanobisConfig{}, err
	}
	cfg.FitOn, cfg.Fit, cfg.Labels = fitOn, fit, params.Labels
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

// ATTR_PC_SCORE params.on values (MAT_PCA's): PCAOnCorrelation (the
// default) and PCAOnCovariance. params.rotation: RotationNone (the
// default) or an orthogonal criterion — PCARotations (an oblique one is
// rotation_oblique_unsupported, as on MAT_PCA).

// PCScoreConfig is ATTR_PC_SCORE's decoded params.
type PCScoreConfig struct {
	// Component is the 1-based component scored (required).
	Component int
	// On is PCAOnCorrelation (default) or PCAOnCovariance.
	On string
	// Rotation is "" (none) or a PCARotations criterion; Epsilon its
	// geomin ε (0 unless RotationGeominOrthogonal).
	Rotation string
	Epsilon  float64
	// Components is the k leading components rotated within (required
	// with a rotation, refused without one); 0 when unrotated.
	Components int
	// FitOn is the fit_on filterer list; nil fits on every filtered row.
	FitOn []*types.Filterer
	// Fit / Labels: as MahalanobisConfig's.
	Fit    string
	Labels []string
}

// PCScoreParams decodes ATTR_PC_SCORE's params: component (an integer
// ≥ 1, required), on ("correlation" default, or "covariance"),
// rotation ("none" default, or an orthogonal criterion — MAT_PCA's
// rule, decodeRotation), epsilon (geomin_orthogonal only), components
// (an integer ≥ component, required with a rotation and refused
// without one), fit_on
// and missing (FittedCommon). Unknown keys are refused; the bounds
// against the member count are CheckMembers. at is the slot path.
// Predict and the runtime factory both call it.
func PCScoreParams(at string, raw json.RawMessage) (PCScoreConfig, *errors.CodedError) {
	cfg := PCScoreConfig{On: PCAOnCorrelation}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return PCScoreConfig{}, attributeInvalid(at, "bad_params", at+" ATTR_PC_SCORE params.component is required (the 1-based component to score)",
			map[string]any{"param": "component"})
	}
	var params struct {
		FittedCommon
		fittedLabels
		Component  *json.Number `json:"component"`
		On         *string      `json:"on"`
		Rotation   *string      `json:"rotation"`
		Epsilon    *float64     `json:"epsilon"`
		Components *json.Number `json:"components"`
	}
	if err := decodeStrict(raw, &params); err != nil {
		return PCScoreConfig{}, attributeInvalid(at, "bad_params",
			at+" ATTR_PC_SCORE params: "+err.Error()+" (keys: component, on, rotation, epsilon, components, fit_on, fit, labels, missing)", nil)
	}
	fitOn, err := params.resolve(at)
	if err != nil {
		return PCScoreConfig{}, err
	}
	fit, err := params.resolveFit(at)
	if err != nil {
		return PCScoreConfig{}, err
	}
	cfg.FitOn, cfg.Fit, cfg.Labels = fitOn, fit, params.Labels
	if params.Component == nil {
		return PCScoreConfig{}, attributeInvalid(at, "bad_params", at+" ATTR_PC_SCORE params.component is required (the 1-based component to score)",
			map[string]any{"param": "component"})
	}
	c, ok := positiveInt(*params.Component)
	if !ok {
		return PCScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.component must be an integer ≥ 1",
			map[string]any{"param": "component", "value": params.Component.String()})
	}
	cfg.Component = c
	if params.On != nil {
		switch v := *params.On; v {
		case PCAOnCorrelation, PCAOnCovariance:
			cfg.On = v
		default:
			return PCScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.on must be \"correlation\" or \"covariance\"",
				map[string]any{"param": "on", "value": v, "valid": PCAOnValues()})
		}
	}
	rot, eps, rerr := decodeRotation(at, params.Rotation, params.Epsilon, true, attributeInvalid)
	if rerr != nil {
		return PCScoreConfig{}, rerr
	}
	cfg.Rotation, cfg.Epsilon = rot, eps
	switch {
	case cfg.Rotation == "" && params.Components != nil:
		return PCScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.components is the k to rotate within; it needs a rotation",
			map[string]any{"param": "components"})
	case cfg.Rotation != "" && params.Components == nil:
		return PCScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.components (the k leading components to rotate within) is required with rotation "+strconv.Quote(cfg.Rotation),
			map[string]any{"param": "components"})
	case cfg.Rotation != "":
		k, ok := positiveInt(*params.Components)
		if !ok || k < cfg.Component {
			return PCScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.components must be an integer ≥ params.component ("+strconv.Itoa(cfg.Component)+"): the scored component is one of the k rotated",
				map[string]any{"param": "components", "value": params.Components.String()})
		}
		cfg.Components = k
	}
	return cfg, nil
}

// CheckMembers refuses a component (or a rotated k) beyond the p
// members: there are only p components.
func (cfg PCScoreConfig) CheckMembers(at string, p int) *errors.CodedError {
	param, v := "component", cfg.Component
	if cfg.Components > v {
		param, v = "components", cfg.Components
	}
	if v <= p {
		return nil
	}
	return attributeInvalid(at, "component_out_of_range",
		at+" params."+param+" is "+strconv.Itoa(v)+" but the "+strconv.Itoa(p)+" members have only "+strconv.Itoa(p)+" components",
		map[string]any{"param": param, "value": v, "members": p})
}

// ATTR_FACTOR_SCORE params.method values.
const (
	// FactorScoreRegression is the default: Thurstone's regression
	// scores, weights R⁻¹·S (S = LΦ, the structure).
	FactorScoreRegression = "regression"
	// FactorScoreBartlett is Bartlett's weighted least-squares scores,
	// weights U⁻²L(LᵀU⁻²L)⁻¹ (U² the uniquenesses).
	FactorScoreBartlett = "bartlett"
)

// FactorScoreMethods returns params.method's values, default first.
func FactorScoreMethods() []string { return []string{FactorScoreRegression, FactorScoreBartlett} }

// FactorScoreConfig is ATTR_FACTOR_SCORE's decoded params.
type FactorScoreConfig struct {
	// Factor is the 1-based factor scored (required).
	Factor int
	// Method is FactorScoreRegression (default) or FactorScoreBartlett.
	Method string
	// Factors, Extraction, Rotation ("" for none), Kappa, Gamma and
	// Epsilon are the inline fit's MAT_FACTOR knobs, decoded by
	// MAT_FACTOR's rule (factors "kaiser" by default, extraction
	// "minres"; every rotation, oblique ones included).
	Factors    FactorCount
	Extraction string
	Rotation   string
	Kappa      float64
	Gamma      float64
	Epsilon    float64
	// FitOn is the fit_on filterer list; nil fits on every filtered row.
	FitOn []*types.Filterer
	// Fit / Labels: as MahalanobisConfig's.
	Fit    string
	Labels []string
}

// FactorScoreParams decodes ATTR_FACTOR_SCORE's params: factor (an
// integer ≥ 1, required), method ("regression" default, or
// "bartlett"), MAT_FACTOR's factors / extraction / rotation / kappa /
// gamma / epsilon (the same rule and defaults; no repair — the fit is listwise),
// fit_on and missing (FittedCommon). A fixed factors below factor is
// refused; the bounds against the member count are CheckMembers.
// Unknown keys are refused. at is the slot path. Predict and the
// runtime factory both call it.
func FactorScoreParams(at string, raw json.RawMessage) (FactorScoreConfig, *errors.CodedError) {
	raw = bytes.TrimSpace(raw)
	required := func() (FactorScoreConfig, *errors.CodedError) {
		return FactorScoreConfig{}, attributeInvalid(at, "bad_params", at+" ATTR_FACTOR_SCORE params.factor is required (the 1-based factor to score)",
			map[string]any{"param": "factor"})
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return required()
	}
	var params struct {
		FittedCommon
		fittedLabels
		Factor     *json.Number    `json:"factor"`
		Method     *string         `json:"method"`
		Factors    json.RawMessage `json:"factors"`
		Extraction *string         `json:"extraction"`
		Rotation   *string         `json:"rotation"`
		Kappa      *float64        `json:"kappa"`
		Gamma      *float64        `json:"gamma"`
		Epsilon    *float64        `json:"epsilon"`
	}
	if err := decodeStrict(raw, &params); err != nil {
		return FactorScoreConfig{}, attributeInvalid(at, "bad_params",
			at+" ATTR_FACTOR_SCORE params: "+err.Error()+" (keys: factor, method, factors, extraction, rotation, kappa, gamma, epsilon, fit_on, fit, labels, missing)", nil)
	}
	fitOn, err := params.resolve(at)
	if err != nil {
		return FactorScoreConfig{}, err
	}
	fit, err := params.resolveFit(at)
	if err != nil {
		return FactorScoreConfig{}, err
	}
	if params.Factor == nil {
		return required()
	}
	f, ok := positiveInt(*params.Factor)
	if !ok {
		return FactorScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.factor must be an integer ≥ 1",
			map[string]any{"param": "factor", "value": params.Factor.String()})
	}
	cfg := FactorScoreConfig{Factor: f, Method: FactorScoreRegression, FitOn: fitOn, Fit: fit, Labels: params.Labels}
	if params.Method != nil {
		switch v := *params.Method; v {
		case FactorScoreRegression, FactorScoreBartlett:
			cfg.Method = v
		default:
			return FactorScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.method must be \"regression\" or \"bartlett\"",
				map[string]any{"param": "method", "value": v, "valid": FactorScoreMethods()})
		}
	}
	m := Matrix{Type: types.MAT_FACTOR, Extraction: FactorExtractionMinres}
	fp := factorParams{Factors: params.Factors, Extraction: params.Extraction, Rotation: params.Rotation, Kappa: params.Kappa, Gamma: params.Gamma, Epsilon: params.Epsilon}
	if err := decodeFactorWith(at, fp, &m, attributeInvalid); err != nil {
		return FactorScoreConfig{}, err
	}
	cfg.Factors, cfg.Extraction, cfg.Rotation, cfg.Kappa, cfg.Gamma, cfg.Epsilon = m.Factors, m.Extraction, m.Rotation, m.Kappa, m.Gamma, m.Epsilon
	if !cfg.Factors.Kaiser() && cfg.Factors.K < cfg.Factor {
		return FactorScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.factors must be at least params.factor ("+strconv.Itoa(cfg.Factor)+"): the scored factor is one of the k fitted",
			map[string]any{"param": "factors", "value": cfg.Factors.K})
	}
	return cfg, nil
}

// CheckMembers refuses a factor (or a fixed factors count) beyond the p
// members: a p-member battery has at most p factors. Whether k factors
// are identified on p members (MAT_FACTOR's (p − k)² ≥ p + k) is the
// fit's call, as on MAT_FACTOR.
func (cfg FactorScoreConfig) CheckMembers(at string, p int) *errors.CodedError {
	param, v := "factor", cfg.Factor
	if cfg.Factors.K > v {
		param, v = "factors", cfg.Factors.K
	}
	if v <= p {
		return nil
	}
	return attributeInvalid(at, "factor_out_of_range",
		at+" params."+param+" is "+strconv.Itoa(v)+" but the "+strconv.Itoa(p)+" members carry at most "+strconv.Itoa(p)+" factors",
		map[string]any{"param": param, "value": v, "members": p})
}

// positiveInt reads n as an integer ≥ 1.
func positiveInt(n json.Number) (int, bool) {
	v, err := strconv.ParseInt(n.String(), 10, 32)
	if err != nil || v < 1 {
		return 0, false
	}
	return int(v), true
}

// AttributeFitOn returns a's decoded params.fit_on filterers when a's
// route typ is a vector-input attribute (nil for "filtered", a non-vector
// type, or params that do not decode — those are refused elsewhere),
// and — a comparison slot — its centroid reference's filter after them
// (ComparisonCentroidFilter). Projection, the field-reference walk and
// the two-pass planner read it to learn which columns the pre-pass
// predicates read.
func AttributeFitOn(typ types.AttributeType, a *types.Attribute) []*types.Filterer {
	if a == nil || !IsVectorAttribute(typ) {
		return nil
	}
	if IsComparisonAttribute(typ) {
		// fit_on, then a centroid reference's filter: both pre-pass
		// predicates over the attribute-step record.
		return append(comparisonFitOn(typ, a.Params), ComparisonCentroidFilter(typ, a)...)
	}
	fitOn, err := fittedParams(attributePath(0), typ, a.Params)
	if err != nil {
		return nil
	}
	return fitOn
}

// AttributeFit returns a's params.fit — the bound vector fit a fitted
// attribute (or a fitted-metric comparison) scores against — when a's
// route typ is a vector-input attribute; "" when absent, empty, for any
// other type, or when the params do not decode (those are refused
// elsewhere). The field-reference walk judges the name against the
// bound fits (internal/model.CheckVectorFit).
func AttributeFit(typ types.AttributeType, a *types.Attribute) string {
	if a == nil || !IsVectorAttribute(typ) {
		return ""
	}
	var c FittedCommon
	if json.Unmarshal(a.Params, &c) != nil || c.Fit == nil {
		return ""
	}
	return *c.Fit
}

// FitBound reports whether attribute a (route typ) is a fitted slot
// bound to a prebuilt vector fit: a FittedAttributeTypes type or an
// ATTR_VEC_DISTANCE whose params.fit names a fit (AttributeFit). Such a
// slot holds its whole fit before the scan, so it is ROW-LOCAL — no
// pre-pass, streamable beside groupers / features / matrices, and (for
// the FittedAttributeTypes) mergeable — where the same slot with
// fit_on takes the two-pass drive. The one per-slot predicate every
// routing site reads (processing attrRequiresTwoPassIn, predict's
// attrTwoPass, the fused-crosstab facts, mergegate). A comparison slot
// whose reference is a centroid is never bound: its pre-pass remains.
func FitBound(typ types.AttributeType, a *types.Attribute) bool {
	if !IsFittedAttribute(typ) && typ != types.ATTR_VEC_DISTANCE {
		return false
	}
	// A centroid reference still folds in a pre-pass, whatever fit the
	// metric reads (ComparisonTwoPass).
	if ComparisonCentroid(typ, a) {
		return false
	}
	return AttributeFit(typ, a) != ""
}

// AttributeInputMembers returns the union of every field req's
// attributes read through inline fields (a named vector's members ride
// Members(req)) plus every comparison slot's params.reference key_field
// (ComparisonKeyField — a row whose key column did not decode would read
// as a missing profile), and whether every one resolved — projection
// decodes wide otherwise.
func AttributeInputMembers(req *types.Request, schema *encoding.Schema) (members []string, ok bool) {
	ok = true
	if req == nil {
		return nil, true
	}
	for i, a := range req.Attributes {
		if a == nil {
			continue
		}
		if kf := ComparisonKeyField(a.Type, a); kf != "" {
			members = append(members, kf)
		}
		if len(a.Fields) == 0 {
			continue
		}
		r, err := ResolveSpec(attributePath(i)+".fields", types.VectorSpec{Fields: a.Fields}, schema)
		if err != nil {
			ok = false
			continue
		}
		members = append(members, r.SourceFields()...)
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

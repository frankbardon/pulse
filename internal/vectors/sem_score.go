package vectors

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/types"
)

// ATTR_SEM_SCORE — each row's score on one latent of a structural
// equation model fitted inline (MAT_SEM's fit, over the slot's members
// by label) on the fit rows: lavaan's lavPredict, method "regression"
// or "Bartlett" (internal/sem/score.go). The fit knobs are MAT_SEM's
// (model, meanstructure, group, group_reference, group_equal,
// group_partial, solver, information, ftol, max_iter, grad_tol,
// singular), decoded by MAT_SEM's own rule (decodeSEM) and resolved by
// it once the members are known (resolveSEM, resolveSEMGroup), so a
// model MAT_SEM refuses is refused here with the same reason. A
// bootstrap has nothing to score and is not a key.
//
// The fit is cohort-wide over the filtered rows params.fit_on admits
// (two-pass); every filtered row is scored. With params.group the fit
// is MAT_SEM's joint multi-group fit and a row is scored with its own
// group's parameters; a level without fit rows scores null.

// SEMScoreConfig is ATTR_SEM_SCORE's decoded params.
type SEMScoreConfig struct {
	// Latent is params.latent ("" until resolved: the model's only
	// latent then).
	Latent string
	// Method is params.method (sem.ScoreRegression default).
	Method sem.ScoreMethod
	// Plan is the fit's MAT_SEM plan; Model and Order are set once the
	// members resolve (ResolveSEMScore).
	Plan *SEMPlan
	// FitOn is the fit_on filterer list; nil fits on every filtered row.
	FitOn []*types.Filterer
	// Labels are params.labels as written (nil: the members').
	Labels []string
	// Fit is params.fit: the bound SEM fit (a Compose `fits` entry of
	// kind sem) the slot scores from instead of fitting inline — Plan is
	// then nil and the model, options, groups and member order are the
	// fit's ("" fits inline).
	Fit string
}

// semScoreParams is ATTR_SEM_SCORE's params object.
type semScoreParams struct {
	FitOn   json.RawMessage `json:"fit_on"`
	Fit     *string         `json:"fit"`
	Missing *string         `json:"missing"`
	fittedLabels
	Latent         *string  `json:"latent"`
	Method         *string  `json:"method"`
	Model          *string  `json:"model"`
	Solver         *string  `json:"solver"`
	Information    *string  `json:"information"`
	Ftol           *float64 `json:"ftol"`
	MaxIter        *int     `json:"max_iter"`
	GradTol        *float64 `json:"grad_tol"`
	Singular       *string  `json:"singular"`
	MeanStructure  *bool    `json:"meanstructure"`
	Group          *string  `json:"group"`
	GroupReference *string  `json:"group_reference"`
	GroupEqual     *semList `json:"group_equal"`
	GroupPartial   *semList `json:"group_partial"`
}

// fitKnob is the first MAT_SEM fit knob params sets ("" for none) — a
// key a bound fit (params.fit) supplies itself.
func (p semScoreParams) fitKnob() string {
	for _, k := range []struct {
		name string
		set  bool
	}{
		{"model", p.Model != nil}, {"meanstructure", p.MeanStructure != nil}, {"group", p.Group != nil},
		{"group_reference", p.GroupReference != nil}, {"group_equal", p.GroupEqual != nil},
		{"group_partial", p.GroupPartial != nil}, {"solver", p.Solver != nil}, {"information", p.Information != nil},
		{"ftol", p.Ftol != nil}, {"max_iter", p.MaxIter != nil}, {"grad_tol", p.GradTol != nil}, {"singular", p.Singular != nil},
	} {
		if k.set {
			return k.name
		}
	}
	return ""
}

// SEMScoreFeatureParams are the ATTR_SEM_SCORE params keys each hideable
// MAT_SEM surface owns (SEMFeatureParams' mean structure and multi-group
// keys; the bootstrap's are no keys here).
func SEMScoreFeatureParams() map[string][]string {
	all := SEMFeatureParams()
	return map[string][]string{
		SEMFeatureMeanStructure: all[SEMFeatureMeanStructure],
		SEMFeatureGroups:        all[SEMFeatureGroups],
	}
}

// asAttributeRefusal re-keys a MAT_SEM decode refusal (details.matrix)
// to the attribute slot (details.attribute).
func asAttributeRefusal(ce *errors.CodedError) *errors.CodedError {
	if ce == nil {
		return nil
	}
	if at, ok := ce.Details["matrix"]; ok {
		delete(ce.Details, "matrix")
		ce.Details["attribute"] = at
	}
	return ce
}

// SEMScoreParams decodes ATTR_SEM_SCORE's params: latent, method
// ("regression" default, or "bartlett"), MAT_SEM's fit knobs (the same
// rule and defaults), fit_on, labels and missing. Unknown keys are
// refused. The model is parsed and matched to the members by
// ResolveSEMScore. at is the slot path; predict and the runtime factory
// both call it.
func SEMScoreParams(at string, raw json.RawMessage) (SEMScoreConfig, *errors.CodedError) {
	var params semScoreParams
	paramread.Declare(paramread.Attribute, string(types.ATTR_SEM_SCORE), &params)
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if err := paramread.DecodeStrict(paramread.Attribute, string(types.ATTR_SEM_SCORE), raw, &params); err != nil {
			return SEMScoreConfig{}, attributeInvalid(at, "bad_params",
				at+" ATTR_SEM_SCORE params: "+err.Error()+" (keys: latent, method, model, meanstructure, group, group_reference, group_equal, group_partial, solver, information, ftol, max_iter, grad_tol, singular, fit_on, fit, labels, missing)", nil)
		}
	}
	common := FittedCommon{FitOn: params.FitOn, Fit: params.Fit, Missing: params.Missing}
	fit, err := common.resolveFit(at)
	if err != nil {
		return SEMScoreConfig{}, err
	}
	fitOn, err := common.resolve(at)
	if err != nil {
		return SEMScoreConfig{}, err
	}
	cfg := SEMScoreConfig{Method: sem.ScoreRegression, FitOn: fitOn, Labels: params.Labels, Fit: fit}
	if params.Latent != nil {
		if *params.Latent == "" {
			return SEMScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.latent is empty; name a latent variable of params.model",
				map[string]any{"param": "latent"})
		}
		cfg.Latent = *params.Latent
	}
	if params.Method != nil {
		if !slices.Contains(sem.ScoreMethods(), *params.Method) {
			return SEMScoreConfig{}, attributeInvalid(at, "bad_params", at+` params.method must be "regression" or "bartlett"`,
				map[string]any{"param": "method", "value": *params.Method, "valid": sem.ScoreMethods()})
		}
		cfg.Method = sem.ScoreMethod(*params.Method)
	}
	if fit != "" {
		// The bound fit supplies the model and every fit knob.
		if k := params.fitKnob(); k != "" {
			return SEMScoreConfig{}, attributeInvalid(at, "fit_and_model",
				at+" sets params.fit and params."+k+"; the bound SEM fit "+strconv.Quote(fit)+" supplies the model and every fit knob — drop "+k+", or drop fit to fit inline",
				map[string]any{"param": k, "fit": fit})
		}
		return cfg, nil
	}
	sp := semParams{Model: params.Model, Solver: params.Solver, Information: params.Information, Ftol: params.Ftol,
		MaxIter: params.MaxIter, GradTol: params.GradTol, Singular: params.Singular, MeanStructure: params.MeanStructure,
		Group: params.Group, GroupReference: params.GroupReference, GroupEqual: params.GroupEqual, GroupPartial: params.GroupPartial}
	var m Matrix
	if ce := decodeSEM(at, sp, &m); ce != nil {
		return SEMScoreConfig{}, asAttributeRefusal(ce)
	}
	cfg.Plan = m.SEM
	return cfg, nil
}

// ResolveSEMScore parses cfg's model against the slot's resolved
// members (matched by label: params.labels over inline fields, else the
// members' labels — slotLabels), checks params.group against the
// schema (nil skips the field checks) and settles the latent: a latent
// variable of the model, defaulting to its only one. cfg.Plan gains the
// parsed model and its member order; the returned labels are the
// members' fit labels.
func ResolveSEMScore(at string, cfg *SEMScoreConfig, members Resolved, schema *encoding.Schema) ([]string, *errors.CodedError) {
	labels, err := slotLabels(at, cfg.Labels, members)
	if err != nil {
		return nil, err
	}
	if cfg.Fit != "" {
		// A bound fit's model, groups and latent are judged against the
		// fit at bind (BindSEMScoreFit): only the labels resolve here.
		return labels, nil
	}
	plan := *cfg.Plan
	m := Matrix{Type: types.MAT_SEM, Members: members, SEM: &plan}
	m.Members.Labels = labels
	if ce := resolveSEM(at, &m); ce != nil {
		return nil, asAttributeRefusal(ce)
	}
	if ce := resolveSEMGroup(at, nil, schema, &m); ce != nil {
		return nil, asAttributeRefusal(ce)
	}
	lats := m.SEM.Model.Latents()
	switch {
	case cfg.Latent == "" && len(lats) == 1:
		cfg.Latent = lats[0]
	case cfg.Latent == "":
		return nil, attributeInvalid(at, "bad_params", at+" params.latent is required: the model has "+strconv.Itoa(len(lats))+" latent variables ("+strings.Join(lats, ", ")+")",
			map[string]any{"param": "latent", "latents": lats})
	case !slices.Contains(lats, cfg.Latent):
		return nil, attributeInvalid(at, "latent_unknown", at+" params.latent "+strconv.Quote(cfg.Latent)+" is not a latent variable of params.model (latents: "+strings.Join(lats, ", ")+")",
			map[string]any{"param": "latent", "value": cfg.Latent, "latents": lats})
	}
	cfg.Plan = m.SEM
	return labels, nil
}

// SEMScoreGroupField is an ATTR_SEM_SCORE slot's params.group field
// ("" when absent, for any other type, or when the params do not
// decode) — a column the fit and every row's scoring read beside the
// members (projection decodes it).
func SEMScoreGroupField(typ types.AttributeType, a *types.Attribute) string {
	if typ != types.ATTR_SEM_SCORE || a == nil || len(a.Params) == 0 {
		return ""
	}
	var p struct {
		Group *string `json:"group"`
	}
	if paramread.Probe(a.Params, &p) != nil || p.Group == nil {
		return ""
	}
	return *p.Group
}

// SEMScoreSlotParams returns an ATTR_SEM_SCORE slot's params with
// params.labels written out as the resolved members' labels, for the
// literal-member rewrite: the rewrite clears a named vector and
// replaces inline fields by their literal members (a native vector
// field's element members included), and the model names its members
// by those labels. Inline fields that carry params.labels keep them.
// ok is false — keep the params as written — when there is nothing to
// write or the params do not decode.
func SEMScoreSlotParams(a *types.Attribute, members Resolved) (json.RawMessage, bool) {
	if a == nil || a.Type != types.ATTR_SEM_SCORE {
		return nil, false
	}
	if a.Vector == "" {
		var p fittedLabels
		if paramread.Probe(a.Params, &p) == nil && p.Labels != nil {
			return nil, false
		}
	}
	return withLabels(a.Params, members)
}

// withLabels is params with params.labels set to members' labels (the
// member names when they carry none).
func withLabels(params json.RawMessage, members Resolved) (json.RawMessage, bool) {
	obj := map[string]json.RawMessage{}
	if raw := bytes.TrimSpace(params); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
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

package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Scale values of ATTR_REG_PREDICT's params.scale (REG_GLM only).
const (
	// ScaleResponse scores a GLM on the response scale: the inverse link
	// of the linear predictor (the fit's own fitted means). The default.
	ScaleResponse = "response"
	// ScaleLink scores a GLM on the link scale: the linear predictor.
	ScaleLink = "link"
)

// The PULSE_MODEL_SCORE_INVALID reasons.
const (
	ReasonParamsInvalid   = "params_invalid"
	ReasonFitMissing      = "fit_missing"
	ReasonSlotFields      = "slot_fields"
	ReasonTermsEmpty      = "terms_empty"
	ReasonTermDuplicate   = "term_duplicate"
	ReasonScaleUnknown    = "scale_unknown"
	ReasonScaleNotGLM     = "scale_not_glm"
	ReasonTypeUnsupported = "type_unsupported"
)

// ScoreParams is ATTR_REG_PREDICT's params block, decoded by the one
// function both the runtime and predict call (DecodeScoreParams).
type ScoreParams struct {
	// Fit names the bound model to score.
	Fit string `json:"fit"`
	// Terms restricts the score to a subset of the model's terms — its
	// predictor names, plus InterceptKey to include the intercept. Nil
	// scores the whole model, intercept included.
	Terms []string `json:"terms,omitempty"`
	// Scale is ScaleResponse (default) or ScaleLink; REG_GLM only.
	Scale string `json:"scale,omitempty"`
}

// Signature is the static face of a bound model: what a scoring
// reference is judged against without the fit's numbers. The runtime
// takes it from a Bound, predict from the fit request as written, so
// both judge one reference the same way.
type Signature struct {
	// Name is the bound name.
	Name string
	// Type is the regression operator that produces the fit.
	Type types.RegressionType
	// Terms are the predictor columns the fit was offered, in spec
	// order: the names `terms` may list and the columns a scoring frame
	// must hold. Under Selection it is the candidate set, a superset of
	// the predictors the fit keeps (a dropped candidate scores 0).
	Terms []string
}

// Catalog resolves bound names to their Signatures: the runtime's Set,
// or predict's FitCatalog. A nil Catalog binds nothing.
type Catalog interface {
	// Signature returns name's signature and whether name is bound.
	Signature(name string) (Signature, bool)
	// Names returns the bound names, sorted.
	Names() []string
}

// Signature satisfies Catalog. Nil-receiver-safe.
func (s *Set) Signature(name string) (Signature, bool) {
	if s == nil {
		return Signature{}, false
	}
	b, ok := s.byName[name]
	if !ok {
		return Signature{}, false
	}
	return b.Signature(), true
}

// Signature is b's static face.
func (b *Bound) Signature() Signature {
	return Signature{Name: b.Name, Type: b.Type, Terms: append([]string(nil), b.Terms...)}
}

// FitCatalog is the static Catalog of a ComposedRequest `fits` list —
// predict's view of the names the runtime's pre-pass binds. Entries
// CheckFits would refuse are skipped (that refusal comes first).
type FitCatalog map[string]Signature

// CatalogFromFits builds the FitCatalog of fits.
func CatalogFromFits(fits []types.FitSpec) FitCatalog {
	out := make(FitCatalog, len(fits))
	for _, f := range fits {
		if f.Name == "" || f.Request == nil || len(f.Request.Regressions) != 1 || f.Request.Regressions[0] == nil {
			continue
		}
		reg := f.Request.Regressions[0]
		out[f.Name] = Signature{Name: f.Name, Type: reg.Type, Terms: append([]string(nil), reg.Predictors...)}
	}
	return out
}

// Signature satisfies Catalog.
func (c FitCatalog) Signature(name string) (Signature, bool) {
	s, ok := c[name]
	return s, ok
}

// Names satisfies Catalog.
func (c FitCatalog) Names() []string {
	out := make([]string, 0, len(c))
	for n := range c {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// catalogNames is cat's names, nil-safe for a nil interface.
func catalogNames(cat Catalog) []string {
	if cat == nil {
		return nil
	}
	return cat.Names()
}

// ScorableType reports whether a fit of type t can be scored: the
// linear-predictor regressions (REG_OLS — penalties and selection
// included — REG_GLM, REG_BAYES_LINEAR at its posterior mean).
func ScorableType(t types.RegressionType) bool {
	switch t {
	case types.REG_OLS, types.REG_GLM, types.REG_BAYES_LINEAR:
		return true
	}
	return false
}

// CheckScore is the one rule over a scoring reference (an
// ATTR_REG_PREDICT slot), run by the runtime's field-reference pass, the
// attribute's own bind and predict alike: params decode, the slot
// carries none of the refit attributes' fields, the fit name resolves in
// cat, the fit type is scorable, `terms` lists distinct names of the
// fit's terms (InterceptKey included) and `scale` is a GLM-only
// response|link. The first fault wins. It does not judge the scoring
// frame's columns — the caller's field-reference walk does, with
// PredictorMissing / PredictorNotNumeric over Signature.Terms.
func CheckScore(attr *types.Attribute, cat Catalog) (ScoreParams, Signature, *errors.CodedError) {
	p, err := DecodeScoreParams(attr.Params)
	if err != nil {
		return p, Signature{}, err
	}
	if attr.Field != "" || attr.Target != "" || len(attr.Predictors) > 0 || attr.Penalty != "" {
		return p, Signature{}, scoreInvalid(p.Fit, ReasonSlotFields,
			"takes no field, target, predictors or penalty — the bound model supplies them; name the fit under params.fit and the output under label", nil)
	}
	if p.Fit == "" {
		return p, Signature{}, scoreInvalid("", ReasonFitMissing, "requires params.fit, the name of a bound model", nil)
	}
	var sig Signature
	ok := false
	if cat != nil {
		sig, ok = cat.Signature(p.Fit)
	}
	if !ok {
		return p, Signature{}, UnknownModel(p.Fit, catalogNames(cat))
	}
	if !ScorableType(sig.Type) {
		return p, sig, scoreInvalid(p.Fit, ReasonTypeUnsupported,
			fmt.Sprintf("cannot score a %s fit; it scores least-squares, generalized-linear and Bayesian-linear fits", sig.Type),
			map[string]any{"type": string(sig.Type)})
	}
	if p.Terms != nil {
		if len(p.Terms) == 0 {
			return p, sig, scoreInvalid(p.Fit, ReasonTermsEmpty, "lists no terms; omit params.terms to score the whole model", nil)
		}
		seen := make(map[string]bool, len(p.Terms))
		for _, t := range p.Terms {
			if seen[t] {
				return p, sig, scoreInvalid(p.Fit, ReasonTermDuplicate,
					fmt.Sprintf("lists term %q twice", t), map[string]any{"term": t})
			}
			seen[t] = true
			if t != InterceptKey && !slices.Contains(sig.Terms, t) {
				return p, sig, TermUnknown(p.Fit, t, sig.Terms)
			}
		}
	}
	switch p.Scale {
	case "", ScaleResponse, ScaleLink:
	default:
		return p, sig, scoreInvalid(p.Fit, ReasonScaleUnknown,
			fmt.Sprintf("params.scale %q is not one of response, link", p.Scale),
			map[string]any{"scale": p.Scale, "valid": []string{ScaleResponse, ScaleLink}})
	}
	if p.Scale != "" && sig.Type != types.REG_GLM {
		return p, sig, scoreInvalid(p.Fit, ReasonScaleNotGLM,
			fmt.Sprintf("params.scale applies to a generalized-linear fit only; %q is %s, whose scores are on the target's scale", p.Fit, sig.Type),
			map[string]any{"scale": p.Scale, "type": string(sig.Type)})
	}
	return p, sig, nil
}

// DecodeScoreParams decodes ATTR_REG_PREDICT's params; anything but a
// JSON object of the ScoreParams keys (with their types) is
// PULSE_MODEL_SCORE_INVALID reason params_invalid. Absent params decode
// to the zero ScoreParams (and then refuse as fit_missing).
func DecodeScoreParams(raw json.RawMessage) (ScoreParams, *errors.CodedError) {
	var p ScoreParams
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return p, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return ScoreParams{}, scoreInvalid("", ReasonParamsInvalid,
			"params must be an object {fit, terms?, scale?}: "+err.Error(), nil)
	}
	return p, nil
}

func scoreInvalid(fit, reason, what string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"attribute": string(types.ATTR_REG_PREDICT), "reason": reason}
	if fit != "" {
		details["fit"] = fit
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_SCORE_INVALID,
		string(types.ATTR_REG_PREDICT)+" "+what, details)
}

// TermUnknown is the PULSE_MODEL_TERM_UNKNOWN refusal: term is neither
// one of fit's terms nor InterceptKey. available lists the terms a
// reference may name, intercept first.
func TermUnknown(fit, term string, terms []string) *errors.CodedError {
	available := append([]string{InterceptKey}, terms...)
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_TERM_UNKNOWN,
		fmt.Sprintf("model %q has no term %q", fit, term),
		map[string]any{"fit": fit, "term": term, "available": available})
}

// PredictorMissing is the PULSE_MODEL_PREDICTOR_MISSING refusal: the
// scoring frame (schema, feature outputs and earlier attribute labels)
// has no column for one of fit's predictors.
func PredictorMissing(fit, column string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_PREDICTOR_MISSING,
		fmt.Sprintf("%s: predictor %q of model %q is not a column of the scoring frame", types.ATTR_REG_PREDICT, column, fit),
		map[string]any{"attribute": string(types.ATTR_REG_PREDICT), "fit": fit, "field": column})
}

// PredictorNotNumeric is the PULSE_MODEL_PREDICTOR_NOT_NUMERIC refusal:
// a predictor column of fit is a schema field of a type a regression
// cannot read (fieldType is its type name).
func PredictorNotNumeric(fit, column, fieldType string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_PREDICTOR_NOT_NUMERIC,
		fmt.Sprintf("%s: predictor %q of model %q is %s, not a numeric column", types.ATTR_REG_PREDICT, column, fit, fieldType),
		map[string]any{"attribute": string(types.ATTR_REG_PREDICT), "fit": fit, "field": column, "field_type": fieldType})
}

// DefaultScoreLabel is ATTR_REG_PREDICT's output label when the slot
// sets none: "ATTR_REG_PREDICT_<fit>" (params unreadable: no suffix).
func DefaultScoreLabel(attr *types.Attribute) string {
	p, _ := DecodeScoreParams(attr.Params)
	return string(attr.Type) + "_" + p.Fit
}

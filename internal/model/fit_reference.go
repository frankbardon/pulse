package model

import (
	"bytes"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// fit_reference.go is params.fit on ATTR_REG_FITTED / ATTR_REG_RESIDUAL
// (U53 FR-25): the slot names a bound fit — a ComposedRequest `fits`
// entry or a loaded `models` entry, the namespace ATTR_REG_PREDICT
// resolves — and scores through that fit's RowScorer instead of its own
// OLS refit: ATTR_REG_FITTED emits the fit's score (a GLM's on the
// response scale, its fitted mean), ATTR_REG_RESIDUAL the fit's target
// minus it. Absent params.fit, the slot refits OLS exactly as before.
//
// A slot naming params.fit is ROW-LOCAL (FitReferenced): its fit exists
// before the scan, so it takes no pre-pass — the routing every
// attribute scheduler reads (vectors.FitBound) says so.

// FitParams is the params block of an ATTR_REG_FITTED / ATTR_REG_RESIDUAL
// slot, decoded by the one function the runtime and predict call
// (DecodeFitParams).
type FitParams struct {
	// Fit names the bound fit the slot scores through.
	Fit string `json:"fit"`
	// Kind is ATTR_REG_RESIDUAL's residual scale (R residuals.glm's
	// type): "response" (the default; empty selects it) y − μ, "pearson"
	// (y − μ)/√V(μ), "deviance" sign(y − μ)·√d(y, μ) or "working"
	// (y − μ)·g′(μ). On an OLS or Bayesian-linear fit (Gaussian,
	// identity) all four are y − μ. Not on ATTR_REG_FITTED, and not on
	// a registered or media-mix regression (no family to read).
	Kind string `json:"kind,omitempty"`
}

// The ATTR_REG_RESIDUAL residual kinds (FitParams.Kind).
const (
	ResidualResponse = "response"
	ResidualPearson  = "pearson"
	ResidualDeviance = "deviance"
	ResidualWorking  = "working"
)

// ResidualKinds lists the accepted FitParams.Kind values.
func ResidualKinds() []string {
	return []string{ResidualResponse, ResidualPearson, ResidualDeviance, ResidualWorking}
}

// familyResidualType reports whether a fit of type t carries the family
// a non-response residual reads: the built-in OLS, GLM and
// Bayesian-linear regressions.
func familyResidualType(t types.RegressionType) bool {
	return t == types.REG_OLS || t == types.REG_GLM || t == types.REG_BAYES_LINEAR
}

// fitReferenceTypes are the refit attributes that take params.fit.
// ATTR_REG_LEVERAGE does not: the hat-matrix diagonal is the OLS
// refit's own design, not a score.
func fitReferenceType(t types.AttributeType) bool {
	return t == types.ATTR_REG_FITTED || t == types.ATTR_REG_RESIDUAL
}

// FitReferenced reports whether a (route typ) is an ATTR_REG_FITTED /
// ATTR_REG_RESIDUAL slot whose params carry a `fit` key: it scores that
// bound fit and is row-local. A params object that does not parse is
// not one (the slot's decode refuses it).
func FitReferenced(typ types.AttributeType, a *types.Attribute) bool {
	_, ok := FitReference(typ, a)
	return ok
}

// FitReference returns the params.fit of an ATTR_REG_FITTED /
// ATTR_REG_RESIDUAL slot and whether the slot names one (the key is
// present — an empty name is then refused fit_missing).
func FitReference(typ types.AttributeType, a *types.Attribute) (string, bool) {
	if a == nil || !fitReferenceType(typ) || len(bytes.TrimSpace(a.Params)) == 0 {
		return "", false
	}
	var probe struct {
		Fit *string `json:"fit"`
	}
	if paramread.Probe(a.Params, &probe) != nil || probe.Fit == nil {
		return "", false
	}
	return *probe.Fit, true
}

// DecodeFitParams decodes an ATTR_REG_FITTED / ATTR_REG_RESIDUAL slot's
// params: absent (or null) is the zero FitParams (the OLS refit);
// anything but an object of the FitParams keys is
// PULSE_MODEL_SCORE_INVALID reason params_invalid.
func DecodeFitParams(attr types.AttributeType, raw []byte) (FitParams, *errors.CodedError) {
	// ATTR_REG_FITTED reads {fit} only; kind is ATTR_REG_RESIDUAL's.
	if attr != types.ATTR_REG_RESIDUAL {
		var f fittedParams
		paramread.Declare(paramread.Attribute, string(attr), &f)
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			return FitParams{}, nil
		}
		if err := paramread.DecodeStrict(paramread.Attribute, string(attr), raw, &f); err != nil {
			return FitParams{}, scoreInvalidFor(attr, "", ReasonParamsInvalid,
				"params must be an object {fit}: "+err.Error(), nil)
		}
		return FitParams{Fit: f.Fit}, nil
	}
	var p FitParams
	paramread.Declare(paramread.Attribute, string(attr), &p)
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return p, nil
	}
	if err := paramread.DecodeStrict(paramread.Attribute, string(attr), raw, &p); err != nil {
		return FitParams{}, scoreInvalidFor(attr, "", ReasonParamsInvalid,
			"params must be an object {fit, kind}: "+err.Error(), nil)
	}
	return p, nil
}

// fittedParams is ATTR_REG_FITTED's params block: the bound fit only.
type fittedParams struct {
	Fit string `json:"fit"`
}

// DefaultFitLabel is a params.fit slot's output label when it sets
// none: "<TYPE>_<fit>" — the refit's "<TYPE>_<target>" has no target to
// name.
func DefaultFitLabel(attr *types.Attribute) string {
	name, _ := FitReference(attr.Type, attr)
	return string(attr.Type) + "_" + name
}

// CheckFitReference is the one rule over an ATTR_REG_FITTED /
// ATTR_REG_RESIDUAL slot naming params.fit, run by the field-reference
// pass (predict and runtime) and the attribute's own bind alike: params
// decode, the slot carries none of the refit's fields (field, target,
// predictors, penalty, alpha, l1_ratio — the fit supplies them;
// slot_fields), the name is set (fit_missing) and resolves in cat
// (PULSE_MODEL_UNKNOWN), it is a regression fit
// (PULSE_MODEL_KIND_MISMATCH), and its type is scorable through
// scorable (nil: ScorableType) — an extension regression without a
// Scorer is no_scorer, the refusal ATTR_REG_PREDICT gives it. The slot
// scores the whole model on its response scale. The first fault wins;
// the scoring frame's columns are the caller's walk.
func CheckFitReference(attr *types.Attribute, cat Catalog, scorable ScorableFunc) (FitParams, Signature, *errors.CodedError) {
	if scorable == nil {
		scorable = ScorableType
	}
	t := attr.Type
	p, err := DecodeFitParams(t, attr.Params)
	if err != nil {
		return p, Signature{}, err
	}
	if attr.Field != "" || attr.Target != "" || len(attr.Predictors) > 0 || attr.Penalty != "" || attr.Alpha != 0 || attr.L1Ratio != 0 {
		return p, Signature{}, scoreInvalidFor(t, p.Fit, ReasonSlotFields,
			"with params.fit takes no field, target, predictors, penalty, alpha or l1_ratio — the bound fit supplies them; drop them, or drop params.fit to refit OLS on the slot's own fields", nil)
	}
	if p.Fit == "" {
		return p, Signature{}, scoreInvalidFor(t, "", ReasonFitMissing, "params.fit is empty; name a bound model (a `fits` or `models` entry)", nil)
	}
	var sig Signature
	ok := false
	if cat != nil {
		sig, ok = cat.Signature(p.Fit)
	}
	if !ok {
		return p, Signature{}, UnknownModel(p.Fit, NamesOfKind(cat, types.FitKindRegression))
	}
	if sig.EffectiveKind() != types.FitKindRegression {
		return p, sig, KindMismatch(p.Fit, types.FitKindRegression, sig.EffectiveKind())
	}
	if !scorable(sig.Type) {
		if !BuiltinRegression(sig.Type) {
			return p, sig, noScorerFor(t, p.Fit, sig.Type)
		}
		return p, sig, typeUnsupportedFor(t, p.Fit, sig.Type)
	}
	if cerr := checkResidualKind(t, p, sig); cerr != nil {
		return p, sig, cerr
	}
	return p, sig, nil
}

// checkResidualKind is params.kind's rule: an ATTR_REG_RESIDUAL key,
// one of ResidualKinds, and — any kind but response — over a fit whose
// family the residual reads (familyResidualType).
func checkResidualKind(t types.AttributeType, p FitParams, sig Signature) *errors.CodedError {
	if p.Kind == "" {
		return nil
	}
	if t != types.ATTR_REG_RESIDUAL {
		return scoreInvalidFor(t, p.Fit, ReasonParamsInvalid,
			"params.kind is an ATTR_REG_RESIDUAL key (the residual scale); drop it", map[string]any{"kind": p.Kind})
	}
	known := false
	for _, k := range ResidualKinds() {
		known = known || k == p.Kind
	}
	if !known {
		return scoreInvalidFor(t, p.Fit, ReasonParamsInvalid,
			"params.kind must be response, pearson, deviance or working; got "+p.Kind,
			map[string]any{"kind": p.Kind, "allowed": ResidualKinds()})
	}
	if p.Kind != ResidualResponse && !familyResidualType(sig.Type) {
		return scoreInvalidFor(t, p.Fit, ReasonParamsInvalid,
			"params.kind "+p.Kind+" reads the fit's error family, which a "+string(sig.Type)+" fit does not declare; use kind response",
			map[string]any{"kind": p.Kind, "type": string(sig.Type)})
	}
	return nil
}

// FitColumns are the scoring-frame columns a params.fit slot over sig
// reads: the fit's terms, then — ATTR_REG_RESIDUAL — its target.
func FitColumns(t types.AttributeType, sig Signature) []string {
	cols := append([]string(nil), sig.Terms...)
	if t == types.ATTR_REG_RESIDUAL && sig.Target != "" {
		cols = append(cols, sig.Target)
	}
	return cols
}

// TargetMissing is the PULSE_MODEL_PREDICTOR_MISSING refusal of an
// ATTR_REG_RESIDUAL slot's params.fit whose fit's target is not a
// column of the scoring frame (details.role "target").
func TargetMissing(attr types.AttributeType, fit, column string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_PREDICTOR_MISSING,
		fmt.Sprintf("%s: target %q of model %q is not a column of the scoring frame", attr, column, fit),
		map[string]any{"attribute": string(attr), "fit": fit, "field": column, "role": "target"})
}

// TargetNotNumeric is the PULSE_MODEL_PREDICTOR_NOT_NUMERIC refusal of
// an ATTR_REG_RESIDUAL slot's params.fit whose fit's target is a schema
// field of a type a regression cannot read (details.role "target").
func TargetNotNumeric(attr types.AttributeType, fit, column, fieldType string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_PREDICTOR_NOT_NUMERIC,
		fmt.Sprintf("%s: target %q of model %q is %s, not a numeric column", attr, column, fit, fieldType),
		map[string]any{"attribute": string(attr), "fit": fit, "field": column, "field_type": fieldType, "role": "target"})
}

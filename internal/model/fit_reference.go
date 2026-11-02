package model

import (
	"bytes"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// fit_reference.go is params.fit on ATTR_REG_FITTED / ATTR_REG_RESIDUAL
// (U53 FR-25) and ATTR_REG_LEVERAGE / ATTR_REG_INFLUENCE (U65 E1-S2):
// the slot names a bound fit — a ComposedRequest `fits` entry or a
// loaded `models` entry, the namespace ATTR_REG_PREDICT resolves — and
// scores through that fit instead of its own OLS refit: ATTR_REG_FITTED
// emits the fit's score (a GLM's on the response scale, its fitted
// mean), ATTR_REG_RESIDUAL the fit's target minus it (on the kind's
// scale), ATTR_REG_LEVERAGE / ATTR_REG_INFLUENCE and the standardized /
// studentized residual read the fit's design (fit_design.go). Absent
// params.fit, the slot refits OLS exactly as before.
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
	// "standardized" (R rstandard) and "studentized" (R rstudent) read
	// the refit's leverage and σ (refit.go) or, with Fit, the bound
	// fit's design (CheckFitDesign: a REG_OLS / REG_GLM fit with vcov).
	Kind string `json:"kind,omitempty"`
	// ResidualType is a kind standardized ATTR_REG_RESIDUAL's residual
	// the standardization starts from (R rstandard.glm's type):
	// "pearson" (the default; empty selects it) or "deviance". Any value
	// on another kind is refused (checkResidualType); on a Gaussian fit
	// the two are equal.
	ResidualType string `json:"residual_type,omitempty"`
	// Measure is ATTR_REG_INFLUENCE's figure (InfluenceMeasures; empty
	// selects cooks) — that attribute's key only.
	Measure string `json:"measure,omitempty"`
}

// The ATTR_REG_RESIDUAL residual kinds (FitParams.Kind).
const (
	ResidualResponse = "response"
	ResidualPearson  = "pearson"
	ResidualDeviance = "deviance"
	ResidualWorking  = "working"
	// ResidualStandardized / ResidualStudentized are the OLS refit's
	// internally (R rstandard) and externally (R rstudent) studentized
	// residuals; they read the refit's design (ScaledResidualKind).
	ResidualStandardized = "standardized"
	ResidualStudentized  = "studentized"
)

// ResidualKinds lists the accepted FitParams.Kind values.
func ResidualKinds() []string {
	return []string{ResidualResponse, ResidualPearson, ResidualDeviance, ResidualWorking, ResidualStandardized, ResidualStudentized}
}

// ScaledResidualKind reports whether kind divides by σ·√(1 − h): it
// reads the refit's leverage and residual variance, or a bound fit's
// design (FitDesignKind).
func ScaledResidualKind(kind string) bool {
	return kind == ResidualStandardized || kind == ResidualStudentized
}

// knownResidualKind reports whether kind is one of ResidualKinds.
func knownResidualKind(kind string) bool {
	for _, k := range ResidualKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// unknownResidualKind is the PULSE_MODEL_SCORE_INVALID params_invalid
// refusal of a params.kind outside ResidualKinds.
func unknownResidualKind(t types.AttributeType, fit, kind string) *errors.CodedError {
	return scoreInvalidFor(t, fit, ReasonParamsInvalid,
		"params.kind must be response, pearson, deviance, working, standardized or studentized; got "+kind,
		map[string]any{"kind": kind, "allowed": ResidualKinds()})
}

// familyResidualType reports whether a fit of type t carries the family
// a non-response residual reads: the built-in OLS, GLM and
// Bayesian-linear regressions.
func familyResidualType(t types.RegressionType) bool {
	return t == types.REG_OLS || t == types.REG_GLM || t == types.REG_BAYES_LINEAR
}

// fitReferenceType reports whether t is a refit attribute that takes
// params.fit: all four. ATTR_REG_LEVERAGE / ATTR_REG_INFLUENCE read the
// bound fit's design (U65 E1-S2, CheckFitDesign).
func fitReferenceType(t types.AttributeType) bool {
	switch t {
	case types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE, types.ATTR_REG_INFLUENCE:
		return true
	}
	return false
}

// FitReadsTarget reports whether a params.fit slot of type t reads the
// bound fit's target column: ATTR_REG_RESIDUAL and ATTR_REG_INFLUENCE
// (both read y − μ). ATTR_REG_FITTED and ATTR_REG_LEVERAGE read the
// fit's terms only.
func FitReadsTarget(t types.AttributeType) bool {
	return t == types.ATTR_REG_RESIDUAL || t == types.ATTR_REG_INFLUENCE
}

// FitReferenced reports whether a (route typ) is a regression attribute
// slot (fitReferenceType) whose params carry a `fit` key: it scores
// that bound fit and is row-local. A params object that does not parse
// is not one (the slot's decode refuses it).
func FitReferenced(typ types.AttributeType, a *types.Attribute) bool {
	_, ok := FitReference(typ, a)
	return ok
}

// FitReference returns the params.fit of a regression attribute slot
// (fitReferenceType) and whether the slot names one (the key is
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

// DecodeFitParams decodes a regression attribute slot's params as its
// params.fit form reads them: ATTR_REG_FITTED / ATTR_REG_LEVERAGE
// {fit}, ATTR_REG_RESIDUAL {fit, kind}, ATTR_REG_INFLUENCE {fit,
// measure}. Absent (or null) is the zero FitParams (the OLS refit);
// anything but an object of the type's keys is PULSE_MODEL_SCORE_INVALID
// reason params_invalid. ATTR_REG_INFLUENCE's measure is judged by
// CheckFitReference (checkInfluenceMeasure — the refit's rule).
func DecodeFitParams(attr types.AttributeType, raw []byte) (FitParams, *errors.CodedError) {
	trimmed := bytes.TrimSpace(raw)
	empty := len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
	switch attr {
	case types.ATTR_REG_RESIDUAL:
		var p residualParams
		paramread.Declare(paramread.Attribute, string(attr), &p)
		if empty {
			return FitParams{}, nil
		}
		if err := paramread.DecodeStrict(paramread.Attribute, string(attr), raw, &p); err != nil {
			return FitParams{}, scoreInvalidFor(attr, "", ReasonParamsInvalid,
				"params must be an object {fit, kind, residual_type}: "+err.Error(), nil)
		}
		return FitParams{Fit: p.Fit, Kind: p.Kind, ResidualType: p.ResidualType}, nil
	case types.ATTR_REG_INFLUENCE:
		var p influenceFitParams
		paramread.Declare(paramread.Attribute, string(attr), &p)
		if empty {
			return FitParams{}, nil
		}
		if err := paramread.DecodeStrict(paramread.Attribute, string(attr), raw, &p); err != nil {
			return FitParams{}, scoreInvalidFor(attr, "", ReasonParamsInvalid,
				"params must be an object {fit, measure}: "+err.Error(), nil)
		}
		return FitParams{Fit: p.Fit, Measure: p.Measure}, nil
	}
	// ATTR_REG_FITTED / ATTR_REG_LEVERAGE read {fit} only.
	var f fittedParams
	paramread.Declare(paramread.Attribute, string(attr), &f)
	if empty {
		return FitParams{}, nil
	}
	if err := paramread.DecodeStrict(paramread.Attribute, string(attr), raw, &f); err != nil {
		return FitParams{}, scoreInvalidFor(attr, "", ReasonParamsInvalid,
			"params must be an object {fit}: "+err.Error(), nil)
	}
	return FitParams{Fit: f.Fit}, nil
}

// fittedParams is ATTR_REG_FITTED's (and a params.fit
// ATTR_REG_LEVERAGE's) params block: the bound fit only.
type fittedParams struct {
	Fit string `json:"fit"`
}

// residualParams is ATTR_REG_RESIDUAL's params block.
type residualParams struct {
	Fit          string `json:"fit"`
	Kind         string `json:"kind,omitempty"`
	ResidualType string `json:"residual_type,omitempty"`
}

// influenceFitParams is a params.fit ATTR_REG_INFLUENCE's params block.
type influenceFitParams struct {
	Fit     string `json:"fit"`
	Measure string `json:"measure,omitempty"`
}

// DefaultFitLabel is a params.fit slot's output label when it sets
// none: "<TYPE>_<fit>" — the refit's "<TYPE>_<target>" has no target to
// name.
func DefaultFitLabel(attr *types.Attribute) string {
	name, _ := FitReference(attr.Type, attr)
	return string(attr.Type) + "_" + name
}

// CheckFitReference is the one rule over a regression attribute slot
// naming params.fit, run by the field-reference
// pass (predict and runtime) and the attribute's own bind alike: params
// decode, the slot carries none of the refit's fields (field, target,
// predictors, penalty, alpha, l1_ratio — the fit supplies them;
// slot_fields), the name is set (fit_missing) and resolves in cat
// (PULSE_MODEL_UNKNOWN), it is a regression fit
// (PULSE_MODEL_KIND_MISMATCH), and its type is scorable through
// scorable (nil: ScorableType) — an extension regression without a
// Scorer is no_scorer, the refusal ATTR_REG_PREDICT gives it — then
// params.kind (checkResidualKind), params.measure (the refit's rule,
// PROCESSING_CONFIG) and, for a design-reading slot (FitDesignKind),
// CheckFitDesign. The slot scores the whole model on its response
// scale. The first fault wins; the scoring frame's columns are the
// caller's walk.
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
	if t == types.ATTR_REG_INFLUENCE {
		m, cerr := checkInfluenceMeasure(p.Measure)
		if cerr != nil {
			return p, sig, cerr
		}
		p.Measure = m
	}
	if FitDesignKind(t, p) {
		if cerr := CheckFitDesign(t, p, sig); cerr != nil {
			return p, sig, cerr
		}
	}
	return p, sig, nil
}

// checkResidualKind is params.kind's rule over a bound fit: an
// ATTR_REG_RESIDUAL key, one of ResidualKinds, and — any kind but
// response — over a fit whose family the residual reads
// (familyResidualType). A scaled kind's design rule is CheckFitDesign.
func checkResidualKind(t types.AttributeType, p FitParams, sig Signature) *errors.CodedError {
	if cerr := checkResidualType(t, p); cerr != nil {
		return cerr
	}
	if p.Kind == "" {
		return nil
	}
	if t != types.ATTR_REG_RESIDUAL {
		return scoreInvalidFor(t, p.Fit, ReasonParamsInvalid,
			"params.kind is an ATTR_REG_RESIDUAL key (the residual scale); drop it", map[string]any{"kind": p.Kind})
	}
	if !knownResidualKind(p.Kind) {
		return unknownResidualKind(t, p.Fit, p.Kind)
	}
	if p.Kind != ResidualResponse && !familyResidualType(sig.Type) {
		return scoreInvalidFor(t, p.Fit, ReasonParamsInvalid,
			"params.kind "+p.Kind+" reads the fit's error family, which a "+string(sig.Type)+" fit does not declare; use kind response",
			map[string]any{"kind": p.Kind, "type": string(sig.Type)})
	}
	return nil
}

// The ATTR_REG_RESIDUAL residual types of kind standardized
// (FitParams.ResidualType).
const (
	ResidualTypePearson  = "pearson"
	ResidualTypeDeviance = "deviance"
)

// ResidualTypes lists the accepted FitParams.ResidualType values, the
// default first.
func ResidualTypes() []string { return []string{ResidualTypePearson, ResidualTypeDeviance} }

// checkResidualType is params.residual_type's rule, the refit's and a
// params.fit slot's alike: one of ResidualTypes, and only beside kind
// standardized — R's studentized residual, Cook's distance and DFFITS
// read the Pearson residual alone (PULSE_MODEL_SCORE_INVALID
// params_invalid).
func checkResidualType(t types.AttributeType, p FitParams) *errors.CodedError {
	if p.ResidualType == "" {
		return nil
	}
	if p.ResidualType != ResidualTypePearson && p.ResidualType != ResidualTypeDeviance {
		return scoreInvalidFor(t, p.Fit, ReasonParamsInvalid,
			"params.residual_type must be pearson or deviance; got "+p.ResidualType,
			map[string]any{"residual_type": p.ResidualType, "allowed": ResidualTypes()})
	}
	if p.Kind != ResidualStandardized {
		return scoreInvalidFor(t, p.Fit, ReasonParamsInvalid,
			"params.residual_type applies to kind standardized only (the studentized residual, Cook's distance and DFFITS read the Pearson residual); drop it, or set kind standardized",
			map[string]any{"residual_type": p.ResidualType, "kind": p.Kind})
	}
	return nil
}

// FitColumns are the scoring-frame columns a params.fit slot over sig
// reads: the fit's terms, then — FitReadsTarget — its target.
func FitColumns(t types.AttributeType, sig Signature) []string {
	cols := append([]string(nil), sig.Terms...)
	if FitReadsTarget(t) && sig.Target != "" {
		cols = append(cols, sig.Target)
	}
	return cols
}

// TargetMissing is the PULSE_MODEL_PREDICTOR_MISSING refusal of an
// ATTR_REG_RESIDUAL / ATTR_REG_INFLUENCE slot's params.fit whose fit's target is not a
// column of the scoring frame (details.role "target").
func TargetMissing(attr types.AttributeType, fit, column string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_PREDICTOR_MISSING,
		fmt.Sprintf("%s: target %q of model %q is not a column of the scoring frame", attr, column, fit),
		map[string]any{"attribute": string(attr), "fit": fit, "field": column, "role": "target"})
}

// TargetNotNumeric is the PULSE_MODEL_PREDICTOR_NOT_NUMERIC refusal of
// an ATTR_REG_RESIDUAL / ATTR_REG_INFLUENCE slot's params.fit whose fit's target is a schema
// field of a type a regression cannot read (details.role "target").
func TargetNotNumeric(attr types.AttributeType, fit, column, fieldType string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_PREDICTOR_NOT_NUMERIC,
		fmt.Sprintf("%s: target %q of model %q is %s, not a numeric column", attr, column, fit, fieldType),
		map[string]any{"attribute": string(attr), "fit": fit, "field": column, "field_type": fieldType, "role": "target"})
}

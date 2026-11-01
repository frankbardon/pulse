package model

import (
	"bytes"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// refit.go is the one rule over a regression attribute that REFITS OLS
// on its own target and predictors (no params.fit): ATTR_REG_FITTED,
// ATTR_REG_RESIDUAL, ATTR_REG_LEVERAGE and ATTR_REG_INFLUENCE. The
// factory (internal/processing newRegressionAttribute) and the
// field-reference pass of predict both call CheckRefit, so a refusal
// here is the same code, message and details on either side (U65
// E1-S2). The weight-kind rule is separate: a design-reading slot is
// frequency-only per spec (weighting.AttributeClassOf, NeedsRefitDesign).

// The ATTR_REG_INFLUENCE measures (InfluenceParams.Measure).
const (
	// InfluenceCooks is Cook's distance r²·h / (p′·(1 − h)) (R
	// cooks.distance), the default.
	InfluenceCooks = "cooks"
	// InfluenceDFFITS is t·√(h / (1 − h)) (R dffits), t the externally
	// studentized residual.
	InfluenceDFFITS = "dffits"
)

// InfluenceMeasures lists the accepted InfluenceParams.Measure values,
// the default first.
func InfluenceMeasures() []string { return []string{InfluenceCooks, InfluenceDFFITS} }

// InfluenceParams is ATTR_REG_INFLUENCE's params block.
type InfluenceParams struct {
	// Measure selects the figure: "cooks" (default; empty selects it)
	// or "dffits".
	Measure string `json:"measure,omitempty"`
}

// DecodeInfluenceParams decodes an ATTR_REG_INFLUENCE slot's params:
// absent (or null) is the default measure; anything but an object of
// the InfluenceParams keys, or a measure outside InfluenceMeasures, is
// PROCESSING_CONFIG. The returned Measure is never empty.
func DecodeInfluenceParams(raw []byte) (InfluenceParams, *errors.CodedError) {
	var p InfluenceParams
	op := string(types.ATTR_REG_INFLUENCE)
	paramread.Declare(paramread.Attribute, op, &p)
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := paramread.DecodeStrict(paramread.Attribute, op, raw, &p); err != nil {
			return InfluenceParams{}, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				op+" params must be an object {measure}: "+err.Error(),
				map[string]any{"attribute": op})
		}
	}
	m, cerr := checkInfluenceMeasure(p.Measure)
	if cerr != nil {
		return InfluenceParams{}, cerr
	}
	p.Measure = m
	return p, nil
}

// checkInfluenceMeasure is params.measure's rule, the refit's and a
// params.fit slot's alike: empty selects cooks, anything outside
// InfluenceMeasures is PROCESSING_CONFIG. Returns the resolved measure.
func checkInfluenceMeasure(m string) (string, *errors.CodedError) {
	op := string(types.ATTR_REG_INFLUENCE)
	switch m {
	case "":
		return InfluenceCooks, nil
	case InfluenceCooks, InfluenceDFFITS:
		return m, nil
	}
	return "", errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		op+" params.measure must be cooks or dffits; got "+m,
		map[string]any{"attribute": op, "measure": m, "allowed": InfluenceMeasures()})
}

// refitDesignUnpenalized is the PROCESSING_CONFIG refusal of a penalty
// on a slot that reads the refit's design (leverage and σ): penalized
// leverage needs the regularized resolvent, which is not implemented.
func refitDesignUnpenalized(t types.AttributeType, penalty, what string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		string(t)+what+" supports only unpenalized OLS — penalized leverage / GLM leverage not implemented; drop penalty",
		map[string]any{"attribute": string(t), "penalty": penalty})
}

// NeedsRefitDesign reports whether a (routed as t) is a refit slot that
// reads the OLS refit's leverage and residual variance:
// ATTR_REG_LEVERAGE, ATTR_REG_INFLUENCE, or an ATTR_REG_RESIDUAL whose
// params.kind is standardized / studentized. A params object that does
// not parse is not one (CheckRefit refuses it).
func NeedsRefitDesign(t types.AttributeType, a *types.Attribute) bool {
	switch t {
	case types.ATTR_REG_LEVERAGE, types.ATTR_REG_INFLUENCE:
		return true
	case types.ATTR_REG_RESIDUAL:
		return ScaledResidualKind(RefitResidualKind(a))
	}
	return false
}

// PerObservationRefit reports whether a (routed as t) emits a
// per-OBSERVATION figure of the refit — ATTR_REG_INFLUENCE, or a
// standardized / studentized ATTR_REG_RESIDUAL: under a frequency
// weight each record is w identical observations and the figure is
// one copy's, which a probability weight does not define, so such a
// slot is frequency-only (weighting.AttributeClassOf).
// ATTR_REG_LEVERAGE is not: its figure is the record's (grouped) hat
// value, defined under either kind.
func PerObservationRefit(t types.AttributeType, a *types.Attribute) bool {
	return t != types.ATTR_REG_LEVERAGE && NeedsRefitDesign(t, a)
}

// RefitResidualKind is an ATTR_REG_RESIDUAL slot's params.kind as a
// lenient probe ("" when absent or unparseable).
func RefitResidualKind(a *types.Attribute) string {
	if a == nil || len(bytes.TrimSpace(a.Params)) == 0 {
		return ""
	}
	var probe struct {
		Kind string `json:"kind"`
	}
	if paramread.Probe(a.Params, &probe) != nil {
		return ""
	}
	return probe.Kind
}

// CheckRefit is the one rule over a refit regression attribute slot
// (routed as t, no params.fit): its params decode (ATTR_REG_FITTED
// {fit}; ATTR_REG_RESIDUAL {fit, kind} with kind one of ResidualKinds —
// PULSE_MODEL_SCORE_INVALID params_invalid; ATTR_REG_INFLUENCE
// {measure} — PROCESSING_CONFIG), and a slot reading the refit's
// design (NeedsRefitDesign) carries no penalty (PROCESSING_CONFIG).
// Target / predictor presence and types are the callers' walk.
func CheckRefit(t types.AttributeType, a *types.Attribute) *errors.CodedError {
	if a == nil {
		return nil
	}
	switch t {
	case types.ATTR_REG_FITTED:
		_, err := DecodeFitParams(t, a.Params)
		return err
	case types.ATTR_REG_RESIDUAL:
		p, err := DecodeFitParams(t, a.Params)
		if err != nil {
			return err
		}
		if p.Kind != "" && !knownResidualKind(p.Kind) {
			return unknownResidualKind(t, "", p.Kind)
		}
		if ScaledResidualKind(p.Kind) && a.Penalty != "" {
			return refitDesignUnpenalized(t, a.Penalty, " params.kind "+p.Kind)
		}
	case types.ATTR_REG_LEVERAGE:
		if a.Penalty != "" {
			return refitDesignUnpenalized(t, a.Penalty, "")
		}
	case types.ATTR_REG_INFLUENCE:
		if _, err := DecodeInfluenceParams(a.Params); err != nil {
			return err
		}
		if a.Penalty != "" {
			return refitDesignUnpenalized(t, a.Penalty, "")
		}
	}
	return nil
}

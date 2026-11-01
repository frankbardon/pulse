// Package regmargins is the one rule over a regression's marginal-effects
// block (types.RegressionSpec.Margins), shared by the no-execute predict
// pass (internal/descriptor) and the regression engines
// (internal/processing/regression) so the two refuse the same requests
// with the same code, message and details. It reads the spec (and, for
// the derived-predictor rule, the cohort schema) only and imports
// neither side.
//
// Every refusal is PROCESSING_CONFIG with details.requires naming what
// the request must change:
//
//   - shape: type ame | mem, scale response | link, terms each a
//     predictor, once (requires "type ame or mem", "scale response or
//     link", "a predictor");
//   - the regression type: REG_OLS or REG_GLM (requires "type REG_OLS or
//     REG_GLM"); an extension or model regression is the shared-slot
//     table's refusal (slot margins), never this rule's;
//   - the fit: no penalty ("unpenalized" — a shrunken β has no
//     delta-method sampling distribution), no selection ("no_selection"
//     — post-selection inference), no resample ("no_resample" — the
//     replicate covariance is not the AME's: bootstrap the effect
//     itself), no bounds ("unbounded" — a binding bound has no
//     covariance);
//   - CheckDerived: every predictor a cohort field ("schema_predictors")
//     — a derived column (a feature or attribute output) may be a
//     function of another predictor (FEAT_POLY's x², an interaction),
//     and Pulse does not chain-rule through it, so the effect would
//     silently ignore that dependence.
package regmargins

import (
	"slices"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Terms is spec's resolved term list: Margins.Terms, or every predictor
// in Predictors order. Nil when spec carries no margins block.
func Terms(spec *types.RegressionSpec) []string {
	if spec == nil || spec.Margins == nil {
		return nil
	}
	if len(spec.Margins.Terms) != 0 {
		return slices.Clone(spec.Margins.Terms)
	}
	return slices.Clone(spec.Predictors)
}

func refuse(spec *types.RegressionSpec, msg, requires string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"type": string(spec.Type), "slot": "margins", "requires": requires}
	for k, v := range extra {
		details[k] = v
	}
	if spec.Name != "" {
		details["name"] = spec.Name
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, string(spec.Type)+" margins "+msg, details)
}

// builtin reports whether t is a built-in regression this rule judges
// (an extension or model regression is the shared-slot table's).
func builtin(t types.RegressionType) bool {
	switch t {
	case types.REG_OLS, types.REG_GLM, types.REG_BAYES_LINEAR:
		return true
	}
	return false
}

// Check returns the PROCESSING_CONFIG refusal of spec's margins block,
// or nil (no block, a well-formed block on a supported fit, or a
// non-built-in type). The engine (ValidateRegression) and predict
// (validateRegressions) both call it.
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Margins == nil || !builtin(spec.Type) {
		return nil
	}
	m := spec.Margins
	if m.Type != "" && !slices.Contains(types.AllMarginsTypes(), m.Type) {
		return refuse(spec, "type must be one of "+strings.Join(types.AllMarginsTypes(), ", ")+" (empty selects ame); got "+m.Type,
			"type ame or mem", map[string]any{"key": "type", "value": m.Type, "allowed": types.AllMarginsTypes()})
	}
	if m.Scale != "" && !slices.Contains(types.AllMarginsScales(), m.Scale) {
		return refuse(spec, "scale must be one of "+strings.Join(types.AllMarginsScales(), ", ")+" (empty selects response); got "+m.Scale,
			"scale response or link", map[string]any{"key": "scale", "value": m.Scale, "allowed": types.AllMarginsScales()})
	}
	for i, t := range m.Terms {
		if !slices.Contains(spec.Predictors, t) {
			return refuse(spec, "terms names "+t+", which is not one of the regression's predictors",
				"a predictor", map[string]any{"key": "terms", "term": t})
		}
		if slices.Contains(m.Terms[:i], t) {
			return refuse(spec, "terms names "+t+" twice",
				"a predictor", map[string]any{"key": "terms", "term": t})
		}
	}
	if spec.Type != types.REG_OLS && spec.Type != types.REG_GLM {
		return refuse(spec, "are reported by REG_OLS and REG_GLM only (a posterior has no delta-method effect); drop margins",
			"type REG_OLS or REG_GLM", nil)
	}
	switch {
	case spec.Penalty != "":
		return refuse(spec, "need an unpenalized fit: a shrunken coefficient has no delta-method sampling distribution; drop penalty or margins",
			"unpenalized", map[string]any{"penalty": spec.Penalty})
	case spec.Selection != "":
		return refuse(spec, "need a fixed predictor set: after selection the coefficient covariance ignores the search; drop selection or margins",
			"no_selection", map[string]any{"selection": spec.Selection})
	case spec.Resample != "":
		return refuse(spec, "read the fit's analytical coefficient covariance, not a replicate one (the replicates would have to be of the effect itself); drop resample or margins",
			"no_resample", map[string]any{"resample": spec.Resample})
	case spec.Bounds.Active():
		return refuse(spec, "need an unbounded fit: a binding bound leaves the coefficients without a covariance; drop bounds or margins",
			"unbounded", nil)
	}
	return nil
}

// CheckDerived returns the PROCESSING_CONFIG refusal of a margins block
// on a regression whose predictor is not a field of schema (a derived
// column: a feature, attribute or window output), or nil. A nil schema
// or spec, no block, or a non-built-in type passes. Check runs first;
// the engine (BuildWith) and predict both call it after the field-type
// rule.
func CheckDerived(spec *types.RegressionSpec, schema *encoding.Schema) *errors.CodedError {
	if spec == nil || spec.Margins == nil || schema == nil || (spec.Type != types.REG_OLS && spec.Type != types.REG_GLM) {
		return nil
	}
	for _, p := range spec.Predictors {
		if schema.Field(p) == nil {
			return refuse(spec, "need every predictor to be a cohort field: "+p+" is a derived column, which may depend on another predictor (a polynomial or interaction term), and the effect is not chain-ruled through it; drop margins or fit the cohort fields",
				"schema_predictors", map[string]any{"predictor": p})
		}
	}
	return nil
}

package descriptor

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// knownRegressionTypes is the lookup set used by Predict to flag
// unknown REG_* types without taking a dependency on processing/.
var knownRegressionTypes = func() map[types.RegressionType]struct{} {
	out := make(map[types.RegressionType]struct{}, len(types.AllRegressionTypes()))
	for _, rt := range types.AllRegressionTypes() {
		out[rt] = struct{}{}
	}
	return out
}()

// validateRegressions performs structural validation on every
// RegressionSpec in req. Phase 0 catches:
//   - unknown Type
//   - missing Target
//   - empty Predictors slice
//   - Target / Predictors that name unknown fields
//   - Target / Predictors that name non-numeric fields
//   - GLM-required Family present and in the supported set
//   - Modifier coupling (Selection requires Criterion)
//
// Deeper runtime checks (n ≥ p + 1, link compatibility, regularization
// parameter bounds) live with the engines in Phases 1–4. predict
// cannot import processing/regression, so registry membership is
// checked against types.AllRegressionTypes() instead.
func validateRegressions(env *Envelope, req *types.Request, schema *encoding.Schema, projected map[string]bool) {
	for _, reg := range req.Regressions {
		if reg == nil {
			continue
		}
		if _, ok := knownRegressionTypes[reg.Type]; !ok {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"regression references unknown type: "+string(reg.Type),
				map[string]any{"type": string(reg.Type)},
			)
			continue
		}

		if reg.Target == "" {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"regression "+string(reg.Type)+" missing required Target field",
				map[string]any{"type": string(reg.Type)},
			)
		} else {
			if f := schema.Field(reg.Target); f == nil {
				if !projected[reg.Target] {
					env.AddError(
						string(errors.SERVICE_VALIDATION),
						"regression references unknown target field: "+reg.Target,
						map[string]any{"field": reg.Target, "type": string(reg.Type)},
					)
				}
			} else if !regressionAcceptsType(reg.Type, f.Type) {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					"regression target field "+reg.Target+" is not numeric (got "+f.Type.String()+")",
					map[string]any{"field": reg.Target, "type": f.Type.String()},
				)
			}
		}

		if len(reg.Predictors) == 0 {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"regression "+string(reg.Type)+" missing Predictors (≥1 required)",
				map[string]any{"type": string(reg.Type)},
			)
		}
		for _, p := range reg.Predictors {
			f := schema.Field(p)
			if f == nil {
				if !projected[p] {
					env.AddError(
						string(errors.SERVICE_VALIDATION),
						"regression references unknown predictor field: "+p,
						map[string]any{"field": p, "type": string(reg.Type)},
					)
				}
				continue
			}
			if !regressionAcceptsType(reg.Type, f.Type) {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					"regression predictor field "+p+" is not numeric (got "+f.Type.String()+")",
					map[string]any{"field": p, "type": f.Type.String()},
				)
			}
		}

		// REG_GLM requires Family ∈ {binomial, poisson, gamma}.
		if reg.Type == types.REG_GLM {
			switch reg.Family {
			case "binomial", "poisson", "gamma":
				// ok
			case "":
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					"REG_GLM requires a Family (binomial | poisson | gamma)",
					map[string]any{"type": string(reg.Type)},
				)
			default:
				env.AddError(
					string(errors.PROCESSING_REGRESSION_INVALID_FAMILY),
					"REG_GLM family must be one of binomial, poisson, gamma; got "+reg.Family,
					map[string]any{"family": reg.Family},
				)
			}
		}

		// Selection requires Criterion when set.
		if reg.Selection != "" && reg.Criterion == "" {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"regression Selection requires a Criterion (aic | bic)",
				map[string]any{"selection": reg.Selection},
			)
		}

		// L1-penalized OLS fits (Penalty=="l1" or "elasticnet") produce
		// analytical standard errors only as a coarse plug-in over the
		// data-dependent active set. Warn callers so they treat the
		// reported SE / p-value entries as approximate. Suppressed when
		// a Resample modifier is set — bootstrap / jackknife is the
		// rigorous answer and replaces the analytical SE entirely
		// (Phase 5).
		if reg.Type == types.REG_OLS && (reg.Penalty == "l1" || reg.Penalty == "elasticnet") && reg.Resample == "" {
			env.AddWarning(
				"PROCESSING_REGRESSION_APPROXIMATE_SE",
				"REG_OLS with Penalty="+reg.Penalty+" emits std errors / p-values for the active set only, as a naive plug-in estimate; for rigorous uncertainty quantification set Resample to \"bootstrap\" or \"jackknife\"",
				map[string]any{"penalty": reg.Penalty, "type": string(reg.Type)},
			)
		}

		// Regularized OLS + Selection: regularization already does
		// feature selection (lasso zeroes coefficients) or shrinks
		// (ridge); pairing it with greedy stepwise / forward / backward
		// is rarely meaningful but not strictly invalid. Warn the user
		// so they know the combination is unusual.
		if reg.Type == types.REG_OLS && reg.Penalty != "" && reg.Selection != "" {
			env.AddWarning(
				"PROCESSING_REGRESSION_REGULARIZED_SELECTION",
				"REG_OLS combines Penalty="+reg.Penalty+" with Selection="+reg.Selection+"; regularization already performs feature shrinkage / selection, so layering greedy subset search on top is unusual and may produce a model harder to interpret than either alone",
				map[string]any{"penalty": reg.Penalty, "selection": reg.Selection, "type": string(reg.Type)},
			)
		}
	}
}

// isNumericFieldType is the narrow numeric predicate (integer + float +
// decimal). Delegates to the canonical encoding.FieldType.IsNumeric().
// Retained as a package-local helper so call sites read like other
// descriptor predicates; widening the predicate happens on FieldType.
func isNumericFieldType(t encoding.FieldType) bool {
	return t.IsNumeric()
}

// regressionAcceptsType reports whether a given encoding.FieldType is a
// valid Target / Predictor for the named regression operator. All three
// engines (REG_OLS, REG_GLM, REG_BAYES_LINEAR) consume the analytics-
// numeric set: integer / float / decimal families plus the bit-packed
// integer encodings (nullable_u4, nullable_bool, packed_bool) and date.
// The runtime path collects values via Record.NumericValue, which
// returns float64 + null for every member of the set, so the fit
// algorithms (Welford accumulator for OLS, IRLS for GLM, conjugate NIG
// for Bayes) consume them without per-type branching.
func regressionAcceptsType(rt types.RegressionType, t encoding.FieldType) bool {
	_ = rt
	return t.IsNumericForAnalytics()
}

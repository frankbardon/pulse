package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regcov"
	"github.com/frankbardon/pulse/internal/regpenalty"
	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/types"
)

// knownRegressionTypes is the lookup set used by Predict to flag
// unknown REG_* types without taking a dependency on internal/processing/.
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
//   - Target / Predictors that name non-numeric fields
//   - GLM-required Family present and in the supported set
//   - Modifier coupling (Selection requires Criterion)
//
// Deeper runtime checks (n ≥ p + 1, link compatibility, regularization
// parameter bounds) live with the engines in Phases 1–4. predict
// cannot import internal/processing/regression, so registry membership is
// checked against types.AllRegressionTypes() instead. A type inst hides
// is refused exactly as a type outside that set.
func validateRegressions(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, projected map[string]bool, inst *InstanceSnapshot) {
	// Every `save` path and their uniqueness — the runtime's
	// model.CheckRequestSaves, the one rule (first fault,
	// details.regression; a duplicate names both sites).
	if cerr := model.CheckRequestSaves(req); cerr != nil {
		env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
	}
	for _, reg := range req.Regressions {
		if reg == nil {
			continue
		}
		if _, ok := knownRegressionTypes[opRoute(inst, reg.Type)]; !ok {
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
			// An unknown name is the field-reference rule's refusal
			// (FieldRefRefusals); only a schema field is type-checked.
			// The accepted-types table's refusal, the runtime's
			// (regression.BuildWith) word for word.
			if cerr := fieldaccept.Check(string(reg.Type), fieldaccept.SlotTarget, reg.Target, schema); cerr != nil {
				env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
			}
		}

		if len(reg.Predictors) == 0 {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"regression "+string(reg.Type)+" missing Predictors (≥1 required)",
				map[string]any{"type": string(reg.Type)},
			)
		}
		// Unknown: FieldRefRefusals; derived: untyped; vector:
		// vectorScalarRefusals — fieldaccept.Check judges none of them.
		for _, p := range reg.Predictors {
			if cerr := fieldaccept.Check(string(reg.Type), fieldaccept.SlotPredictor, p, schema); cerr != nil {
				env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
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

		// conf_level, when set, must lie in (0, 1) — the runtime's
		// regression.validateConfLevel rule, mirrored here.
		if c := reg.ConfLevel; c != 0 && !(c > 0 && c < 1) {
			env.AddError(
				string(errors.PROCESSING_CONFIG),
				string(reg.Type)+" conf_level must satisfy 0 < conf_level < 1 (zero selects the default of 0.95)",
				map[string]any{"conf_level": c, "type": string(reg.Type)},
			)
		}

		// Penalty / Alpha / L1Ratio (and no penalty on gamma), then vcov:
		// true on lasso / elastic net (under a Resample / Selection
		// modifier too) — the runtime's rules themselves
		// (internal/regpenalty, shared).
		if cerr := regpenalty.Check(reg); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
		if cerr := regpenalty.VcovRefusal(reg); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}

		// covariance: the block's shape and the robust-type refusals —
		// the runtime's rule itself (internal/regcov.Check, shared) — then
		// the cluster / order_by field types, which only predict checks
		// (the engine reads values, never types).
		if cerr := regcov.Check(reg); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		} else if reg.Covariance.Robust() {
			checkCovarianceFields(env, reg, schema)
		}
		// jackknife_groups / jackknife_by — the runtime's rule itself
		// (internal/regresample.Check, shared).
		if cerr := regresample.Check(reg); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}

		// L1-penalized OLS / GLM fits (Penalty=="l1" or "elasticnet")
		// without Resample report null standard errors and p-values;
		// that is PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED
		// (predict_advisories.go), never a warning.

		// Regularized OLS / GLM + Selection: regularization already
		// does feature selection (lasso zeroes coefficients) or shrinks
		// (ridge); pairing it with greedy stepwise / forward / backward
		// is rarely meaningful but not strictly invalid. Warn the user
		// so they know the combination is unusual.
		if (reg.Type == types.REG_OLS || reg.Type == types.REG_GLM) && reg.Penalty != "" && reg.Selection != "" {
			env.AddWarning(
				"PROCESSING_REGRESSION_REGULARIZED_SELECTION",
				string(reg.Type)+" combines Penalty="+reg.Penalty+" with Selection="+reg.Selection+"; regularization already performs feature shrinkage / selection, so layering greedy subset search on top is unusual and may produce a model harder to interpret than either alone",
				map[string]any{"penalty": reg.Penalty, "selection": reg.Selection, "type": string(reg.Type)},
			)
		}
	}
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

// routedRegression returns reg, or a copy whose Type is the route ("")
// when inst hides reg.Type — so RegressionSpec.Streamable answers for a
// hidden type exactly as for a never-registered one.
func routedRegression(reg *types.RegressionSpec, inst *InstanceSnapshot) *types.RegressionSpec {
	if !inst.Hidden(string(reg.Type)) {
		return reg
	}
	routed := *reg
	routed.Type = ""
	return &routed
}

// checkCovarianceFields type-checks a robust covariance's field
// references against schema: a cluster field is categorical or an
// unsigned integer (its levels are the clusters), an order_by field is
// a date, datetime or analytics-numeric field. An unknown name is the
// field-reference rule's refusal (FieldRefRefusals) and a derived
// column is untyped, so only a schema field is checked.
func checkCovarianceFields(env *descriptor.Envelope, reg *types.RegressionSpec, schema *encoding.Schema) {
	c := reg.Covariance
	if c.Cluster != "" {
		if f := schema.Field(c.Cluster); f != nil && !covarianceClusterType(f.Type) {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"regression covariance cluster field "+c.Cluster+" is not categorical or an unsigned integer (got "+f.Type.String()+")",
				map[string]any{"field": c.Cluster, "type": f.Type.String(), "regression": string(reg.Type)},
			)
		}
	}
	if c.OrderBy != "" {
		if f := schema.Field(c.OrderBy); f != nil && !(f.Type.IsNumericForAnalytics() || f.Type == encoding.FieldTypeDateTime) {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"regression covariance order_by field "+c.OrderBy+" is not a date, datetime or numeric field (got "+f.Type.String()+")",
				map[string]any{"field": c.OrderBy, "type": f.Type.String(), "regression": string(reg.Type)},
			)
		}
	}
}

// covarianceClusterType reports whether t can key clusters: a
// categorical dictionary or an unsigned integer id.
func covarianceClusterType(t encoding.FieldType) bool {
	switch t {
	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32,
		encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64:
		return true
	}
	return false
}

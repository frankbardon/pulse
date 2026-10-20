// Package regcov is the one rule over a regression's covariance block
// (types.RegressionSpec.Covariance), shared by the no-execute predict
// pass (internal/descriptor) and the regression engines
// (internal/processing/regression) so the two refuse the same requests
// with the same code and details. It reads the spec only — never a
// schema or a record — and imports neither side.
package regcov

import (
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Refusal reasons (details.reason) of PROCESSING_REGRESSION_VCOV_UNSUPPORTED
// raised for a robust covariance. The engine adds two data-dependent
// ones predict cannot see: ReasonClusters and ReasonOrderTies.
const (
	// ReasonType: the regression type does not carry the robust
	// estimators.
	ReasonType = "type"
	// ReasonPenalty: a penalized fit (l1 / l2 / elasticnet) has no
	// sandwich for its shrunken coefficients.
	ReasonPenalty = "penalty"
	// ReasonResample: Resample already replaces the standard errors.
	ReasonResample = "resample"
	// ReasonSelection: Selection's final refit is conditional on a
	// data-dependent predictor set.
	ReasonSelection = "selection"
	// ReasonBounds: a bounds-constrained fit's estimator sits on the
	// boundary when a bound binds, where no sandwich is valid.
	ReasonBounds = "bounds"
	// ReasonClusters: fewer than two clusters reached the fit.
	ReasonClusters = "clusters"
	// ReasonOrderTies: two fitted rows share an order_by value, so the
	// rows are not one series.
	ReasonOrderTies = "order_by_ties"
)

// robustTypes are the regression types that take a robust covariance:
// REG_OLS (residual sandwich), REG_GLM (IRLS score sandwich) and
// REG_BAYES_LINEAR (Müller's sandwich posterior).
var robustTypes = []types.RegressionType{types.REG_OLS, types.REG_GLM, types.REG_BAYES_LINEAR}

// Check returns the first fault of spec's covariance block, or nil when
// it is absent or well formed and supported:
//
//   - PROCESSING_CONFIG for a malformed block: an unknown or empty type,
//     cluster without a cluster field (or a cluster field on another
//     type), hac without order_by (or order_by / lags on another type),
//     a negative lags;
//   - PROCESSING_REGRESSION_VCOV_UNSUPPORTED for a robust type the fit
//     cannot honour: details.reason type, penalty, resample, selection
//     or bounds.
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Covariance == nil {
		return nil
	}
	return check(spec, slices.Contains(robustTypes, spec.Type))
}

// CheckDeclared is Check for an extension regression model whose
// registration declares the covariance type itself (the model computes
// it): the type is judged by the declaration, never by robustTypes, so
// only the block's shape and the penalty / resample / selection
// refusals apply.
func CheckDeclared(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Covariance == nil {
		return nil
	}
	return check(spec, true)
}

// check is Check with the type's robust support decided by the caller.
func check(spec *types.RegressionSpec, robustType bool) *errors.CodedError {
	c := spec.Covariance
	if !slices.Contains(types.AllCovarianceTypes(), c.Type) {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			string(spec.Type)+" covariance.type must be one of classical, hc0, hc1, hc2, hc3, cluster, hac; got \""+string(c.Type)+"\"",
			map[string]any{"covariance_type": string(c.Type), "type": string(spec.Type)},
		)
	}
	if err := checkShape(spec, c); err != nil {
		return err
	}
	if !c.Robust() {
		return nil
	}
	switch {
	case !robustType:
		return unsupported(spec, ReasonType, string(spec.Type))
	case spec.Penalty != "":
		return unsupported(spec, ReasonPenalty, spec.Penalty)
	case spec.Resample != "":
		return unsupported(spec, ReasonResample, spec.Resample)
	case spec.Selection != "":
		return unsupported(spec, ReasonSelection, spec.Selection)
	case spec.Type == types.REG_OLS && spec.Bounds.Active():
		return Unsupported(spec, ReasonBounds, map[string]any{"bounds": map[string]any{"nonnegative": spec.Bounds.Nonnegative}},
			"covariance "+string(spec.Covariance.Type)+" is not available with bounds (a binding bound puts the estimator on the boundary, where no sandwich is valid); drop covariance — resample bootstrap gives replicate standard errors of the bounded fit")
	}
	return nil
}

// checkShape is the field-coupling half of Check.
func checkShape(spec *types.RegressionSpec, c *types.RegressionCovariance) *errors.CodedError {
	config := func(msg string, details map[string]any) *errors.CodedError {
		details["covariance_type"] = string(c.Type)
		details["type"] = string(spec.Type)
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, string(spec.Type)+" "+msg, details)
	}
	switch {
	case c.Type == types.CovarianceCluster && c.Cluster == "":
		return config("covariance type cluster requires covariance.cluster (the field whose levels are the clusters)", map[string]any{})
	case c.Type != types.CovarianceCluster && c.Cluster != "":
		return config("covariance.cluster is read only by covariance type cluster", map[string]any{"cluster": c.Cluster})
	case c.Type == types.CovarianceHAC && c.OrderBy == "":
		return config("covariance type hac requires covariance.order_by (the field that orders the series)", map[string]any{})
	case c.Type != types.CovarianceHAC && c.OrderBy != "":
		return config("covariance.order_by is read only by covariance type hac", map[string]any{"order_by": c.OrderBy})
	case c.Type != types.CovarianceHAC && !c.Lags.IsZero():
		return config("covariance.lags is read only by covariance type hac", map[string]any{})
	}
	if n, ok := c.Lags.Value(); ok && n < 0 {
		return config("covariance.lags must be a non-negative integer or \"auto\"", map[string]any{"lags": n})
	}
	return nil
}

// unsupported is the VCOV_UNSUPPORTED refusal naming the blocking
// setting.
func unsupported(spec *types.RegressionSpec, reason, value string) *errors.CodedError {
	return Unsupported(spec, reason, map[string]any{reason: value},
		"covariance "+string(spec.Covariance.Type)+" is not available with "+reason+"="+value+"; drop covariance or the "+reason)
}

// Unsupported builds a PROCESSING_REGRESSION_VCOV_UNSUPPORTED refusal of
// spec's robust covariance with details.reason, the extra details and
// the covariance type; the engine's data-dependent refusals use it too.
func Unsupported(spec *types.RegressionSpec, reason string, extra map[string]any, msg string) *errors.CodedError {
	details := map[string]any{
		"reason":          reason,
		"type":            string(spec.Type),
		"covariance_type": string(spec.Covariance.EffectiveType()),
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, string(spec.Type)+" "+msg, details)
}

// TooFewClusters is the engine's refusal of a cluster fit whose rows
// span fewer than two clusters.
func TooFewClusters(spec *types.RegressionSpec, g int) *errors.CodedError {
	return Unsupported(spec, ReasonClusters, map[string]any{"clusters": g, "cluster": spec.Covariance.Cluster},
		"covariance cluster needs at least 2 clusters in the fitted rows; got "+strconv.Itoa(g))
}

// OrderTies is the engine's refusal of a hac fit whose order_by value
// repeats across fitted rows.
func OrderTies(spec *types.RegressionSpec, value float64) *errors.CodedError {
	return Unsupported(spec, ReasonOrderTies, map[string]any{"order_by": spec.Covariance.OrderBy, "value": value},
		"covariance hac reads one series: order_by "+spec.Covariance.OrderBy+" repeats a value across fitted rows")
}

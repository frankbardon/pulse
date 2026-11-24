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
	"strings"

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
	// rows are not one series — under an entity, two rows of the SAME
	// entity share it.
	ReasonOrderTies = "order_by_ties"
)

// panelType reports whether t is a covariance type that read a series order
// (order_by, lags) and accept a panel entity (entity).
func panelType(t types.CovarianceType) bool {
	return t == types.CovarianceHAC || t == types.CovarianceDriscollKraay
}

// robustTypes are the regression types that take a robust covariance,
// each with the robust estimators it computes (nil: every one):
// REG_OLS (residual sandwich), REG_GLM (IRLS score sandwich),
// REG_BAYES_LINEAR (Müller's sandwich posterior) and REG_MULTINOMIAL
// (the score sandwich over its K − 1 blocks, U68 E3-S1: hc0 / hc1 /
// cluster — hc2 / hc3 need a row leverage the multinomial's
// block-structured hat matrix has no scalar form of, hac /
// driscoll_kraay a series order, as REG_ORDINAL).
var robustTypes = map[types.RegressionType][]types.CovarianceType{
	types.REG_OLS:          nil,
	types.REG_GLM:          nil,
	types.REG_BAYES_LINEAR: nil,
	types.REG_MULTINOMIAL:  {types.CovarianceHC0, types.CovarianceHC1, types.CovarianceCluster},
}

// Types are the covariance.type values regression type t computes:
// classical first, then its robust estimators in AllCovarianceTypes'
// order — classical alone on a type without them.
func Types(t types.RegressionType) []types.CovarianceType {
	out := []types.CovarianceType{types.CovarianceClassical}
	for _, ct := range types.AllCovarianceTypes() {
		if ct != types.CovarianceClassical && supports(t, ct) {
			out = append(out, ct)
		}
	}
	return out
}

// supports reports whether regression type t computes the robust
// covariance ct.
func supports(t types.RegressionType, ct types.CovarianceType) bool {
	only, ok := robustTypes[t]
	return ok && (only == nil || slices.Contains(only, ct))
}

// Check returns the first fault of spec's covariance block, or nil when
// it is absent or well formed and supported:
//
//   - PROCESSING_CONFIG for a malformed block: an unknown or empty type,
//     cluster without a cluster field (or a cluster / cluster2 field on
//     another type, or cluster2 naming cluster), hac / driscoll_kraay without order_by (or order_by / lags /
//     entity on another type), an entity naming order_by, a negative
//     lags;
//   - PROCESSING_REGRESSION_VCOV_UNSUPPORTED for a robust type the fit
//     cannot honour: details.reason type (cluster2 included, off
//     REG_OLS / REG_GLM / REG_BAYES_LINEAR), penalty, resample, selection
//     or bounds.
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Covariance == nil {
		return nil
	}
	return check(spec, supports(spec.Type, spec.Covariance.Type))
}

// CheckDeclared is Check for an extension regression model whose
// registration declares the covariance type itself (the model computes
// it): the type is judged by the declaration, never by robustTypes, so
// only the block's shape and the penalty / resample / selection
// refusals apply — and cluster2: a declaration names covariance types,
// not the two-way form, so a declared cluster never reads a second
// field (ReasonType; an extension type is never in robustTypes).
func CheckDeclared(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Covariance == nil {
		return nil
	}
	return check(spec, true)
}

// twoWay reports whether regression type t computes the two-way cluster
// sandwich (covariance.cluster2): the types with every robust estimator
// (REG_OLS, REG_GLM, REG_BAYES_LINEAR) — never REG_MULTINOMIAL or an
// extension model, whose declared cluster is one-way.
func twoWay(t types.RegressionType) bool {
	only, ok := robustTypes[t]
	return ok && only == nil
}

// check is Check with the type's robust support decided by the caller.
func check(spec *types.RegressionSpec, robustType bool) *errors.CodedError {
	c := spec.Covariance
	if !slices.Contains(types.AllCovarianceTypes(), c.Type) {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			string(spec.Type)+" covariance.type must be one of "+strings.Join(typeNames(types.AllCovarianceTypes()), ", ")+"; got \""+string(c.Type)+"\"",
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
	case !robustType && len(robustTypes[spec.Type]) > 0:
		// A type with some robust estimators, not this one.
		var names []string
		for _, ct := range Types(spec.Type)[1:] {
			names = append(names, string(ct))
		}
		return Unsupported(spec, ReasonType, map[string]any{"supported": names},
			"covariance "+string(c.Type)+" is not computed by "+string(spec.Type)+" (it computes "+strings.Join(names, ", ")+"; hc2 / hc3 need a per-row leverage, hac / driscoll_kraay a series order); use one of those or drop covariance")
	case !robustType:
		return unsupported(spec, ReasonType, string(spec.Type))
	case c.Cluster2 != "" && !twoWay(spec.Type):
		return Unsupported(spec, ReasonType, map[string]any{"cluster2": c.Cluster2},
			"two-way covariance cluster (covariance.cluster2) is computed by REG_OLS, REG_GLM and REG_BAYES_LINEAR only; drop cluster2 for one-way clustering on cluster")
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
	case c.Type != types.CovarianceCluster && c.Cluster2 != "":
		return config("covariance.cluster2 is read only by covariance type cluster (two-way, beside cluster)", map[string]any{"cluster2": c.Cluster2})
	case c.Cluster2 != "" && c.Cluster2 == c.Cluster:
		return config("covariance.cluster2 names covariance.cluster "+c.Cluster+"; two-way clustering needs two different fields (drop cluster2 for one-way)", map[string]any{"cluster2": c.Cluster2})
	case panelType(c.Type) && c.OrderBy == "":
		return config("covariance type "+string(c.Type)+" requires covariance.order_by (the field that orders the series)", map[string]any{})
	case !panelType(c.Type) && c.OrderBy != "":
		return config("covariance.order_by is read only by covariance types hac and driscoll_kraay", map[string]any{"order_by": c.OrderBy})
	case !panelType(c.Type) && !c.Lags.IsZero():
		return config("covariance.lags is read only by covariance types hac and driscoll_kraay", map[string]any{})
	case !panelType(c.Type) && c.Entity != "":
		return config("covariance.entity is read only by covariance types hac and driscoll_kraay", map[string]any{"entity": c.Entity})
	case c.Entity != "" && c.Entity == c.OrderBy:
		return config("covariance.entity names covariance.order_by "+c.OrderBy+"; the entity is the field that splits the rows into series (a market), not the one that orders them", map[string]any{"entity": c.Entity})
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
// span fewer than two clusters of covariance.cluster.
func TooFewClusters(spec *types.RegressionSpec, g int) *errors.CodedError {
	return TooFewClustersOf(spec, spec.Covariance.Cluster, g)
}

// TooFewClustersOf is TooFewClusters naming field — covariance.cluster,
// or covariance.cluster2 under two-way clustering — in details.cluster.
func TooFewClustersOf(spec *types.RegressionSpec, field string, g int) *errors.CodedError {
	return Unsupported(spec, ReasonClusters, map[string]any{"clusters": g, "cluster": field},
		"covariance cluster needs at least 2 clusters of "+field+" in the fitted rows; got "+strconv.Itoa(g))
}

// OrderTies is the engine's refusal of a hac fit whose order_by value
// repeats across fitted rows.
func OrderTies(spec *types.RegressionSpec, value float64) *errors.CodedError {
	return Unsupported(spec, ReasonOrderTies, map[string]any{"order_by": spec.Covariance.OrderBy, "value": value},
		"covariance hac reads one series: order_by "+spec.Covariance.OrderBy+" repeats a value across fitted rows")
}

// EntityOrderTies is the engine's refusal of a panel fit (hac or
// driscoll_kraay with covariance.entity) in which one entity repeats an
// order_by value: each entity contributes at most one row per period.
// entity is the entity's level as the engine keys it (a label or a
// number, never the internal prefix).
func EntityOrderTies(spec *types.RegressionSpec, entity string, value float64) *errors.CodedError {
	c := spec.Covariance
	return Unsupported(spec, ReasonOrderTies, map[string]any{"order_by": c.OrderBy, "value": value, "entity": c.Entity, "entity_value": entity},
		"covariance "+string(c.Type)+" reads one row per entity per period: entity "+entity+" of "+c.Entity+" repeats order_by "+c.OrderBy+" value "+strconv.FormatFloat(value, 'g', -1, 64))
}

// typeNames spells covariance types as strings.
func typeNames(ts []types.CovarianceType) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

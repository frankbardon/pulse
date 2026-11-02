// Package regmargins is the one rule over a regression's marginal-effects
// block (types.RegressionSpec.Margins), shared by the no-execute predict
// pass (internal/descriptor) and the regression engines
// (internal/processing/regression) so the two refuse the same requests
// with the same code, message and details. It reads the spec (and, for
// the dependent-predictor rule, the request's derived-column reads) only
// and imports neither side.
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
//   - CheckDependent: no predictor derived from another predictor of the
//     same regression ("independent_predictors") — FEAT_POLY's x² beside
//     x, an interaction beside its factors: the effect of one holding the
//     other fixed is not defined. Any other derived predictor (a one-hot
//     dummy, a log column) is differentiated as given — the effect of a
//     unit change in that column.
package regmargins

import (
	"slices"
	"strings"

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

// CheckDependent returns the PROCESSING_CONFIG refusal of a margins
// block on a regression one of whose predictors is derived from another
// of its predictors, or nil. reads maps each derived column of the
// request (a feature, attribute or window output label) to the names
// its producer reads (record columns and earlier derived columns); a
// cohort field has no entry. A predictor's dependence is the transitive
// closure of reads; reaching another predictor of the same regression
// refuses ("independent_predictors": FEAT_POLY's x² beside x, an
// interaction beside its factors, a log of a predictor), because the
// effect of a unit change in one column holding the other fixed is then
// not the derivative of anything the analyst can change. Any other
// derived predictor — a one-hot dummy, FEAT_LOG of a column not itself
// a predictor, a lag — is differentiated as given: its effect is that
// of a unit change in the column. A nil spec, no block, or a type other
// than REG_OLS / REG_GLM passes; the field-reference walk (predict's and
// the runtime's one rule, internal/descriptor) calls it.
func CheckDependent(spec *types.RegressionSpec, reads map[string][]string) *errors.CodedError {
	if spec == nil || spec.Margins == nil || len(reads) == 0 || (spec.Type != types.REG_OLS && spec.Type != types.REG_GLM) {
		return nil
	}
	for _, p := range spec.Predictors {
		if _, derived := reads[p]; !derived {
			continue
		}
		if q, path := dependsOn(p, spec.Predictors, reads); q != "" {
			return refuse(spec, "need independent predictors: "+p+" is derived from "+q+", another predictor of this regression (through "+strings.Join(path, " <- ")+"), so the effect of a unit change in "+p+" holding "+q+" fixed is not defined; drop margins, or drop one of the two predictors",
				"independent_predictors", map[string]any{"predictor": p, "depends_on": q, "path": path})
		}
	}
	return nil
}

// dependsOn walks reads from p (depth first, each column once) and
// returns the first other predictor it reaches with the path p <- … <- q,
// or "".
func dependsOn(p string, predictors []string, reads map[string][]string) (string, []string) {
	seen := map[string]bool{p: true}
	var walk func(name string, path []string) (string, []string)
	walk = func(name string, path []string) (string, []string) {
		for _, in := range reads[name] {
			if seen[in] {
				continue
			}
			seen[in] = true
			next := append(slices.Clone(path), in)
			if slices.Contains(predictors, in) {
				return in, next
			}
			if q, qp := walk(in, next); q != "" {
				return q, qp
			}
		}
		return "", nil
	}
	return walk(p, []string{p})
}

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
//   - shape: type ame | mem, scale response | link, binary slope |
//     contrast, terms each a predictor, once (requires "type ame or
//     mem", "scale response or link", "binary slope or contrast", "a
//     predictor");
//   - the regression type: REG_BAYES_LINEAR is refused (requires "a
//     type that reports margins": REG_OLS, REG_GLM, REG_MULTINOMIAL here,
//     REG_ORDINAL through its registration); an extension or model
//     regression is the shared-slot
//     table's refusal (slot margins, declared by the registration's
//     Margins), and once declared CheckDeclared judges its block's shape
//     and fit exactly as Check does a built-in's;
//   - the fit: no penalty ("unpenalized" — a shrunken β has no
//     delta-method sampling distribution), no selection ("no_selection"
//     — post-selection inference), no bounds ("unbounded" — a binding
//     bound has no covariance). A resampled fit is admitted (U69 #639):
//     every replicate refit re-estimates the effects and their replicate
//     spread is the inference;
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
	case types.REG_OLS, types.REG_GLM, types.REG_BAYES_LINEAR, types.REG_MULTINOMIAL:
		return true
	}
	return false
}

// Contrast reports whether spec's block asks for binary predictors as
// discrete contrasts (binary contrast).
func Contrast(spec *types.RegressionSpec) bool {
	return spec != nil && spec.Margins != nil && spec.Margins.EffectiveBinary() == types.MarginsBinaryContrast
}

// IsBinaryValue reports whether v is exactly 0 or 1 — the values a
// predictor must take on every fitted row to be reported as a contrast.
func IsBinaryValue(v float64) bool { return v == 0 || v == 1 }

// Key is the result key of term's effect on outcome: "<outcome>:<term>"
// (the coefficients' "<category>:<term>" convention), or term alone when
// outcome is empty (a fit with no outcome dimension).
func Key(outcome, term string) string {
	if outcome == "" {
		return term
	}
	return outcome + ":" + term
}

// ResultKeys lists mg's effect keys in order: outcome-major over
// Outcomes then Terms ("<outcome>:<term>"), else Terms. The multiplicity
// fold's family order.
func ResultKeys(mg *types.RegressionMarginsResult) []string {
	if mg == nil {
		return nil
	}
	if len(mg.Outcomes) == 0 {
		return slices.Clone(mg.Terms)
	}
	out := make([]string, 0, len(mg.Outcomes)*len(mg.Terms))
	for _, o := range mg.Outcomes {
		for _, t := range mg.Terms {
			out = append(out, Key(o, t))
		}
	}
	return out
}

// Check returns the PROCESSING_CONFIG refusal of spec's margins block,
// or nil (no block, a well-formed block on a supported fit, or a
// non-built-in type). The engine (ValidateRegression) and predict
// (validateRegressions) both call it.
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Margins == nil || !builtin(spec.Type) {
		return nil
	}
	if cerr := checkShape(spec); cerr != nil {
		return cerr
	}
	if spec.Type == types.REG_BAYES_LINEAR {
		return refuse(spec, "are not reported by REG_BAYES_LINEAR (a posterior has no delta-method effect); drop margins, or fit a type that reports them",
			"a type that reports margins", nil)
	}
	return checkFit(spec)
}

// CheckDeclared is Check for a model whose registration declares margins
// (the extension shared-slot table admitted the block): the same shape
// and fit refusals, with the same codes and details — the type rule
// aside, which the declaration answers. Predict and the runtime adapter
// both reach it through descx.ExtensionRegressionSlotRefusal. Nil when
// spec carries no block.
func CheckDeclared(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Margins == nil {
		return nil
	}
	if cerr := checkShape(spec); cerr != nil {
		return cerr
	}
	return checkFit(spec)
}

// checkShape judges the block itself: type, scale, binary, terms.
func checkShape(spec *types.RegressionSpec) *errors.CodedError {
	m := spec.Margins
	if m.Type != "" && !slices.Contains(types.AllMarginsTypes(), m.Type) {
		return refuse(spec, "type must be one of "+strings.Join(types.AllMarginsTypes(), ", ")+" (empty selects ame); got "+m.Type,
			"type ame or mem", map[string]any{"key": "type", "value": m.Type, "allowed": types.AllMarginsTypes()})
	}
	if m.Scale != "" && !slices.Contains(types.AllMarginsScales(), m.Scale) {
		return refuse(spec, "scale must be one of "+strings.Join(types.AllMarginsScales(), ", ")+" (empty selects response); got "+m.Scale,
			"scale response or link", map[string]any{"key": "scale", "value": m.Scale, "allowed": types.AllMarginsScales()})
	}
	if m.Binary != "" && !slices.Contains(types.AllMarginsBinary(), m.Binary) {
		return refuse(spec, "binary must be one of "+strings.Join(types.AllMarginsBinary(), ", ")+" (empty selects slope); got "+m.Binary,
			"binary slope or contrast", map[string]any{"key": "binary", "value": m.Binary, "allowed": types.AllMarginsBinary()})
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
	return nil
}

// checkFit judges the fit the effects read: unpenalized, no selection,
// unbounded. A Resample is admitted: the engine re-estimates the effects
// on every replicate refit.
func checkFit(spec *types.RegressionSpec) *errors.CodedError {
	switch {
	case spec.Penalty != "":
		return refuse(spec, "need an unpenalized fit: a shrunken coefficient has no delta-method sampling distribution; drop penalty or margins",
			"unpenalized", map[string]any{"penalty": spec.Penalty})
	case spec.Selection != "":
		return refuse(spec, "need a fixed predictor set: after selection the coefficient covariance ignores the search; drop selection or margins",
			"no_selection", map[string]any{"selection": spec.Selection})
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
// of a unit change in the column. A nil spec, no block, or a built-in
// type that reports no margins (REG_BAYES_LINEAR, refused by its own
// rule) passes; REG_OLS, REG_GLM, REG_MULTINOMIAL and every model or
// extension regression (a declared block's effects are derivatives too)
// are judged. The field-reference walk (predict's and the runtime's one
// rule, internal/descriptor) calls it.
func CheckDependent(spec *types.RegressionSpec, reads map[string][]string) *errors.CodedError {
	if spec == nil || spec.Margins == nil || len(reads) == 0 || spec.Type == types.REG_BAYES_LINEAR {
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

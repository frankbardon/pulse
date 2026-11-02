// Package regcompare is the nested-model comparison of two Compose
// regression fits (types.ComposedRequest.Compare, U65 E3-S3): the one
// rule over a batch's `compare` entries (Check), read by the no-execute
// predict pass (internal/descriptor) and the Compose runtime
// (internal/service) at the same point — right after the fits' own
// structural rules — so the two refuse the same requests with the same
// code, message and details; and the arithmetic over two finished fits
// (Compute), which the runtime alone calls. It imports neither side.
//
// Nesting is verified on the specs, never guessed from the numbers:
//
//   - both names bind request-bound regression `fits` entries (not a
//     vector / SEM fit, not `from_sweep`), two different ones, both
//     REG_OLS or both REG_GLM;
//   - the restricted fit's terms — its predictors, plus "(intercept)"
//     when it fits one — are all terms of the full fit, which adds at
//     least one (the tested terms, in the full fit's order);
//   - the two fit requests are the same rows: equal (as JSON) once the
//     regression's predictors, intercept and reporting knobs (name,
//     vcov, conf_level, margins, collinearity, save, multiplicity) and
//     the request's label, return, disable_components and multiplicity
//     are blanked and a GLM link is resolved to its family default — so
//     the cohort, filters, features, weight, holdout, family, link, θ,
//     dispersion and covariance all agree. The full fit's rows (listwise
//     over a superset of columns) are then a subset of the restricted
//     fit's, and Compute's equal-row-count check makes them the same
//     rows (R's anova stops on unequal sizes the same way).
//
// The tests, R's references:
//
//   - f: ((D₀ − D₁)/q)/φ̂₁ on (q, df₁) — anova(m0, m1) on lm,
//     anova(m0, m1, test = "F") on glm; needs a dispersion the fit
//     estimates (REG_OLS σ̂², gamma's or a fixed-θ negative binomial's
//     Pearson χ²/df under dispersion "estimated").
//   - lrt: (D₀ − D₁)/φ₁ on q — anova(m0, m1, test = "Chisq" | "LRT"),
//     φ₁ = 1 on a fixed-dispersion family; a negative binomial with θ
//     estimated reads 2ℓ₁ − 2ℓ₀ (MASS anova.negbin).
//   - wald: b_Sᵀ V_SS⁻¹ b_S over the full fit's own covariance (classical
//     or robust) — lmtest::waldtest(m0, m1, vcov = V); W/q on (q, df)
//     where the coefficient table is Student t, χ² on q otherwise.
//
// Default (empty test): R's anova default — f on REG_OLS and a family
// that estimates its dispersion, lrt otherwise. Refused (reason
// "requires", details.requires): a penalty ("unpenalized"), selection
// ("no_selection"), active bounds ("unbounded"), a Wald test under
// resample ("no_resample" — the replicate covariance is not the one the
// test reads), an F test on a fixed dispersion ("estimated_dispersion"),
// an F or likelihood-ratio test under a probability weight
// ("no_probability_weight", alternative wald — the fits'
// pseudo-likelihoods are no likelihoods; the dbd35fb rule that leaves
// two_log_lik / aic absent there).
//
// Arithmetic (Compute) is FMA-free: every product feeding an add or
// subtract is barriered, the Wald solve is linalg's reference SolveSPD,
// the tails internal/statdist's.
package regcompare

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/internal/regsolve"
	"github.com/frankbardon/pulse/types"
)

// InterceptTerm is the intercept's coefficient key (the engines'
// regression.InterceptKey, "(intercept)").
const InterceptTerm = "(intercept)"

// The requires values (details.requires).
const (
	RequiresUnpenalized         = "unpenalized"
	RequiresNoSelection         = "no_selection"
	RequiresUnbounded           = "unbounded"
	RequiresNoResample          = "no_resample"
	RequiresEstimatedDispersion = "estimated_dispersion"
	RequiresNoProbabilityWeight = "no_probability_weight"
)

// Plan is one comparison as Check resolved it: what Compute runs and
// predict summarises.
type Plan struct {
	// Index is the entry's index in ComposedRequest.Compare.
	Index int
	// Name, Restricted and Full echo the spec.
	Name, Restricted, Full string
	// Test is the resolved test (the default filled in).
	Test types.ComparisonTest
	// Distribution is the statistic's reference distribution.
	Distribution types.ComparisonDistribution
	// Type is the fits' regression type; Family the GLM family ("" on
	// REG_OLS).
	Type   types.RegressionType
	Family string
	// Terms are the tested terms, in the full fit's term order.
	Terms []string
	// LogLik reports a likelihood ratio read off TwoLogLik (a negative
	// binomial with θ estimated).
	LogLik bool
	// FixedDispersion reports a family whose φ is fixed at 1.
	FixedDispersion bool
}

// FitNames returns every fit name a comparison reads (the fits the
// runtime runs with a capture for their model.RegressionFigures).
func FitNames(compare []types.FitComparisonSpec) map[string]bool {
	out := map[string]bool{}
	for _, c := range compare {
		if c.Restricted != "" {
			out[c.Restricted] = true
		}
		if c.Full != "" {
			out[c.Full] = true
		}
	}
	return out
}

// invalid builds PULSE_COMPARISON_INVALID for entry i.
func invalid(i int, name, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"compare": i, "name": name, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_COMPARISON_INVALID,
		fmt.Sprintf("compare[%d] %q: %s", i, name, msg), details)
}

// requires is a reason-"requires" refusal.
func requires(i int, name, req, msg string, extra map[string]any) *errors.CodedError {
	if extra == nil {
		extra = map[string]any{}
	}
	extra["requires"] = req
	return invalid(i, name, "requires", msg, extra)
}

// Check is the rule over the batch's comparisons (the package doc):
// compare against the batch's fits (as the runtime runs them — params
// merged, from_sweep unresolved) under the instance default weight
// defaultWeight (pulse.Options.DefaultWeight; nil for none). It returns
// each comparison's Plan (index-aligned) — nil, nil without any — or the
// first entry's refusal.
func Check(compare []types.FitComparisonSpec, fits []types.FitSpec, defaultWeight *types.WeightSpec) ([]Plan, *errors.CodedError) {
	if len(compare) == 0 {
		return nil, nil
	}
	byName := make(map[string]*types.FitSpec, len(fits))
	var available []string
	for k := range fits {
		f := &fits[k]
		if _, dup := byName[f.Name]; dup || f.Name == "" {
			continue
		}
		byName[f.Name] = f
		if f.FromSweep == nil && isRegressionFit(f) {
			available = append(available, f.Name)
		}
	}
	sort.Strings(available)
	seen := map[string]bool{}
	plans := make([]Plan, len(compare))
	for i := range compare {
		c := &compare[i]
		if c.Name == "" {
			return nil, invalid(i, c.Name, "name_missing", "a comparison needs a name", nil)
		}
		if seen[c.Name] {
			return nil, invalid(i, c.Name, "name_duplicate", "the name is used by an earlier comparison", nil)
		}
		seen[c.Name] = true
		p, err := checkOne(i, c, byName, available, defaultWeight)
		if err != nil {
			return nil, err
		}
		plans[i] = p
	}
	return plans, nil
}

// isRegressionFit reports a request-bound regression fit (one
// regression, no vector battery: model.FitKindOf).
func isRegressionFit(f *types.FitSpec) bool {
	return model.FitKindOf(f.Request) == types.FitKindRegression
}

// fitOf resolves one side's fit name.
func fitOf(i int, c *types.FitComparisonSpec, field, name string, byName map[string]*types.FitSpec, available []string) (*types.FitSpec, *errors.CodedError) {
	if name == "" {
		return nil, invalid(i, c.Name, "fit_missing", "`"+field+"` must name a `fits` entry", map[string]any{"field": field})
	}
	f, ok := byName[name]
	if !ok {
		return nil, invalid(i, c.Name, "fit_unknown", "`"+field+"` "+strconv.Quote(name)+" names no `fits` entry",
			map[string]any{"field": field, "value": name, "available": nonNil(available)})
	}
	if f.FromSweep != nil {
		return nil, invalid(i, c.Name, "from_sweep", "`"+field+"` "+strconv.Quote(name)+" is bound from the sweep; a comparison reads request-bound fits only",
			map[string]any{"field": field, "value": name})
	}
	if !isRegressionFit(f) {
		return nil, invalid(i, c.Name, "fit_kind", "`"+field+"` "+strconv.Quote(name)+" is not a regression fit",
			map[string]any{"field": field, "value": name, "available": nonNil(available)})
	}
	return f, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// checkOne judges comparison i.
func checkOne(i int, c *types.FitComparisonSpec, byName map[string]*types.FitSpec, available []string, defaultWeight *types.WeightSpec) (Plan, *errors.CodedError) {
	p := Plan{Index: i, Name: c.Name, Restricted: c.Restricted, Full: c.Full}
	f0, err := fitOf(i, c, "restricted", c.Restricted, byName, available)
	if err != nil {
		return p, err
	}
	f1, err := fitOf(i, c, "full", c.Full, byName, available)
	if err != nil {
		return p, err
	}
	if c.Restricted == c.Full {
		return p, invalid(i, c.Name, "same_fit", "`restricted` and `full` name the same fit", map[string]any{"value": c.Full})
	}
	r0, r1 := f0.Request.Regressions[0], f1.Request.Regressions[0]
	for _, side := range []struct {
		field string
		spec  *types.RegressionSpec
	}{{"restricted", r0}, {"full", r1}} {
		if side.spec.Type != types.REG_OLS && side.spec.Type != types.REG_GLM {
			return p, invalid(i, c.Name, "type_unsupported", "the "+side.field+" fit is "+string(side.spec.Type)+"; a comparison reads REG_OLS or REG_GLM fits",
				map[string]any{"field": side.field, "value": string(side.spec.Type), "requires": "type REG_OLS or REG_GLM"})
		}
	}
	if r0.Type != r1.Type {
		return p, invalid(i, c.Name, "type_mismatch", "the fits are "+string(r0.Type)+" and "+string(r1.Type)+"; both must be one type",
			map[string]any{"restricted_type": string(r0.Type), "full_type": string(r1.Type)})
	}
	p.Type = r0.Type
	if c.Test != "" && !slices.Contains(types.AllComparisonTests(), c.Test) {
		valid := make([]string, 0, 3)
		for _, t := range types.AllComparisonTests() {
			valid = append(valid, string(t))
		}
		return p, invalid(i, c.Name, "test_unknown", "test "+strconv.Quote(string(c.Test))+" is not f, lrt or wald",
			map[string]any{"field": "test", "value": string(c.Test), "valid": valid})
	}
	// The modifiers no comparison reads (either side: the requests
	// agree on them once the rows check passes, so naming the full fit's
	// is enough after it — but judge both so the refusal never depends
	// on the order the rules run in).
	for _, spec := range []*types.RegressionSpec{r0, r1} {
		switch {
		case spec.Penalty != "":
			return p, requires(i, c.Name, RequiresUnpenalized, "a penalized fit has no likelihood or covariance a nested test reads", nil)
		case spec.Selection != "":
			return p, requires(i, c.Name, RequiresNoSelection, "a selection fit's predictors are the search's, so its terms are not the spec's", nil)
		case spec.Bounds.Active():
			return p, requires(i, c.Name, RequiresUnbounded, "a bounded fit's constrained estimate is no nested-model test's", nil)
		}
	}
	terms0, terms1 := Terms(r0), Terms(r1)
	var missing []string
	for _, t := range terms0 {
		if !slices.Contains(terms1, t) {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		msg := "the restricted fit's terms are not all terms of the full fit"
		if len(terms0) > len(terms1) {
			msg += " (are `restricted` and `full` swapped?)"
		}
		return p, invalid(i, c.Name, "not_nested", msg, map[string]any{"missing": missing})
	}
	for _, t := range terms1 {
		if !slices.Contains(terms0, t) {
			p.Terms = append(p.Terms, t)
		}
	}
	if len(p.Terms) == 0 {
		return p, invalid(i, c.Name, "no_extra_terms", "the full fit adds no term to the restricted one", nil)
	}
	if keys := rowsDiffer(f0.Request, f1.Request); len(keys) > 0 {
		return p, invalid(i, c.Name, "request_mismatch",
			"the fit requests differ beyond the regression's predictors, intercept and reporting knobs, so the fits are not on the same rows",
			map[string]any{"keys": keys})
	}
	// The requests agree past here: the full fit's spec speaks for both.
	estimates := true
	if p.Type == types.REG_GLM {
		p.Family = r1.Family
		switch {
		case r1.Family == "gamma":
		case r1.Family == reglink.NegativeBinomial && r1.Theta > 0 && r1.Dispersion == types.NegBinDispersionEstimated:
		default:
			estimates = false
		}
		p.LogLik = r1.Family == reglink.NegativeBinomial && !(r1.Theta > 0)
		p.FixedDispersion = !estimates
	}
	p.Test = c.Test
	if p.Test == "" {
		p.Test = types.ComparisonTestLRT
		if estimates {
			p.Test = types.ComparisonTestF
		}
	}
	probability := probabilityWeighted(f1.Request, r1, defaultWeight)
	switch p.Test {
	case types.ComparisonTestF:
		if !estimates {
			return p, requires(i, c.Name, RequiresEstimatedDispersion,
				"an F test divides by an estimated dispersion; the "+p.Family+" fit fixes it (use lrt, or wald)", nil)
		}
		if probability {
			return p, requires(i, c.Name, RequiresNoProbabilityWeight,
				"under a probability weight the fits' deviances are pseudo-likelihoods, so no F test is defined",
				map[string]any{"alternative": string(types.ComparisonTestWald)})
		}
		p.Distribution = types.ComparisonDistributionF
	case types.ComparisonTestLRT:
		if probability {
			return p, requires(i, c.Name, RequiresNoProbabilityWeight,
				"under a probability weight the fits' likelihoods are pseudo-likelihoods, so no likelihood ratio is defined",
				map[string]any{"alternative": string(types.ComparisonTestWald)})
		}
		p.Distribution = types.ComparisonDistributionChiSquare
	case types.ComparisonTestWald:
		if r1.Resample != "" {
			return p, requires(i, c.Name, RequiresNoResample,
				"a Wald test reads the full fit's classical or robust covariance; a resampled fit's is the replicates'", nil)
		}
		// The coefficient table's reference distribution: Student t on
		// REG_OLS, and on a classical fit of a family that estimates its
		// dispersion; Wald z otherwise (a robust REG_GLM fit too).
		p.Distribution = types.ComparisonDistributionChiSquare
		if p.Type == types.REG_OLS || (estimates && !r1.Covariance.Robust()) {
			p.Distribution = types.ComparisonDistributionF
		}
	}
	return p, nil
}

// Terms is a regression spec's coefficient terms: "(intercept)" when it
// fits one, then its predictors in order.
func Terms(spec *types.RegressionSpec) []string {
	out := make([]string, 0, len(spec.Predictors)+1)
	if regsolve.FitsIntercept(spec) {
		out = append(out, InterceptTerm)
	}
	return append(out, spec.Predictors...)
}

// probabilityWeighted reports a fit whose resolved weight (its slot
// weight, else the request's, else the instance default) is of kind
// probability.
func probabilityWeighted(req *types.Request, spec *types.RegressionSpec, defaultWeight *types.WeightSpec) bool {
	var w *types.WeightSpec
	switch {
	case !spec.Weight.IsZero():
		w = spec.Weight.Spec()
	case req.Weight != nil:
		w = req.Weight
	default:
		w = defaultWeight
	}
	return w != nil && w.EffectiveKind() == types.WeightKindProbability
}

// rowsDiffer returns the keys on which the two fit requests differ once
// normalized (rowsView), sorted: top-level request keys, and
// "regressions[0].<key>" inside the regression. Nil when they agree.
func rowsDiffer(a, b *types.Request) []string {
	ma, mb := rowsView(a), rowsView(b)
	keys := map[string]bool{}
	for k := range ma {
		keys[k] = true
	}
	for k := range mb {
		keys[k] = true
	}
	var out []string
	for k := range keys {
		if string(ma[k]) != string(mb[k]) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// rowsView is a fit request as the rows rule compares it: its JSON keys
// (each value canonical JSON), the regression spread into
// "regressions[0].<key>", with the comparison-neutral knobs blanked.
func rowsView(req *types.Request) map[string]json.RawMessage {
	r := *req
	r.Label, r.Return, r.DisableComponents, r.Multiplicity = "", nil, nil, nil
	spec := *req.Regressions[0]
	spec.Name, spec.Predictors, spec.Intercept = "", nil, nil
	spec.Vcov, spec.ConfLevel, spec.Margins, spec.Collinearity = false, 0, nil, nil
	spec.Save, spec.Multiplicity = "", nil
	if spec.Type == types.REG_GLM && reglink.KnownFamily(spec.Family) {
		spec.Link = reglink.Resolve(spec.Family, spec.Link)
	}
	r.Regressions = nil
	out := map[string]json.RawMessage{}
	decodeInto(out, "", &r)
	decodeInto(out, "regressions[0].", &spec)
	return out
}

// decodeInto adds v's top-level JSON keys to out, prefixed.
func decodeInto(out map[string]json.RawMessage, prefix string, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		out[prefix+"<unmarshalable>"] = json.RawMessage(strconv.Quote(err.Error()))
		return
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		out[prefix+"<undecodable>"] = raw
		return
	}
	for k, val := range m {
		// Re-marshal through any so object keys sort: equal values
		// compare equal whatever their source order.
		var anyv any
		if json.Unmarshal(val, &anyv) == nil {
			if canon, err := json.Marshal(anyv); err == nil {
				val = canon
			}
		}
		out[prefix+k] = val
	}
}

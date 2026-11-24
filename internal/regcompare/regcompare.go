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
//   - both names bind regression `fits` entries (two vector fits are
//     nested member-wise — battery.go; two SEM fits are the chi-square
//     difference test — sem.go; a `from_sweep` one is judged per sweep
//     slot — sweep.go), two
//     different ones, both
//     REG_OLS, both REG_GLM, both REG_MULTINOMIAL (admitted by the rule
//     itself as a declared-like plan) or both one model whose
//     declaration carries Compare (REG_ORDINAL, an embedder's: the
//     declared predicate, read by predict and the runtime off the same
//     instance view);
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
//     where the coefficient table is Student t, χ² on q otherwise. Under
//     resample (U69 #642) V is the full fit's jackknife / bootstrap
//     replicate covariance — the one its standard errors are the √diag
//     of — and the test is χ² on q, as the resampled coefficient table is
//     Wald z: car::linearHypothesis(m1, vcov. = V, test = "Chisq").
//
// A declared model (Plan.Declared) reports its own figures
// (extend.CompareRegression): its terms are its predictors (an ordinal
// fit's thresholds are no tested term), lrt is the plain deviance change
// D₀ − D₁ on χ²(q) — anova.polr — and the default, wald is χ² on q, f is
// refused (estimated_dispersion: no dispersion is estimated); two fits
// whose params.levels differ are refused levels_differ (their thresholds
// are other terms), and Compute fails the comparison alone
// (PULSE_COMPARISON_ROWS_DIFFER, reason terms_differ) when the fits'
// non-tested terms still differ by name.
//
// REG_MULTINOMIAL is planned the same way (Plan.Declared): its terms are
// its predictors, and its figures map each to the K − 1
// "<category>:<term>" keys it fits (model.RegressionFigures.TermKeys),
// so lrt is anova.multinom — the deviance change on χ²((K − 1)·q) — and
// wald reads every key of the added predictors; two fits naming
// different references are refused reference_differs (their
// coefficients are other contrasts), and fits whose category sets differ
// fail alone at Compute (terms_differ).
//
// Default (empty test): R's anova default — f on REG_OLS and a family
// that estimates its dispersion, lrt otherwise. Refused (reason
// "requires", details.requires): a penalty ("unpenalized"), selection
// ("no_selection"), active bounds ("unbounded"), an F test on a fixed
// dispersion ("estimated_dispersion"),
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
	"github.com/frankbardon/pulse/internal/paramread"
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
	RequiresEstimatedDispersion = "estimated_dispersion"
	RequiresNoProbabilityWeight = "no_probability_weight"
	// RequiresComparableType is type_unsupported's requires: REG_OLS,
	// REG_GLM, REG_MULTINOMIAL or a model whose declaration carries
	// Compare.
	RequiresComparableType = "type REG_OLS, REG_GLM, REG_MULTINOMIAL or declared_compare"
)

// builtinComparable reports a built-in regression type the rule admits
// itself, never through the declared predicate: REG_OLS, REG_GLM and
// REG_MULTINOMIAL (a declared-like plan, U68 E5-S2: its terms its
// predictors, its figures one "<category>:<term>" key per non-reference
// category and term).
func builtinComparable(t types.RegressionType) bool {
	return t == types.REG_OLS || t == types.REG_GLM || t == types.REG_MULTINOMIAL
}

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
	// Declared reports an extension-shaped model whose declaration
	// carries Compare (REG_ORDINAL, an embedder's): its terms are its
	// predictors, its likelihood ratio the plain deviance change, its
	// Wald test chi-square, and Compute holds the two fits' other terms
	// (an ordinal model's thresholds) equal by name.
	Declared bool
	// SweepBound reports a side bound from the sweep (`from_sweep`):
	// the runtime re-judges the plan on the winning slot (Rebind).
	SweepBound bool
	// Unresolved is what the static rule could not decide over the
	// sweep slots (nil when every slot resolves the same plan).
	Unresolved *Unresolved
	// Battery reports a comparison of two vector fits (battery.go):
	// Terms are the tested members, Members the restricted battery's
	// members — one test row each (ComputeBattery).
	Battery bool
	Members []string
	// SEM reports a comparison of two SEM fits (sem.go: ComputeSEM runs
	// it); Scaled its Satorra–Bentler (2001) scaled difference (two
	// WLSMV fits).
	SEM, Scaled bool
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
//
// declared reports a regression type beyond REG_OLS / REG_GLM whose
// declaration carries Compare (descriptor.ComparableRegressions — predict
// and the runtime hand it the same instance view); nil admits none.
//
// A from_sweep fit is judged as its Request reads (the representative
// model.ResolveSweepFits filled, or the winner's fit request at run):
// CheckSweep is the static rule that judges it over every sweep slot.
func Check(compare []types.FitComparisonSpec, fits []types.FitSpec, defaultWeight *types.WeightSpec, declared func(types.RegressionType) bool) ([]Plan, *errors.CodedError) {
	return CheckSweep(compare, fits, nil, defaultWeight, declared)
}

// indexFits is fits by name (the first of a duplicated name; an
// unnamed entry skipped) and the sorted names of the regression fits
// among them — a from_sweep entry's as its resolved Request reads.
func indexFits(fits []types.FitSpec) (map[string]*types.FitSpec, []string) {
	byName := make(map[string]*types.FitSpec, len(fits))
	var available []string
	for k := range fits {
		f := &fits[k]
		if _, dup := byName[f.Name]; dup || f.Name == "" {
			continue
		}
		byName[f.Name] = f
		if isRegressionFit(f) || isSEMFit(f) {
			available = append(available, f.Name)
		}
	}
	sort.Strings(available)
	return byName, available
}

// CheckSweep is Check with the batch's expanded sweep slots (the
// model.SweepScope.Slots both sides build from one expansion; nil
// without a sweep): a comparison reading a from_sweep fit is judged
// against the fit request of every slot that could win (sweep.go) —
// refused when every slot refuses, its Plan marked Unresolved when only
// some do or the passing slots resolve another test or distribution.
// Predict and the runtime's pre-fit rule call it with the same slots;
// the runtime re-judges the actual winner with Rebind.
func CheckSweep(compare []types.FitComparisonSpec, fits []types.FitSpec, slots []*types.Request, defaultWeight *types.WeightSpec, declared func(types.RegressionType) bool) ([]Plan, *errors.CodedError) {
	if len(compare) == 0 {
		return nil, nil
	}
	byName, available := indexFits(fits)
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
		p, err := checkEntry(i, c, byName, available, slots, defaultWeight, declared)
		if err != nil {
			return nil, err
		}
		plans[i] = p
	}
	return plans, nil
}

// isRegressionFit reports a regression fit (one
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
	if !isRegressionFit(f) && !isSEMFit(f) {
		return nil, invalid(i, c.Name, "fit_kind", "`"+field+"` "+strconv.Quote(name)+" is not a regression or SEM fit",
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
func checkOne(i int, c *types.FitComparisonSpec, byName map[string]*types.FitSpec, available []string, defaultWeight *types.WeightSpec, declared func(types.RegressionType) bool) (Plan, *errors.CodedError) {
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
	if isSEMFit(f0) || isSEMFit(f1) {
		return checkSEM(i, c, f0, f1)
	}
	r0, r1 := f0.Request.Regressions[0], f1.Request.Regressions[0]
	for _, side := range []struct {
		field string
		spec  *types.RegressionSpec
	}{{"restricted", r0}, {"full", r1}} {
		if !builtinComparable(side.spec.Type) && (declared == nil || !declared(side.spec.Type)) {
			return p, invalid(i, c.Name, "type_unsupported", "the "+side.field+" fit is "+string(side.spec.Type)+"; a comparison reads REG_OLS, REG_GLM, REG_MULTINOMIAL or a model declaring comparisons",
				map[string]any{"field": side.field, "value": string(side.spec.Type), "requires": RequiresComparableType})
		}
	}
	if r0.Type != r1.Type {
		return p, invalid(i, c.Name, "type_mismatch", "the fits are "+string(r0.Type)+" and "+string(r1.Type)+"; both must be one type",
			map[string]any{"restricted_type": string(r0.Type), "full_type": string(r1.Type)})
	}
	p.Type = r0.Type
	p.Declared = p.Type != types.REG_OLS && p.Type != types.REG_GLM
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
	if p.Declared {
		terms0, terms1 = slices.Clone(r0.Predictors), slices.Clone(r1.Predictors)
	}
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
		if p.Type == types.REG_MULTINOMIAL && slices.Contains(keys, "regressions[0].reference") {
			return p, invalid(i, c.Name, "reference_differs",
				"the fits' reference categories differ, so their \"<category>:<term>\" coefficients are other contrasts and the models are not nested",
				map[string]any{"restricted_reference": r0.Reference, "full_reference": r1.Reference})
		}
		if p.Declared && slices.Contains(keys, "regressions[0].params") {
			if l0, l1 := paramLevels(r0), paramLevels(r1); string(l0) != string(l1) {
				return p, invalid(i, c.Name, "levels_differ",
					"the fits' outcome levels (params.levels) differ, so their thresholds are not the same terms and the models are not nested",
					map[string]any{"restricted_levels": rawOrNil(l0), "full_levels": rawOrNil(l1)})
			}
		}
		return p, invalid(i, c.Name, "request_mismatch",
			"the fit requests differ beyond the regression's predictors, intercept and reporting knobs, so the fits are not on the same rows",
			map[string]any{"keys": keys})
	}
	// The requests agree past here: the full fit's spec speaks for both.
	estimates := true
	if p.Declared {
		// A declared model's deviance is −2ℓ: no dispersion is
		// estimated, so a likelihood ratio divides by none and no F test
		// is defined.
		estimates = false
		p.FixedDispersion = true
	}
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
			fit := p.Family
			if p.Declared {
				fit = string(p.Type)
			}
			return p, requires(i, c.Name, RequiresEstimatedDispersion,
				"an F test divides by an estimated dispersion; the "+fit+" fit fixes it (use lrt, or wald)", nil)
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
		p.Distribution = waldDistribution(p, r1, estimates)
	}
	return p, nil
}

// waldDistribution is a Wald test's reference distribution: the full
// fit's coefficient table's — Student t (so F = W/q on its df) on
// REG_OLS and on a classical fit of a family that estimates its
// dispersion; Wald z (χ² on q) otherwise: a robust REG_GLM fit, a
// declared model, and any resampled fit (U69 #642: its table is Wald z
// on the replicate standard errors, the replicate covariance the
// statistic reads).
func waldDistribution(p Plan, full *types.RegressionSpec, estimates bool) types.ComparisonDistribution {
	switch {
	case p.Declared, full.Resample != "":
		return types.ComparisonDistributionChiSquare
	case p.Type == types.REG_OLS, estimates && !full.Covariance.Robust():
		return types.ComparisonDistributionF
	}
	return types.ComparisonDistributionChiSquare
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

// paramLevels is a spec's params.levels as canonical JSON (nil when
// absent): the outcome level set a declared categorical-response model
// pins (REG_ORDINAL's), which its threshold terms are named by.
func paramLevels(spec *types.RegressionSpec) json.RawMessage {
	if len(spec.Params) == 0 {
		return nil
	}
	// A lenient look: the model decodes (and records) params.levels
	// itself.
	var m map[string]any
	if paramread.Probe(spec.Params, &m) != nil {
		return nil
	}
	v, ok := m["levels"]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

// rawOrNil is a levels value for details: the decoded list, nil when
// absent (the data's own order).
func rawOrNil(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
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

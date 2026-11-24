package regcompare

import (
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/types"
)

// sem.go is the nested comparison of two SEM fits (types.FitKindSEM,
// U69 #643): the chi-square difference test of lavaan's lavTestLRT.
// Nesting is declared, never proven: the two fits are taken on the same
// data, groups and observed variables, and the restricted fit has more
// degrees of freedom — the constraints its model (params.model,
// group_equal, group_partial) declares are trusted to nest it in the
// full fit (no NET check).
//
// Statically (checkSEM — predict and the runtime's pre-fit rule) a pair
// is refused PULSE_COMPARISON_INVALID with reason
//
//   - kind_mismatch    — one side a SEM fit, the other a regression fit;
//   - test_unsupported — test f or wald (a SEM comparison is lrt, the
//     chi-square difference; valid ["lrt"]);
//   - request_mismatch — the fit requests differ beyond the model
//     (params.model / group_equal / group_partial) and the reporting
//     and solver knobs (the request's label, return,
//     disable_components, multiplicity; the matrix's name; params
//     ci_level, resample, bootstrap_iters, rng_seed, solver,
//     information, ftol, max_iter, grad_tol): one cohort, filters,
//     members, weight, group, estimator, ordered set, mean structure;
//   - same_model       — the model knobs agree too (no df can differ);
//   - observed_differ  — both models written and their observed
//     variables differ (restricted_only / full_only). The members agree
//     (request_mismatch) and MAT_SEM refuses a member its model never
//     names, so this is the one way two otherwise-valid fits differ in
//     observed variables — reported here, ahead of the fit's own
//     refusal.
//
// At run (ComputeSEM, over the fits' model.SEMFit figures) a pair fails
// ALONE: a failed fit with its own code (details.fit); one fit
// following lavaan's conventions or modelling the means and the other
// not (conventions_differ: a `~ 1` line or a := line in one model
// only), or a restricted fit whose df does not exceed the full fit's
// (df_not_positive) — PULSE_COMPARISON_INVALID, the df being the fits'
// (thresholds and group levels are the data's); unequal row or group
// counts — PULSE_COMPARISON_ROWS_DIFFER.
//
// The test (Distribution chisq on q = d₀ − d₁):
//
//   - ml / dwls: T₀ − T₁, each T the fit's model χ² (lavaan's standard
//     test) — lavTestLRT(m0, m1) on standard tests;
//   - wlsmv: the Satorra–Bentler (2001) scaled difference (T₀ − T₁)/c_d,
//     c_d = (d₀c₀ − d₁c₁)/q, c the mean-adjusted scaling factor
//     tr(UΓ)/df (c₁ = 1 on a saturated full fit, 0 when T₁ ≈ 0; c_d < 0
//     is undefined) — lavTestLRT(m0, m1, method =
//     "satorra.bentler.2001") on the satorra.bentler test. MLR is no
//     Pulse estimator.

// The SEM-pair reasons (details.reason).
const (
	ReasonKindMismatch      = "kind_mismatch"
	ReasonTestUnsupported   = "test_unsupported"
	ReasonSameModel         = "same_model"
	ReasonObservedDiffer    = "observed_differ"
	ReasonConventionsDiffer = "conventions_differ"
	ReasonDFNotPositive     = "df_not_positive"
)

// semSaturatedTol is lavaan's all.equal(T, 0): a statistic this close
// to zero is a perfect fit, whose scaling factor is 0.
const semSaturatedTol = 1.5e-8

// semNeutralParams are the MAT_SEM params a SEM comparison's fits may
// differ on: the model knobs (semModelParams) and the reporting and
// solver knobs, which move no row and no test statistic.
var semNeutralParams = []string{"model", "group_equal", "group_partial", "ci_level", "resample", "bootstrap_iters", "rng_seed",
	"solver", "information", "ftol", "max_iter", "grad_tol"}

// isSEMFit reports a SEM fit (one MAT_SEM battery: model.FitKindOf).
func isSEMFit(f *types.FitSpec) bool {
	return model.FitKindOf(f.Request) == types.FitKindSEM
}

// fitKind is f's kind for a refusal's details.
func fitKind(f *types.FitSpec) string {
	if f.FromSweep != nil {
		return string(types.FitKindRegression)
	}
	return string(model.FitKindOf(f.Request))
}

// checkSEM judges comparison i between f0 and f1, at least one a SEM fit.
func checkSEM(i int, c *types.FitComparisonSpec, f0, f1 *types.FitSpec) (Plan, *errors.CodedError) {
	p := Plan{Index: i, Name: c.Name, Restricted: c.Restricted, Full: c.Full, SEM: true}
	if !isSEMFit(f0) || !isSEMFit(f1) {
		return p, invalid(i, c.Name, ReasonKindMismatch, "the fits are a "+fitKind(f0)+" and a "+fitKind(f1)+" fit; a SEM fit compares with a SEM fit only",
			map[string]any{"restricted_kind": fitKind(f0), "full_kind": fitKind(f1)})
	}
	switch c.Test {
	case "", types.ComparisonTestLRT:
	case types.ComparisonTestF, types.ComparisonTestWald:
		return p, invalid(i, c.Name, ReasonTestUnsupported, "test "+strconv.Quote(string(c.Test))+" reads regression coefficients; two SEM fits compare by their chi-square difference (lrt)",
			map[string]any{"field": "test", "value": string(c.Test), "valid": []string{string(types.ComparisonTestLRT)}})
	default:
		valid := make([]string, 0, 3)
		for _, t := range types.AllComparisonTests() {
			valid = append(valid, string(t))
		}
		return p, invalid(i, c.Name, "test_unknown", "test "+strconv.Quote(string(c.Test))+" is not f, lrt or wald",
			map[string]any{"field": "test", "value": string(c.Test), "valid": valid})
	}
	if keys := semRowsDiffer(f0.Request, f1.Request); len(keys) > 0 {
		return p, invalid(i, c.Name, "request_mismatch",
			"the SEM fit requests differ beyond the model (params.model, group_equal, group_partial) and the reporting and solver knobs, so the fits are not on the same data",
			map[string]any{"keys": keys})
	}
	m0, m1 := semModelView(f0.Request), semModelView(f1.Request)
	if m0 == m1 {
		return p, invalid(i, c.Name, ReasonSameModel,
			"the two SEM fits are the same model (params.model, group_equal and group_partial agree), so neither restricts the other", nil)
	}
	if o0, ok0 := semObserved(f0.Request); ok0 {
		if o1, ok1 := semObserved(f1.Request); ok1 && !slices.Equal(o0, o1) {
			return p, observedDiffer(i, c.Name, o0, o1)
		}
	}
	p.Test = types.ComparisonTestLRT
	p.Distribution = types.ComparisonDistributionChiSquare
	p.Terms = []string{}
	p.Scaled = semEstimator(f1.Request) == string(sem.EstimatorWLSMV)
	return p, nil
}

// observedDiffer is the observed_differ refusal over two sorted sets.
func observedDiffer(i int, name string, o0, o1 []string) *errors.CodedError {
	only := func(a, b []string) []string {
		out := []string{}
		for _, v := range a {
			if !slices.Contains(b, v) {
				out = append(out, v)
			}
		}
		return out
	}
	return invalid(i, name, ReasonObservedDiffer, "the two SEM models are not over the same observed variables, so their chi-squares are not against the same saturated model",
		map[string]any{"restricted_only": only(o0, o1), "full_only": only(o1, o0)})
}

// semParams is a SEM fit request's MAT_SEM params, decoded leniently
// (nil when absent or undecodable — the slot's own rule refuses those).
func semParams(req *types.Request) map[string]any {
	if len(req.Matrices) != 1 || len(req.Matrices[0].Params) == 0 {
		return nil
	}
	var m map[string]any
	if paramread.Probe(req.Matrices[0].Params, &m) != nil {
		return nil
	}
	return m
}

// semEstimator is params.estimator, "ml" when absent.
func semEstimator(req *types.Request) string {
	if s, ok := semParams(req)["estimator"].(string); ok {
		return s
	}
	return string(sem.EstimatorML)
}

// semRowsDiffer returns the keys on which two SEM fit requests differ
// once the comparison-neutral knobs are blanked, sorted: top-level
// request keys, "matrices[0].<key>" and "matrices[0].params.<key>".
func semRowsDiffer(a, b *types.Request) []string {
	ma, mb := semRowsView(a), semRowsView(b)
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
	slices.Sort(out)
	return out
}

// semRowsView is a SEM fit request as the rows rule compares it.
func semRowsView(req *types.Request) map[string]json.RawMessage {
	r := *req
	r.Label, r.Return, r.DisableComponents, r.Multiplicity = "", nil, nil, nil
	spec := req.Matrices[0]
	spec.Name, spec.Params = "", nil
	r.Matrices = nil
	out := map[string]json.RawMessage{}
	decodeInto(out, "", &r)
	decodeInto(out, "matrices[0].", &spec)
	params := maps.Clone(semParams(req))
	if params == nil {
		params = map[string]any{}
	}
	for _, k := range semNeutralParams {
		delete(params, k)
	}
	// The defaults written or not: estimator ml, no mean structure.
	if v, ok := params["estimator"]; !ok || v == string(sem.EstimatorML) {
		delete(params, "estimator")
	}
	if v, ok := params["meanstructure"]; !ok || v == false {
		delete(params, "meanstructure")
	}
	for k, v := range params {
		raw, err := json.Marshal(v)
		if err != nil {
			raw = json.RawMessage(strconv.Quote(err.Error()))
		}
		out["matrices[0].params."+k] = raw
	}
	return out
}

// semModelView is a SEM fit request's model knobs as one comparable
// string: params.model (lines trimmed), group_equal and group_partial
// (each a sorted set; a comma-separated string is a list).
func semModelView(req *types.Request) string {
	p := semParams(req)
	desc, _ := p["model"].(string)
	lines := strings.Split(desc, "\n")
	for k := range lines {
		lines[k] = strings.TrimSpace(lines[k])
	}
	view := struct {
		Model        string   `json:"model"`
		GroupEqual   []string `json:"group_equal"`
		GroupPartial []string `json:"group_partial"`
	}{strings.TrimSpace(strings.Join(lines, "\n")), semSet(p["group_equal"]), semSet(p["group_partial"])}
	raw, _ := json.Marshal(view)
	return string(raw)
}

// semSet is a group_equal / group_partial value as a sorted set.
func semSet(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		for _, s := range strings.Split(x, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// semObserved is a written model's observed variables, sorted (false
// for the default model — its observed variables are the battery's,
// known only to the schema — and for a description that does not
// specify: the slot's own rule refuses it).
func semObserved(req *types.Request) ([]string, bool) {
	p := semParams(req)
	desc, _ := p["model"].(string)
	if strings.TrimSpace(desc) == "" {
		return nil, false
	}
	var (
		m   *sem.Model
		err error
	)
	if g, _ := p["group"].(string); g != "" {
		m, err = sem.NewGroupModel(desc, sem.ModelOptions{})
	} else {
		m, err = sem.NewModel(desc, false)
	}
	if err != nil {
		return nil, false
	}
	out := slices.Clone(m.Observed)
	slices.Sort(out)
	return out, true
}

// ComputeSEM runs SEM comparison p over its two finished fits: their
// ComposedResponse.Fits summaries (r0 / r1, for a failed fit's error)
// and the bound figures (s0 / s1, model.SEMFit — nil for a failed fit).
// Data-dependent refusals fail the comparison alone (the result's
// Error, a nil error); a fitted fit without its figures is an
// invariant violation (PROCESSING_INTERNAL).
func ComputeSEM(p Plan, r0, r1 *types.SEMFitResult, s0, s1 *model.SEMFit) (types.FitComparisonResult, error) {
	out := types.FitComparisonResult{Name: p.Name, Restricted: p.Restricted, Full: p.Full, Test: p.Test}
	for _, side := range []struct {
		name string
		res  *types.SEMFitResult
		fit  *model.SEMFit
	}{{p.Restricted, r0, s0}, {p.Full, r1, s1}} {
		if side.res != nil && side.res.Error != nil {
			se := side.res.Error
			details := maps.Clone(se.Details)
			if details == nil {
				details = map[string]any{}
			}
			details["fit"] = side.name
			out.Error = &types.SlotError{Code: se.Code, Message: "fit " + side.name + " failed: " + se.Message, Details: details}
			return out, nil
		}
		if side.fit == nil {
			return out, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"comparison "+p.Name+" reads SEM fit "+side.name+", which carries no fitted figures",
				map[string]any{"compare": p.Index, "fit": side.name})
		}
	}
	fail := func(code errors.Code, reason, msg string, extra map[string]any) (types.FitComparisonResult, error) {
		details := map[string]any{"compare": p.Index, "name": p.Name, "reason": reason, "restricted": p.Restricted, "full": p.Full}
		maps.Copy(details, extra)
		out.Error = &types.SlotError{Code: string(code), Message: "compare[" + strconv.Itoa(p.Index) + "] " + strconv.Quote(p.Name) + ": " + msg, Details: details}
		return out, nil
	}
	if s0.Lavaan != s1.Lavaan || s0.Means != s1.Means {
		return fail(errors.PULSE_COMPARISON_INVALID, ReasonConventionsDiffer,
			"one SEM fit follows lavaan's conventions or models the means and the other does not, so their chi-squares are not comparable",
			map[string]any{"restricted_conventions": s0.Conventions(), "full_conventions": s1.Conventions(),
				"restricted_means": s0.Means, "full_means": s1.Means})
	}
	if s0.N != s1.N || s0.SumWeights != s1.SumWeights || !sameGroups(s0, s1) {
		return fail(errors.PULSE_COMPARISON_ROWS_DIFFER, "rows_differ",
			"SEM fits "+p.Restricted+" and "+p.Full+" used different rows or groups, so they cannot be compared",
			map[string]any{"restricted_n_obs": s0.N, "full_n_obs": s1.N, "restricted_groups": len(s0.Groups), "full_groups": len(s1.Groups)})
	}
	d0, d1 := s0.DoF, s1.DoF
	q := d0 - d1
	if !(q > 0) {
		msg := "the restricted SEM fit's degrees of freedom do not exceed the full fit's, so it restricts nothing"
		if q < 0 {
			msg += " (are `restricted` and `full` swapped?)"
		}
		return fail(errors.PULSE_COMPARISON_INVALID, ReasonDFNotPositive, msg,
			map[string]any{"restricted_df": d0, "full_df": d1})
	}
	out.Distribution = p.Distribution
	out.Terms = []string{}
	out.DF = q
	out.NObs = int(s1.N)
	out.RestrictedDFResidual, out.FullDFResidual = d0, d1
	out.RestrictedDeviance, out.FullDeviance = s0.Chi2, s1.Chi2
	diff := s0.Chi2 - s1.Chi2
	if p.Scaled {
		cd := sbScaling(s0, s1, q)
		out.ScalingFactor = cd
		out.Statistic = diff / cd
	} else {
		out.Statistic = diff
	}
	out.PValue = chiSquareP(out.Statistic, q)
	return out, nil
}

// sbScaling is the Satorra–Bentler (2001) difference scaling factor
// (d₀c₀ − d₁c₁)/q — lavaan's lav_test_diff_sb2001 — NaN where negative.
func sbScaling(s0, s1 *model.SEMFit, q float64) float64 {
	c0 := s0.TraceUG / s0.DoF
	var c1 float64
	switch {
	case s1.DoF == 0:
		c1 = 1
	case math.Abs(s1.Chi2) < semSaturatedTol:
		c1 = 0
	default:
		c1 = s1.TraceUG / s1.DoF
	}
	cd := (float64(s0.DoF*c0) - float64(s1.DoF*c1)) / q
	if cd < 0 || math.IsNaN(cd) {
		return math.NaN()
	}
	return cd
}

// sameGroups reports two fits over the same groups: one level list,
// each level's row count and weight equal.
func sameGroups(a, b *model.SEMFit) bool {
	if len(a.Groups) != len(b.Groups) {
		return false
	}
	for g := range a.Groups {
		x, y := a.Groups[g], b.Groups[g]
		if x.Level != y.Level || x.N != y.N || x.SumWeights != y.SumWeights {
			return false
		}
	}
	return true
}

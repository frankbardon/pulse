package regcompare

import (
	"encoding/json"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// battery.go is a comparison of two vector fits (U69 #643, owner
// decision 2026-11-24): member-wise nesting over the batteries'
// co-moments. A vector fit holds a battery's listwise mean and raw
// co-moment M2 — no response, no predictors — so the comparison reads
// each restricted member j as the response of a Gaussian linear model
// on the other members:
//
//	restricted  lm(j ~ the other restricted members)
//	full        lm(j ~ the other full members)
//
// one row per restricted member, in the restricted battery's order.
// The rule (checkBattery, predict and the runtime's pre-fit rule):
//
//   - both names bind vector fits (a restricted vector fit with a full
//     regression or SEM fit is fit_kind);
//   - each battery lists its members — a MAT_* slot's or fitted
//     attribute's `fields`, or a request `vectors` entry it names that
//     lists `fields` (labels from its `labels`); a `pattern` or a
//     vec_* field resolves its members against the cohort only at run
//     (members_unlisted);
//   - every restricted member (label and column) is a full member
//     (not_nested, the strays under `missing`), and the full battery
//     adds at least one (no_extra_terms: the tested members, the full
//     battery's order);
//   - the two fit requests are the same rows: equal (as JSON) once the
//     battery's member lists (its `fields`, the named `vectors` entry's
//     `fields` / `labels`), the battery's reporting knobs (a matrix
//     slot's name, encoding, multiplicity; an attribute's label) and the
//     request's label, return, disable_components and multiplicity are
//     blanked (request_mismatch, `keys`);
//   - no probability weight (requires no_probability_weight): the
//     co-moment under one carries no likelihood or coefficient
//     covariance a nested test reads.
//
// The tests, exact from the full fit's M2 (both models' residual sums of
// squares are Schur complements of it: RSS(j | S) = M2_jj −
// M2_jS M2_SS⁻¹ M2_Sj, the intercept implied by the centring), with
// N* the fit's (Σw unweighted or under a frequency weight), q the tested
// member count, df₁ = N* − p_full and σ̂² = RSS₁/df₁ — R's anova(lm(j ~
// restricted), lm(j ~ full)) per member:
//
//   - f (the default): ((RSS₀ − RSS₁)/q)/σ̂² on (q, df₁);
//   - lrt: (RSS₀ − RSS₁)/σ̂² on χ²(q) — anova(..., test = "Chisq");
//   - wald: W/q on (q, df₁), the classical covariance's Wald statistic
//     — equal to the F statistic on a Gaussian linear model
//     (lmtest::waldtest).
//
// Every row's p-value is one member of the comparison's multiplicity
// family. Gaussian only: a battery has no family.

// ReasonMembersUnlisted is the refusal of a battery whose members the
// request does not list.
const ReasonMembersUnlisted = "members_unlisted"

// batteryMember is one battery member: its label (the axis name a
// consumer aligns to) and its column.
type batteryMember struct {
	label, field string
}

// isVectorFit reports a vector fit (model.FitKindOf).
func isVectorFit(f *types.FitSpec) bool {
	return f != nil && model.FitKindOf(f.Request) == types.FitKindVector
}

// vectorNames are the sorted names of fits' vector fits.
func vectorNames(byName map[string]*types.FitSpec) []string {
	out := []string{}
	for name, f := range byName {
		if isVectorFit(f) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// batteryOf is req's battery: its one Matrices spec or vector-fit
// attribute — the member list, the slot weight, the named `vectors`
// entry ("" when inline) and the slot's location.
func batteryOf(req *types.Request) (fields []string, vector string, weight types.SlotWeight, where string) {
	if len(req.Matrices) == 1 {
		m := &req.Matrices[0]
		return m.Fields, m.Vector, m.Weight, "matrices[0]"
	}
	for k, a := range req.Attributes {
		if a != nil && model.IsVectorFitAttribute(a.Type) {
			return a.Fields, a.Vector, a.Weight, "attributes[" + strconv.Itoa(k) + "]"
		}
	}
	return nil, "", types.SlotWeight{}, ""
}

// batteryMembers lists req's battery members, ok false when the request
// does not list them (a pattern vector, a vec_* field, no members).
func batteryMembers(req *types.Request) ([]batteryMember, bool) {
	fields, vector, _, _ := batteryOf(req)
	var labels []string
	if vector != "" {
		if len(fields) > 0 {
			return nil, false
		}
		k := slices.IndexFunc(req.Vectors, func(v types.VectorSpec) bool { return v.Name == vector })
		if k < 0 || req.Vectors[k].Pattern != "" {
			return nil, false
		}
		fields, labels = req.Vectors[k].Fields, req.Vectors[k].Labels
	}
	if len(fields) == 0 || (len(labels) > 0 && len(labels) != len(fields)) {
		return nil, false
	}
	out := make([]batteryMember, len(fields))
	for k, f := range fields {
		out[k] = batteryMember{label: f, field: f}
		if len(labels) > 0 {
			out[k].label = labels[k]
		}
	}
	return out, true
}

// batteryRowsView is a vector fit request as the battery rows rule
// compares it (rowsView's battery twin): its JSON keys with the member
// lists and the reporting knobs blanked.
func batteryRowsView(req *types.Request) map[string]json.RawMessage {
	r := *req
	r.Label, r.Return, r.DisableComponents, r.Multiplicity = "", nil, nil, nil
	vector := ""
	if len(r.Matrices) == 1 {
		m := r.Matrices[0]
		vector = m.Vector
		m.Fields, m.Name, m.Encoding, m.Multiplicity = nil, "", "", nil
		r.Matrices = []types.MatrixSpec{m}
	} else {
		r.Attributes = slices.Clone(r.Attributes)
		for k, a := range r.Attributes {
			if a != nil && model.IsVectorFitAttribute(a.Type) {
				b := *a
				vector = b.Vector
				b.Fields, b.Label = nil, ""
				r.Attributes[k] = &b
				break
			}
		}
	}
	if vector != "" {
		r.Vectors = slices.Clone(r.Vectors)
		for k := range r.Vectors {
			if r.Vectors[k].Name == vector {
				r.Vectors[k].Fields, r.Vectors[k].Labels = nil, nil
			}
		}
	}
	out := map[string]json.RawMessage{}
	decodeInto(out, "", &r)
	return out
}

// batteryProbabilityWeighted reports a battery whose resolved weight
// (its slot weight, else the request's, else the instance default) is
// of kind probability.
func batteryProbabilityWeighted(req *types.Request, defaultWeight *types.WeightSpec) bool {
	_, _, sw, _ := batteryOf(req)
	var w *types.WeightSpec
	switch {
	case !sw.IsZero():
		w = sw.Spec()
	case req.Weight != nil:
		w = req.Weight
	default:
		w = defaultWeight
	}
	return w != nil && w.EffectiveKind() == types.WeightKindProbability
}

// checkBattery judges comparison i whose restricted side names a vector
// fit (the file doc).
func checkBattery(i int, c *types.FitComparisonSpec, byName map[string]*types.FitSpec, defaultWeight *types.WeightSpec) (Plan, *errors.CodedError) {
	p := Plan{Index: i, Name: c.Name, Restricted: c.Restricted, Full: c.Full, Battery: true}
	available := vectorNames(byName)
	if c.Full == "" {
		return p, invalid(i, c.Name, "fit_missing", "`full` must name a `fits` entry", map[string]any{"field": "full"})
	}
	f1, ok := byName[c.Full]
	if !ok {
		return p, invalid(i, c.Name, "fit_unknown", "`full` "+strconv.Quote(c.Full)+" names no `fits` entry",
			map[string]any{"field": "full", "value": c.Full, "available": available})
	}
	if !isVectorFit(f1) {
		return p, invalid(i, c.Name, "fit_kind", "`full` "+strconv.Quote(c.Full)+" is not a vector fit; the restricted fit is one, and a vector fit compares with a vector fit",
			map[string]any{"field": "full", "value": c.Full, "available": available})
	}
	if c.Restricted == c.Full {
		return p, invalid(i, c.Name, "same_fit", "`restricted` and `full` name the same fit", map[string]any{"value": c.Full})
	}
	if c.Test != "" && !slices.Contains(types.AllComparisonTests(), c.Test) {
		valid := make([]string, 0, 3)
		for _, t := range types.AllComparisonTests() {
			valid = append(valid, string(t))
		}
		return p, invalid(i, c.Name, "test_unknown", "test "+strconv.Quote(string(c.Test))+" is not f, lrt or wald",
			map[string]any{"field": "test", "value": string(c.Test), "valid": valid})
	}
	f0 := byName[c.Restricted]
	var sides [2][]batteryMember
	for k, side := range []struct {
		field string
		fit   *types.FitSpec
	}{{"restricted", f0}, {"full", f1}} {
		m, ok := batteryMembers(side.fit.Request)
		if !ok {
			_, _, _, where := batteryOf(side.fit.Request)
			return p, invalid(i, c.Name, ReasonMembersUnlisted,
				"the "+side.field+" vector fit's battery does not list its members (`fields`, or a `vectors` entry with `fields`); a `pattern` or a vector field resolves them only at run, so nesting cannot be judged",
				map[string]any{"field": side.field, "value": side.fit.Name, "slot": where})
		}
		sides[k] = m
	}
	m0, m1 := sides[0], sides[1]
	var missing []string
	for _, m := range m0 {
		if !slices.Contains(m1, m) {
			missing = append(missing, m.label)
		}
	}
	if len(missing) > 0 {
		msg := "the restricted battery's members are not all members of the full battery"
		if len(m0) > len(m1) {
			msg += " (are `restricted` and `full` swapped?)"
		}
		return p, invalid(i, c.Name, "not_nested", msg, map[string]any{"missing": missing})
	}
	for _, m := range m1 {
		if !slices.Contains(m0, m) {
			p.Terms = append(p.Terms, m.label)
		}
	}
	if len(p.Terms) == 0 {
		return p, invalid(i, c.Name, "no_extra_terms", "the full battery adds no member to the restricted one", nil)
	}
	for _, m := range m0 {
		p.Members = append(p.Members, m.label)
	}
	if keys := batteryRowsDiffer(f0.Request, f1.Request); len(keys) > 0 {
		return p, invalid(i, c.Name, "request_mismatch",
			"the vector fit requests differ beyond the battery's members and reporting knobs, so the fits are not on the same rows",
			map[string]any{"keys": keys})
	}
	if batteryProbabilityWeighted(f1.Request, defaultWeight) {
		return p, requires(i, c.Name, RequiresNoProbabilityWeight,
			"under a probability weight a vector fit's co-moment carries no likelihood or coefficient covariance a nested test reads", nil)
	}
	p.Test = c.Test
	if p.Test == "" {
		p.Test = types.ComparisonTestF
	}
	p.Distribution = types.ComparisonDistributionF
	if p.Test == types.ComparisonTestLRT {
		p.Distribution = types.ComparisonDistributionChiSquare
	}
	return p, nil
}

// batteryRowsDiffer is rowsDiffer over batteryRowsView.
func batteryRowsDiffer(a, b *types.Request) []string {
	ma, mb := batteryRowsView(a), batteryRowsView(b)
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

// ComputeBattery runs battery comparison p (Plan.Battery) over its two
// finished vector fits: their ComposedResponse.Fits summaries
// (restricted, full) and bound states (v0, v1). A fit that failed on
// its data, or unequal rows (N or Σw), fail the comparison alone (the
// result's Error); a fitted fit without its state is an invariant
// violation (PROCESSING_INTERNAL). The top-level single-pair figures
// (statistic, p_value, the deviances) are undefined — null on the wire —
// and each restricted member's test is its Members row.
func ComputeBattery(p Plan, restricted, full *types.VectorFitResult, v0, v1 *model.VectorFit) (types.FitComparisonResult, error) {
	out := types.FitComparisonResult{Name: p.Name, Restricted: p.Restricted, Full: p.Full, Test: p.Test}
	for _, side := range []struct {
		name string
		res  *types.VectorFitResult
		v    *model.VectorFit
	}{{p.Restricted, restricted, v0}, {p.Full, full, v1}} {
		if side.res == nil {
			return out, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"comparison "+p.Name+" reads vector fit "+side.name+", which produced no result",
				map[string]any{"compare": p.Index, "fit": side.name})
		}
		if se := side.res.Error; se != nil {
			details := maps.Clone(se.Details)
			if details == nil {
				details = map[string]any{}
			}
			details["fit"] = side.name
			out.Error = &types.SlotError{Code: se.Code, Message: "fit " + side.name + " failed: " + se.Message, Details: details}
			return out, nil
		}
		if side.v == nil {
			return out, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"comparison "+p.Name+" reads vector fit "+side.name+", which bound no state",
				map[string]any{"compare": p.Index, "fit": side.name})
		}
	}
	if v0.N != v1.N || v0.SumWeights != v1.SumWeights {
		out.Error = &types.SlotError{
			Code:    string(errors.PULSE_COMPARISON_ROWS_DIFFER),
			Message: "fits " + p.Restricted + " and " + p.Full + " used different rows, so they cannot be compared",
			Details: map[string]any{
				"restricted": p.Restricted, "full": p.Full,
				"restricted_n_obs": v0.N, "full_n_obs": v1.N,
			},
		}
		return out, nil
	}
	idx := func(labels []string) ([]int, error) {
		out := make([]int, len(labels))
		for k, l := range labels {
			out[k] = slices.Index(v1.Labels, l)
			if out[k] < 0 {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
					"comparison "+p.Name+": member "+strconv.Quote(l)+" is not a label of vector fit "+p.Full,
					map[string]any{"compare": p.Index, "fit": p.Full, "member": l})
			}
		}
		return out, nil
	}
	rIdx, err := idx(p.Members)
	if err != nil {
		return out, err
	}
	nStar := v1.NStar()
	q := float64(len(p.Terms))
	pFull, pRes := float64(v1.P()), float64(len(p.Members))
	df1, df0 := nStar-pFull, nStar-pRes
	out.Distribution = p.Distribution
	out.Terms = slices.Clone(p.Terms)
	out.DF = q
	out.NObs = int(v1.N)
	out.RestrictedDFResidual, out.FullDFResidual = df0, df1
	if p.Distribution == types.ComparisonDistributionF {
		out.DFResidual = df1
	}
	nan := math.NaN()
	out.Statistic, out.PValue, out.RestrictedDeviance, out.FullDeviance = nan, nan, nan, nan
	all := make([]int, v1.P())
	for k := range all {
		all[k] = k
	}
	out.Members = make([]types.FitComparisonMember, len(rIdx))
	for k, j := range rIdx {
		others := func(set []int) []int {
			return slices.DeleteFunc(slices.Clone(set), func(x int) bool { return x == j })
		}
		rss0 := schurRSS(v1.M2, j, others(rIdx))
		rss1 := schurRSS(v1.M2, j, others(all))
		row := types.FitComparisonMember{Member: p.Members[k], RestrictedDeviance: rss0, FullDeviance: rss1}
		sigma2 := nan
		if df1 > 0 {
			sigma2 = rss1 / df1
		}
		switch p.Test {
		case types.ComparisonTestLRT:
			row.Dispersion = sigma2
			row.Statistic = (rss0 - rss1) / sigma2
			row.PValue = chiSquareP(row.Statistic, q)
		case types.ComparisonTestF, types.ComparisonTestWald:
			if p.Test == types.ComparisonTestF {
				row.Dispersion = sigma2
			}
			row.Statistic = ((rss0 - rss1) / q) / sigma2
			row.PValue = nan
			if !math.IsNaN(row.Statistic) {
				row.PValue = statdist.FSurvival(row.Statistic, q, df1)
			}
		default:
			return out, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"comparison "+p.Name+" has no resolved test", map[string]any{"compare": p.Index})
		}
		out.Members[k] = row
	}
	return out, nil
}

// schurRSS is the residual sum of squares of member j regressed (with
// an intercept) on the members s over the co-moment m2: m2_jj −
// m2_js m2_ss⁻¹ m2_sj, FMA-free (linalg's reference SolveSPD, every
// product barriered). NaN when m2_ss is not positive definite or a cell
// is not finite.
func schurRSS(m2 [][]float64, j int, s []int) float64 {
	if !finite(m2[j][j]) {
		return math.NaN()
	}
	if len(s) == 0 {
		return m2[j][j]
	}
	n := len(s)
	v, err := linalg.NewSym(n, nil)
	if err != nil {
		return math.NaN()
	}
	b := make([]float64, n)
	for a := 0; a < n; a++ {
		b[a] = m2[s[a]][j]
		if !finite(b[a]) {
			return math.NaN()
		}
		for c := a; c < n; c++ {
			x := m2[s[a]][s[c]]
			if !finite(x) {
				return math.NaN()
			}
			v.Set(a, c, x)
		}
	}
	x, err := linalg.SolveSPD(v, linalg.NewVec(b))
	if err != nil {
		return math.NaN()
	}
	explained := 0.0
	for a := 0; a < n; a++ {
		explained += float64(b[a] * x.At(a))
	}
	return m2[j][j] - explained
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

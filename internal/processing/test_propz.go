package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// propZRow implements TEST_PROP_Z as a streaming row test: a
// two-proportion z-test on the conversion (success) rate of a
// categorical field, split into two groups by SplitBy.
//
// Spec:
//
//	field    — categorical column whose dictionary contains the success
//	           value, or a packed_bool (levels "0" / "1")
//	split_by — categorical or packed_bool column producing exactly two groups
//	params   — {"success": "<dictionary value>"} ("0" / "1" on a packed_bool)
//
// State is a pair of counters per split group: (n, successes). Each
// record contributes ±1 to either successes (when field == success
// value) or just to n. Finalize emits the pooled z-statistic, the
// per-group rates, and a Wald confidence interval on the rate diff.
//
// One-proportion (params.p0, split_by absent — TEST_T's params.mu
// precedent): one bucket, the score z = (p̂ − p0)/√(p0(1−p0)/N*) with
// a Wald interval on p̂ − p0 (finalizeOneProportion).
//
// Weighted (.claude/reference/weighting.md, Weighted inference): each
// group's rate is p̂_g = Σw_success/Σw_g and its sample size N*_g (Σw_g
// under frequency, Kish n_eff_g under probability); the pooled rate is
// Σ(p̂_g·N*_g)/ΣN*_g. Cohen's h is scale-free. `n` stays the raw row
// count; `successes` becomes Σw_success.
//
// Covariate-adjusted (params.covariate, CUPED — test_cuped.go): each
// group also folds (x, I) into a CoWelford bucket, I the success
// indicator, and Finalize reads the adjusted indicator
// I′ = I − θ(x − x̄) by the Wald form (Variant two_proportion_adjusted):
// diff = Ī′_a − Ī′_b, SE² = Σ_g v′_g/N*_g with v′_g = M2′_g/Σw_g, the
// weighted POPULATION variance of I′ (so θ = 0 is p̂(1 − p̂) exactly and
// the interval is the unadjusted Wald one), z = diff/SE — the pooled
// rate is no null variance for an adjusted difference.
type propZRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	success string
	alpha   float64
	// p0 is the one-proportion test's hypothesised rate; one is set
	// when params.p0 selects that form (split_by absent).
	p0  float64
	one bool
	// reference is params.reference (two-proportion): the split_by
	// level read second, so diff = other − reference ("" = alphabetical).
	reference string
	// cuped is the params.covariate adjustment's state (two-proportion;
	// nil without a covariate), folded beside groups.
	cuped *cupedArms

	groups map[string]*propGroup
	order  []string

	// testWeight is the slot's stamped weight (nil: unweighted).
	testWeight
}

// propGroup is one split group: its weight moments (raw row count N,
// Σw, Σw²; the value folded is irrelevant) plus the raw success count
// and the success weight Σw_success.
type propGroup struct {
	w         weighting.Welford
	successes int64
	succW     float64
}

type propZParams struct {
	Success   string   `json:"success"`
	P0        *float64 `json:"p0"`
	Reference string   `json:"reference"`
	Covariate string   `json:"covariate"`
}

func newPropZRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PROP_Z requires field (categorical outcome column)")
	}
	var params propZParams
	paramread.Declare(paramread.Test, string(spec.Type), &params)
	if len(spec.Params) > 0 {
		if err := paramread.Decode(paramread.Test, string(spec.Type), spec.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"TEST_PROP_Z params: "+err.Error())
		}
	}
	if spec.SplitBy == "" && params.P0 == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PROP_Z requires split_by (categorical group column)")
	}
	alpha := spec.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	if alpha <= 0 || alpha >= 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INVALID_ALPHA,
			fmt.Sprintf("alpha %g not in (0, 1)", spec.Alpha),
			map[string]any{"alpha": spec.Alpha})
	}
	if params.Success == "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SUCCESS_VALUE_MISSING,
			"TEST_PROP_Z requires params.success (the dictionary value treated as a success)",
			map[string]any{"type": string(types.TEST_PROP_Z)})
	}
	var p0 float64
	if params.P0 != nil {
		p0 = *params.P0
		if cerr := fieldaccept.P0Refusal(string(spec.Type), spec.SplitBy, p0); cerr != nil {
			return nil, cerr
		}
	}
	// The accepted-types table's refusals, field then split_by (a
	// categorical outcome and a categorical split); predict raises the
	// same ones.
	if cerr := fieldaccept.Check(string(spec.Type), fieldaccept.SlotField, spec.Field, schema); cerr != nil {
		return nil, cerr
	}
	if cerr := fieldaccept.Check(string(spec.Type), fieldaccept.SlotSplitBy, spec.SplitBy, schema); cerr != nil {
		return nil, cerr
	}
	// success is strictly one of the outcome's levels — "0" / "1" on a
	// packed_bool, a dictionary entry on a categorical: a typo (or
	// "true") would count no success and report a confident zero rate.
	// Predict raises the same refusal.
	if cerr := fieldaccept.SuccessLevelRefusal(string(spec.Type), spec.Field, params.Success, schema); cerr != nil {
		return nil, cerr
	}
	// A reference names a split_by level (and needs split_by: the
	// one-proportion form has no second group).
	if cerr := fieldaccept.ReferenceRefusal(string(spec.Type), spec.SplitBy, params.Reference, schema); cerr != nil {
		return nil, cerr
	}
	// A covariate (CUPED) adjusts the two-proportion difference: it
	// needs split_by (so the one-proportion form refuses it), must be
	// numeric and not the outcome. Predict raises the same refusal.
	if cerr := fieldaccept.CovariateRefusal(string(spec.Type), spec.Field, spec.SplitBy, params.Covariate, schema); cerr != nil {
		return nil, cerr
	}
	return &propZRow{
		spec:       spec,
		schema:     schema,
		field:      spec.Field,
		splitBy:    spec.SplitBy,
		success:    params.Success,
		alpha:      alpha,
		p0:         p0,
		one:        params.P0 != nil,
		reference:  params.Reference,
		cuped:      newCupedArms(params.Covariate),
		groups:     make(map[string]*propGroup),
		testWeight: newTestWeight(spec),
	}, nil
}

func (p *propZRow) UpdateRow(record *Record) error {
	outcome, oOk, err := record.LevelValueChecked(p.field)
	if err != nil {
		return err
	}
	if !oOk {
		return nil
	}
	var key string
	if !p.one {
		k, kOk, err := record.LevelValueChecked(p.splitBy)
		if err != nil {
			return err
		}
		if !kOk {
			return nil
		}
		key = k
	}
	// Under a covariate a null one drops the row listwise, before its
	// weight is judged (the mean tests' order).
	var x float64
	if p.cuped != nil {
		var xOk bool
		if x, xOk = p.cuped.covariateValue(record); !xOk {
			return nil
		}
	}
	w, ok := p.rowWeight(record)
	if !ok {
		return nil
	}
	g, exists := p.groups[key]
	if !exists {
		g = &propGroup{}
		p.groups[key] = g
		p.order = append(p.order, key)
	}
	g.w.Add(0, w)
	indicator := 0.0
	if outcome == p.success {
		g.successes++
		g.succW += w
		indicator = 1
	}
	if p.cuped != nil {
		p.cuped.add(key, x, indicator, w)
	}
	return nil
}

func (p *propZRow) Finalize() (*types.TestResult, error) {
	defer p.reset()
	if p.one {
		return p.finalizeOneProportion()
	}
	keys := append([]string(nil), p.order...)
	sort.Strings(keys)
	if len(keys) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_PROP_Z requires 2 split groups, got %d", len(keys)),
			map[string]any{"groups": keys, "min_required": 2})
	}
	if len(keys) > 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_PROP_Z sees %d groups", len(keys))+remedyTwoGroupsOrChiSq(nil),
			map[string]any{"groups": keys, "max_allowed": 2})
	}
	keys, err := referenceOrder(types.TEST_PROP_Z, p.reference, keys)
	if err != nil {
		return nil, err
	}
	a, b := p.groups[keys[0]], p.groups[keys[1]]
	if a.w.N < 1 || b.w.N < 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_PROP_Z requires n ≥ 1 per group, got %d / %d", a.w.N, b.w.N),
			map[string]any{"n": []int64{a.w.N, b.w.N}})
	}
	// Unweighted, Σw is the exact integer n and Σw_success the exact
	// success count, so every expression below reduces to the
	// unweighted arithmetic bit for bit.
	na, nb := a.w.NStar(p.basis), b.w.NStar(p.basis)
	pa := a.succW / a.w.SumW
	pb := b.succW / b.w.SumW
	// Σ(p̂_g·N*_g) is the success mass on w*: Σw_success itself under
	// frequency, c_g·Σw_success under probability.
	sa := a.succW * p.basis.Scale(a.w.SumW, a.w.SumWSq)
	sb := b.succW * p.basis.Scale(b.w.SumW, b.w.SumWSq)
	pooled := (sa + sb) / (na + nb)
	if pooled == 0 || pooled == 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_PROP_Z: pooled proportion is 0 or 1; z-statistic undefined",
			map[string]any{"pooled": pooled})
	}
	if p.cuped != nil {
		return p.finalizeAdjusted(keys, a, b, na, nb, pa, pb, pooled)
	}
	sePool := math.Sqrt(pooled * (1 - pooled) * (1/na + 1/nb))
	z := (pa - pb) / sePool
	pvalue := normalTwoSidedP(z)
	// Wald (unpooled) standard error for the CI on the rate diff.
	seUnpool := math.Sqrt(pa*(1-pa)/na + pb*(1-pb)/nb)
	// Two-sided normal critical value at alpha/2.
	zcrit := normalCriticalTwoSided(p.alpha)
	ciLow := (pa - pb) - zcrit*seUnpool
	ciHigh := (pa - pb) + zcrit*seUnpool
	details := map[string]any{
		"success":    p.success,
		"groups":     keys,
		"n":          []int64{a.w.N, b.w.N},
		"successes":  []int64{a.successes, b.successes},
		"proportion": []float64{pa, pb},
		"diff":       pa - pb,
		"pooled":     pooled,
		"ci_low":     ciLow,
		"ci_high":    ciHigh,
	}
	if p.basis.Weighted() {
		details["successes"] = []float64{a.succW, b.succW}
	}
	noteReference(details, p.reference)
	p.noteGroups(details, []*weighting.Welford{&a.w, &b.w})
	setEffectSize(details, "cohens_h", cohensH(pa, pb))
	return &types.TestResult{
		Label:      testLabel(p.spec),
		Type:       types.TEST_PROP_Z,
		Variant:    "two_proportion_pooled",
		Statistic:  z,
		PValue:     pvalue,
		Alpha:      p.alpha,
		RejectNull: pvalue < p.alpha,
		Details:    details,
	}, nil
}

// finalizeAdjusted is the covariate-adjusted two-proportion test
// (params.covariate, CUPED): the Wald z on the adjusted indicator's
// moments (the type comment). proportion, diff, the interval, z, p and
// Cohen's h read the adjusted rates; successes, pooled and n stay the
// listwise sample's raw figures; diff_unadjusted is p̂_a − p̂_b.
func (p *propZRow) finalizeAdjusted(keys []string, a, b *propGroup, na, nb, pa, pb, pooled float64) (*types.TestResult, error) {
	adjA, adjB, f := p.cuped.adjust(p.cuped.groups[keys[0]], p.cuped.groups[keys[1]])
	se2 := (adjA.M2/adjA.SumW)/na + (adjB.M2/adjB.SumW)/nb
	if !(se2 > 0) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_PROP_Z: the covariate-adjusted success indicator has zero variance in both groups; z-statistic undefined",
			map[string]any{"pooled": pooled, "theta": f.theta})
	}
	se := math.Sqrt(se2)
	diff := adjA.Mean - adjB.Mean
	z := diff / se
	pvalue := normalTwoSidedP(z)
	half := normalCriticalTwoSided(p.alpha) * se
	details := map[string]any{
		"success":    p.success,
		"groups":     keys,
		"n":          []int64{a.w.N, b.w.N},
		"successes":  []int64{a.successes, b.successes},
		"proportion": []float64{adjA.Mean, adjB.Mean},
		"diff":       diff,
		"pooled":     pooled,
		"ci_low":     diff - half,
		"ci_high":    diff + half,
	}
	if p.basis.Weighted() {
		details["successes"] = []float64{a.succW, b.succW}
	}
	noteReference(details, p.reference)
	f.note(details, pa-pb, se2, pa*(1-pa)/na+pb*(1-pb)/nb)
	p.noteGroups(details, []*weighting.Welford{&a.w, &b.w})
	setEffectSize(details, "cohens_h", cohensH(adjA.Mean, adjB.Mean))
	return &types.TestResult{
		Label:      testLabel(p.spec),
		Type:       types.TEST_PROP_Z,
		Variant:    "two_proportion_adjusted",
		Statistic:  z,
		PValue:     pvalue,
		Alpha:      p.alpha,
		RejectNull: pvalue < p.alpha,
		Details:    details,
	}, nil
}

// finalizeOneProportion is the one-proportion z-test against params.p0:
// z = (p̂ − p0)/√(p0(1−p0)/N*) (the score test, R's prop.test without
// continuity correction gives z²), a Wald interval on diff = p̂ − p0
// with √(p̂(1−p̂)/N*), and Cohen's h of p̂ against p0. Weighted, p̂ =
// Σw_success/Σw and N* is Σw (frequency) or Kish n_eff (probability);
// `n` stays the raw row count and `successes` becomes Σw_success.
func (p *propZRow) finalizeOneProportion() (*types.TestResult, error) {
	g, ok := p.groups[""]
	if !ok || g.w.N < 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			"TEST_PROP_Z one-proportion requires n ≥ 1, got 0",
			map[string]any{"n": 0, "min_required": 1})
	}
	nStar := g.w.NStar(p.basis)
	ph := g.succW / g.w.SumW
	z := (ph - p.p0) / math.Sqrt(p.p0*(1-p.p0)/nStar)
	pvalue := normalTwoSidedP(z)
	half := normalCriticalTwoSided(p.alpha) * math.Sqrt(ph*(1-ph)/nStar)
	diff := ph - p.p0
	details := map[string]any{
		"success":    p.success,
		"p0":         p.p0,
		"n":          g.w.N,
		"successes":  g.successes,
		"proportion": ph,
		"diff":       diff,
		"ci_low":     diff - half,
		"ci_high":    diff + half,
	}
	if p.basis.Weighted() {
		details["successes"] = g.succW
	}
	p.noteScalar(details, g.w.SumW, g.w.SumWSq)
	setEffectSize(details, "cohens_h", cohensH(ph, p.p0))
	return &types.TestResult{
		Label:      testLabel(p.spec),
		Type:       types.TEST_PROP_Z,
		Variant:    "one_proportion",
		Statistic:  z,
		PValue:     pvalue,
		Alpha:      p.alpha,
		RejectNull: pvalue < p.alpha,
		Details:    details,
	}, nil
}

func (p *propZRow) reset() {
	p.groups = make(map[string]*propGroup)
	p.order = nil
	p.cuped.reset()
}

// normalCriticalTwoSided is the two-sided standard-normal critical
// value z_{α/2} = Φ⁻¹(1 − α/2), taken as −Φ⁻¹(α/2) so the tail
// probability is never formed as one minus a small number. It is
// R's qnorm(1 − α/2) to a few ulp (standardNormalPPF is checked against
// qnorm by TestReferenceOracle_StandardNormalPPF) and backs every
// normal-critical confidence bound: AGG_CI_LOWER / AGG_CI_UPPER, the
// TEST_Z_TWO_SAMPLE and TEST_PROP_Z Wald intervals and the
// TEST_PEARSON_R Fisher-z interval. (It replaced a Winitzki inverse-erf
// approximation whose z was ~4.7e-4 relative too small at α = 0.05.)
func normalCriticalTwoSided(alpha float64) float64 {
	return -standardNormalPPF(alpha / 2)
}

package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tier-2 (post-test) implementations for the row tests that already
// existed as tier-1 only. Each consumes the materialized result row
// set after the window stage. Field references resolve against
// upstream stage outputs (aggregator labels, attribute labels, window
// outputs, grouper keys), not the raw cohort schema.
//
// Math mirrors the tier-1 finalize step in each test's source file;
// the row-source differs (result rows vs raw records) but the
// statistic and p-value derivation are identical.

// ---------- TEST_PAIRED_T tier-2 ----------

// pairedTPost: one-sample t-test on per-row diff d = field − field2
// across result rows. Use case: before/after metrics that surface as
// two columns per group row (e.g. pre_mean vs post_mean produced by
// grouped aggregations or windowed pre/post slices).
type pairedTPost struct {
	spec   *types.Test
	field  string
	field2 string
	alpha  float64
}

func newPairedTPost(spec *types.Test, _ *encoding.Schema) (PostTest, error) {
	if spec.Field == "" || spec.Field2 == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_PAIRED_T (post): requires field and field2 (paired columns)")
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
	return &pairedTPost{spec: spec, field: spec.Field, field2: spec.Field2, alpha: alpha}, nil
}

func (p *pairedTPost) Run(rows []map[string]any) (*types.TestResult, error) {
	b := &welfordBucket{}
	for i, row := range rows {
		a, err := floatFromRow(row, p.field, i)
		if err != nil {
			return nil, err
		}
		c, err := floatFromRow(row, p.field2, i)
		if err != nil {
			return nil, err
		}
		b.add(a - c)
	}
	if b.n < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_PAIRED_T (post) requires n ≥ 2 paired rows, got %d", b.n),
			map[string]any{"n": b.n, "min_required": 2})
	}
	variance := b.sampleVariance()
	if variance == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
			"TEST_PAIRED_T (post): sample variance of paired differences is zero",
			map[string]any{"n": b.n, "mean_diff": b.mean})
	}
	sd := math.Sqrt(variance)
	se := sd / math.Sqrt(float64(b.n))
	tstat := b.mean / se
	df := float64(b.n - 1)
	pvalue := studentTTwoSidedP(tstat, df)
	tcrit := studentTInverseTwoSided(p.alpha, df)
	return &types.TestResult{
		Label:      testLabel(p.spec),
		Type:       types.TEST_PAIRED_T,
		Variant:    "paired_two_sided_post",
		Statistic:  tstat,
		DF:         df,
		PValue:     pvalue,
		Alpha:      p.alpha,
		RejectNull: pvalue < p.alpha,
		Details: map[string]any{
			"n":          b.n,
			"mean_diff":  b.mean,
			"variance":   variance,
			"ci_low":     b.mean - tcrit*se,
			"ci_high":    b.mean + tcrit*se,
			"effect_size": map[string]any{
				"cohens_d": b.mean / sd,
			},
		},
	}, nil
}

// ---------- TEST_SPEARMAN_R tier-2 ----------

// spearmanRPost: rank-based correlation between two result columns.
// Mid-rank each column independently then compute Pearson r on the
// ranks. Same algorithm as tier-1; row source differs.
type spearmanRPost struct {
	spec   *types.Test
	field  string
	field2 string
	alpha  float64
}

func newSpearmanRPost(spec *types.Test, _ *encoding.Schema) (PostTest, error) {
	if spec.Field == "" || spec.Field2 == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_SPEARMAN_R (post): requires field and field2")
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
	return &spearmanRPost{spec: spec, field: spec.Field, field2: spec.Field2, alpha: alpha}, nil
}

func (s *spearmanRPost) Run(rows []map[string]any) (*types.TestResult, error) {
	xs := make([]float64, 0, len(rows))
	ys := make([]float64, 0, len(rows))
	for i, row := range rows {
		x, err := floatFromRow(row, s.field, i)
		if err != nil {
			return nil, err
		}
		y, err := floatFromRow(row, s.field2, i)
		if err != nil {
			return nil, err
		}
		xs = append(xs, x)
		ys = append(ys, y)
	}
	n := len(xs)
	if n < 3 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_SPEARMAN_R (post) requires n ≥ 3, got %d", n),
			map[string]any{"n": n, "min_required": 3})
	}
	rx, tiesX := midRanks(xs)
	ry, tiesY := midRanks(ys)
	mean := float64(n+1) / 2
	var sxx, syy, sxy float64
	for i := 0; i < n; i++ {
		dx := rx[i] - mean
		dy := ry[i] - mean
		sxx += dx * dx
		syy += dy * dy
		sxy += dx * dy
	}
	denom := math.Sqrt(sxx * syy)
	if denom == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_CORRELATION_UNDEFINED,
			"TEST_SPEARMAN_R (post): at least one column collapses to a single rank; ρ is undefined",
			map[string]any{"n": n})
	}
	rho := sxy / denom
	if rho > 1 {
		rho = 1
	} else if rho < -1 {
		rho = -1
	}
	df := float64(n - 2)
	var t, p float64
	switch {
	case rho == 1 || rho == -1:
		t = math.Inf(int(math.Copysign(1, rho)))
		p = 0
	default:
		t = rho * math.Sqrt(df/(1-rho*rho))
		p = studentTTwoSidedP(t, df)
	}
	res := &types.TestResult{
		Label:      testLabel(s.spec),
		Type:       types.TEST_SPEARMAN_R,
		Variant:    "rank_pearson_post",
		Statistic:  rho,
		DF:         df,
		PValue:     p,
		Alpha:      s.alpha,
		RejectNull: p < s.alpha,
		Details: map[string]any{
			"n":      n,
			"t":      t,
			"ties_x": tiesX,
			"ties_y": tiesY,
		},
	}
	if tiesDominate(tiesX, n) || tiesDominate(tiesY, n) {
		res.Warnings = append(res.Warnings, string(errors.PULSE_TEST_TIES_DOMINATE)+
			": ≥ 50% of values are tied in at least one column; asymptotic p-value is unreliable")
	}
	return res, nil
}

// ---------- TEST_KENDALL_TAU tier-2 ----------

// kendallTauPost: concordance-based correlation (τ-b) between two
// result columns. O(n²) pair count over result rows.
type kendallTauPost struct {
	spec   *types.Test
	field  string
	field2 string
	alpha  float64
}

func newKendallTauPost(spec *types.Test, _ *encoding.Schema) (PostTest, error) {
	if spec.Field == "" || spec.Field2 == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_KENDALL_TAU (post): requires field and field2")
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
	return &kendallTauPost{spec: spec, field: spec.Field, field2: spec.Field2, alpha: alpha}, nil
}

func (k *kendallTauPost) Run(rows []map[string]any) (*types.TestResult, error) {
	xs := make([]float64, 0, len(rows))
	ys := make([]float64, 0, len(rows))
	for i, row := range rows {
		x, err := floatFromRow(row, k.field, i)
		if err != nil {
			return nil, err
		}
		y, err := floatFromRow(row, k.field2, i)
		if err != nil {
			return nil, err
		}
		xs = append(xs, x)
		ys = append(ys, y)
	}
	n := len(xs)
	if n < 3 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_KENDALL_TAU (post) requires n ≥ 3, got %d", n),
			map[string]any{"n": n, "min_required": 3})
	}
	var c, d, tx, ty int64
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			dx := xs[i] - xs[j]
			dy := ys[i] - ys[j]
			switch {
			case dx == 0 && dy == 0:
			case dx == 0:
				tx++
			case dy == 0:
				ty++
			case (dx > 0) == (dy > 0):
				c++
			default:
				d++
			}
		}
	}
	S := float64(c - d)
	denomA := float64(c+d) + float64(tx)
	denomB := float64(c+d) + float64(ty)
	denom := math.Sqrt(denomA * denomB)
	if denom == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_CORRELATION_UNDEFINED,
			"TEST_KENDALL_TAU (post): degenerate input (all pairs tied)",
			map[string]any{"n": n, "c": c, "d": d, "tx": tx, "ty": ty})
	}
	tau := S / denom
	_, tiesX := midRanks(xs)
	_, tiesY := midRanks(ys)
	nf := float64(n)
	v0 := nf * (nf - 1) * (2*nf + 5)
	vt := tieKendallSum(tiesX)
	vu := tieKendallSum(tiesY)
	v1 := 0.0
	if n > 1 {
		v1 = tieKendallV1(tiesX) * tieKendallV1(tiesY) / (2 * nf * (nf - 1))
	}
	v2 := 0.0
	if n > 2 {
		v2 = tieKendallV2(tiesX) * tieKendallV2(tiesY) / (9 * nf * (nf - 1) * (nf - 2))
	}
	varS := (v0-vt-vu)/18 + v1 + v2
	var z, p float64
	if varS <= 0 || S == 0 {
		z = 0
		p = 1
	} else {
		corrected := S
		if S > 0 {
			corrected -= 1
		} else {
			corrected += 1
		}
		z = corrected / math.Sqrt(varS)
		p = 2 * (1 - standardNormalCDF(math.Abs(z)))
	}
	res := &types.TestResult{
		Label:      testLabel(k.spec),
		Type:       types.TEST_KENDALL_TAU,
		Variant:    "tau_b_post",
		Statistic:  tau,
		PValue:     p,
		Alpha:      k.alpha,
		RejectNull: p < k.alpha,
		Details: map[string]any{
			"n":          n,
			"concordant": c,
			"discordant": d,
			"ties_x":     tx,
			"ties_y":     ty,
			"s":          S,
			"var_s":      varS,
			"z":          z,
		},
	}
	if tiesDominate(tiesX, n) || tiesDominate(tiesY, n) {
		res.Warnings = append(res.Warnings, string(errors.PULSE_TEST_TIES_DOMINATE)+
			": ≥ 50% of values are tied in at least one column; asymptotic p-value is unreliable")
	}
	return res, nil
}

// ---------- TEST_WILCOXON_SR tier-2 ----------

// wilcoxonSRPost: paired nonparametric on (field, field2) across
// result rows. Drops zero diffs; mid-ranks |d| with tie correction.
// Same asymptotic z formula as tier-1.
type wilcoxonSRPost struct {
	spec   *types.Test
	field  string
	field2 string
	alpha  float64
}

func newWilcoxonSRPost(spec *types.Test, _ *encoding.Schema) (PostTest, error) {
	if spec.Field == "" || spec.Field2 == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_WILCOXON_SR (post): requires field and field2 (paired columns)")
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
	return &wilcoxonSRPost{spec: spec, field: spec.Field, field2: spec.Field2, alpha: alpha}, nil
}

func (w *wilcoxonSRPost) Run(rows []map[string]any) (*types.TestResult, error) {
	var diffs []float64
	var dropped int
	for i, row := range rows {
		x, err := floatFromRow(row, w.field, i)
		if err != nil {
			return nil, err
		}
		y, err := floatFromRow(row, w.field2, i)
		if err != nil {
			return nil, err
		}
		d := x - y
		if d == 0 {
			dropped++
			continue
		}
		diffs = append(diffs, d)
	}
	n := len(diffs)
	if n < 6 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_WILCOXON_SR (post) requires n ≥ 6 non-zero pairs, got %d", n),
			map[string]any{"n": n, "min_required": 6})
	}
	abs := make([]float64, n)
	for i, d := range diffs {
		abs[i] = math.Abs(d)
	}
	ranks, ties := midRanks(abs)
	var wPlus, wMinus float64
	for i, d := range diffs {
		if d > 0 {
			wPlus += ranks[i]
		} else {
			wMinus += ranks[i]
		}
	}
	nf := float64(n)
	muW := nf * (nf + 1) / 4
	varW := nf*(nf+1)*(2*nf+1)/24 - tieCorrection(ties)/48
	var z, p float64
	if varW <= 0 {
		z = 0
		p = 1
	} else {
		diff := wPlus - muW
		switch {
		case diff > 0:
			diff -= 0.5
			if diff < 0 {
				diff = 0
			}
		case diff < 0:
			diff += 0.5
			if diff > 0 {
				diff = 0
			}
		}
		z = diff / math.Sqrt(varW)
		p = 2 * (1 - standardNormalCDF(math.Abs(z)))
	}
	res := &types.TestResult{
		Label:      testLabel(w.spec),
		Type:       types.TEST_WILCOXON_SR,
		Variant:    "asymptotic_post",
		Statistic:  math.Min(wPlus, wMinus),
		PValue:     p,
		Alpha:      w.alpha,
		RejectNull: p < w.alpha,
		Details: map[string]any{
			"n":          n,
			"w_plus":     wPlus,
			"w_minus":    wMinus,
			"mu_w":       muW,
			"var_w":      varW,
			"z":          z,
			"zero_diffs": dropped,
		},
	}
	if tiesDominate(ties, n) {
		res.Warnings = append(res.Warnings, string(errors.PULSE_TEST_TIES_DOMINATE)+
			": ≥ 50% of |diff| values are tied; asymptotic p-value is unreliable")
	}
	return res, nil
}

// ---------- TEST_ANOVA_WELCH tier-2 ----------

// anovaWelchPost: heteroscedasticity-robust one-way ANOVA over result
// rows partitioned by split_by. Pulls per-group n, mean, and variance
// from caller-supplied columns (params.n_col / params.variance_col),
// mirroring the TEST_ANOVA_F tier-2 contract. Same Welch (1951)
// finalization as tier-1 with df-Satterthwaite correction.
type anovaWelchPost struct {
	spec    *types.Test
	field   string
	splitBy string
	nCol    string
	varCol  string
	alpha   float64
}

func newAnovaWelchPost(spec *types.Test, _ *encoding.Schema) (PostTest, error) {
	if spec.Field == "" || spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_ANOVA_WELCH (post): requires field (per-group mean column) and split_by (group label column)")
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
	var params anovaPostParams
	if err := unmarshalTestParams(spec.Params, &params); err != nil {
		return nil, err
	}
	if params.NCol == "" || params.VarianceCol == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_ANOVA_WELCH (post): params.n_col and params.variance_col are required")
	}
	return &anovaWelchPost{
		spec:    spec,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		nCol:    params.NCol,
		varCol:  params.VarianceCol,
		alpha:   alpha,
	}, nil
}

func (a *anovaWelchPost) Run(rows []map[string]any) (*types.TestResult, error) {
	if len(rows) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_ANOVA_WELCH (post): need ≥ 2 group rows, got %d", len(rows)),
			map[string]any{"rows": len(rows)})
	}
	order := make([]string, 0, len(rows))
	means := make([]float64, 0, len(rows))
	ns := make([]int64, 0, len(rows))
	vars_ := make([]float64, 0, len(rows))
	for i, row := range rows {
		key, err := stringFromRow(row, a.splitBy, i)
		if err != nil {
			return nil, err
		}
		mean, err := floatFromRow(row, a.field, i)
		if err != nil {
			return nil, err
		}
		n, err := int64FromRow(row, a.nCol, i)
		if err != nil {
			return nil, err
		}
		if n < 2 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
				fmt.Sprintf("TEST_ANOVA_WELCH (post): group %q has n=%d (need ≥ 2)", key, n),
				map[string]any{"group": key, "n": n})
		}
		variance, err := floatFromRow(row, a.varCol, i)
		if err != nil {
			return nil, err
		}
		if variance == 0 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_VARIANCE_ZERO,
				fmt.Sprintf("TEST_ANOVA_WELCH (post): group %q has zero variance", key),
				map[string]any{"group": key})
		}
		order = append(order, key)
		means = append(means, mean)
		ns = append(ns, n)
		vars_ = append(vars_, variance)
	}
	k := len(order)
	weights := make([]float64, k)
	var W, weightedMean float64
	for i := 0; i < k; i++ {
		weights[i] = float64(ns[i]) / vars_[i]
		W += weights[i]
		weightedMean += weights[i] * means[i]
	}
	weightedMean /= W
	var num, tailSum float64
	for i := 0; i < k; i++ {
		diff := means[i] - weightedMean
		num += weights[i] * diff * diff
		share := 1 - weights[i]/W
		tailSum += share * share / float64(ns[i]-1)
	}
	num /= float64(k - 1)
	kf := float64(k)
	den := 1 + 2*(kf-2)/(kf*kf-1)*tailSum
	F := num / den
	df1 := kf - 1
	df2 := (kf*kf - 1) / (3 * tailSum)
	p := fSurvival(F, df1, df2)
	return &types.TestResult{
		Label:      testLabel(a.spec),
		Type:       types.TEST_ANOVA_WELCH,
		Variant:    "welch_one_way_post",
		Statistic:  F,
		DF:         df1,
		PValue:     p,
		Alpha:      a.alpha,
		RejectNull: p < a.alpha,
		Details: map[string]any{
			"groups":          order,
			"n":               ns,
			"group_means":     means,
			"group_variances": vars_,
			"weights":         weights,
			"weighted_mean":   weightedMean,
			"df_between":      df1,
			"df_within":       df2,
		},
	}, nil
}

// ---------- TEST_BROWN_FORSYTHE tier-2 ----------

// brownForsythePost: median-based variance homogeneity on result rows
// grouped by split_by. Computes per-group median, then runs one-way
// ANOVA on |x_ij − median_i|. Result rows carry the raw values (not
// pre-aggregated per-group summary stats), so the input is a flat
// row stream with one row per observation.
type brownForsythePost struct {
	spec    *types.Test
	field   string
	splitBy string
	alpha   float64
}

func newBrownForsythePost(spec *types.Test, _ *encoding.Schema) (PostTest, error) {
	if spec.Field == "" || spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_BROWN_FORSYTHE (post): requires field and split_by")
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
	return &brownForsythePost{spec: spec, field: spec.Field, splitBy: spec.SplitBy, alpha: alpha}, nil
}

func (b *brownForsythePost) Run(rows []map[string]any) (*types.TestResult, error) {
	values := make(map[string][]float64)
	var order []string
	for i, row := range rows {
		v, err := floatFromRow(row, b.field, i)
		if err != nil {
			return nil, err
		}
		key, err := stringFromRow(row, b.splitBy, i)
		if err != nil {
			return nil, err
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = append(values[key], v)
	}
	keys := append([]string(nil), order...)
	sort.Strings(keys)
	k := len(keys)
	if k < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_BROWN_FORSYTHE (post) requires ≥ 2 groups, got %d", k),
			map[string]any{"groups": keys, "min_required": 2})
	}
	medians := make([]float64, k)
	devBuckets := make(map[string]*welfordBucket, k)
	devOrder := make([]string, 0, k)
	for i, key := range keys {
		vs := append([]float64(nil), values[key]...)
		if len(vs) < 2 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
				fmt.Sprintf("TEST_BROWN_FORSYTHE (post) requires n ≥ 2 per group; %q has %d", key, len(vs)),
				map[string]any{"group": key, "n": len(vs), "min_required": 2})
		}
		sort.Float64s(vs)
		medians[i] = median(vs)
		bk := &welfordBucket{}
		for _, v := range vs {
			bk.add(math.Abs(v - medians[i]))
		}
		devBuckets[key] = bk
		devOrder = append(devOrder, key)
	}
	stats := summariseANOVA(devOrder, devBuckets)
	dfB := float64(k - 1)
	dfW := float64(stats.N - int64(k))
	if dfW <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			"TEST_BROWN_FORSYTHE (post): zero within-group degrees of freedom",
			map[string]any{"k": k, "n": stats.N})
	}
	msB := stats.SSB / dfB
	msW := stats.SSW / dfW
	var F, p float64
	if msW == 0 {
		F = 0
		p = 0
	} else {
		F = msB / msW
		p = fSurvival(F, dfB, dfW)
	}
	return &types.TestResult{
		Label:      testLabel(b.spec),
		Type:       types.TEST_BROWN_FORSYTHE,
		Variant:    "median_post",
		Statistic:  F,
		DF:         dfB,
		PValue:     p,
		Alpha:      b.alpha,
		RejectNull: p < b.alpha,
		Details: map[string]any{
			"groups":        keys,
			"n":             stats.Ns,
			"group_medians": medians,
			"abs_dev_means": stats.Means,
			"ss_between":    stats.SSB,
			"ss_within":     stats.SSW,
			"df_between":    dfB,
			"df_within":     dfW,
		},
	}, nil
}

// ---------- TEST_SHAPIRO_WILK tier-2 ----------

// shapiroWilkPost: normality test on a result column. Optional
// split_by emits per-group results. Same Shapiro-Francia approximation
// as tier-1.
type shapiroWilkPost struct {
	spec    *types.Test
	field   string
	splitBy string
	alpha   float64
}

func newShapiroWilkPost(spec *types.Test, _ *encoding.Schema) (PostTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_SHAPIRO_WILK (post): requires field")
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
	return &shapiroWilkPost{spec: spec, field: spec.Field, splitBy: spec.SplitBy, alpha: alpha}, nil
}

func (s *shapiroWilkPost) Run(rows []map[string]any) (*types.TestResult, error) {
	groups := make(map[string][]float64)
	var order []string
	for i, row := range rows {
		v, err := floatFromRow(row, s.field, i)
		if err != nil {
			return nil, err
		}
		key := ""
		if s.splitBy != "" {
			k, err := stringFromRow(row, s.splitBy, i)
			if err != nil {
				return nil, err
			}
			key = k
		}
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], v)
	}
	keys := append([]string(nil), order...)
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			"TEST_SHAPIRO_WILK (post): no rows",
			map[string]any{"n": 0})
	}
	perGroup := make([]map[string]any, 0, len(keys))
	var headlineW, headlineP float64
	var headlineWarn []string
	for _, key := range keys {
		vs := append([]float64(nil), groups[key]...)
		n := len(vs)
		if n < 3 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
				fmt.Sprintf("TEST_SHAPIRO_WILK (post) requires n ≥ 3 per group; %q has %d", key, n),
				map[string]any{"group": key, "n": n, "min_required": 3})
		}
		sort.Float64s(vs)
		W, z, p, warn := shapiroFranciaStat(vs)
		entry := map[string]any{
			"n":       n,
			"w":       W,
			"z":       z,
			"p_value": p,
		}
		if key != "" {
			entry["group"] = key
		}
		if warn != "" {
			entry["warning"] = warn
			headlineWarn = append(headlineWarn, fmt.Sprintf("%s: %s", key, warn))
		}
		perGroup = append(perGroup, entry)
		if len(perGroup) == 1 || p < headlineP {
			headlineP = p
			headlineW = W
		}
	}
	res := &types.TestResult{
		Label:      testLabel(s.spec),
		Type:       types.TEST_SHAPIRO_WILK,
		Variant:    "shapiro_francia_post",
		Statistic:  headlineW,
		PValue:     headlineP,
		Alpha:      s.alpha,
		RejectNull: headlineP < s.alpha,
		Details: map[string]any{
			"per_group": perGroup,
		},
	}
	res.Warnings = append(res.Warnings, headlineWarn...)
	return res, nil
}

// ---------- TEST_KS tier-2 ----------

// ksPost: two-sample Kolmogorov-Smirnov over result rows partitioned
// by split_by. Same Smirnov asymptotic p as tier-1.
type ksPost struct {
	spec    *types.Test
	field   string
	splitBy string
	alpha   float64
}

func newKSPost(spec *types.Test, _ *encoding.Schema) (PostTest, error) {
	if spec.Field == "" || spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_KS (post): requires field and split_by")
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
	return &ksPost{spec: spec, field: spec.Field, splitBy: spec.SplitBy, alpha: alpha}, nil
}

func (k *ksPost) Run(rows []map[string]any) (*types.TestResult, error) {
	values := make(map[string][]float64)
	var order []string
	for i, row := range rows {
		v, err := floatFromRow(row, k.field, i)
		if err != nil {
			return nil, err
		}
		key, err := stringFromRow(row, k.splitBy, i)
		if err != nil {
			return nil, err
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = append(values[key], v)
	}
	keys := append([]string(nil), order...)
	sort.Strings(keys)
	if len(keys) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_KS (post) requires 2 split groups, got %d", len(keys)),
			map[string]any{"groups": keys, "min_required": 2})
	}
	if len(keys) > 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_KS (post) sees %d groups; restrict to exactly two", len(keys)),
			map[string]any{"groups": keys, "max_allowed": 2})
	}
	a := append([]float64(nil), values[keys[0]]...)
	b := append([]float64(nil), values[keys[1]]...)
	if len(a) < 2 || len(b) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_KS (post) requires n ≥ 2 per group, got %d / %d", len(a), len(b)),
			map[string]any{"n": []int{len(a), len(b)}, "min_required": 2})
	}
	sort.Float64s(a)
	sort.Float64s(b)
	D := ksTwoSampleD(a, b)
	n1 := float64(len(a))
	n2 := float64(len(b))
	en := math.Sqrt(n1 * n2 / (n1 + n2))
	p := kolmogorovSurvival((en + 0.12 + 0.11/en) * D)
	return &types.TestResult{
		Label:      testLabel(k.spec),
		Type:       types.TEST_KS,
		Variant:    "two_sample_post",
		Statistic:  D,
		PValue:     p,
		Alpha:      k.alpha,
		RejectNull: p < k.alpha,
		Details: map[string]any{
			"groups": keys,
			"n":      []int{len(a), len(b)},
		},
	}, nil
}

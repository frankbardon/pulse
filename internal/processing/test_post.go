package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/types"
)

// Tier-2 post-test implementations. These consume the materialized
// result row set after the window stage, so each Test must reference
// columns produced by upstream stages (aggregator labels, grouper keys,
// window outputs, etc.).

// anovaPost runs a one-way ANOVA over per-group summary rows produced
// by an upstream aggregation. The Test spec reads:
//
//	field    — column with the per-group mean
//	split_by — column with the group label (each row's identity)
//	params:  {"n_col": "count_col", "variance_col": "var_col"}
//
// where n_col and variance_col point to other result columns carrying
// the per-group count and sample variance respectively. Recovering SSB
// and SSW from summary stats:
//
//	SSB = Σ n_i (μ_i - grand_mean)²
//	SSW = Σ (n_i - 1) s²_i
type anovaPost struct {
	spec    *types.Test
	field   string
	splitBy string
	nCol    string
	varCol  string
	alpha   float64
}

type anovaPostParams struct {
	NCol        string `json:"n_col"`
	VarianceCol string `json:"variance_col"`
}

func newAnovaPost(spec *types.Test, schema *encoding.Schema) (PostTest, error) {
	if cerr := fieldaccept.RequiredSlotsRefusal(spec.Type, true, fieldaccept.TestSlotsOf(spec)); cerr != nil {
		return nil, cerr
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
	if err := unmarshalTestParams(string(spec.Type), spec.Params, &params); err != nil {
		return nil, err
	}
	if params.NCol == "" || params.VarianceCol == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_ANOVA_F (post): params.n_col and params.variance_col are required")
	}
	_ = schema
	return &anovaPost{
		spec:    spec,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		nCol:    params.NCol,
		varCol:  params.VarianceCol,
		alpha:   alpha,
	}, nil
}

func (a *anovaPost) Run(rows []map[string]any) (*types.TestResult, error) {
	order := make([]string, 0, len(rows))
	groups := make(map[string]*welfordBucket, len(rows))
	for i, row := range rows {
		if postRowHasNull(row, a.splitBy, a.field, a.nCol, a.varCol) {
			continue
		}
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
		if n < 1 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
				fmt.Sprintf("TEST_ANOVA_F (post): row %d has n=%d", i, n),
				map[string]any{"row": i, "n": n})
		}
		variance, err := floatFromRow(row, a.varCol, i)
		if err != nil {
			return nil, err
		}
		// Reconstruct M2 = (n - 1) * sample_variance so summariseANOVA
		// hits the same SSW formula it uses for tier-1 buckets.
		m2 := float64(n-1) * variance
		groups[key] = &welfordBucket{n: n, mean: mean, m2: m2}
		order = append(order, key)
	}
	if len(order) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_ANOVA_F (post): need ≥ 2 group rows without a null, got %d", len(order)),
			map[string]any{"rows": len(order)})
	}
	stats := summariseANOVA(order, groups)
	k := len(order)
	dfBetween := float64(k - 1)
	dfWithin := float64(stats.N - int64(k))
	if dfWithin <= 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			"TEST_ANOVA_F (post): zero within-group degrees of freedom",
			map[string]any{"k": k, "n": stats.N})
	}
	msBetween := stats.SSB / dfBetween
	msWithin := stats.SSW / dfWithin
	var F, p float64
	if msWithin == 0 {
		F = 0
		p = 0
	} else {
		F = msBetween / msWithin
		p = fSurvival(F, dfBetween, dfWithin)
	}
	eta2 := 0.0
	if stats.SSB+stats.SSW > 0 {
		eta2 = stats.SSB / (stats.SSB + stats.SSW)
	}
	res := &types.TestResult{
		Label:      testLabel(a.spec),
		Type:       types.TEST_ANOVA_F,
		Variant:    "one_way_from_summary",
		Statistic:  F,
		DF:         dfBetween,
		PValue:     p,
		Alpha:      a.alpha,
		RejectNull: p < a.alpha,
		Details: map[string]any{
			"groups":      order,
			"n":           stats.Ns,
			"group_means": stats.Means,
			"ss_between":  stats.SSB,
			"ss_within":   stats.SSW,
			"df_between":  dfBetween,
			"df_within":   dfWithin,
			"ms_within":   msWithin,
			"effect_size": map[string]any{
				"eta_squared": eta2,
			},
		},
	}
	setEffectSize(res.Details, "omega_squared", omegaSquared(stats.SSB, stats.SSW, dfBetween, msWithin))
	return res, nil
}

// trendPost implements TEST_TREND (Mann-Kendall) on a numeric column of
// the post-pipeline row set. Rows are ordered by the OrderBy keys
// (defaults to insertion order if empty). The S statistic counts pair
// concordances, and the asymptotic Z under the null is used for the
// two-sided p-value.
//
// Tied groups are accounted for in Var(S) via the standard correction
// factor: Var(S) = (n(n-1)(2n+5) - Σ t_g(t_g-1)(2t_g+5)) / 18.
type trendPost struct {
	spec    *types.Test
	field   string
	orderBy []types.OrderKey
	alpha   float64
	variant string
	// keyOrders is the intrinsic key order of every grouper key column
	// of the run (setKeyOrders), nil before it is stamped.
	keyOrders window.KeyOrders
}

// setKeyOrders implements keyOrderedPostTest: an order_by naming a
// grouper key column sorts by that grouper's key order.
func (tr *trendPost) setKeyOrders(orders window.KeyOrders) { tr.keyOrders = orders }

func newTrendPost(spec *types.Test, schema *encoding.Schema) (PostTest, error) {
	if cerr := fieldaccept.RequiredSlotsRefusal(spec.Type, true, fieldaccept.TestSlotsOf(spec)); cerr != nil {
		return nil, cerr
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
	variant := "mann_kendall"
	var p struct {
		Variant string `json:"variant"`
	}
	if err := unmarshalTestParams(string(spec.Type), spec.Params, &p); err != nil {
		return nil, err
	}
	if p.Variant != "" {
		variant = p.Variant
	}
	if variant != "mann_kendall" {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"TEST_TREND variant "+variant+" not supported; only mann_kendall in v1",
			map[string]any{"variant": variant})
	}
	_ = schema
	return &trendPost{
		spec:    spec,
		field:   spec.Field,
		orderBy: spec.OrderBy,
		alpha:   alpha,
		variant: variant,
	}, nil
}

func (tr *trendPost) Run(rows []map[string]any) (*types.TestResult, error) {
	// Listwise: a row with a null value or a null order_by key leaves the
	// series (R's complete.cases on (index, y)); copy so we don't mutate
	// the caller's data.
	used := make([]string, 0, 1+len(tr.orderBy))
	used = append(used, tr.field)
	for _, k := range tr.orderBy {
		used = append(used, k.Field)
	}
	ordered := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if !postRowHasNull(row, used...) {
			ordered = append(ordered, row)
		}
	}
	if len(ordered) < 3 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_TREND (mann_kendall) requires n ≥ 3 rows without a null, got %d", len(ordered)),
			map[string]any{"n": len(ordered), "min_required": 3})
	}
	// Order rows by OrderBy if provided.
	if len(tr.orderBy) > 0 {
		sort.SliceStable(ordered, func(i, j int) bool {
			// A grouper key column sorts by its grouper's key order —
			// day_of_week Monday-first, GROUP_DATE_RANGES labels in
			// supplied order, numeric buckets by value — reversed under
			// Desc; an unranked key (ISO date), a numeric or null cell
			// and any other column keep the CompareKey order.
			return window.CompareRows(ordered[i], ordered[j], tr.orderBy, tr.keyOrders) < 0
		})
	}
	values := make([]float64, 0, len(ordered))
	for i, row := range ordered {
		v, err := floatFromRow(row, tr.field, i)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	S, varS, tiedPairs := mannKendall(values)
	var Z float64
	switch {
	case S > 0:
		Z = (S - 1) / math.Sqrt(varS)
	case S < 0:
		Z = (S + 1) / math.Sqrt(varS)
	default:
		Z = 0
	}
	p := normalTwoSidedP(Z)
	// tau is Kendall's tau-b of the series against its order (the
	// order positions never tie): S / √(n₀ (n₀ − n₂)), n₀ = n(n−1)/2,
	// n₂ the tied value pairs — Kendall::MannKendall's S / D and R's
	// cor(seq_along(y), y, method = "kendall"). Undefined (NaN, null on
	// the wire) when every value ties.
	n := float64(len(values))
	n0 := float64(0.5 * n * (n - 1))
	tau := S / math.Sqrt(n0*(n0-tiedPairs))
	var warnings []string
	if len(values) < 8 {
		warnings = append(warnings,
			"TEST_TREND with n < 8: asymptotic Mann-Kendall p-value is unreliable; treat as advisory")
	}
	return &types.TestResult{
		Label:      testLabel(tr.spec),
		Type:       types.TEST_TREND,
		Variant:    tr.variant,
		Statistic:  Z,
		PValue:     p,
		Alpha:      tr.alpha,
		RejectNull: p < tr.alpha,
		Details: map[string]any{
			"s":     S,
			"var_s": varS,
			"tau":   tau,
			"n":     len(values),
		},
		Warnings: warnings,
	}, nil
}

// mannKendall returns (S, Var(S), n₂) with the standard tied-group
// correction for Var(S); n₂ = Σ t(t−1)/2 counts the tied value pairs.
func mannKendall(x []float64) (float64, float64, float64) {
	n := len(x)
	S := 0
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			switch {
			case x[j] > x[i]:
				S++
			case x[j] < x[i]:
				S--
			}
		}
	}
	// Tied-group correction.
	counts := make(map[float64]int, n)
	for _, v := range x {
		counts[v]++
	}
	N := float64(n)
	varS := N * (N - 1) * (2*N + 5) / 18.0
	tiedPairs := 0.0
	for _, t := range counts {
		if t > 1 {
			tf := float64(t)
			varS -= tf * (tf - 1) * (2*tf + 5) / 18.0
			tiedPairs += float64(tf * (tf - 1) / 2)
		}
	}
	if varS < 0 {
		varS = 0
	}
	return float64(S), varS, tiedPairs
}

// compareRows returns negative / 0 / positive for ordering by keys.
// Operates on the post-aggregate row map. It is the same comparator
// window ORDER BY and Request.Sort use (window.CompareRows): values order
// by type (numbers, decimals, labels), and a null or missing key sorts
// last regardless of direction. This arm's own comparator used to tie a
// present-null against everything and could not order a decimal.
func compareRows(a, b map[string]any, keys []types.OrderKey) int {
	return window.CompareRows(a, b, keys, nil)
}

func toFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	return math.NaN()
}

// postRowHasNull reports whether row holds a null — a present nil — in
// any of fields: the post-test listwise drop, R's complete.cases (its
// default na.action) on the columns the test reads. Every post-test
// skips such a row and reports the n it actually used, as its row
// variant skips a null record. A missing key is not a null (floatFromRow
// still refuses it, SERVICE_VALIDATION), a present non-numeric value
// still refuses (PULSE_TEST_FIELD_NOT_NUMERIC), and an undefined figure
// (NaN, an aggregate over no value) is not a null either: it stays in and
// the statistic is NaN. An empty name (an unset optional split_by) is
// skipped.
func postRowHasNull(row map[string]any, fields ...string) bool {
	for _, f := range fields {
		if f == "" {
			continue
		}
		if v, ok := row[f]; ok && v == nil {
			return true
		}
	}
	return false
}

func floatFromRow(row map[string]any, field string, idx int) (float64, error) {
	v, ok := row[field]
	if !ok {
		return 0, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("post-test row %d missing field %q", idx, field),
			map[string]any{"row": idx, "field": field})
	}
	if x, ok := floatValue(v); ok {
		return x, nil
	}
	return 0, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
		fmt.Sprintf("post-test row %d field %q has non-numeric value", idx, field),
		map[string]any{"row": idx, "field": field, "go_type": fmt.Sprintf("%T", v)})
}

func int64FromRow(row map[string]any, field string, idx int) (int64, error) {
	v, ok := row[field]
	if !ok {
		return 0, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("post-test row %d missing field %q", idx, field),
			map[string]any{"row": idx, "field": field})
	}
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case float64:
		return int64(x), nil
	}
	return 0, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
		fmt.Sprintf("post-test row %d field %q is not an integer", idx, field),
		map[string]any{"row": idx, "field": field, "go_type": fmt.Sprintf("%T", v)})
}

func stringFromRow(row map[string]any, field string, idx int) (string, error) {
	v, ok := row[field]
	if !ok {
		return "", errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("post-test row %d missing field %q", idx, field),
			map[string]any{"row": idx, "field": field})
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", v), nil
}

// unmarshalTestParams decodes post-test op's spec.Params into target
// with a clean error code. Empty raw message is OK (no params).
func unmarshalTestParams(op string, raw []byte, target any) error {
	if err := paramread.DecodeOptional(paramread.PostTest, op, raw, target); err != nil {
		return errors.NewCodedError(errors.PROCESSING_CONFIG,
			"invalid test params: "+err.Error())
	}
	return nil
}

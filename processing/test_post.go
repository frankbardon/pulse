package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// jsonUnmarshalStrict wraps encoding/json so the post-test code below
// can keep a single import alias.
func jsonUnmarshalStrict(raw []byte, target any) error {
	return json.Unmarshal(raw, target)
}

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
	if spec.Field == "" || spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_ANOVA_F (post): requires field (per-group mean column) and split_by (group label column)")
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
	if len(rows) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_ANOVA_F (post): need ≥ 2 group rows, got %d", len(rows)),
			map[string]any{"rows": len(rows)})
	}
	order := make([]string, 0, len(rows))
	groups := make(map[string]*welfordBucket, len(rows))
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
	return &types.TestResult{
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
	}, nil
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
}

func newTrendPost(spec *types.Test, schema *encoding.Schema) (PostTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_TREND requires field")
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
	if len(spec.Params) > 0 {
		var p struct {
			Variant string `json:"variant"`
		}
		if err := unmarshalTestParams(spec.Params, &p); err != nil {
			return nil, err
		}
		if p.Variant != "" {
			variant = p.Variant
		}
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
	if len(rows) < 3 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_TREND (mann_kendall) requires n ≥ 3, got %d", len(rows)),
			map[string]any{"n": len(rows), "min_required": 3})
	}
	// Order rows by OrderBy if provided; copy the slice so we don't
	// mutate the caller's data.
	ordered := append([]map[string]any(nil), rows...)
	if len(tr.orderBy) > 0 {
		sort.SliceStable(ordered, func(i, j int) bool {
			return compareRows(ordered[i], ordered[j], tr.orderBy) < 0
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
	S, varS := mannKendall(values)
	var Z float64
	switch {
	case S > 0:
		Z = (S - 1) / math.Sqrt(varS)
	case S < 0:
		Z = (S + 1) / math.Sqrt(varS)
	default:
		Z = 0
	}
	p := 2 * (1 - standardNormalCDF(math.Abs(Z)))
	tau := 0.0
	n := float64(len(values))
	if n > 1 {
		tau = S / (0.5 * n * (n - 1))
	}
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

// mannKendall returns (S, Var(S)) with the standard tied-group
// correction for Var(S).
func mannKendall(x []float64) (float64, float64) {
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
	for _, t := range counts {
		if t > 1 {
			tf := float64(t)
			varS -= tf * (tf - 1) * (2*tf + 5) / 18.0
		}
	}
	if varS < 0 {
		varS = 0
	}
	return float64(S), varS
}

// compareRows returns negative / 0 / positive for ordering by keys.
// Operates on the post-aggregate row map, so values may be numeric,
// string, or absent. Missing keys sort last regardless of direction.
func compareRows(a, b map[string]any, keys []types.OrderKey) int {
	for _, k := range keys {
		va, aOk := a[k.Field]
		vb, bOk := b[k.Field]
		if !aOk && !bOk {
			continue
		}
		if !aOk {
			return 1
		}
		if !bOk {
			return -1
		}
		c := compareAny(va, vb)
		if c != 0 {
			if k.Desc {
				c = -c
			}
			return c
		}
	}
	return 0
}

func compareAny(a, b any) int {
	switch av := a.(type) {
	case float64:
		bv := toFloat64(b)
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		default:
			return 0
		}
	case int64:
		return compareAny(float64(av), b)
	case int:
		return compareAny(float64(av), b)
	case string:
		bv, _ := b.(string)
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		default:
			return 0
		}
	}
	return 0
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

// ---------- value extractors shared across post-test impls ----------

func floatFromRow(row map[string]any, field string, idx int) (float64, error) {
	v, ok := row[field]
	if !ok {
		return 0, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("post-test row %d missing field %q", idx, field),
			map[string]any{"row": idx, "field": field})
	}
	switch x := v.(type) {
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	case int:
		return float64(x), nil
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

// unmarshalTestParams decodes spec.Params into target with a clean
// error code. Empty raw message is OK (no params).
func unmarshalTestParams(raw []byte, target any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := jsonUnmarshalStrict(raw, target); err != nil {
		return errors.NewCodedError(errors.PROCESSING_CONFIG,
			"invalid test params: "+err.Error())
	}
	return nil
}

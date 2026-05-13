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

// tukeyHSDPost implements TEST_TUKEY_HSD as a tier-2 post test:
// pairwise comparison of group means via Tukey's Honestly Significant
// Difference, with p-values from the studentized-range distribution.
//
// Inputs come from the materialized result row set after windows.
// Required columns on each row:
//   - SplitBy column (group label)
//   - Field column   (per-group mean, typically AGG_AVERAGE alias)
//   - "n" or params.n_column (per-group counts, typically AGG_COUNT alias)
//
// Params (JSON) must also supply:
//   - ms_within: f64 — within-group mean square from a preceding ANOVA
//   - df_within: f64 — within-group degrees of freedom
//   - n_column:  optional override for the per-group count column name
//
// Pairwise statistics:
//
//	diff_ij = mean_i − mean_j
//	SE      = √(ms_within · (1/n_i + 1/n_j) / 2)    (Tukey-Kramer SE)
//	q       = |diff_ij| / SE
//	p_adj   = studentizedRangeSurvival(q, k, df_within)
//	CI      = diff_ij ± q_crit(alpha) · SE
type tukeyHSDPost struct {
	spec    *types.Test
	field   string
	splitBy string
	nCol    string
	alpha   float64
	msW     float64
	dfW     float64
}

func newTukeyHSDPost(spec *types.Test, schema *encoding.Schema) (PostTest, error) {
	if spec.Field == "" || spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_TUKEY_HSD requires field (per-group mean column) and split_by (group label column)")
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
	var params struct {
		MSWithin float64 `json:"ms_within"`
		DFWithin float64 `json:"df_within"`
		NColumn  string  `json:"n_column"`
	}
	if len(spec.Params) > 0 {
		if err := json.Unmarshal(spec.Params, &params); err != nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"TEST_TUKEY_HSD: params JSON parse failed: "+err.Error(), nil)
		}
	}
	if params.MSWithin <= 0 || params.DFWithin <= 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_TUKEY_HSD: params.ms_within > 0 and params.df_within > 0 are required (typically lifted from a preceding TEST_ANOVA_F)")
	}
	nCol := params.NColumn
	if nCol == "" {
		nCol = "n"
	}
	return &tukeyHSDPost{
		spec:    spec,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		nCol:    nCol,
		alpha:   alpha,
		msW:     params.MSWithin,
		dfW:     params.DFWithin,
	}, nil
}

func (t *tukeyHSDPost) Run(rows []map[string]any) (*types.TestResult, error) {
	type group struct {
		label string
		mean  float64
		n     float64
	}
	groups := make([]group, 0, len(rows))
	for _, row := range rows {
		label, ok := stringValue(row[t.splitBy])
		if !ok {
			continue
		}
		mean, ok := floatValue(row[t.field])
		if !ok {
			continue
		}
		n, ok := floatValue(row[t.nCol])
		if !ok {
			continue
		}
		groups = append(groups, group{label, mean, n})
	}
	k := len(groups)
	if k < 3 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_TUKEY_HSD requires k ≥ 3 groups; got %d", k),
			map[string]any{"groups": k, "min_required": 3})
	}
	// Determinism: sort by label.
	sort.Slice(groups, func(i, j int) bool { return groups[i].label < groups[j].label })
	qCrit := studentizedRangeInverse(t.alpha, k, t.dfW)
	comparisons := make([]map[string]any, 0, k*(k-1)/2)
	minP := 1.0
	anyReject := false
	for i := 0; i < k-1; i++ {
		for j := i + 1; j < k; j++ {
			a := groups[i]
			b := groups[j]
			diff := a.mean - b.mean
			se := math.Sqrt(t.msW * (1/a.n + 1/b.n) / 2)
			q := math.Abs(diff) / se
			pAdj := studentizedRangeSurvival(q, k, t.dfW)
			ciHalf := qCrit * se
			rejected := pAdj < t.alpha
			if pAdj < minP {
				minP = pAdj
			}
			if rejected {
				anyReject = true
			}
			comparisons = append(comparisons, map[string]any{
				"a":           a.label,
				"b":           b.label,
				"diff":        diff,
				"se":          se,
				"q":           q,
				"p_adj":       pAdj,
				"reject_null": rejected,
				"ci_low":      diff - ciHalf,
				"ci_high":     diff + ciHalf,
			})
		}
	}
	return &types.TestResult{
		Label:      testLabel(t.spec),
		Type:       types.TEST_TUKEY_HSD,
		Variant:    "tukey_kramer",
		PValue:     minP,
		Alpha:      t.alpha,
		RejectNull: anyReject,
		Details: map[string]any{
			"k_groups":     k,
			"family_alpha": t.alpha,
			"ms_within":    t.msW,
			"df_within":    t.dfW,
			"q_critical":   qCrit,
			"comparisons":  comparisons,
		},
	}, nil
}

// floatValue normalizes the post-pipeline row's any-typed slot into a
// float64. Aggregator/window outputs land here as float64 or int64.
func floatValue(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint8:
		return float64(x), true
	}
	return 0, false
}

// stringValue normalizes the post-pipeline row's any-typed slot into a
// string. Grouper output keys are strings; categorical aggregator
// summaries may report []string for FREQUENCY.
func stringValue(v any) (string, bool) {
	if s, ok := v.(string); ok {
		return s, true
	}
	return "", false
}

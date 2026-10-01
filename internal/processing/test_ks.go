package processing

import (
	"fmt"
	"math"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ksRow implements TEST_KS as a buffered row test: two-sample
// Kolmogorov-Smirnov on a numeric field split by a categorical.
//
// K-S cannot stream — it needs the empirical CDF of each group, which
// requires the full sorted value set. Tier-1 routing flips to buffered
// when this test is present (TestType.Streamable() returns false), so
// `processStreamingPath = false` is enforced upstream and the test
// simply collects values per group during UpdateRow.
type ksRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	alpha   float64

	values map[string][]float64
	order  []string
}

func newKSRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_KS requires field")
	}
	if spec.SplitBy == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_KS requires split_by")
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
	if schema != nil {
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_KS field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.SplitBy); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("TEST_KS split_by %q must be categorical, got %s", spec.SplitBy, f.Type.String()),
				map[string]any{"split_by": spec.SplitBy, "field_type": f.Type.String()})
		}
	}
	return &ksRow{
		spec:    spec,
		schema:  schema,
		field:   spec.Field,
		splitBy: spec.SplitBy,
		alpha:   alpha,
		values:  make(map[string][]float64),
	}, nil
}

func (k *ksRow) UpdateRow(record *Record) error {
	v, ok := record.NumericValue(k.field)
	if !ok {
		return nil
	}
	key, ok := record.StringValue(k.splitBy)
	if !ok {
		return nil
	}
	if _, exists := k.values[key]; !exists {
		k.order = append(k.order, key)
	}
	k.values[key] = append(k.values[key], v)
	return nil
}

func (k *ksRow) Finalize() (*types.TestResult, error) {
	defer k.reset()
	keys := append([]string(nil), k.order...)
	sort.Strings(keys)
	if len(keys) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_KS requires 2 split groups, got %d", len(keys)),
			map[string]any{"groups": keys, "min_required": 2})
	}
	if len(keys) > 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_SPLIT_GROUPS_LT_2,
			fmt.Sprintf("TEST_KS sees %d groups; specify two groups via a FILTER_INCLUDE/FILTER_EXCLUDE upstream", len(keys)),
			map[string]any{"groups": keys, "max_allowed": 2})
	}
	a := append([]float64(nil), k.values[keys[0]]...)
	b := append([]float64(nil), k.values[keys[1]]...)
	if len(a) < 2 || len(b) < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_KS requires n ≥ 2 per group, got %d / %d", len(a), len(b)),
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
		Variant:    "two_sample",
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

func (k *ksRow) reset() {
	k.values = make(map[string][]float64)
	k.order = nil
}

// ksTwoSampleD computes the maximum absolute difference between two
// empirical CDFs for sorted samples a and b. Mirrors scipy's
// ks_2samp routine.
func ksTwoSampleD(a, b []float64) float64 {
	i, j := 0, 0
	n1 := float64(len(a))
	n2 := float64(len(b))
	var fa, fb, D float64
	for i < len(a) && j < len(b) {
		va := a[i]
		vb := b[j]
		if va <= vb {
			i++
			fa = float64(i) / n1
		}
		if va >= vb {
			j++
			fb = float64(j) / n2
		}
		if diff := math.Abs(fa - fb); diff > D {
			D = diff
		}
	}
	return D
}

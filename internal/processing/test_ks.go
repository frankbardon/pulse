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
//
// Frequency-weighted (ClassFrequencyOnly), each row stands for w
// identical rows: the ECDFs step by Σw (ksTwoSampleDW) and the
// asymptotic p reads the expanded sizes n₁ = Σw₁, n₂ = Σw₂ — exactly
// the unweighted test on the expanded rows. Details keep the raw `n`
// and add `sum_weights` shaped like it.
type ksRow struct {
	spec    *types.Test
	schema  *encoding.Schema
	field   string
	splitBy string
	alpha   float64

	values map[string]*rankSample
	order  []string

	testWeight
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
		values:  make(map[string]*rankSample),

		testWeight: newTestWeight(spec),
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
	w, ok := k.rowWeight(record)
	if !ok {
		return nil
	}
	s, exists := k.values[key]
	if !exists {
		s = &rankSample{}
		k.values[key] = s
		k.order = append(k.order, key)
	}
	s.add(v, w)
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
			fmt.Sprintf("TEST_KS sees %d groups", len(keys))+remedyTwoGroupsFilterUpstream(nil),
			map[string]any{"groups": keys, "max_allowed": 2})
	}
	sa, sb := k.values[keys[0]], k.values[keys[1]]
	if sa.n() < 2 || sb.n() < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_KS requires n ≥ 2 per group, got %d / %d", sa.n(), sb.n()),
			map[string]any{"n": []int{sa.n(), sb.n()}, "min_required": 2})
	}
	a, wa := sortedWeighted(sa)
	b, wb := sortedWeighted(sb)
	D := ksTwoSampleDW(a, wa, b, wb)
	// Σw per group (the row counts unweighted).
	n1 := sa.sumW
	n2 := sb.sumW
	en := math.Sqrt(n1 * n2 / (n1 + n2))
	p := kolmogorovSurvival((en + 0.12 + 0.11/en) * D)
	res := &types.TestResult{
		Label:      testLabel(k.spec),
		Type:       types.TEST_KS,
		Variant:    "two_sample",
		Statistic:  D,
		PValue:     p,
		Alpha:      k.alpha,
		RejectNull: p < k.alpha,
		Details: map[string]any{
			"groups": keys,
			"n":      []int{sa.n(), sb.n()},
		},
	}
	k.noteGroupSums(res.Details, []float64{sa.sumW, sb.sumW}, []float64{sa.sumWSq, sb.sumWSq})
	return res, nil
}

func (k *ksRow) reset() {
	k.values = make(map[string]*rankSample)
	k.order = nil
}

// sortedWeighted returns a sample's values in ascending order
// (sort.Float64s order: NaN first) with their weights alongside.
func sortedWeighted(s *rankSample) ([]float64, []float64) {
	idx := make([]int, len(s.values))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		x, y := s.values[idx[i]], s.values[idx[j]]
		return x < y || (math.IsNaN(x) && !math.IsNaN(y))
	})
	vs := make([]float64, len(idx))
	ws := make([]float64, len(idx))
	for i, j := range idx {
		vs[i], ws[i] = s.values[j], s.weights[j]
	}
	return vs, ws
}

// ksTwoSampleD computes the maximum absolute difference between two
// empirical CDFs for sorted samples a and b (every row weighing 1).
func ksTwoSampleD(a, b []float64) float64 {
	return ksTwoSampleDW(a, nil, b, nil)
}

// ksTwoSampleDW is the two-sample D for sorted samples a and b with
// row weights wa / wb (nil: every row weighs 1): sup |F_a(x) − F_b(x)|
// over the weighted ECDFs, F(x) = Σ_{v ≤ x} w / Σw. The ECDFs are
// compared only once every row tied at a value has been consumed on
// both sides — the supremum is attained at a data value, and a
// comparison inside a tie run would read a step the ECDF never takes
// (with unit weights this is scipy ks_2samp / R ks.test's D, ties
// included). A frequency weight w steps the ECDF by w, exactly as w
// identical rows would. A NaN run (sorted first) is consumed as one
// step on both sides.
func ksTwoSampleDW(a, wa, b, wb []float64) float64 {
	weight := func(ws []float64, i int) float64 {
		if ws == nil {
			return 1
		}
		return ws[i]
	}
	total := func(xs, ws []float64) float64 {
		var t float64
		for i := range xs {
			t += weight(ws, i)
		}
		return t
	}
	same := func(x, y float64) bool { return x == y || (x != x && y != y) }
	n1, n2 := total(a, wa), total(b, wb)
	i, j := 0, 0
	var ca, cb, D float64
	for i < len(a) && j < len(b) {
		va, vb := a[i], b[j]
		// !(x > y) is x ≤ y, and true when either is NaN, so a NaN
		// run always advances and the loop terminates.
		if !(va > vb) {
			for i < len(a) && same(a[i], va) {
				ca += weight(wa, i)
				i++
			}
		}
		if !(vb > va) {
			for j < len(b) && same(b[j], vb) {
				cb += weight(wb, j)
				j++
			}
		}
		if diff := math.Abs(ca/n1 - cb/n2); diff > D {
			D = diff
		}
	}
	return D
}

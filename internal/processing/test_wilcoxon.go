package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// wilcoxonSRRow implements TEST_WILCOXON_SR as a buffered row test:
// nonparametric paired alternative to TEST_PAIRED_T.
//
// Algorithm:
//  1. d_i = Field − Field2 over rows where both values are present.
//  2. Drop zero diffs.
//  3. Mid-rank |d_i| with tie correction.
//  4. W⁺ = Σ rank_i over positive diffs.
//  5. μ_W = n(n+1)/4
//     σ²_W = n(n+1)(2n+1)/24 − (Σ(t³−t))/48
//  6. z = (W⁺ − μ_W) / σ_W with continuity correction; p = 2(1−Φ(|z|)).
//
// Drop-pair semantics on null mismatch; the mismatch count surfaces as
// a PULSE_TEST_PAIRED_LENGTH_MISMATCH warning so the caller knows.
//
// Frequency-weighted (ClassFrequencyOnly), each pair stands for w
// identical pairs: weighted mid-ranks of |d|, W⁺ = Σ w·rank over
// positive diffs, n = Σw in μ_W / σ²_W and the expanded tie sizes —
// exactly the unweighted test on the expanded pairs. Details keep the
// raw `n` (and raw `zero_diffs` row count) and add `sum_weights`.
type wilcoxonSRRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field  string
	field2 string
	alpha  float64

	diffs    rankSample
	dropped  int
	mismatch int

	testWeight
}

func newWilcoxonSRRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" || spec.Field2 == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_WILCOXON_SR requires field and field2 (paired columns)")
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
				fmt.Sprintf("TEST_WILCOXON_SR field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.Field2); f != nil && (f.Type.IsCategorical()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD2_NOT_NUMERIC,
				fmt.Sprintf("TEST_WILCOXON_SR field2 %q has non-numeric type %s", spec.Field2, f.Type.String()),
				map[string]any{"field2": spec.Field2, "field_type": f.Type.String()})
		}
	}
	return &wilcoxonSRRow{
		spec:       spec,
		schema:     schema,
		field:      spec.Field,
		field2:     spec.Field2,
		alpha:      alpha,
		testWeight: newTestWeight(spec),
	}, nil
}

func (w *wilcoxonSRRow) UpdateRow(record *Record) error {
	x, xOk := record.NumericValue(w.field)
	y, yOk := record.NumericValue(w.field2)
	if !xOk || !yOk {
		if xOk != yOk {
			w.mismatch++
		}
		return nil
	}
	wt, ok := w.rowWeight(record)
	if !ok {
		return nil
	}
	d := x - y
	if d == 0 {
		w.dropped++
		return nil
	}
	w.diffs.add(d, wt)
	return nil
}

func (w *wilcoxonSRRow) Finalize() (*types.TestResult, error) {
	defer w.reset()
	n := w.diffs.n()
	if n < 6 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_WILCOXON_SR requires n ≥ 6 non-zero pairs, got %d", n),
			map[string]any{"n": n, "min_required": 6})
	}
	abs := make([]float64, n)
	for i, d := range w.diffs.values {
		abs[i] = math.Abs(d)
	}
	ranks, ties := weightedMidRanks(abs, w.diffs.weights)
	var wPlus, wMinus float64
	for i, d := range w.diffs.values {
		if d > 0 {
			wPlus += w.diffs.weights[i] * ranks[i]
		} else {
			wMinus += w.diffs.weights[i] * ranks[i]
		}
	}
	nf := w.diffs.sumW // the pair count unweighted
	muW := nf * (nf + 1) / 4
	varW := nf*(nf+1)*(2*nf+1)/24 - tieCorrectionW(ties)/48
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
		p = normalTwoSidedP(z)
	}
	res := &types.TestResult{
		Label:      testLabel(w.spec),
		Type:       types.TEST_WILCOXON_SR,
		Variant:    "asymptotic",
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
			"zero_diffs": w.dropped,
		},
	}
	// Signed: > 0 ⇒ Field tends to exceed Field2 (same direction as z).
	setEffectSize(res.Details, "rank_biserial", rankBiserialPaired(wPlus, wMinus))
	if w.mismatch > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %d row(s) had one paired value null; pair dropped",
			errors.PULSE_TEST_PAIRED_LENGTH_MISMATCH, w.mismatch))
	}
	w.noteScalar(res.Details, w.diffs.sumW, w.diffs.sumWSq)
	if tiesDominateW(ties, nf) {
		res.Warnings = append(res.Warnings, string(errors.PULSE_TEST_TIES_DOMINATE)+
			": ≥ 50% of |diff| values are tied; asymptotic p-value is unreliable")
	}
	return res, nil
}

func (w *wilcoxonSRRow) reset() {
	w.diffs = rankSample{}
	w.dropped = 0
	w.mismatch = 0
}

package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// kendallTauRow implements TEST_KENDALL_TAU (Kendall's τ-b) as a buffered
// row test: concordance-based correlation between Field and Field2.
//
// Algorithm: buffer paired values, then for every i<j classify the pair:
//
//	concordant: (x_i−x_j)·(y_i−y_j) > 0
//	discordant: (x_i−x_j)·(y_i−y_j) < 0
//	tied-x: x_i = x_j, y_i ≠ y_j
//	tied-y: y_i = y_j, x_i ≠ x_j
//	tied-both: dropped from both ties counts
//
//	τ_b = (C − D) / √((C+D+T_x) · (C+D+T_y))
//
// Variance under the null (no association) using the standard tie-
// adjusted formula:
//
//	Var(S) = ( n(n−1)(2n+5)
//	         − Σ t_x(t_x−1)(2t_x+5) − Σ t_y(t_y−1)(2t_y+5) ) / 18
//	         + extra ties cross-term (Kendall 1948 formula).
//
// p-value via the standard normal: z = (S − sign(S)) / √Var(S);
// p = 2(1−Φ(|z|)).
//
// Baseline implementation is O(n²) — the plan documents that an
// O(n log n) upgrade lands later if benchmarks demand it.
//
// Frequency-weighted (ClassFrequencyOnly), each pair stands for w
// identical pairs: a row pair (i, j) counts w_i·w_j times in C, D, T_x
// and T_y (copies of one row tie in both and drop out, as on the
// expansion), n reads Σw and the tie sums read the expanded tie sizes —
// exactly the unweighted τ-b on the expanded pairs. Details keep the
// raw `n` and add `sum_weights`; the pair counts become Σ w_i·w_j.
type kendallTauRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field  string
	field2 string
	alpha  float64

	xs rankSample
	ys []float64

	testWeight
}

func newKendallTauRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	if spec.Field == "" || spec.Field2 == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"TEST_KENDALL_TAU requires field and field2")
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
				fmt.Sprintf("TEST_KENDALL_TAU field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.Field2); f != nil && (f.Type.IsCategorical()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD2_NOT_NUMERIC,
				fmt.Sprintf("TEST_KENDALL_TAU field2 %q has non-numeric type %s", spec.Field2, f.Type.String()),
				map[string]any{"field2": spec.Field2, "field_type": f.Type.String()})
		}
	}
	return &kendallTauRow{
		spec:       spec,
		schema:     schema,
		field:      spec.Field,
		field2:     spec.Field2,
		alpha:      alpha,
		testWeight: newTestWeight(spec),
	}, nil
}

func (k *kendallTauRow) UpdateRow(record *Record) error {
	x, xOk := record.NumericValue(k.field)
	if !xOk {
		return nil
	}
	y, yOk := record.NumericValue(k.field2)
	if !yOk {
		return nil
	}
	w, ok := k.rowWeight(record)
	if !ok {
		return nil
	}
	k.xs.add(x, w)
	k.ys = append(k.ys, y)
	return nil
}

func (k *kendallTauRow) Finalize() (*types.TestResult, error) {
	defer k.reset()
	n := k.xs.n()
	if n < 3 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_KENDALL_TAU requires n ≥ 3, got %d", n),
			map[string]any{"n": n, "min_required": 3})
	}
	xs, ws := k.xs.values, k.xs.weights
	// Pair weights w_i·w_j (exactly 1 unweighted, so every sum is the
	// integer pair count).
	var c, d, tx, ty float64
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			dx := xs[i] - xs[j]
			dy := k.ys[i] - k.ys[j]
			pw := ws[i] * ws[j]
			switch {
			case dx == 0 && dy == 0:
				// tied in both — excluded from all counts
			case dx == 0:
				tx += pw
			case dy == 0:
				ty += pw
			case (dx > 0) == (dy > 0):
				c += pw
			default:
				d += pw
			}
		}
	}
	S := c - d
	denomA := (c + d) + tx
	denomB := (c + d) + ty
	denom := math.Sqrt(denomA * denomB)
	if denom == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_CORRELATION_UNDEFINED,
			"TEST_KENDALL_TAU: degenerate input (all pairs tied)",
			map[string]any{"n": n, "c": c, "d": d, "tx": tx, "ty": ty})
	}
	tau := S / denom
	// Variance under the null. Compute the tie-group sizes by sorting
	// and walking each column independently.
	_, tiesX := weightedMidRanks(xs, ws)
	_, tiesY := weightedMidRanks(k.ys, ws)
	nf := k.xs.sumW // n unweighted
	v0 := nf * (nf - 1) * (2*nf + 5)
	vt := tieKendallSumW(tiesX)
	vu := tieKendallSumW(tiesY)
	v1 := 0.0
	if nf > 1 {
		v1 = tieKendallV1W(tiesX) * tieKendallV1W(tiesY) / (2 * nf * (nf - 1))
	}
	v2 := 0.0
	if nf > 2 {
		v2 = tieKendallV2W(tiesX) * tieKendallV2W(tiesY) / (9 * nf * (nf - 1) * (nf - 2))
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
		p = normalTwoSidedP(z)
	}
	res := &types.TestResult{
		Label:      testLabel(k.spec),
		Type:       types.TEST_KENDALL_TAU,
		Variant:    "tau_b",
		Statistic:  tau,
		PValue:     p,
		Alpha:      k.alpha,
		RejectNull: p < k.alpha,
		Details: map[string]any{
			"n":          n,
			"concordant": kendallPairMass(c, k.basis.Weighted()),
			"discordant": kendallPairMass(d, k.basis.Weighted()),
			"ties_x":     kendallPairMass(tx, k.basis.Weighted()),
			"ties_y":     kendallPairMass(ty, k.basis.Weighted()),
			"s":          S,
			"var_s":      varS,
			"z":          z,
		},
	}
	k.noteScalar(res.Details, k.xs.sumW, k.xs.sumWSq)
	if tiesDominateW(tiesX, nf) || tiesDominateW(tiesY, nf) {
		res.Warnings = append(res.Warnings, string(errors.PULSE_TEST_TIES_DOMINATE)+
			": ≥ 50% of values are tied in at least one column; asymptotic p-value is unreliable")
	}
	return res, nil
}

func (k *kendallTauRow) reset() {
	k.xs = rankSample{}
	k.ys = nil
}

// kendallPairMass renders a Kendall pair count for the details: the int64
// count unweighted (the pre-weighting Go type), Σ w_i·w_j weighted.
func kendallPairMass(v float64, weighted bool) any {
	if weighted {
		return v
	}
	return int64(v)
}

// tieKendallSum returns Σ t_i(t_i−1)(2t_i+5) used by Var(S).
func tieKendallSum(ties []int) float64 { return tieKendallSumW(intsToFloats(ties)) }

// tieKendallSumW is tieKendallSum over weighted tie-group sizes.
func tieKendallSumW(ties []float64) float64 {
	sum := 0.0
	for _, t := range ties {
		sum += t * (t - 1) * (2*t + 5)
	}
	return sum
}

// tieKendallV1 returns Σ t_i(t_i−1) used by the Kendall v1 cross-term.
func tieKendallV1(ties []int) float64 { return tieKendallV1W(intsToFloats(ties)) }

// tieKendallV1W is tieKendallV1 over weighted tie-group sizes.
func tieKendallV1W(ties []float64) float64 {
	sum := 0.0
	for _, t := range ties {
		sum += t * (t - 1)
	}
	return sum
}

// tieKendallV2 returns Σ t_i(t_i−1)(t_i−2) used by the v2 cross-term.
func tieKendallV2(ties []int) float64 { return tieKendallV2W(intsToFloats(ties)) }

// tieKendallV2W is tieKendallV2 over weighted tie-group sizes.
func tieKendallV2W(ties []float64) float64 {
	sum := 0.0
	for _, t := range ties {
		sum += t * (t - 1) * (t - 2)
	}
	return sum
}

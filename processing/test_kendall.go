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
//   concordant: (x_i−x_j)·(y_i−y_j) > 0
//   discordant: (x_i−x_j)·(y_i−y_j) < 0
//   tied-x: x_i = x_j, y_i ≠ y_j
//   tied-y: y_i = y_j, x_i ≠ x_j
//   tied-both: dropped from both ties counts
//
//   τ_b = (C − D) / √((C+D+T_x) · (C+D+T_y))
//
// Variance under the null (no association) using the standard tie-
// adjusted formula:
//
//   Var(S) = ( n(n−1)(2n+5)
//            − Σ t_x(t_x−1)(2t_x+5) − Σ t_y(t_y−1)(2t_y+5) ) / 18
//            + extra ties cross-term (Kendall 1948 formula).
//
// p-value via the standard normal: z = (S − sign(S)) / √Var(S);
// p = 2(1−Φ(|z|)).
//
// Baseline implementation is O(n²) — the plan documents that an
// O(n log n) upgrade lands later if benchmarks demand it.
type kendallTauRow struct {
	spec   *types.Test
	schema *encoding.Schema

	field  string
	field2 string
	alpha  float64

	xs []float64
	ys []float64
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
		if f := schema.Field(spec.Field); f != nil && (f.Type.IsCategorical() || f.Type.IsGeo()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD_NOT_NUMERIC,
				fmt.Sprintf("TEST_KENDALL_TAU field %q has non-numeric type %s", spec.Field, f.Type.String()),
				map[string]any{"field": spec.Field, "field_type": f.Type.String()})
		}
		if f := schema.Field(spec.Field2); f != nil && (f.Type.IsCategorical() || f.Type.IsGeo()) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_FIELD2_NOT_NUMERIC,
				fmt.Sprintf("TEST_KENDALL_TAU field2 %q has non-numeric type %s", spec.Field2, f.Type.String()),
				map[string]any{"field2": spec.Field2, "field_type": f.Type.String()})
		}
	}
	return &kendallTauRow{
		spec:   spec,
		schema: schema,
		field:  spec.Field,
		field2: spec.Field2,
		alpha:  alpha,
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
	k.xs = append(k.xs, x)
	k.ys = append(k.ys, y)
	return nil
}

func (k *kendallTauRow) Finalize() (*types.TestResult, error) {
	defer k.reset()
	n := len(k.xs)
	if n < 3 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_KENDALL_TAU requires n ≥ 3, got %d", n),
			map[string]any{"n": n, "min_required": 3})
	}
	var c, d, tx, ty int64
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			dx := k.xs[i] - k.xs[j]
			dy := k.ys[i] - k.ys[j]
			switch {
			case dx == 0 && dy == 0:
				// tied in both — excluded from all counts
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
			"TEST_KENDALL_TAU: degenerate input (all pairs tied)",
			map[string]any{"n": n, "c": c, "d": d, "tx": tx, "ty": ty})
	}
	tau := S / denom
	// Variance under the null. Compute the tie-group sizes by sorting
	// and walking each column independently.
	_, tiesX := midRanks(k.xs)
	_, tiesY := midRanks(k.ys)
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
		Variant:    "tau_b",
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

func (k *kendallTauRow) reset() {
	k.xs = nil
	k.ys = nil
}

// tieKendallSum returns Σ t_i(t_i−1)(2t_i+5) used by Var(S).
func tieKendallSum(ties []int) float64 {
	sum := 0.0
	for _, t := range ties {
		tf := float64(t)
		sum += tf * (tf - 1) * (2*tf + 5)
	}
	return sum
}

// tieKendallV1 returns Σ t_i(t_i−1) used by the Kendall v1 cross-term.
func tieKendallV1(ties []int) float64 {
	sum := 0.0
	for _, t := range ties {
		tf := float64(t)
		sum += tf * (tf - 1)
	}
	return sum
}

// tieKendallV2 returns Σ t_i(t_i−1)(t_i−2) used by the v2 cross-term.
func tieKendallV2(ties []int) float64 {
	sum := 0.0
	for _, t := range ties {
		tf := float64(t)
		sum += tf * (tf - 1) * (tf - 2)
	}
	return sum
}

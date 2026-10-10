package processing

import (
	"math"

	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_partial.go is MAT_PARTIAL_CORRELATION: the partial correlation
// of every output pair, read off the precision matrix of the folded
// columns' Pearson correlation (CoMoment.Corr — listwise, or pairwise
// with each pair over its own rows).
//
//   - params.control "all" (the default): each pair controls for every
//     other member — partial_ij = −P_ij / √(P_ii·P_jj), P = R⁻¹ (ppcor::pcor,
//     corpcor::cor2pcor).
//   - a control list C: the output axis O is the members not in C, and
//     each pair controls for exactly C — the correlation of the
//     conditional covariance R_OO − R_OC·R_CC⁻¹·R_CO, which is
//     (P_OO)⁻¹ (ppcor::pcor.test(x, y, Z) per pair). A control outside
//     the vector joins the fold (and so the missing-data mode).
//
// Both inversions are the REFERENCE linalg.InverseSPD (FMA-free), so the
// matrix is arch-independent and, being a function of the merged
// co-moments, worker-invariant: the operator is streamable and
// mergeable like MAT_CORRELATION.
//
// Outputs: primary (the partials, diagonal 1), pairwise auxiliary.n
// (each output pair's own N) and the warnings; no scalars, no vectors.
//
// Undefined figures: when any input correlation is undefined (a
// zero-spread member, a thin pair, no rows) every partial cell is NaN —
// each partial reads the whole matrix — and the co-moment warnings say
// why. A decomposition operator: the input passes the shared PSD guard
// (guardPSD) first — a pairwise input that is not PSD is refused with
// PULSE_MATRIX_NOT_PSD unless params.repair "nearest" — and a singular
// input (a member or control that is a linear combination of others)
// is PULSE_MATRIX_SINGULAR with dependent_fields.
func finalizePartialCorrelation(in *matrixFinalizeInput) (matrixOutput, error) {
	m := in.slot
	plan := in.Plan()
	r := in.CM.Corr()
	out := matrixOutput{Warnings: m.warnings(in.CM, r)}
	axis := in.Output()
	if plan.Pairwise && in.WantAuxiliary() {
		// The output pairs' own N (CoMoment.PairN); every partial also
		// rests on the other pairs (Components min_pair_n).
		out.Auxiliary = map[string]*types.MatrixValues{
			"n": in.Square(func(a, b int) float64 { return float64(in.CM.PairN(axis[a], axis[b])) }),
		}
	}
	if !judgeable(r) {
		out.Primary = in.Square(func(int, int) float64 { return math.NaN() })
		return out, nil
	}
	r, warn, err := in.guardPSD(r, nil)
	if err != nil {
		return matrixOutput{}, err
	}
	if warn != nil {
		out.Warnings = append(out.Warnings, warn)
	}
	pc, err := partialCorrelation(r, axis, plan.Controls == nil)
	if err != nil {
		return matrixOutput{}, singularMatrixError(err, plan.Name, in.Members())
	}
	out.Primary = in.Square(pc.At)
	return out, nil
}

// partialCorrelation returns the partial correlation matrix over the
// output positions axis of the correlation (or covariance — the result
// is scale-free) matrix r: controlling for every other column when all
// is true (axis is then every column), else for exactly the columns not
// on axis. A singular r is the InverseSPD PULSE_MATRIX_SINGULAR (with
// the linalg singular diagnostics over r's columns).
func partialCorrelation(r *linalg.Sym, axis []int, all bool) (*linalg.Sym, error) {
	prec, err := linalg.InverseSPD(r)
	if err != nil {
		return nil, err
	}
	k := len(axis)
	sub, _ := linalg.NewSym(k, nil)
	for a := 0; a < k; a++ {
		for b := a; b < k; b++ {
			sub.Set(a, b, prec.At(axis[a], axis[b]))
		}
	}
	sign := -1.0
	if !all {
		// The conditional covariance given the controls: (P_OO)⁻¹,
		// positive definite whenever P is.
		if sub, err = linalg.InverseSPD(sub); err != nil {
			return nil, err
		}
		sign = 1
	}
	out, _ := linalg.NewSym(k, nil)
	for a := 0; a < k; a++ {
		out.Set(a, a, 1)
		for b := a + 1; b < k; b++ {
			v := sign * sub.At(a, b) / math.Sqrt(float64(sub.At(a, a)*sub.At(b, b)))
			out.Set(a, b, math.Max(-1, math.Min(1, v)))
		}
	}
	return out, nil
}

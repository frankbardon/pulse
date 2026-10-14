package linalg

import "math"

// ListwiseState is the complete stored state of a Listwise CoMoment:
// every figure its outputs (Mean, Cov, Corr, NEff, …) derive from, at
// full precision. A fitted state is captured once (CoMoment.ListwiseState)
// and rebuilt later (NewCoMomentFromListwise) — on another request, or
// after a round trip through storage — so a consumer that scores against
// a prebuilt fit derives every block (covariance, correlation, a
// decomposition) by the same arithmetic, to the same bits, as one that
// folded the rows itself.
type ListwiseState struct {
	// N, NMass and NWeightInvalid are CoMoment.N, the rows counted with
	// mass (w > 0) and CoMoment.NWeightInvalid.
	N, NMass, NWeightInvalid int64
	// W and W2 are Σw and Σw² over the rows N counts.
	W, W2 float64
	// Mean is the stored running mean, axis order — the raw figure, so
	// an accumulator with no mass keeps its zeros (Mean() reports NaN
	// there).
	Mean []float64
	// M2 is the raw weighted co-moment Σw(xi−mi)(xj−mj) — PairM2 at
	// every (i, j).
	M2 *Sym
}

// ListwiseState captures c's stored state (independent copies). A
// Pairwise accumulator has no single state and is
// PULSE_MATRIX_SHAPE_MISMATCH.
func (c *CoMoment) ListwiseState() (ListwiseState, error) {
	if c.mode != Listwise {
		return ListwiseState{}, shapeError("only a listwise co-moment has a single fitted state",
			map[string]any{"mode": c.mode.String()})
	}
	return ListwiseState{
		N: c.n, NMass: c.nMass, NWeightInvalid: c.nInvalid,
		W: c.w, W2: c.w2,
		Mean: append([]float64(nil), c.mean...),
		M2:   &Sym{n: c.p, data: append([]float64(nil), c.m2...)},
	}, nil
}

// NewCoMomentFromListwise rebuilds the Listwise accumulator s was
// captured from: every output, and every later Add or Merge, is
// bit-identical to the original's. A nil M2, a Mean whose length is not
// M2's order, a negative count, NMass above N, or a W / W2 that is not
// finite and non-negative is PULSE_MATRIX_SHAPE_MISMATCH.
func NewCoMomentFromListwise(s ListwiseState) (*CoMoment, error) {
	if s.M2 == nil {
		return nil, nilOperand("co-moment M2")
	}
	p := s.M2.N()
	if len(s.Mean) != p {
		return nil, shapeError("listwise state mean length must equal the co-moment order",
			map[string]any{"mean": len(s.Mean), "p": p})
	}
	if s.N < 0 || s.NMass < 0 || s.NWeightInvalid < 0 || s.NMass > s.N {
		return nil, shapeError("listwise state counts must be non-negative with NMass ≤ N",
			map[string]any{"n": s.N, "n_mass": s.NMass, "n_weight_invalid": s.NWeightInvalid})
	}
	for _, v := range []float64{s.W, s.W2} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return nil, shapeError("listwise state weight sums must be finite and non-negative",
				map[string]any{"w": s.W, "w2": s.W2})
		}
	}
	return &CoMoment{
		p: p, mode: Listwise,
		n: s.N, nMass: s.NMass, nInvalid: s.NWeightInvalid,
		w: s.W, w2: s.W2,
		mean: append([]float64(nil), s.Mean...),
		m2:   append([]float64(nil), s.M2.data...),
	}, nil
}

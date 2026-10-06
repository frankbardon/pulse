package vectors

import "github.com/frankbardon/pulse/types"

// matrix_rules.go holds the per-matrix facts predict reports BEFORE a
// run and the engine honours DURING one, so the two cannot drift: both
// read them off the one resolved Matrix. Schema-only, no execution —
// importable from internal/descriptor.

// PSDRisk reports whether the matrix can come back NOT positive
// semidefinite — the only shapes the engine runs its
// PULSE_MATRIX_NOT_PSD check on, so a false here is a guarantee the
// warning never fires. Listwise matrices are Gram matrices over one row
// set and always PSD. Under pairwise each cell rests on its own rows:
//
//   - MAT_COVARIANCE at p ≥ 2 — a variance and a covariance over
//     different rows need not satisfy |C_ij| ≤ √(V_i·V_j);
//   - MAT_CORRELATION at p ≥ 3 — a 2 × 2 correlation is clamped to
//     [−1, 1] with a unit diagonal, which is always PSD, but three
//     pairwise r's need not be mutually consistent.
func (m Matrix) PSDRisk() bool {
	if !m.Pairwise {
		return false
	}
	p := len(m.Members.Members)
	if m.Type == types.MAT_CORRELATION {
		return p >= 3
	}
	return p >= 2
}

// Accumulator byte layout (linalg.CoMoment): the shared header — n and
// nInvalid (int64), w and w2 (float64) — then listwise p means plus the
// packed upper-triangle co-moments, or pairwise one per-pair state
// (n int64; w, mi, mj, mii, mjj, cij float64) per packed cell.
const (
	coMomentHeaderBytes = 4 * 8
	pairMomentBytes     = 7 * 8
)

// AccumulatorBytes estimates the payload bytes of one co-moment state
// over the matrix's members: 32 + 8·(p + p(p+1)/2) listwise,
// 32 + 56·p(p+1)/2 pairwise. The engine holds one such state per
// populated merge block (linalg.MergeBlockSize rows) until finalize, so
// a run's matrix state is this times its block count. Go headers and
// map overhead are not counted.
func (m Matrix) AccumulatorBytes() int64 {
	p := int64(len(m.Members.Members))
	cells := p * (p + 1) / 2
	if m.Pairwise {
		return coMomentHeaderBytes + pairMomentBytes*cells
	}
	return coMomentHeaderBytes + 8*(p+cells)
}

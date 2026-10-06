package processing

import (
	stderrors "errors"
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_warnings.go derives a matrix's data-quality warnings
// (MatrixResult.Warnings) from its merged co-moment state at finalize.
// Every input is a function of the merged blocks and the integer drop
// count, so the warnings are as worker-invariant as the matrix. Order:
// PULSE_MATRIX_INSUFFICIENT_N, PULSE_MATRIX_ZERO_VARIANCE,
// PULSE_MATRIX_LISTWISE_HEAVY_DROP, PULSE_MATRIX_NOT_PSD.

// psdTolerance is the diagonal tolerance of the NOT_PSD check: the
// diagonal-scaled matrix is judged PSD when the reference Cholesky
// succeeds once psdTolerance is added to its unit diagonal, so a
// singular-but-PSD matrix (collinear members) and last-ulp rounding are
// not reported.
const psdTolerance = 1e-10

// warnings returns the slot's warnings for the merged state cm and its
// primary matrix (nil when there is none).
func (m *matrixSlot) warnings(cm *linalg.CoMoment, primary *linalg.Sym) []*types.ResponseWarning {
	var out []*types.ResponseWarning
	members := m.plan.Members.Members
	p := len(members)

	// Matrix-level thinness: fewer than 2 rows or no mass. The member
	// and PSD checks then say nothing more.
	if cm.N() < 2 || !(cm.W() > 0) {
		return append(out, matrixWarning(errors.PULSE_MATRIX_INSUFFICIENT_N,
			"matrix "+m.plan.Name+" rests on fewer than 2 rows or no weight mass; its cells are undefined or degenerate",
			map[string]any{"matrix": m.plan.Name, "scope": "matrix", "n": cm.N(), "sum_weights": cm.W()}))
	}

	if m.plan.Pairwise {
		var thin []map[string]any
		for i := 0; i < p; i++ {
			for j := i; j < p; j++ {
				if cm.PairN(i, j) < 2 || !(cm.PairW(i, j) > 0) {
					thin = append(thin, map[string]any{"row": members[i], "col": members[j], "n": cm.PairN(i, j)})
				}
			}
		}
		if len(thin) > 0 {
			out = append(out, matrixWarning(errors.PULSE_MATRIX_INSUFFICIENT_N,
				"matrix "+m.plan.Name+" has pairs resting on fewer than 2 rows or no weight mass; their cells are undefined or degenerate",
				map[string]any{"matrix": m.plan.Name, "scope": "pairs", "pairs": thin}))
		}
	}

	// Zero spread: a member whose own rows (pair (i, i)) are enough but
	// all equal on the massed rows.
	pop := cm.Cov(0)
	var flat []string
	for i := 0; i < p; i++ {
		if cm.PairN(i, i) >= 2 && cm.PairW(i, i) > 0 && pop.At(i, i) == 0 {
			flat = append(flat, members[i])
		}
	}
	if len(flat) > 0 {
		out = append(out, matrixWarning(errors.PULSE_MATRIX_ZERO_VARIANCE,
			"matrix "+m.plan.Name+" has members with zero spread; correlations touching them are null",
			map[string]any{"matrix": m.plan.Name, "members": flat}))
	}

	if share := m.plan.MaxDropShare; share != nil && !m.plan.Pairwise {
		rows := cm.N() + cm.NWeightInvalid() + m.dropped
		if rows > 0 {
			got := float64(m.dropped) / float64(rows)
			if got > *share {
				out = append(out, matrixWarning(errors.PULSE_MATRIX_LISTWISE_HEAVY_DROP,
					"listwise deletion dropped more rows from matrix "+m.plan.Name+" than params.max_drop_share allows",
					map[string]any{"matrix": m.plan.Name, "dropped": m.dropped, "rows": rows, "share": got, "max_drop_share": *share}))
			}
		}
	}

	if m.plan.Pairwise {
		if pivot, checked, bad := notPSD(primary); bad {
			out = append(out, matrixWarning(errors.PULSE_MATRIX_NOT_PSD,
				"pairwise matrix "+m.plan.Name+" is not positive semidefinite (reference Cholesky fails at member "+members[pivot]+")",
				map[string]any{"matrix": m.plan.Name, "pivot": pivot, "member": members[pivot], "checked": checked, "tolerance": psdTolerance}))
		}
	}
	return out
}

// notPSD judges s positive semidefinite through the REFERENCE Cholesky
// (FMA-free, arch-independent). Members with an undefined or
// non-positive diagonal are left out; when any remaining cell is
// undefined nothing is judged. The rest is scaled by its diagonal
// (C_ij / (√S_ii·√S_jj) — PSD-ness is scale-invariant, and a
// correlation matrix is unchanged) and factored with psdTolerance added
// to the diagonal. It returns the failing pivot as an axis index of s,
// the number of members judged, and whether s is not PSD.
func notPSD(s *linalg.Sym) (pivot, checked int, bad bool) {
	var keep []int
	for i := 0; i < s.N(); i++ {
		if d := s.At(i, i); d > 0 && !math.IsInf(d, 0) {
			keep = append(keep, i)
		}
	}
	if len(keep) == 0 {
		return 0, 0, false
	}
	k := len(keep)
	root := make([]float64, k)
	for a, i := range keep {
		root[a] = math.Sqrt(s.At(i, i))
	}
	scaled, _ := linalg.NewSym(k, nil)
	for a := 0; a < k; a++ {
		for b := a; b < k; b++ {
			v := s.At(keep[a], keep[b])
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return 0, k, false
			}
			if a == b {
				v = 1 + psdTolerance
			} else {
				v = v / float64(root[a]*root[b])
			}
			scaled.Set(a, b, v)
		}
	}
	_, err := linalg.Cholesky(scaled)
	if err == nil {
		return 0, k, false
	}
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		if at, ok := ce.Details["pivot"].(int); ok && at >= 0 && at < k {
			return keep[at], k, true
		}
	}
	return keep[0], k, true
}

func matrixWarning(code errors.Code, msg string, details map[string]any) *types.ResponseWarning {
	return &types.ResponseWarning{Code: string(code), Message: msg, Details: details}
}

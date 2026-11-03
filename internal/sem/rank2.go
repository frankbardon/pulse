package sem

import (
	"gonum.org/v1/gonum/mat"
)

// Every ∂Σ/∂θ_k is a sum of symmetric rank-2 terms a·bᵀ + b·aᵀ, one per
// matrix location of the parameter:
//
//	Beta (i,j):   a = M[:,i], b = D[j,:]   (M = ΛC, D = CΨCᵀΛᵀ)
//	Lambda (i,j): a = e_i,    b = D[j,:]
//	Psi (i,j):    a = M[:,i], b = M[:,j]   (b halved on the diagonal)
//	Theta (i,j):  a = e_i,    b = e_j      (b halved on the diagonal)
//
// With K = Σ⁻¹ and W = K − KSK the derivatives reduce to dot products:
//
//	g_k  = tr(W ∂Σ_k)        = Σ 2·aᵀWb
//	H_kl = tr(K∂Σ_k K∂Σ_l)   = Σ 2·[(aᵀKc)(bᵀKd) + (aᵀKd)(bᵀKc)]
//
// over the terms (a,b) of k and (c,d) of l.

type rank2Term struct{ a, b, ka, kb []float64 }

func (st *state) rank2Terms(mm, c *mat.Dense, k *mat.SymDense) [][]rank2Term {
	d := mul(mul(c, st.mats[mPsi]), mm.T())
	n := st.nObs
	col := func(m *mat.Dense, j int, scale float64) []float64 {
		v := make([]float64, n)
		for i := range v {
			v[i] = scale * m.At(i, j)
		}
		return v
	}
	row := func(m *mat.Dense, i int) []float64 {
		v := make([]float64, n)
		mat.Row(v, i, m)
		return v
	}
	unit := func(i int, scale float64) []float64 {
		v := make([]float64, n)
		v[i] = scale
		return v
	}
	mulK := func(v []float64) []float64 { return mulVec(k, v) }
	out := make([][]rank2Term, len(st.free))
	for q, par := range st.free {
		for _, l := range par.locs {
			var a, b []float64
			half := 1.0
			if !l.sym {
				half = 0.5
			}
			switch l.mat {
			case mBeta:
				a, b = col(mm, l.i, 1), row(d, l.j)
			case mLambda:
				a, b = unit(l.i, 1), row(d, l.j)
			case mPsi:
				a, b = col(mm, l.i, 1), col(mm, l.j, half)
			case mTheta:
				a, b = unit(l.i, 1), unit(l.j, half)
			default:
				continue // a mean parameter leaves Σ unchanged
			}
			out[q] = append(out[q], rank2Term{a: a, b: b, ka: mulK(a), kb: mulK(b)})
		}
	}
	return out
}

func (st *state) rank2Derivatives(mm, c *mat.Dense, k *mat.SymDense, w *mat.Dense, needH bool) ([]float64, *mat.SymDense) {
	terms := st.rank2Terms(mm, c, k)
	g := make([]float64, len(terms))
	for q, ts := range terms {
		s := 0.0
		for _, t := range ts {
			wb := mulVec(w, t.b)
			s += float64(2 * dot(t.a, wb))
		}
		g[q] = s
	}
	if !needH {
		return g, nil
	}
	h := mat.NewSymDense(len(terms), nil)
	for i, ti := range terms {
		for j := i; j < len(terms); j++ {
			s := 0.0
			for _, x := range ti {
				for _, y := range terms[j] {
					s += float64(2 * (float64(dot(x.ka, y.a)*dot(x.kb, y.b)) + float64(dot(x.ka, y.b)*dot(x.kb, y.a))))
				}
			}
			h.SetSym(i, j, s)
		}
	}
	return g, h
}

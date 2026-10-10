package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_collinearity.go is MAT_COLLINEARITY: a collinearity check of
// the members as regression predictors (there is no response), off the
// slot's merged co-moments, so the operator is streamable and mergeable
// like MAT_CORRELATION.
//
// R is the members' correlation (CoMoment.Corr — listwise, or pairwise
// with each pair over its own rows). It passes the shared PSD guard
// (guardPSD; params.repair "nearest"), then:
//
//   - VIF = diag(R⁻¹) (reference InverseSPD, FMA-free, so the figures
//     are arch-independent), tolerance = 1/VIF — car::vif on lm over
//     the same predictors, which equals diag(R⁻¹) for any response. A
//     singular R (a member is a linear combination of others) refuses
//     the matrix with PULSE_MATRIX_SINGULAR (rank, condition_number,
//     dependent_fields).
//   - Belsley's condition indices and variance-decomposition
//     proportions (perturb::colldiag). The default (params.center
//     false) is Belsley's own recommendation: the scaled, UNCENTERED
//     predictors with an intercept column. Their cross-product is
//     rebuilt from the co-moment as W(Σ + μμᵀ) — bordered by the
//     intercept row [1, μᵀ] (over W, which the column scaling cancels)
//     — with Σ = D·R·D, D = the members' population standard
//     deviations: exactly the population covariance listwise, and under
//     pairwise each member's own-row mean and spread around the guarded
//     R, so the bordered matrix is PSD whenever R is (its Schur
//     complement is D·R·D). params.center true decomposes R itself (the
//     scaled, centered predictors, no intercept). Each column is
//     scaled to unit length (B_ij / √(B_ii·B_jj)), B = VΛVᵀ
//     (linalg.SymEigen, descending), condition index η_k = √(λ_1/λ_k)
//     — ascending, the first exactly 1 — and the proportion of
//     variable j's coefficient variance on dimension k is
//     (v_jk²/λ_k) / Σ_k (v_jk²/λ_k): squared, so sign-free.
//
// Outputs: primary R (the slot's encoding; the repaired R when the
// guard repaired it); pairwise auxiliary.n; auxiliary
// variance_decomposition (MatrixKindRectangular — a row per variable,
// "(intercept)" first when uncentered, a column per dimension "D1" …
// "Dq" in condition-index order; each row sums to 1); vectors vif,
// tolerance (one per member), condition_indices (one per dimension);
// scalars condition_number (the largest condition index) and max_vif
// (the largest VIF).
//
// Weights: R and the uncentered moments are scale-free ratios of the
// weighted co-moments, so both weight kinds give the same figures.
//
// An undefined input (a zero-spread member, a thin pair, no rows)
// nulls every figure — the co-moment warnings say why. A dimension
// whose eigenvalue rounds to 0 or below (an intercept collinearity
// beyond double precision) nulls the Belsley figures with a
// PULSE_MATRIX_SINGULAR warning; VIF stays defined.
func finalizeCollinearity(in *matrixFinalizeInput) (matrixOutput, error) {
	m := in.slot
	plan := in.Plan()
	cm := in.CM
	r := cm.Corr()
	p := r.N()
	out := matrixOutput{Warnings: m.warnings(cm, r)}
	if plan.Pairwise && in.WantAuxiliary() {
		out.Auxiliary = map[string]*types.MatrixValues{
			"n": in.Square(func(i, j int) float64 { return float64(cm.PairN(i, j)) }),
		}
	}
	q := p
	if !plan.Center {
		q = p + 1
	}
	mean := cm.Mean()
	sd := make([]float64, p)
	cov := cm.Cov(0)
	defined := judgeable(r)
	for i := 0; i < p; i++ {
		sd[i] = math.Sqrt(cov.At(i, i))
		if math.IsNaN(mean.At(i)) || math.IsInf(mean.At(i), 0) || !(sd[i] > 0) || math.IsInf(sd[i], 0) {
			defined = false
		}
	}
	if !defined {
		out.Primary = in.Square(r.At)
		in.collinearityParts(&out, nanVector(p), nanVector(q), nanSquare(q), math.NaN())
		return out, nil
	}
	g, warns, err := in.guardPSD(r, nil)
	if err != nil {
		return matrixOutput{}, err
	}
	out.Warnings = append(out.Warnings, warns...)
	out.Primary = in.Square(g.At)

	inv, err := linalg.InverseSPD(g)
	if err != nil {
		return matrixOutput{}, singularMatrixError(err, plan.Name, in.Members())
	}
	vif := make([]float64, p)
	for i := range vif {
		vif[i] = inv.At(i, i)
	}

	b := g
	if !plan.Center {
		b = uncenteredMoments(g, mean, sd)
	}
	cond, prop, ok, err := belsley(b)
	if err != nil {
		return matrixOutput{}, singularMatrixError(err, plan.Name, in.Members())
	}
	condNumber := cond[q-1]
	if !ok {
		cond, prop, condNumber = nanVector(q), nanSquare(q), math.NaN()
		out.Warnings = append(out.Warnings, matrixWarning(errors.PULSE_MATRIX_SINGULAR,
			"matrix "+plan.Name+": the scaled cross-product behind Belsley's diagnostics has an eigenvalue at or below 0 in double precision (a collinearity, usually with the intercept, beyond about 16 "+
				"significant digits), so the condition indices and variance-decomposition proportions are null; VIF and tolerance are still defined",
			map[string]any{"matrix": plan.Name, "outputs": []string{"condition_indices", "condition_number", "variance_decomposition"}, "reason": "not_positive_definite"}))
	}
	in.collinearityParts(&out, vif, cond, prop, condNumber)
	return out, nil
}

// collinearityParts fills the parts the run renders: vectors vif /
// tolerance (from vif) and condition_indices, the rectangular
// auxiliary variance_decomposition (prop[j][k], a row per variable)
// and scalars condition_number and max_vif (the largest VIF; null when
// any is).
func (in *matrixFinalizeInput) collinearityParts(out *matrixOutput, vif, cond []float64, prop [][]float64, condNumber float64) {
	if in.WantAuxiliary() {
		if out.Auxiliary == nil {
			out.Auxiliary = map[string]*types.MatrixValues{}
		}
		out.Auxiliary["variance_decomposition"] = in.belsleyValues(prop)
	}
	if in.WantVectors() {
		tol := make([]float64, len(vif))
		for i, v := range vif {
			tol[i] = 1 / v
		}
		out.Vectors = map[string]any{
			"vif":               vif,
			"tolerance":         tol,
			"condition_indices": cond,
		}
	}
	if in.WantScalars() {
		maxVIF := math.Inf(-1)
		for _, v := range vif {
			if math.IsNaN(v) {
				maxVIF = math.NaN()
				break
			}
			maxVIF = math.Max(maxVIF, v)
		}
		out.Scalars = map[string]float64{"condition_number": condNumber, "max_vif": maxVIF}
	}
}

// collinearityIntercept is the row key (and label) of the intercept in
// MAT_COLLINEARITY's uncentered variance decomposition. The
// parentheses keep it apart from any member named "intercept".
const collinearityIntercept = "(intercept)"

// belsleyValues renders the variance-decomposition proportions as a
// rectangular matrix: rows the variables ("(intercept)" first when
// uncentered, then the members, labels alike when explicit), columns
// the dimensions "D1" … "Dq" in condition-index order.
func (in *matrixFinalizeInput) belsleyValues(prop [][]float64) *types.MatrixValues {
	plan := in.Plan()
	members, labels := plan.OutputMembers()
	rows := append([]string(nil), members...)
	if !plan.Center {
		rows = append([]string{collinearityIntercept}, rows...)
		if plan.ExplicitLabels {
			labels = append([]string{collinearityIntercept}, labels...)
		}
	}
	cols := make([]string, len(rows))
	for k := range cols {
		cols[k] = "D" + strconv.Itoa(k+1)
	}
	out := &types.MatrixValues{
		Kind:       types.MatrixKindRectangular,
		Encoding:   types.MatrixEncodingFull,
		RowKeys:    rows,
		ColumnKeys: cols,
		Values:     make([][]float64, len(rows)),
	}
	if plan.ExplicitLabels {
		out.Labels = labels
	}
	for j := range rows {
		out.Values[j] = append([]float64(nil), prop[j]...)
	}
	return out
}

// uncenteredMoments is the (p + 1) × (p + 1) uncentered second-moment
// matrix of [1, x] over the weight mass, W(Σ + μμᵀ)/W: B_00 = 1,
// B_0j = μ_j, B_ij = sd_i·r_ij·sd_j + μ_i·μ_j. r is a PSD correlation,
// so B is PSD (its Schur complement on B_00 is D·r·D).
func uncenteredMoments(r *linalg.Sym, mean *linalg.Vec, sd []float64) *linalg.Sym {
	p := r.N()
	b, _ := linalg.NewSym(p+1, nil)
	b.Set(0, 0, 1)
	for i := 0; i < p; i++ {
		mi := mean.At(i)
		b.Set(0, i+1, mi)
		for j := i; j < p; j++ {
			b.Set(i+1, j+1, float64(float64(sd[i]*r.At(i, j))*sd[j])+float64(mi*mean.At(j)))
		}
	}
	return b
}

// belsley is Belsley's collinearity diagnostics over the cross-product
// b (perturb::colldiag with scale = TRUE): b scaled to unit diagonal,
// eigen-decomposed, the condition indices √(λ_1/λ_k) ascending and the
// variance-decomposition proportions prop[j][k] = (v_jk²/λ_k) /
// Σ_k (v_jk²/λ_k). ok is false when an eigenvalue is at or below 0
// (numerically singular); the figures are then not returned.
func belsley(b *linalg.Sym) (cond []float64, prop [][]float64, ok bool, err error) {
	q := b.N()
	s, _ := linalg.NewSym(q, nil)
	for i := 0; i < q; i++ {
		s.Set(i, i, 1)
		for j := i + 1; j < q; j++ {
			s.Set(i, j, b.At(i, j)/math.Sqrt(float64(b.At(i, i)*b.At(j, j))))
		}
	}
	eig, err := linalg.SymEigen(s)
	if err != nil {
		return nil, nil, false, err
	}
	lam := eig.Values.Slice()
	for _, v := range lam {
		if !(v > 0) {
			return nil, nil, false, nil
		}
	}
	cond = make([]float64, q)
	for k := range lam {
		cond[k] = math.Sqrt(lam[0] / lam[k])
	}
	prop = make([][]float64, q)
	for j := 0; j < q; j++ {
		row := make([]float64, q)
		total := 0.0
		for k := 0; k < q; k++ {
			v := eig.Vectors.At(j, k)
			row[k] = float64(v*v) / lam[k]
			total += row[k]
		}
		for k := range row {
			row[k] /= total
		}
		prop[j] = row
	}
	return cond, prop, true, nil
}

// nanSquare is a q × q matrix of NaN.
func nanSquare(q int) [][]float64 {
	out := make([][]float64, q)
	for i := range out {
		out[i] = nanVector(q)
	}
	return out
}

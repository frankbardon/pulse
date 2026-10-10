package processing

import (
	stderrors "errors"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_pca.go is MAT_PCA: a principal component analysis off the
// slot's merged co-moments, so the operator is streamable and mergeable
// like MAT_CORRELATION.
//
// The analysed matrix M is the members' correlation R (CoMoment.Corr —
// listwise, or pairwise with each pair over its own rows, R's
// cor(use = "pairwise")) under params.on "correlation", or their sample
// covariance under "covariance" (the kind's denominator, as
// MAT_RELIABILITY's: Σw − 1 unweighted / frequency, Σw − Σw²/Σw under
// probability weights). M passes the shared PSD guard (guardPSD;
// params.repair "nearest"), then linalg.SymEigen — eigenvalues
// descending, ties ordered by dominant index, every eigenvector's
// largest-magnitude component positive — so sign, order and shape are
// portable and the values agree across architectures within the
// gonum-backed eigensolver's last ulps.
//
// Outputs over the eigenpairs (λ_k, v_k):
//
//   - primary: the p × k loadings L_ik = v_ik·√max(λ_k, 0) (rectangular,
//     columns "PC1" … "PCk");
//   - auxiliary.eigenvectors: the p × k v (rectangular); pairwise
//     auxiliary.n as on every operator;
//   - vectors: eigenvalues (all p), explained_variance λ/Σλ, cumulative
//     (its running sum), communalities Σ_{k retained} L_ik², kmo_msa;
//   - scalars: kmo, bartlett_chisq, bartlett_df, bartlett_p,
//     components_retained.
//
// k is params.components: an integer, "kaiser" (λ > 1, the correlation
// default) or {"variance": s} (the fewest leading components whose
// cumulative share reaches s; p when rounding keeps the last share
// below s).
//
// KMO (psych::KMO) and Bartlett's sphericity test
// (psych::cortest.bartlett) always read the correlation — R, or the
// repaired correlation when the guard repaired M. KMO compares the
// squared correlations with the squared anti-image (partial)
// correlations off P = R⁻¹ (reference InverseSPD); Bartlett is
// χ² = −ln|R|·(N* − 1 − (2p + 5)/6) on p(p − 1)/2 df, |R| from the
// reference Cholesky. N* is the weighting.md inference size: N
// listwise, Σw under frequency weights, Kish n_eff under probability
// weights; pairwise the smallest pair's (pair Σw, scaled by n_eff/Σw
// under probability weights) with PULSE_MATRIX_PAIRWISE_N_STAR saying
// so. A singular R (a member that is a linear combination of others)
// nulls KMO, kmo_msa and Bartlett with a PULSE_MATRIX_SINGULAR warning
// (rank, condition_number, dependent_fields) — the components are still
// defined; N* ≤ 1 + (2p + 5)/6 nulls Bartlett with
// PULSE_MATRIX_INSUFFICIENT_N (scope "bartlett").
//
// An undefined input (a zero-spread member, a thin pair, no rows) nulls
// every figure — the co-moment warnings say why — with k columns of
// nulls for an integer k and none otherwise.
func finalizePCA(in *matrixFinalizeInput) (matrixOutput, error) {
	m := in.slot
	plan := in.Plan()
	cm := in.CM
	r := cm.Corr()
	s := r
	var corr *linalg.Sym
	if plan.On == vectors.PCAOnCovariance {
		s = reliabilityCov(cm, m.weight)
		corr = r
	}
	p := s.N()
	out := matrixOutput{Warnings: m.warnings(cm, s)}
	if plan.Pairwise && in.WantAuxiliary() {
		out.Auxiliary = map[string]*types.MatrixValues{
			"n": in.Square(func(i, j int) float64 { return float64(cm.PairN(i, j)) }),
		}
	}
	if !judgeable(s) || !judgeable(r) {
		k := 0
		if plan.Components.Rule == vectors.PCAComponentsFixed {
			k = plan.Components.K
		}
		nan := func(int, int) float64 { return math.NaN() }
		out.Primary = in.Rect(pcaColumns(k), nan)
		if in.WantAuxiliary() {
			if out.Auxiliary == nil {
				out.Auxiliary = map[string]*types.MatrixValues{}
			}
			out.Auxiliary["eigenvectors"] = in.Rect(pcaColumns(k), nan)
		}
		if in.WantVectors() {
			out.Vectors = map[string]any{}
			for _, key := range []string{"eigenvalues", "explained_variance", "cumulative", "communalities", "kmo_msa"} {
				out.Vectors[key] = nanVector(p)
			}
		}
		if in.WantScalars() {
			retained := math.NaN()
			if plan.Components.Rule == vectors.PCAComponentsFixed {
				retained = float64(k)
			}
			out.Scalars = map[string]float64{
				"kmo": math.NaN(), "bartlett_chisq": math.NaN(), "bartlett_df": float64(p * (p - 1) / 2),
				"bartlett_p": math.NaN(), "components_retained": retained,
			}
		}
		return out, nil
	}
	g, warns, err := in.guardPSD(s, corr)
	if err != nil {
		return matrixOutput{}, err
	}
	out.Warnings = append(out.Warnings, warns...)
	kmoInput := r
	if len(warns) > 0 {
		// The guard repaired M: KMO and Bartlett read the repaired
		// correlation (M itself on a correlation; cov2cor of the
		// repaired covariance otherwise).
		kmoInput = covToCorr(g)
	}

	eig, err := linalg.SymEigen(g)
	if err != nil {
		return matrixOutput{}, singularMatrixError(err, plan.Name, in.Members())
	}
	lam := eig.Values.Slice()
	k := pcaRetained(plan.Components, lam)
	cols := pcaColumns(k)
	loading := func(i, c int) float64 {
		return float64(eig.Vectors.At(i, c) * math.Sqrt(math.Max(lam[c], 0)))
	}
	out.Primary = in.Rect(cols, loading)
	if in.WantAuxiliary() {
		if out.Auxiliary == nil {
			out.Auxiliary = map[string]*types.MatrixValues{}
		}
		out.Auxiliary["eigenvectors"] = in.Rect(cols, eig.Vectors.At)
	}

	stats, statWarns := pcaSphericity(in, kmoInput)
	out.Warnings = append(out.Warnings, statWarns...)

	if in.WantVectors() {
		total := 0.0
		for _, v := range lam {
			total += v
		}
		explained := make([]float64, p)
		cumulative := make([]float64, p)
		communalities := make([]float64, p)
		run := 0.0
		for c := 0; c < p; c++ {
			run += lam[c]
			explained[c] = lam[c] / total
			cumulative[c] = run / total
		}
		for i := 0; i < p; i++ {
			h := 0.0
			for c := 0; c < k; c++ {
				l := loading(i, c)
				h += float64(l * l)
			}
			communalities[i] = h
		}
		out.Vectors = map[string]any{
			"eigenvalues":        append([]float64(nil), lam...),
			"explained_variance": explained,
			"cumulative":         cumulative,
			"communalities":      communalities,
			"kmo_msa":            stats.msa,
		}
	}
	if in.WantScalars() {
		out.Scalars = map[string]float64{
			"kmo":                 stats.kmo,
			"bartlett_chisq":      stats.chisq,
			"bartlett_df":         stats.df,
			"bartlett_p":          stats.p,
			"components_retained": float64(k),
		}
	}
	return out, nil
}

// pcaRetained is the number of leading components rule keeps over the
// descending eigenvalues lam.
func pcaRetained(rule vectors.PCAComponents, lam []float64) int {
	p := len(lam)
	switch rule.Rule {
	case vectors.PCAComponentsFixed:
		return min(rule.K, p)
	case vectors.PCAComponentsVariance:
		total := 0.0
		for _, v := range lam {
			total += v
		}
		run := 0.0
		for c, v := range lam {
			run += v
			if run/total >= rule.Share {
				return c + 1
			}
		}
		return p
	}
	k := 0
	for _, v := range lam {
		if v > 1 {
			k++
		}
	}
	return k
}

// pcaColumns are the rectangular column keys "PC1" … "PCk".
func pcaColumns(k int) []string {
	out := make([]string, k)
	for c := range out {
		out[c] = "PC" + strconv.Itoa(c+1)
	}
	return out
}

// pcaStats are KMO and Bartlett's test over the analysed correlation.
type pcaStats struct {
	kmo, chisq, df, p float64
	msa               []float64
}

// pcaSphericity computes KMO (overall and per member) and Bartlett's
// sphericity test over the correlation r, with the warnings that
// explain an N* choice or a null: PULSE_MATRIX_PAIRWISE_N_STAR on a
// pairwise slot, PULSE_MATRIX_SINGULAR (a warning here) when r cannot
// be inverted, PULSE_MATRIX_INSUFFICIENT_N (scope "bartlett") when N*
// leaves Bartlett's multiplier non-positive.
func pcaSphericity(in *matrixFinalizeInput, r *linalg.Sym) (pcaStats, []*types.ResponseWarning) {
	plan := in.Plan()
	p := r.N()
	st := pcaStats{kmo: math.NaN(), chisq: math.NaN(), p: math.NaN(), df: float64(p * (p - 1) / 2), msa: nanVector(p)}
	var warns []*types.ResponseWarning
	nStar, pw := pcaNStar(in)
	if pw != nil {
		warns = append(warns, pw)
	}

	prec, err := linalg.InverseSPD(r)
	if err != nil {
		details := map[string]any{"matrix": plan.Name, "outputs": []string{"kmo", "kmo_msa", "bartlett_chisq", "bartlett_p"}}
		var ce *errors.CodedError
		if stderrors.As(singularMatrixError(err, plan.Name, in.Members()), &ce) {
			for _, key := range []string{"rank", "condition_number", "dependent_fields"} {
				if v, ok := ce.Details[key]; ok {
					details[key] = v
				}
			}
		}
		warns = append(warns, matrixWarning(errors.PULSE_MATRIX_SINGULAR,
			"matrix "+plan.Name+": the correlation is singular (a member is a linear combination of others), so it has no inverse and no determinant above 0: KMO, the per-member MSA and Bartlett's test are null; the components are still defined (a zero eigenvalue marks the dependency)",
			details))
		return st, warns
	}
	// Anti-image correlations a_ij = P_ij / √(P_ii·P_jj) (psych's
	// cov2cor(solve(r))); the sign is irrelevant (squared).
	sumR, sumA := 0.0, 0.0
	for i := 0; i < p; i++ {
		ri, ai := 0.0, 0.0
		for j := 0; j < p; j++ {
			if j == i {
				continue
			}
			rv := r.At(i, j)
			av := prec.At(i, j) / math.Sqrt(float64(prec.At(i, i)*prec.At(j, j)))
			ri += float64(rv * rv)
			ai += float64(av * av)
		}
		st.msa[i] = ri / (ri + ai)
		sumR += ri
		sumA += ai
	}
	st.kmo = sumR / (sumR + sumA)

	mult := nStar - 1 - float64(2*p+5)/6
	if !(mult > 0) {
		warns = append(warns, matrixWarning(errors.PULSE_MATRIX_INSUFFICIENT_N,
			"matrix "+plan.Name+": Bartlett's test needs an inference size N* above 1 + (2p + 5)/6 = "+strconv.FormatFloat(1+float64(2*p+5)/6, 'g', 6, 64)+
				" for "+strconv.Itoa(p)+" members; N* is "+strconv.FormatFloat(nStar, 'g', 6, 64)+", so bartlett_chisq and bartlett_p are null",
			map[string]any{"matrix": plan.Name, "scope": "bartlett", "n_star": nStar, "min_n_star": 1 + float64(2*p+5)/6}))
		return st, warns
	}
	det := determinantSPD(r)
	st.chisq = -math.Log(det) * mult
	st.p = chiSquareSurvival(st.chisq, st.df)
	return st, warns
}

// pcaNStar is Bartlett's inference size (weighting.md "Weighted
// inference"): listwise N (unweighted), Σw (frequency) or Kish n_eff
// (probability); pairwise the smallest pair's — min over i ≤ j of the
// pair's Σw scaled by N*/Σw (so PairN unweighted, pair Σw under
// frequency, pair Σw·n_eff/Σw under probability) — with the
// PULSE_MATRIX_PAIRWISE_N_STAR warning naming it.
func pcaNStar(in *matrixFinalizeInput) (float64, *types.ResponseWarning) {
	cm := in.CM
	w := in.slot.weight
	scale := 1.0
	var full float64
	switch {
	case w == nil:
		full = float64(cm.N())
	case w.EffectiveKind() == types.WeightKindProbability:
		full = cm.NEff()
		if cm.W() > 0 {
			scale = cm.NEff() / cm.W()
		}
	default:
		full = cm.W()
	}
	plan := in.Plan()
	if !plan.Pairwise {
		return full, nil
	}
	members := in.Members()
	best := math.Inf(1)
	bi, bj := 0, 0
	for i := range members {
		for j := i; j < len(members); j++ {
			v := float64(cm.PairN(i, j))
			if w != nil {
				v = float64(cm.PairW(i, j) * scale)
			}
			if v < best {
				best, bi, bj = v, i, j
			}
		}
	}
	return best, matrixWarning(errors.PULSE_MATRIX_PAIRWISE_N_STAR,
		"matrix "+plan.Name+": under pairwise deletion each correlation rests on its own rows, so Bartlett's test uses the smallest pair's inference size ("+strconv.FormatFloat(best, 'g', 6, 64)+
			", pair "+members[bi]+" × "+members[bj]+"), the conservative choice; listwise deletion gives one N for every cell",
		map[string]any{"matrix": plan.Name, "outputs": []string{"bartlett_chisq", "bartlett_p"}, "n_star": best, "row": members[bi], "col": members[bj]})
}

// covToCorr scales the symmetric s by its own diagonal (cov2cor).
func covToCorr(s *linalg.Sym) *linalg.Sym {
	n := s.N()
	out, _ := linalg.NewSym(n, nil)
	for i := 0; i < n; i++ {
		out.Set(i, i, 1)
		for j := i + 1; j < n; j++ {
			out.Set(i, j, s.At(i, j)/math.Sqrt(float64(s.At(i, i)*s.At(j, j))))
		}
	}
	return out
}

// nanVector is a length-p vector of NaN.
func nanVector(p int) []float64 {
	out := make([]float64, p)
	for i := range out {
		out[i] = math.NaN()
	}
	return out
}

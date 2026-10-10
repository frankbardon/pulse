package processing

import (
	stderrors "errors"
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_reliability.go is MAT_RELIABILITY: a battery's scale
// reliability off the slot's merged co-moments (the items as stored,
// reverse-keyed items already flipped x' = scale_min + scale_max − x in
// UpdateRow, so the operator is streamable and mergeable like
// MAT_CORRELATION).
//
// Let C be the items' covariance and R their correlation (CoMoment.Corr:
// listwise, or pairwise with each pair over its own rows), p items:
//
//   - alpha = p/(p−1)·(1 − tr C / ΣC) (psych::alpha raw_alpha);
//   - mean_inter_item_r r̄ = the mean off-diagonal R;
//   - alpha_standardized = p·r̄ / (1 + (p−1)·r̄) (psych std.alpha);
//   - item_total_r_i = Σ_{j≠i} C_ij / √(C_ii · Σ_{j,k≠i} C_jk), the
//     CORRECTED item-total r (the item against the sum of the others,
//     psych r.drop);
//   - alpha_if_deleted_i = alpha over the other p−1 items (null at
//     p = 2, where one item is left);
//   - item_mean = CoMoment.Mean; item_sd = √C_ii;
//   - omega = (Σλ)² / ((Σλ)² + Σψ), McDonald's omega_total on the
//     one-factor minres fit of R (minresOneFactor, matrix_minres.go):
//     the model-implied total variance in the denominator (the PRD's
//     formula). psych::omega's omega.tot uses the observed total ΣR
//     instead; the two agree on a battery the one factor fits and part
//     on one it does not (the reversed_items oracle case left unkeyed:
//     0.21 against −0.03), where alpha already reads low.
//
// C is the kind's sample covariance: M2/(Σw − 1) unweighted and under
// frequency weights (the rep() expansion), M2/(Σw − Σw²/Σw) under
// probability weights (cov.wt "unbiased" on normalised weights — the
// slot-wide Kish factor n_eff/(n_eff − 1) on the population form, also
// on a pairwise slot). Alpha, its item forms and r̄ are scale-free, so
// only item_sd reads the denominator.
//
// omega is null with a warning (never a quiet number):
//   - p = 2: one factor is not identified (PULSE_MATRIX_NOT_IDENTIFIED);
//   - a pairwise R that is not positive semidefinite without
//     params.repair "nearest": the shared guard's PULSE_MATRIX_NOT_PSD
//     refusal is turned into a warning, since alpha needs no PSD input;
//     under the repair omega fits the nearest correlation matrix, with
//     the guard's repair warning;
//   - a Heywood fit (some ψ ≤ 0): PULSE_MATRIX_HEYWOOD. psych bounds ψ
//     at 0.005 and stops on an optimiser-path value there; Pulse's
//     unconstrained fit differs, so neither is reported.
//
// A fit stopped at its sweep cap keeps omega (the last iterate) beside
// PULSE_MATRIX_NOT_CONVERGED. Any undefined input figure (a zero-spread
// item, too few rows) nulls the figures it reaches, the co-moment
// warnings saying why. Components carry the fit's iterations and
// converged whenever the fit ran.
func finalizeReliability(in *matrixFinalizeInput) (matrixOutput, error) {
	m := in.slot
	plan := in.Plan()
	cm := in.CM
	r := cm.Corr()
	c := reliabilityCov(cm, m.weight)
	p := r.N()
	all := make([]int, p)
	for i := range all {
		all[i] = i
	}

	out := matrixOutput{Primary: in.Square(r.At), Warnings: m.warnings(cm, r)}
	if plan.Pairwise && in.WantAuxiliary() {
		out.Auxiliary = map[string]*types.MatrixValues{
			"n": in.Square(func(i, j int) float64 { return float64(cm.PairN(i, j)) }),
		}
	}

	omega, warns, fit, err := reliabilityOmega(in, r)
	if err != nil {
		return matrixOutput{}, err
	}
	out.Warnings = append(out.Warnings, warns...)
	if fit != nil {
		out.Operator = map[string]any{"iterations": fit.Iterations, "converged": fit.Converged}
	}

	if in.WantScalars() {
		rbar := meanOffDiagonal(r)
		out.Scalars = map[string]float64{
			"alpha":              cronbachAlpha(c, all),
			"alpha_standardized": standardizedAlpha(p, rbar),
			"mean_inter_item_r":  rbar,
			"omega":              omega,
		}
	}
	if in.WantVectors() {
		means := cm.Mean()
		itemTotal := make([]float64, p)
		ifDeleted := make([]float64, p)
		mean := make([]float64, p)
		sd := make([]float64, p)
		for i := 0; i < p; i++ {
			itemTotal[i] = correctedItemTotal(c, i)
			ifDeleted[i] = math.NaN()
			if p > 2 {
				ifDeleted[i] = cronbachAlpha(c, without(all, i))
			}
			mean[i] = means.At(i)
			sd[i] = math.Sqrt(c.At(i, i))
		}
		out.Vectors = map[string]any{
			"item_total_r":     itemTotal,
			"alpha_if_deleted": ifDeleted,
			"item_mean":        mean,
			"item_sd":          sd,
		}
	}
	return out, nil
}

// reliabilityOmega is MAT_RELIABILITY's omega over the inter-item
// correlation r, with the warnings that explain a null and the fit
// (nil when none ran). Only a non-NOT_PSD guard error is returned.
func reliabilityOmega(in *matrixFinalizeInput, r *linalg.Sym) (float64, []*types.ResponseWarning, *minresFit, error) {
	plan := in.Plan()
	members := in.Members()
	p := r.N()
	if p < minresMinMembers {
		return math.NaN(), []*types.ResponseWarning{matrixWarning(errors.PULSE_MATRIX_NOT_IDENTIFIED,
			"matrix "+plan.Name+": omega needs a one-factor fit, which needs at least 3 items (2 items give one correlation for two loadings), so omega is null; alpha and the item statistics are unaffected",
			map[string]any{"matrix": plan.Name, "output": "omega", "members": append([]string(nil), members...), "min_members": minresMinMembers})}, nil, nil
	}
	if !judgeable(r) {
		// An undefined correlation (zero spread, a thin pair, no rows):
		// the co-moment warnings already say why omega is null.
		return math.NaN(), nil, nil, nil
	}
	s, warns, err := in.guardPSD(r, nil)
	if err != nil {
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_NOT_PSD {
			return 0, nil, nil, err
		}
		details := make(map[string]any, len(ce.Details)+1)
		for k, v := range ce.Details {
			details[k] = v
		}
		details["output"] = "omega"
		return math.NaN(), []*types.ResponseWarning{matrixWarning(errors.PULSE_MATRIX_NOT_PSD,
			"matrix "+plan.Name+": the pairwise inter-item correlation is not positive semidefinite (reference Cholesky fails at member "+pivotMember(ce.Details)+"), so no factor can be fitted and omega is null; alpha and the item statistics do not need it and are reported; set params.repair \"nearest\" to fit omega on the nearest correlation matrix",
			details)}, nil, nil
	}
	fit, err := minresOneFactor(s)
	if err != nil {
		return 0, nil, nil, err
	}
	warns = append(warns, minresWarnings(plan.Name, fit)...)
	if len(fit.Heywood) > 0 {
		names := make([]string, len(fit.Heywood))
		psi := make([]float64, len(fit.Heywood))
		for k, i := range fit.Heywood {
			names[k] = members[i]
			psi[k] = fit.Uniquenesses[i]
		}
		warns = append(warns, matrixWarning(errors.PULSE_MATRIX_HEYWOOD,
			"matrix "+plan.Name+": the one-factor fit behind omega is a Heywood case (uniqueness at or below 0 for "+joinNames(names)+"), so omega is null; alpha and the item statistics do not use the fit and are reported",
			map[string]any{"matrix": plan.Name, "output": "omega", "members": names, "uniquenesses": psi}))
		return math.NaN(), warns, &fit, nil
	}
	sl, sp := 0.0, 0.0
	for i := range fit.Loadings {
		sl += fit.Loadings[i]
		sp += fit.Uniquenesses[i]
	}
	sl2 := float64(sl * sl)
	return sl2 / (sl2 + sp), warns, &fit, nil
}

// minresMinMembers is the fewest members a one-factor fit is identified
// on.
const minresMinMembers = 3

// reliabilityCov is the items' sample covariance under the slot's
// weight kind: M2/(Σw − 1) unweighted or frequency-weighted; under
// probability weights M2/Σw times n_eff/(n_eff − 1) (= M2/(Σw −
// Σw²/Σw), cov.wt "unbiased"), NaN when n_eff ≤ 1.
func reliabilityCov(cm *linalg.CoMoment, w *types.WeightSpec) *linalg.Sym {
	if w == nil || w.EffectiveKind() != types.WeightKindProbability {
		return cm.Cov(1)
	}
	c := cm.Cov(0)
	f := math.NaN()
	if neff := cm.NEff(); neff > 1 {
		f = neff / (neff - 1)
	}
	for i := 0; i < c.N(); i++ {
		for j := i; j < c.N(); j++ {
			c.Set(i, j, float64(c.At(i, j)*f))
		}
	}
	return c
}

// cronbachAlpha is alpha over the items idx of covariance c:
// q/(q−1)·(1 − Σ_i c_ii / Σ_ij c_ij), q = len(idx) ≥ 2.
func cronbachAlpha(c *linalg.Sym, idx []int) float64 {
	q := float64(len(idx))
	tr, sum := 0.0, 0.0
	for _, i := range idx {
		tr += c.At(i, i)
		for _, j := range idx {
			sum += c.At(i, j)
		}
	}
	return float64(q/(q-1)) * (1 - tr/sum)
}

// standardizedAlpha is p·r̄ / (1 + (p−1)·r̄).
func standardizedAlpha(p int, rbar float64) float64 {
	fp := float64(p)
	return float64(fp*rbar) / (1 + float64((fp-1)*rbar))
}

// meanOffDiagonal is the mean of r's off-diagonal cells (NaN when any
// is undefined).
func meanOffDiagonal(r *linalg.Sym) float64 {
	p := r.N()
	sum := 0.0
	for i := 0; i < p; i++ {
		for j := i + 1; j < p; j++ {
			sum += r.At(i, j)
		}
	}
	return sum / float64(p*(p-1)/2)
}

// correctedItemTotal is item i's correlation with the sum of the other
// items: Σ_{j≠i} c_ij / √(c_ii · Σ_{j,k≠i} c_jk).
func correctedItemTotal(c *linalg.Sym, i int) float64 {
	p := c.N()
	cross, rest := 0.0, 0.0
	for j := 0; j < p; j++ {
		if j == i {
			continue
		}
		cross += c.At(i, j)
		for k := 0; k < p; k++ {
			if k != i {
				rest += c.At(j, k)
			}
		}
	}
	return cross / math.Sqrt(float64(c.At(i, i)*rest))
}

// without returns idx minus position k.
func without(idx []int, k int) []int {
	out := make([]int, 0, len(idx)-1)
	out = append(out, idx[:k]...)
	return append(out, idx[k+1:]...)
}

// joinNames joins field names with ", ".
func joinNames(names []string) string {
	s := ""
	for i, n := range names {
		if i > 0 {
			s += ", "
		}
		s += n
	}
	return s
}

// pivotMember is a NOT_PSD refusal's details "member" (the field the
// reference Cholesky failed at).
func pivotMember(details map[string]any) string {
	s, _ := details["member"].(string)
	return s
}

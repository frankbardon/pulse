package processing

import (
	"math"

	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg"
)

// sphericity.go is the one Bartlett sphericity core (psych::
// cortest.bartlett), shared by MAT_PCA's bartlett_* scalars and
// TEST_BARTLETT_SPHERICITY, and keyed on what both hold: a merged
// linalg.CoMoment plus the weight basis it was folded under. Neither
// caller re-derives the statistic, so the two agree bit for bit on the
// same co-moment.

// sphericityNStar is Bartlett's listwise inference size off cm under
// basis (weighting.md "Weighted inference"): N unweighted, Σw under
// frequency weights, Kish n_eff under probability weights. scale is
// N*/Σw — 1 except under probability weights (0 rows: 1) — the factor
// a pairwise caller applies to a pair's Σw (sphericityPairNStar).
func sphericityNStar(cm *linalg.CoMoment, basis weighting.Basis) (nStar, scale float64) {
	switch basis {
	case weighting.Unweighted:
		return float64(cm.N()), 1
	case weighting.Probability:
		scale = 1.0
		if cm.W() > 0 {
			scale = cm.NEff() / cm.W()
		}
		return cm.NEff(), scale
	}
	return cm.W(), 1
}

// sphericity is Bartlett's test of H0: R = I over a p × p correlation.
type sphericity struct {
	// ChiSq = −ln|R|·(N* − 1 − (2p + 5)/6) on DF = p(p − 1)/2, PValue its
	// upper tail; ChiSq and PValue are NaN when Singular or Insufficient.
	ChiSq, DF, PValue float64
	// Det is |R| (the reference Cholesky); NaN when not computed.
	Det float64
	// NStar is the inference size the statistic used; MinNStar the
	// bound it must exceed, 1 + (2p + 5)/6.
	NStar, MinNStar float64
	// Prec is R⁻¹ (linalg.InverseSPD), nil when Singular — MAT_PCA's
	// KMO reads it.
	Prec *linalg.Sym
	// Singular: R has no inverse (a member is a linear combination of
	// others); SingularErr is InverseSPD's coded error.
	Singular    bool
	SingularErr error
	// Insufficient: N* ≤ MinNStar leaves the multiplier non-positive.
	Insufficient bool
}

// bartlettSphericity is the shared Bartlett core over the correlation r
// at inference size nStar. The singular DECISION is linalg.InverseSPD's
// (the reference Cholesky), so a caller that also needs R⁻¹ reads Prec
// rather than inverting twice; the statistic is
// χ² = −ln|R|·(N* − 1 − (2p + 5)/6), |R| = determinantSPD(r).
func bartlettSphericity(r *linalg.Sym, nStar float64) sphericity {
	p := r.N()
	st := sphericity{
		ChiSq: math.NaN(), PValue: math.NaN(), Det: math.NaN(),
		DF: float64(p * (p - 1) / 2), NStar: nStar, MinNStar: 1 + float64(2*p+5)/6,
	}
	prec, err := linalg.InverseSPD(r)
	if err != nil {
		st.Singular, st.SingularErr = true, err
		return st
	}
	st.Prec = prec
	mult := nStar - 1 - float64(2*p+5)/6
	if !(mult > 0) {
		st.Insufficient = true
		return st
	}
	st.Det = determinantSPD(r)
	st.ChiSq = -math.Log(st.Det) * mult
	st.PValue = chiSquareSurvival(st.ChiSq, st.DF)
	return st
}

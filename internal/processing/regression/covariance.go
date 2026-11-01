package regression

import (
	"math"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/internal/regcov"
	"github.com/frankbardon/pulse/types"
)

// covariance.go is the robust coefficient covariance engine behind
// RegressionSpec.Covariance (hc0–hc3, cluster, hac). It is engine-free:
// a fit hands it its bread (the inverse information on the w* scale, in
// whatever coordinates the fit uses), one row per fitted record — the
// record's unit-weight score g, its w*, its leverage and its cluster /
// order keys — and N*; it returns the k×k sandwich in the same
// coordinates. REG_OLS reaches it through olsSandwich (ols_covariance.go);
// another engine slots in by supplying its own scores and bread.
//
// Weights follow the one rule (.claude/reference/weighting.md): every
// weighted formula is the frequency formula on w* = w·N*/Σw, so under
// kind frequency each estimator equals the unweighted one on the rep()-
// expanded rows:
//
//   - hc0–hc3: meat Σ ω·w*·g·gᵀ, ω = 1, 1, 1/(1−h), 1/(1−h)²; hc1
//     scales by N*/(N* − k);
//   - cluster (CR1): meat Σ_g s_g·s_gᵀ with s_g = Σ_{i∈g} w*·g_i, scaled
//     G/(G − 1)·(N* − 1)/(N* − k), inference on G − 1 df;
//   - hac (Newey–West, Bartlett kernel): each row is one period with
//     score u_t = w*·g_t (a period's copies share one residual, so they
//     add before the product); meat Γ₀ + Σ_{l=1..L} (1 − l/(L+1))·(Γ_l +
//     Γ_lᵀ), Γ_l = Σ_t u_t·u_{t−l}ᵀ over the rows in order_by order,
//     scaled N*/(N* − k) — R's sandwich::NeweyWest(prewhite = FALSE,
//     adjust = TRUE) unweighted.

// sandwichRow is one fitted row's input to the sandwich.
type sandwichRow struct {
	// score is g = e·z: the unit-weight score in the bread's coordinates.
	score []float64
	// weight is w* (exactly 1 unweighted).
	weight float64
	// lev is the leverage h = w-free zᵀ·bread·z (read by hc2 / hc3).
	lev float64
	// cluster is the row's cluster key (type cluster).
	cluster string
	// order is the row's order_by value (type hac).
	order float64
}

// sandwichFit is the robust covariance and what the result echoes.
type sandwichFit struct {
	// v is the k×k covariance in the bread's coordinates, exactly
	// symmetric.
	v [][]float64
	// df is the inference degrees of freedom the estimator imposes
	// (G − 1 under cluster); hasDF is false when the fit keeps its own.
	df    float64
	hasDF bool
	// info is the RegressionResult.Covariance echo.
	info *types.RegressionCovarianceResult
}

// sandwich builds spec's robust covariance from bread (k×k, symmetric)
// and the fitted rows. nStar is the fit's inference sample size N*.
// Refuses a cluster fit on fewer than two clusters and a hac fit whose
// order_by repeats (regcov.TooFewClusters / regcov.OrderTies).
func sandwich(spec *types.RegressionSpec, bread [][]float64, rows []sandwichRow, nStar float64) (*sandwichFit, error) {
	c := spec.Covariance
	k := len(bread)
	kf := float64(k)
	fit := &sandwichFit{info: &types.RegressionCovarianceResult{Type: c.Type}}
	var meat [][]float64
	adj := 1.0
	switch c.Type {
	case types.CovarianceHC0, types.CovarianceHC1, types.CovarianceHC2, types.CovarianceHC3:
		meat = newSquare(k)
		for _, r := range rows {
			addOuter(meat, r.score, r.score, hcOmega(c.Type, r.lev)*r.weight)
		}
		if c.Type == types.CovarianceHC1 {
			adj = nStar / (nStar - kf)
		}
	case types.CovarianceCluster:
		sums := map[string][]float64{}
		for _, r := range rows {
			s, ok := sums[r.cluster]
			if !ok {
				s = make([]float64, k)
				sums[r.cluster] = s
			}
			for j := range s {
				s[j] += float64(r.weight * r.score[j])
			}
		}
		g := len(sums)
		if g < 2 {
			return nil, regcov.TooFewClusters(spec, g)
		}
		// Sorted keys: the meat's summation order is a function of the
		// clusters, never of map iteration.
		keys := make([]string, 0, g)
		for key := range sums {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		meat = newSquare(k)
		for _, key := range keys {
			addOuter(meat, sums[key], sums[key], 1)
		}
		gf := float64(g)
		adj = gf / (gf - 1) * ((nStar - 1) / (nStar - kf))
		fit.df, fit.hasDF = gf-1, true
		fit.info.Cluster, fit.info.Clusters = c.Cluster, g
	case types.CovarianceHAC:
		ordered := append([]sandwichRow(nil), rows...)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })
		for t := 1; t < len(ordered); t++ {
			if ordered[t].order == ordered[t-1].order {
				return nil, regcov.OrderTies(spec, ordered[t].order)
			}
		}
		lags, fixed := c.Lags.Value()
		if !fixed {
			lags = types.AutoHACLags(len(ordered))
		}
		u := make([][]float64, len(ordered))
		for t, r := range ordered {
			u[t] = make([]float64, k)
			for j := range u[t] {
				u[t][j] = r.weight * r.score[j]
			}
		}
		meat = newSquare(k)
		for t := range u {
			addOuter(meat, u[t], u[t], 1)
		}
		for l := 1; l <= lags && l < len(u); l++ {
			bw := 1 - float64(l)/float64(lags+1)
			for t := l; t < len(u); t++ {
				addOuter(meat, u[t], u[t-l], bw)
				addOuter(meat, u[t-l], u[t], bw)
			}
		}
		adj = nStar / (nStar - kf)
		fit.info.OrderBy, fit.info.Lags = c.OrderBy, &lags
	default:
		// Unreachable: regcov.Check admits only the types above as
		// robust.
		return nil, regcov.Unsupported(spec, regcov.ReasonType, nil, "covariance type "+strconv.Quote(string(c.Type))+" has no sandwich")
	}
	fit.v = sandwichProduct(bread, meat, adj)
	return fit, nil
}

// hcOmega is the hc-type weight on a row's squared score: 1 for hc0 /
// hc1, 1/(1−h) for hc2, 1/(1−h)² for hc3. A leverage of 1 (or above,
// by rounding) leaves the row's residual undefined: NaN, so the
// covariance reads null rather than ±Inf.
func hcOmega(t types.CovarianceType, h float64) float64 {
	switch t {
	case types.CovarianceHC2, types.CovarianceHC3:
		d := 1 - h
		if !(d > 0) {
			return math.NaN()
		}
		if t == types.CovarianceHC2 {
			return 1 / d
		}
		return 1 / (d * d)
	}
	return 1
}

func newSquare(k int) [][]float64 {
	m := make([][]float64, k)
	for i := range m {
		m[i] = make([]float64, k)
	}
	return m
}

// addOuter adds s·a·bᵀ into m.
func addOuter(m [][]float64, a, b []float64, s float64) {
	for i := range m {
		sa := s * a[i]
		for j := range m[i] {
			m[i][j] += float64(sa * b[j])
		}
	}
}

// sandwichProduct is adj·B·M·B, computed on the upper triangle and
// mirrored so the result is exactly symmetric.
func sandwichProduct(b, m [][]float64, adj float64) [][]float64 {
	k := len(b)
	mb := newSquare(k)
	for i := 0; i < k; i++ {
		for j := 0; j < k; j++ {
			s := 0.0
			for l := 0; l < k; l++ {
				s += float64(m[i][l] * b[l][j])
			}
			mb[i][j] = s
		}
	}
	v := newSquare(k)
	for i := 0; i < k; i++ {
		for j := i; j < k; j++ {
			s := 0.0
			for l := 0; l < k; l++ {
				s += float64(b[i][l] * mb[l][j])
			}
			s *= adj
			v[i][j], v[j][i] = s, s
		}
	}
	return v
}

// robustStdErrors clamps v's diagonal at 0 in place and returns its
// square roots: the diagonal IS the variance the standard errors read,
// so √diag(Vcov) == StdErrors bit for bit (sqrt passes NaN through).
// REG_GLM and REG_BAYES_LINEAR use it on their raw-coordinate sandwich;
// REG_OLS clamps after its back-transform (olsSandwich).
func robustStdErrors(v [][]float64) []float64 {
	se := make([]float64, len(v))
	for j := range v {
		if v[j][j] < 0 {
			v[j][j] = 0
		}
		se[j] = sqrt(v[j][j])
	}
	return se
}

// robustRowKey is one fitted row's covariance keys, collected beside the
// design by the buffered engines (REG_GLM's buildWeightedDesign,
// REG_BAYES_LINEAR's second pass).
type robustRowKey struct {
	cluster string
	order   float64
}

package regression

import (
	"math"
	"slices"
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
//     adjust = TRUE) unweighted;
//   - hac + entity (per-entity Newey–West): the periods are the distinct
//     order_by values over every fitted row, ranked 0..T−1; each entity
//     is its own series and Γ_l = Σ_entities Σ u_{e,t}·u_{e,t−l}ᵀ pairs
//     an entity's rows exactly l periods apart (a period the entity
//     lacks leaves no pair), the same Bartlett weights and scale, L
//     auto over T — sandwich::vcovPL(aggregate = FALSE) / plm::vcovNW;
//   - driscoll_kraay: S_t = Σ_{i in period t} w*·g_i summed across
//     entities, then the hac meat over the T periods S_0..S_{T−1} in
//     order, L auto over T, scaled N*/(N* − k) —
//     sandwich::vcovPL(aggregate = TRUE) / plm::vcovSCC.
//
// Under an entity (hac or driscoll_kraay) an entity repeating an
// order_by value is refused (regcov.EntityOrderTies).

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
	// order is the row's order_by value (types hac, driscoll_kraay).
	order float64
	// entity is the row's panel entity key (covariance.entity).
	entity string
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
	case types.CovarianceHAC, types.CovarianceDriscollKraay:
		if c.Type == types.CovarianceDriscollKraay || c.Entity != "" {
			meat, err := panelMeat(spec, rows, k, fit.info)
			if err != nil {
				return nil, err
			}
			fit.v = sandwichProduct(bread, meat, nStar/(nStar-kf))
			return fit, nil
		}
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

// panelMeat is the meat of a panel series estimator — hac with an
// entity (per-entity Newey–West) or driscoll_kraay — and fills info's
// echo (order_by, the resolved lag, the period count, the entity and
// its level count). The periods are the distinct order_by values over
// every row, ranked in order; a lag counts periods. Refuses an entity
// repeating an order_by value.
func panelMeat(spec *types.RegressionSpec, rows []sandwichRow, k int, info *types.RegressionCovarianceResult) ([][]float64, error) {
	c := spec.Covariance
	// Every row in (entity, order) order: the entity series are
	// contiguous runs, the ties adjacent, and the summation order is a
	// function of the data, never of the row order.
	ordered := append([]sandwichRow(nil), rows...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].entity != ordered[j].entity {
			return ordered[i].entity < ordered[j].entity
		}
		return ordered[i].order < ordered[j].order
	})
	entities := 0
	for t := range ordered {
		if t == 0 || ordered[t].entity != ordered[t-1].entity {
			entities++
			continue
		}
		if c.Entity != "" && ordered[t].order == ordered[t-1].order {
			return nil, regcov.EntityOrderTies(spec, entityLabel(ordered[t].entity), ordered[t].order)
		}
	}
	// The periods: distinct order values ascending, and each row's rank.
	values := make([]float64, len(ordered))
	for i, r := range ordered {
		values[i] = r.order
	}
	sort.Float64s(values)
	values = slices.Compact(values)
	periods := len(values)
	rank := func(v float64) int { return sort.SearchFloat64s(values, v) }

	lags, fixed := c.Lags.Value()
	if !fixed {
		lags = types.AutoHACLags(periods)
	}
	bartlett := func(l int) float64 { return 1 - float64(l)/float64(lags+1) }
	score := func(r sandwichRow) []float64 {
		u := make([]float64, k)
		for j := range u {
			u[j] = r.weight * r.score[j]
		}
		return u
	}

	meat := newSquare(k)
	if c.Type == types.CovarianceDriscollKraay {
		// S_t: the period's scores summed across entities, in the
		// (entity, order) order fixed above.
		s := make([][]float64, periods)
		for t := range s {
			s[t] = make([]float64, k)
		}
		for _, r := range ordered {
			st := s[rank(r.order)]
			for j := range st {
				st[j] += float64(r.weight * r.score[j])
			}
		}
		for t := range s {
			addOuter(meat, s[t], s[t], 1)
		}
		for l := 1; l <= lags && l < periods; l++ {
			bw := bartlett(l)
			for t := l; t < periods; t++ {
				addOuter(meat, s[t], s[t-l], bw)
				addOuter(meat, s[t-l], s[t], bw)
			}
		}
	} else {
		// Per-entity Newey–West: within each entity's run, pair rows
		// whose period ranks are 1..lags apart.
		for start := 0; start < len(ordered); {
			end := start + 1
			for end < len(ordered) && ordered[end].entity == ordered[start].entity {
				end++
			}
			run := ordered[start:end]
			u := make([][]float64, len(run))
			rk := make([]int, len(run))
			for i, r := range run {
				u[i], rk[i] = score(r), rank(r.order)
			}
			for b := range run {
				addOuter(meat, u[b], u[b], 1)
				for a := b - 1; a >= 0 && rk[b]-rk[a] <= lags; a-- {
					bw := bartlett(rk[b] - rk[a])
					addOuter(meat, u[b], u[a], bw)
					addOuter(meat, u[a], u[b], bw)
				}
			}
			start = end
		}
	}
	info.OrderBy, info.Lags, info.Periods = c.OrderBy, &lags, periods
	if c.Entity != "" {
		info.Entity, info.Entities = c.Entity, entities
	}
	return meat, nil
}

// entityLabel is an entity key as a refusal names it: clusterKey's
// label or number without its kind prefix.
func entityLabel(key string) string {
	if len(key) > 2 && (key[:2] == "s:" || key[:2] == "n:") {
		return key[2:]
	}
	return key
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
	entity  string
}

package regmmm

import (
	stderrors "errors"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mmmfixture"
)

// bayes_geo_model_test.go holds the geo panel's hierarchical model
// (bayes_geo_model.go) to its contract: the density is an independently
// written oracle's (per-geo intercepts, pooled values through the
// population prior's transform, the τ and η priors), the analytic
// gradient is the finite-difference one, a one-geo panel at η = 0 is
// the one-market model, the all-geo figures are the documented pools,
// and the refusals and MAP start hold.

// geoTestPanel is a three-geo panel cut from the joint fixture: geo g
// holds weeks 10·g … 10·g + 89 (ragged starts and ends), its outcome
// scaled by 1 + 0.25·g and its spend by 1 + 0.2·g.
func geoTestPanel() geoPanel {
	y, spend, controls := jointModelData()
	in := geoPanel{geos: []string{"a", "b", "c"}}
	for _, c := range mmmfixture.JointChannels {
		in.channels = append(in.channels, c.Name)
	}
	for g := range 3 {
		lo, hi := 10*g, 10*g+90
		ys := make([]float64, 0, hi-lo)
		for _, v := range y[lo:hi] {
			ys = append(ys, float64(v*(1+float64(0.25*float64(g)))))
		}
		var sp, ct [][]float64
		for _, x := range spend {
			col := make([]float64, 0, hi-lo)
			for _, v := range x[lo:hi] {
				col = append(col, float64(v*(1+float64(0.2*float64(g)))))
			}
			sp = append(sp, col)
		}
		for _, z := range controls {
			ct = append(ct, slices.Clone(z[lo:hi]))
		}
		in.y, in.spend, in.controls = append(in.y, ys), append(in.spend, sp), append(in.controls, ct)
	}
	return in
}

func geoTestModel(t *testing.T, in geoPanel, pr bayesPriors) *bayesGeoModel {
	t.Helper()
	m, err := newBayesGeoModel(in, defaultMaxLag, pr)
	if err != nil {
		t.Fatalf("newBayesGeoModel: %v", err)
	}
	return m
}

// geoRandomPoint is a θ near the default start: each coordinate moved by
// up to ±spread.
func geoRandomPoint(m *bayesGeoModel, rng *rand.Rand, spread float64) []float64 {
	theta := m.defaultInit()
	for i := range theta {
		theta[i] += float64(float64(2*spread) * (float64(rng.Float64()) - 0.5))
	}
	return theta
}

// geoPriorSets are the prior sets the density and gradient tests run:
// the defaults, the mixed set (a β channel, every family), a two-
// parameter kernel of each shape and a non-Hill curve.
func geoPriorSets() map[string]bayesPriors {
	return map[string]bayesPriors{
		"roi":            defaultBayesPriors(len(mmmfixture.JointChannels)),
		"mixed":          mixedPriors(),
		"delayed":        kernelPriorSets()["delayed"],
		"weibull_pdf":    kernelPriorSets()["weibull_pdf"],
		"logistic_mixed": curveMixedPriors("logistic"),
	}
}

// oracleGeoLift is one lift test the oracle scores: geo g, channel c,
// window rows [from, to] of the geo's series, each window period's spend
// scaled by 1 + frac, and the measured lift and se.
type oracleGeoLift struct {
	geo, channel, from, to int
	frac, lift, se         float64
}

// oracleGeoLogPosterior is the panel's posterior written out plainly
// from the model doc, with math.*: the panel-wide scaling, the per-geo
// intercepts, each pooled value T(μ + τ·η) under the population prior's
// transform (no density of its own), the τ ~ HalfNormal(geoTauSigma)
// and η ~ Normal(0, 1) priors with their Jacobians, the curves (shared,
// or each geo's own pooled values under in.perGeoCurves) and controls,
// one Normal likelihood over every geo's rows, and each lift test's
// −½((lift − Δ)/se)², Δ the geo's own window-only change.
func oracleGeoLogPosterior(in geoPanel, maxLag int, pr bayesPriors, theta []float64, lifts []oracleGeoLift) float64 {
	ng, nc, nz := len(in.y), len(in.spend[0]), len(in.controls[0])
	kernel := pr.Kernel
	if kernel == nil {
		kernel = geometricKernel{}
	}
	curve := pr.Curve
	if curve == nil {
		curve = hillCurveModel{}
	}
	np, nq := len(kernel.Params()), len(curve.Params())
	nk := 1 + np
	if in.perGeoCurves {
		nk += nq
	}
	iSigma := ng + nz + (np+nq+1)*nc
	iTau := func(k, c int) int { return iSigma + 1 + k*nc + c }
	iEta := func(k, c, g int) int { return iSigma + 1 + nk*nc + (k*nc+c)*ng + g }
	iMu := func(k, c int) int {
		switch {
		case k == 0:
			return ng + nz + (np+nq)*nc + c
		}
		// A kernel parameter's μ (k ≤ np), then a pooled curve
		// parameter's: the one-market slots, kernel then curve.
		return ng + nz + (k-1)*nc + c
	}
	lp := 0.0
	val := func(p prior, i int) float64 {
		m := oracleTransform(p, theta[i])
		lp += oracleDensity(p, m.y) + m.logJac
		return m.y
	}
	ymax, nObs := 0.0, 0
	xmax := make([]float64, nc)
	for g := range ng {
		nObs += len(in.y[g])
		for _, v := range in.y[g] {
			ymax = math.Max(ymax, math.Abs(v))
		}
		for c, x := range in.spend[g] {
			for _, v := range x {
				xmax[c] = math.Max(xmax[c], v)
			}
		}
	}
	alpha := make([]float64, ng)
	for g := range ng {
		alpha[g] = val(pr.Intercept, g)
	}
	gamma := make([]float64, nz)
	zMean, zScale := make([]float64, nz), make([]float64, nz)
	for j := range nz {
		gamma[j] = val(pr.Control, ng+j)
		for g := range ng {
			for _, v := range in.controls[g][j] {
				zMean[j] += v / float64(nObs)
			}
		}
		for g := range ng {
			for _, v := range in.controls[g][j] {
				zScale[j] = math.Max(zScale[j], math.Abs(v-zMean[j]))
			}
		}
	}
	q := make([][]float64, nc)
	for c := range nc {
		cp := pr.Channels[c]
		for i, kp := range kernel.Params() {
			val(*kp.prior(&cp), ng+nz+i*nc+c)
		}
		q[c] = make([]float64, nq)
		for i, cq := range curve.Params() {
			q[c][i] = val(*cq.prior(&cp), ng+nz+(np+i)*nc+c)
		}
		val(cp.Effect, ng+nz+(np+nq)*nc+c)
	}
	sigma := val(pr.Sigma, iSigma)
	tau := make([][]float64, nk)
	for k := range nk {
		tau[k] = make([]float64, nc)
		for c := range nc {
			tau[k][c] = val(halfNormalPrior(geoTauSigma), iTau(k, c))
			for g := range ng {
				val(normalPrior(0, 1), iEta(k, c, g))
			}
		}
	}
	muPrior := func(k, c int) prior {
		cp := pr.Channels[c]
		switch {
		case k == 0:
			return cp.Effect
		case k <= np:
			return *kernel.Params()[k-1].prior(&cp)
		}
		return *curve.Params()[k-1-np].prior(&cp)
	}
	// hOf is the curve at series x's adstock at t (kernel ad, curve qc).
	hOf := func(x []float64, t, c int, ad, qc []float64) float64 {
		a := 0.0
		for l := 0; l <= maxLag && l <= t; l++ {
			a += float64(oracleKernelWeight(kernel.Name(), ad, l) * x[t-l])
		}
		if !(a > 0) {
			return 0
		}
		a /= xmax[c]
		switch curve.Name() {
		case "hill":
			return math.Pow(a, qc[1]) / (math.Pow(a, qc[1]) + math.Pow(qc[0], qc[1]))
		case "logistic":
			return math.Tanh(qc[0] * a / 2)
		}
		return a / (qc[0] + a)
	}
	for g := range ng {
		n := len(in.y[g])
		mu := make([]float64, n)
		for t := range mu {
			mu[t] = alpha[g]
			for j := range nz {
				mu[t] += gamma[j] * (in.controls[g][j][t] - zMean[j]) / zScale[j]
			}
		}
		for c := range nc {
			geoVal := func(k int) float64 {
				return oracleTransform(muPrior(k, c), theta[iMu(k, c)]+float64(tau[k][c]*theta[iEta(k, c, g)])).y
			}
			ad := make([]float64, np)
			for i := range ad {
				ad[i] = geoVal(1 + i)
			}
			qc := q[c]
			if in.perGeoCurves {
				qc = make([]float64, nq)
				for i := range qc {
					qc[i] = geoVal(1 + np + i)
				}
			}
			e := geoVal(0)
			x := in.spend[g][c]
			total, hsum := 0.0, 0.0
			h := make([]float64, n)
			for t := range n {
				total += x[t]
				h[t] = hOf(x, t, c, ad, qc)
				hsum += h[t]
			}
			beta := e
			if !pr.Channels[c].Beta {
				beta = e * total / ymax / hsum
			}
			for t := range mu {
				mu[t] += float64(beta * h[t])
			}
			for _, l := range lifts {
				if l.geo != g || l.channel != c {
					continue
				}
				changed := slices.Clone(x)
				for t := l.from; t <= l.to; t++ {
					changed[t] = x[t] * (1 + l.frac)
				}
				d := 0.0
				for t := l.from; t <= l.to; t++ {
					d += hOf(changed, t, c, ad, qc) - h[t]
				}
				r := (l.lift - float64(ymax*beta*d)) / l.se
				lp -= float64(0.5 * r * r)
			}
		}
		for t := range mu {
			r := in.y[g][t]/ymax - mu[t]
			lp += float64(-0.5*r*r)/(sigma*sigma) - math.Log(sigma)
		}
	}
	return lp
}

// TestMMMBayes_GeoDensityMatchesOracle: the panel model's log density
// equals the oracle's at random points (both drop the likelihood's
// ½·log 2π constant), under every geoPriorSets set.
func TestMMMBayes_GeoDensityMatchesOracle(t *testing.T) {
	t.Parallel()

	in := geoTestPanel()
	for name, pr := range geoPriorSets() {
		m := geoTestModel(t, in, pr)
		rng := rand.New(rand.NewPCG(3, 3^0x9E3779B97F4A7C15))
		for pt := range 4 {
			theta := geoRandomPoint(m, rng, 1)
			got := m.LogDensityGrad(theta, make([]float64, m.Dim()))
			want := oracleGeoLogPosterior(in, defaultMaxLag, pr, theta, nil)
			if !(math.Abs(got-want) <= 1e-9*max(1, math.Abs(want))) {
				t.Errorf("%s point %d: density %.15g, oracle %.15g", name, pt, got, want)
			}
		}
	}
}

// TestMMMBayes_GeoGradientMatchesFiniteDifferences: at random points the
// analytic gradient matches a fourth-order central difference to a
// relative 1e-6 under every geoPriorSets set, so μ, τ and every η are
// chained through the pooled values right.
func TestMMMBayes_GeoGradientMatchesFiniteDifferences(t *testing.T) {
	t.Parallel()

	in := geoTestPanel()
	for name, pr := range geoPriorSets() {
		m := geoTestModel(t, in, pr)
		dim := m.Dim()
		rng := rand.New(rand.NewPCG(9, 9^0x9E3779B97F4A7C15))
		f := func(x []float64) float64 { return m.LogDensityGrad(x, make([]float64, dim)) }
		for pt := range 2 {
			theta := geoRandomPoint(m, rng, 1)
			grad := make([]float64, dim)
			if lp := m.LogDensityGrad(theta, grad); math.IsInf(lp, 0) || math.IsNaN(lp) {
				t.Fatalf("%s point %d: density %v", name, pt, lp)
			}
			for i := range dim {
				h := 1e-4
				at := func(k float64) float64 {
					x := slices.Clone(theta)
					x[i] += k * h
					return f(x)
				}
				fd := (float64(8*(at(1)-at(-1))) - (at(2) - at(-2))) / (12 * h)
				if tol := 1e-6 * max(1, math.Abs(grad[i])); math.Abs(grad[i]-fd) > tol {
					t.Errorf("%s point %d coord %d: analytic %.12g, finite difference %.12g", name, pt, i, grad[i], fd)
				}
			}
		}
	}
}

// geoOneOf is a one-geo panel of the joint fixture's full series.
func geoOneOf() geoPanel {
	y, spend, controls := jointModelData()
	in := geoPanel{geos: []string{"only"}, y: [][]float64{y}, spend: [][][]float64{spend}, controls: [][][]float64{controls}}
	for _, c := range mmmfixture.JointChannels {
		in.channels = append(in.channels, c.Name)
	}
	return in
}

// geoFromOneMarket maps a one-market θ onto the one-geo panel's θ with
// every η 0 and every τ at tauTheta: the same values.
func geoFromOneMarket(m *bayesModel, g *bayesGeoModel, theta []float64, tauTheta float64) []float64 {
	out := make([]float64, g.Dim())
	out[g.iIntercept(0)] = theta[m.iIntercept()]
	for j := range m.nz {
		out[g.iControl(j)] = theta[m.iControl(j)]
	}
	for c := range m.nc {
		for i := range m.np {
			out[g.iAdstock(i, c)] = theta[m.iAdstock(i, c)]
		}
		for i := range m.nq {
			out[g.iCurve(i, c)] = theta[m.iCurve(i, c)]
		}
		out[g.iEffect(c)] = theta[m.iEffect(c)]
		for k := range g.nk {
			out[g.iTau(k, c)] = tauTheta
		}
	}
	out[g.iSigma()] = theta[m.iSigma()]
	return out
}

// TestMMMBayes_GeoOneGeoIsOneMarket: a one-geo panel at η = 0 is the
// one-market model — its density is the one-market density plus the τ
// and η priors' own terms, its draw (coefficients and the geo's and
// all-geo ROI, marginal ROI and contribution) is the one-market draw's,
// and so is its prediction past the fitted rows.
func TestMMMBayes_GeoOneGeoIsOneMarket(t *testing.T) {
	t.Parallel()

	for name, pr := range geoPriorSets() {
		y, spend, controls := jointModelData()
		one, err := newBayesModel(y, spend, controls, defaultMaxLag, pr)
		if err != nil {
			t.Fatal(err)
		}
		geo := geoTestModel(t, geoOneOf(), pr)
		rng := rand.New(rand.NewPCG(4, 4^0x9E3779B97F4A7C15))
		theta := randomPoint(one, rng, 0.5)
		gtheta := geoFromOneMarket(one, geo, theta, -1)
		extra := 0.0
		for k := range geo.nk {
			for c := range one.nc {
				extra += geo.mapCoordinate(geo.iTau(k, c), gtheta[geo.iTau(k, c)]).lp
				extra += geo.mapCoordinate(geo.iEta(k, c, 0), 0).lp
			}
		}
		lp1 := one.LogDensityGrad(theta, make([]float64, one.Dim()))
		lpg := geo.LogDensityGrad(gtheta, make([]float64, geo.Dim()))
		if math.Abs(lpg-(lp1+extra)) > 1e-9*max(1, math.Abs(lp1)) {
			t.Errorf("%s: one-geo density %.15g, one-market %.15g + τ, η terms %.15g", name, lpg, lp1, extra)
		}
		con := make([]float64, one.Dim())
		one.constrain(theta, con)
		beta, roi, mroi := one.effects(theta)
		d := geo.draw(gtheta)
		near := func(what string, got, want float64) {
			t.Helper()
			if math.Abs(got-want) > 1e-9*max(1, math.Abs(want)) {
				t.Errorf("%s: %s %.15g, one-market %.15g", name, what, got, want)
			}
		}
		// The panel's table: (intercept)[only], then per channel its
		// population β and the geo's β, the controls, per kernel param
		// and channel the population and geo value, the curves, sigma.
		near("intercept", d.row[0], con[one.iIntercept()])
		at := 1
		for c := range one.nc {
			near("population beta", d.row[at], beta[c])
			near("geo beta", d.row[at+1], beta[c])
			at += 2
			base := c * 2
			near("all-geo roi", d.roi[base], roi[c])
			near("geo roi", d.roi[base+1], roi[c])
			near("all-geo mroi", d.mroi[base], mroi[c])
			near("geo mroi", d.mroi[base+1], mroi[c])
			near("geo contribution", d.contrib[base+1], float64(roi[c]*one.spendTotal[c]))
			near("all-geo contribution", d.contrib[base], float64(roi[c]*one.spendTotal[c]))
		}
		for j := range one.nz {
			near("control", d.row[at], con[one.iControl(j)])
			at++
		}
		for i := range one.np {
			for c := range one.nc {
				near("population kernel", d.row[at], con[one.iAdstock(i, c)])
				near("geo kernel", d.row[at+1], con[one.iAdstock(i, c)])
				at += 2
			}
		}
		for i := range one.nq {
			for c := range one.nc {
				near("curve", d.row[at], con[one.iCurve(i, c)])
				at++
			}
		}
		near("sigma", d.row[at], con[one.iSigma()])
		// The held-out prediction over an extended series is the
		// one-market model's.
		ext := make([][]float64, len(spend))
		for c, x := range spend {
			ext[c] = append(slices.Clone(x), x[:8]...)
		}
		extZ := make([][]float64, len(controls))
		for j, z := range controls {
			extZ[j] = append(slices.Clone(z), z[:8]...)
		}
		want := one.predict(theta, ext, extZ, len(y)-4)
		for i, v := range geo.predictGeo(gtheta, 0, ext, extZ, len(y)-4) {
			near("prediction", v, want[i])
		}
		if want := at + 1 + geo.nk*one.nc; len(d.row) != want {
			t.Errorf("%s: row has %d entries, want %d", name, len(d.row), want)
		}
	}
}

// TestMMMBayes_GeoDrawPools: at a random point the all-geo figures are
// the documented pools of the per-geo ones — contribution summed,
// ROI the summed contribution over the summed spend, marginal ROI
// spend-weighted — and each geo's pooled value is T(μ + τ·η).
func TestMMMBayes_GeoDrawPools(t *testing.T) {
	t.Parallel()

	m := geoTestModel(t, geoTestPanel(), mixedPriors())
	rng := rand.New(rand.NewPCG(8, 8^0x9E3779B97F4A7C15))
	theta := geoRandomPoint(m, rng, 1)
	d := m.draw(theta)
	for c := range m.shape.nc {
		base := c * (1 + m.ng)
		spend, contrib, mroi := 0.0, 0.0, 0.0
		for g := range m.ng {
			s := m.geos[g].spendTotal[c]
			spend += s
			contrib += d.contrib[base+1+g]
			mroi += float64(d.mroi[base+1+g] * s)
			if want := float64(d.roi[base+1+g] * s); math.Abs(d.contrib[base+1+g]-want) > float64(1e-9*math.Abs(want)) {
				t.Errorf("channel %d geo %d: contribution %v, roi·spend %v", c, g, d.contrib[base+1+g], want)
			}
		}
		if math.Abs(d.contrib[base]-contrib) > 1e-9*math.Abs(contrib) {
			t.Errorf("channel %d: all-geo contribution %v, sum %v", c, d.contrib[base], contrib)
		}
		if math.Abs(d.roi[base]-contrib/spend) > 1e-9*math.Abs(contrib/spend) {
			t.Errorf("channel %d: all-geo roi %v, pooled %v", c, d.roi[base], contrib/spend)
		}
		if math.Abs(d.mroi[base]-mroi/spend) > 1e-9*max(1e-12, math.Abs(mroi/spend)) {
			t.Errorf("channel %d: all-geo mroi %v, spend-weighted %v", c, d.mroi[base], mroi/spend)
		}
		// Each geo's decay: T(μ + τ·η) under the population prior.
		cp := mixedPriors().Channels[c]
		tau := oracleTransform(halfNormalPrior(geoTauSigma), theta[m.iTau(1, c)]).y
		for g := range m.ng {
			want := oracleTransform(cp.Decay, theta[m.iAdstock(0, c)]+float64(tau*theta[m.iEta(1, c, g)])).y
			got := m.geoKernel(m.pooledValues(theta, func(k, c int) float64 {
				return oracleTransform(halfNormalPrior(geoTauSigma), theta[m.iTau(k, c)]).y
			}), c, g)[0]
			if math.Abs(got-want) > 1e-12 {
				t.Errorf("channel %d geo %d: decay %v, T(μ + τη) %v", c, g, got, want)
			}
		}
	}
}

// TestMMMBayes_GeoRefusals: a panel too short for its parameters, a
// channel that never spends in one geo (its effect there is
// unidentified), a constant control, an all-zero outcome and a negative
// spend are refused with their codes, naming the geo where it is one
// geo's.
func TestMMMBayes_GeoRefusals(t *testing.T) {
	t.Parallel()

	pr := defaultBayesPriors(len(mmmfixture.JointChannels))
	mut := func(f func(*geoPanel)) geoPanel {
		in := geoTestPanel()
		f(&in)
		return in
	}
	for _, tc := range []struct {
		name string
		in   geoPanel
		code perr.Code
		geo  string
	}{
		{"short", mut(func(in *geoPanel) {
			for g := range in.y {
				in.y[g] = in.y[g][:12]
				for c := range in.spend[g] {
					in.spend[g][c] = in.spend[g][c][:12]
				}
				for j := range in.controls[g] {
					in.controls[g][j] = in.controls[g][j][:12]
				}
			}
		}), perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA, ""},
		{"never spends in a geo", mut(func(in *geoPanel) { in.spend[1][2] = make([]float64, len(in.y[1])) }), perr.PROCESSING_REGRESSION_RANK_DEFICIENT, "b"},
		{"constant control", mut(func(in *geoPanel) {
			for g := range in.controls {
				for t := range in.controls[g][0] {
					in.controls[g][0][t] = 3
				}
			}
		}), perr.PROCESSING_REGRESSION_RANK_DEFICIENT, ""},
		{"zero outcome", mut(func(in *geoPanel) {
			for g := range in.y {
				in.y[g] = make([]float64, len(in.y[g]))
			}
		}), perr.PROCESSING_REGRESSION_RANK_DEFICIENT, ""},
		{"negative spend", mut(func(in *geoPanel) { in.spend[2][0][5] = -1 }), perr.PROCESSING_CONFIG, "c"},
	} {
		_, err := newBayesGeoModel(tc.in, defaultMaxLag, pr)
		var ce *perr.CodedError
		if !perr.HasCode(err, tc.code) || !stderrors.As(err, &ce) {
			t.Errorf("%s: err %v, want %s", tc.name, err, tc.code)
			continue
		}
		if tc.geo != "" && ce.Details["geo"] != tc.geo {
			t.Errorf("%s: details %v, want geo %s", tc.name, ce.Details, tc.geo)
		}
	}
}

// TestMMMBayes_GeoMAPStart: the panel's warm start converges, beats the
// default start's density and is the same bits on a second run.
func TestMMMBayes_GeoMAPStart(t *testing.T) {
	t.Parallel()

	m := geoTestModel(t, geoTestPanel(), defaultBayesPriors(len(mmmfixture.JointChannels)))
	theta, ok := m.mapStart(mapMaxIter)
	if !ok {
		t.Fatal("MAP start fell back on the geo panel")
	}
	again, _ := m.mapStart(mapMaxIter)
	for i := range theta {
		if math.Float64bits(theta[i]) != math.Float64bits(again[i]) {
			t.Fatalf("coord %d: %v then %v", i, theta[i], again[i])
		}
	}
	scratch := make([]float64, m.Dim())
	if lpMAP, lp0 := m.LogDensityGrad(theta, scratch), m.LogDensityGrad(m.defaultInit(), scratch); !(lpMAP > lp0) {
		t.Errorf("MAP density %v not above the default start's %v", lpMAP, lp0)
	}
}

// geoTestLifts are two lift tests on geoTestPanel: a switch-off of
// channel 0 in geo 1 over rows 20–27 and a +50% test of channel 1 in geo
// 2 over rows 40–45, each with a measured lift near the model's scale.
func geoTestLifts(yScale float64) []oracleGeoLift {
	return []oracleGeoLift{
		{geo: 1, channel: 0, from: 20, to: 27, frac: -1, lift: -0.04 * yScale, se: 0.01 * yScale},
		{geo: 2, channel: 1, from: 40, to: 45, frac: 0.5, lift: 0.02 * yScale, se: 0.015 * yScale},
	}
}

// addGeoLifts resolves lifts onto m (newGeoLiftTest, the change frac of
// the geo's window spend).
func addGeoLifts(t *testing.T, m *bayesGeoModel, lifts []oracleGeoLift) {
	t.Helper()
	for _, l := range lifts {
		tot := 0.0
		for r := l.from; r <= l.to; r++ {
			tot += m.geos[l.geo].spend[l.channel][r]
		}
		lt, err := m.newGeoLiftTest(l.geo, l.channel, l.from, l.to, float64(l.frac*tot), l.lift, l.se)
		if err != nil {
			t.Fatal(err)
		}
		m.lifts = append(m.lifts, lt)
	}
}

// geoVariants are the panel variants beyond the shared-curve, lift-free
// model: per-geo curves, lift tests, and both.
var geoVariants = []struct {
	name                string
	perGeoCurves, lifts bool
}{
	{"per_geo_curves", true, false},
	{"lifts", false, true},
	{"per_geo_curves+lifts", true, true},
}

// geoVariantModel is geoTestPanel under pr in variant v, with the lift
// tests the oracle scores (nil without lifts).
func geoVariantModel(t *testing.T, pr bayesPriors, perGeoCurves, lifts bool) (geoPanel, *bayesGeoModel, []oracleGeoLift) {
	t.Helper()
	in := geoTestPanel()
	in.perGeoCurves = perGeoCurves
	m := geoTestModel(t, in, pr)
	var ls []oracleGeoLift
	if lifts {
		ls = geoTestLifts(m.shape.yScale)
		addGeoLifts(t, m, ls)
	}
	return in, m, ls
}

// TestMMMBayes_GeoVariantsDensityMatchesOracle: with per-geo curves
// (each geo's curve T(μ + τ·η), the curve's scales and η's after the
// kernel's), lift tests (each its geo's own Δ) or both, the panel's log
// density is the oracle's at random points under every geoPriorSets
// set, and per-geo curves add exactly nq·channels·(G + 1) coordinates.
func TestMMMBayes_GeoVariantsDensityMatchesOracle(t *testing.T) {
	t.Parallel()

	for _, v := range geoVariants {
		for name, pr := range geoPriorSets() {
			in, m, lifts := geoVariantModel(t, pr, v.perGeoCurves, v.lifts)
			if v.perGeoCurves {
				shared := geoTestModel(t, geoTestPanel(), pr)
				if want := shared.Dim() + m.shape.nq*m.shape.nc*(1+m.ng); m.Dim() != want {
					t.Errorf("%s %s: dim %d, want %d", v.name, name, m.Dim(), want)
				}
			}
			rng := rand.New(rand.NewPCG(5, 5^0x9E3779B97F4A7C15))
			for pt := range 2 {
				theta := geoRandomPoint(m, rng, 1)
				got := m.LogDensityGrad(theta, make([]float64, m.Dim()))
				want := oracleGeoLogPosterior(in, defaultMaxLag, pr, theta, lifts)
				if !(math.Abs(got-want) <= 1e-9*max(1, math.Abs(want))) {
					t.Errorf("%s %s point %d: density %.15g, oracle %.15g", v.name, name, pt, got, want)
				}
			}
		}
	}
}

// TestMMMBayes_GeoVariantsGradientMatchesFiniteDifferences: the analytic
// gradient matches a fourth-order central difference to a relative 1e-6
// with per-geo curves, lift tests and both — each geo's curve partials
// and each lift term's reach the geo's pooled values (and the shared
// curve without per-geo curves).
func TestMMMBayes_GeoVariantsGradientMatchesFiniteDifferences(t *testing.T) {
	t.Parallel()

	sets := geoPriorSets()
	for _, v := range geoVariants {
		for _, name := range []string{"mixed", "delayed", "logistic_mixed"} {
			t.Run(v.name+"/"+name, func(t *testing.T) {
				t.Parallel()
				_, m, _ := geoVariantModel(t, sets[name], v.perGeoCurves, v.lifts)
				dim := m.Dim()
				rng := rand.New(rand.NewPCG(19, 19^0x9E3779B97F4A7C15))
				f := func(x []float64) float64 { return m.LogDensityGrad(x, make([]float64, dim)) }
				theta := geoRandomPoint(m, rng, 1)
				grad := make([]float64, dim)
				if lp := m.LogDensityGrad(theta, grad); math.IsInf(lp, 0) || math.IsNaN(lp) {
					t.Fatalf("density %v", lp)
				}
				for i := range dim {
					h := 1e-3 // the lift terms' larger density: 1e-4 is in the roundoff
					at := func(k float64) float64 {
						x := slices.Clone(theta)
						x[i] += k * h
						return f(x)
					}
					fd := (float64(8*(at(1)-at(-1))) - (at(2) - at(-2))) / (12 * h)
					if tol := 1e-6 * max(1, math.Abs(grad[i])); math.Abs(grad[i]-fd) > tol {
						t.Errorf("coord %d: analytic %.12g, finite difference %.12g", i, grad[i], fd)
					}
				}
			})
		}
	}
}

// TestMMMBayes_GeoOneGeoLiftIsOneMarket: a one-geo panel carrying the
// one-market model's lift tests, at η = 0, is that model — its density
// the one-market density (lift terms included) plus the τ and η priors'
// own terms, with shared curves and with per-geo curves alike — so a
// panel lift test reads its geo's spend, β, kernel and curve.
func TestMMMBayes_GeoOneGeoLiftIsOneMarket(t *testing.T) {
	t.Parallel()

	for name, pr := range liftSets() {
		one := liftModel(t, pr)
		for _, perGeo := range []bool{false, true} {
			in := geoOneOf()
			in.perGeoCurves = perGeo
			geo := geoTestModel(t, in, pr)
			for _, l := range one.lifts {
				tot, moved := 0.0, 0.0
				for r := l.from; r <= l.to; r++ {
					tot += one.spend[l.channel][r]
					moved += l.spend[r]
				}
				lt, err := geo.newGeoLiftTest(0, l.channel, l.from, l.to, moved-tot, l.lift, l.se)
				if err != nil {
					t.Fatal(err)
				}
				geo.lifts = append(geo.lifts, lt)
			}
			rng := rand.New(rand.NewPCG(6, 6^0x9E3779B97F4A7C15))
			theta := randomPoint(one, rng, 0.5)
			gtheta := geoFromOneMarket(one, geo, theta, -1)
			extra := 0.0
			for k := range geo.nk {
				for c := range one.nc {
					extra += geo.mapCoordinate(geo.iTau(k, c), gtheta[geo.iTau(k, c)]).lp
					extra += geo.mapCoordinate(geo.iEta(k, c, 0), 0).lp
				}
			}
			lp1 := one.LogDensityGrad(theta, make([]float64, one.Dim()))
			lpg := geo.LogDensityGrad(gtheta, make([]float64, geo.Dim()))
			if math.Abs(lpg-(lp1+extra)) > 1e-9*max(1, math.Abs(lp1)) {
				t.Errorf("%s per_geo_curves=%v: one-geo density %.15g, one-market %.15g + τ, η terms %.15g", name, perGeo, lpg, lp1, extra)
			}
		}
	}
}

// TestMMMBayes_GeoPerCurveDraw: with per-geo curves the draw's row is the
// coefficient table's length (bayesRegression.geoCoefNames), each geo's
// curve entry is its pooled value in original units, the population
// entry μ's, and the draw's μ for a curve class is that same μ.
func TestMMMBayes_GeoPerCurveDraw(t *testing.T) {
	t.Parallel()

	_, m, _ := geoVariantModel(t, mixedPriors(), true, false)
	rng := rand.New(rand.NewPCG(7, 7^0x9E3779B97F4A7C15))
	theta := geoRandomPoint(m, rng, 1)
	d := m.draw(theta)
	s := m.shape
	if want := m.ng + (1+s.np+s.nq)*s.nc*(1+m.ng) + s.nz + 1 + m.nk*s.nc; len(d.row) != want {
		t.Fatalf("row has %d entries, want %d", len(d.row), want)
	}
	// The curve block follows the intercepts, the β's, the controls and
	// the kernel block.
	at := m.ng + s.nc*(1+m.ng) + s.nz + s.np*s.nc*(1+m.ng)
	pv := m.pooledValues(theta, func(k, c int) float64 {
		return m.priors[m.iTau(k, c)].transform().Forward(theta[m.iTau(k, c)]).Y
	})
	for i, cq := range s.sat.Params() {
		for c := range s.nc {
			mu := cq.raw(m.priors[m.iCurve(i, c)].transform().Forward(theta[m.iCurve(i, c)]).Y, s.xScale[c])
			if d.row[at] != mu || d.mu[m.kCurve(i)][c] != mu {
				t.Errorf("curve %s channel %d: population %v, draw μ %v, want %v", cq.key, c, d.row[at], d.mu[m.kCurve(i)][c], mu)
			}
			for g := range m.ng {
				if want := cq.raw(pv[m.kCurve(i)][c][g].y, s.xScale[c]); d.row[at+1+g] != want {
					t.Errorf("curve %s channel %d geo %d: %v, want %v", cq.key, c, g, d.row[at+1+g], want)
				}
			}
			at += 1 + m.ng
		}
	}
}

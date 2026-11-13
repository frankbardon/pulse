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

// oracleGeoLogPosterior is the panel's posterior written out plainly
// from the model doc, with math.*: the panel-wide scaling, the per-geo
// intercepts, each pooled value T(μ + τ·η) under the population prior's
// transform (no density of its own), the τ ~ HalfNormal(geoTauSigma)
// and η ~ Normal(0, 1) priors with their Jacobians, the shared curves
// and controls, and one Normal likelihood over every geo's rows.
func oracleGeoLogPosterior(in geoPanel, maxLag int, pr bayesPriors, theta []float64) float64 {
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
	iSigma := ng + nz + (np+nq+1)*nc
	iTau := func(k, c int) int { return iSigma + 1 + k*nc + c }
	iEta := func(k, c, g int) int { return iSigma + 1 + nk*nc + (k*nc+c)*ng + g }
	iMu := func(k, c int) int {
		if k == 0 {
			return ng + nz + (np+nq)*nc + c
		}
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
		if k == 0 {
			return cp.Effect
		}
		return *kernel.Params()[k-1].prior(&cp)
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
			e := geoVal(0)
			x := in.spend[g][c]
			total, hsum := 0.0, 0.0
			h := make([]float64, n)
			for t := range n {
				total += x[t]
				a := 0.0
				for l := 0; l <= maxLag && l <= t; l++ {
					a += float64(oracleKernelWeight(kernel.Name(), ad, l) * x[t-l])
				}
				if a > 0 {
					a /= xmax[c]
					switch curve.Name() {
					case "hill":
						h[t] = math.Pow(a, q[c][1]) / (math.Pow(a, q[c][1]) + math.Pow(q[c][0], q[c][1]))
					case "logistic":
						h[t] = math.Tanh(q[c][0] * a / 2)
					case "michaelis_menten":
						h[t] = a / (q[c][0] + a)
					}
				}
				hsum += h[t]
			}
			beta := e
			if !pr.Channels[c].Beta {
				beta = e * total / ymax / hsum
			}
			for t := range mu {
				mu[t] += float64(beta * h[t])
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
			want := oracleGeoLogPosterior(in, defaultMaxLag, pr, theta)
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

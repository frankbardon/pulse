package regmmm

import (
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mmmfixture"
)

// bayes_model_test.go holds REG_MMM_BAYES's model maths to its
// contract: the density is an independently written oracle's (Jacobians, derived ROI
// β and every prior family included), the analytic gradient is the
// finite-difference one, the back-transforms reproduce the scaled
// model in original units and the MAP start is deterministic with a
// fallback. The adstock's bit-equality with WIN_DISTRIBUTED_LAG, which
// needs the engine, is bayes_engine_test.go's.

// jointModelData is the joint-truth cohort as the model reads it: sales,
// the four channel spends and the price and promo controls, week order.
func jointModelData() (y []float64, spend, controls [][]float64) {
	d := mmmfixture.Joint()
	price, promo := make([]float64, len(d.Rows)), make([]float64, len(d.Rows))
	for t, r := range d.Rows {
		y = append(y, r[7].(float64))
		price[t], promo[t] = r[5].(float64), r[6].(float64)
	}
	for _, c := range mmmfixture.JointChannels {
		spend = append(spend, d.Spend[c.Name])
	}
	return y, spend, [][]float64{price, promo}
}

func jointModel(t *testing.T, pr bayesPriors) *bayesModel {
	t.Helper()
	y, spend, controls := jointModelData()
	m, err := newBayesModel(y, spend, controls, defaultMaxLag, pr)
	if err != nil {
		t.Fatalf("newBayesModel: %v", err)
	}
	return m
}

// mixedPriors is the default priors with search in β mode and every
// prior family on some coordinate, so the gradient and oracle tests
// reach each family's derivative and each transform.
func mixedPriors() bayesPriors {
	p := defaultBayesPriors(len(mmmfixture.JointChannels))
	p.Channels[1].Beta, p.Channels[1].Effect = true, defaultBetaPrior()
	p.Channels[2].Decay = truncNormalPrior(0.3, 0.2, 0.05, 0.9)
	p.Channels[2].K = logNormalPrior(0, 1)
	p.Channels[3].Decay = betaPrior(1, 3)
	p.Channels[3].S = halfNormalPrior(2)
	p.Channels[0].Decay = betaPrior(3, 3)
	p.Intercept = truncNormalPrior(0.5, 1, -3, 3)
	return p
}

// randomPoint is a θ near the default start: each coordinate moved by
// up to ±spread.
func randomPoint(m *bayesModel, rng *rand.Rand, spread float64) []float64 {
	theta := m.defaultInit()
	for i := range theta {
		theta[i] += float64(float64(2*spread) * (float64(rng.Float64()) - 0.5))
	}
	return theta
}

// TestMMMBayes_GradientMatchesFiniteDifferences: at random points the
// analytic gradient matches a fourth-order central difference to a
// relative 1e-6, under the default (all-ROI) priors and the mixed set
// (a β channel and every prior family).
func TestMMMBayes_GradientMatchesFiniteDifferences(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		pr   bayesPriors
	}{
		{"roi", defaultBayesPriors(len(mmmfixture.JointChannels))},
		{"mixed", mixedPriors()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := jointModel(t, tc.pr)
			dim := m.Dim()
			rng := rand.New(rand.NewPCG(7, 7^0x9E3779B97F4A7C15))
			f := func(x []float64) float64 { return m.LogDensityGrad(x, make([]float64, dim)) }
			for pt := range 20 {
				theta := randomPoint(m, rng, 1)
				grad := make([]float64, dim)
				if lp := m.LogDensityGrad(theta, grad); math.IsInf(lp, 0) || math.IsNaN(lp) {
					t.Fatalf("point %d: density %v", pt, lp)
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
						t.Errorf("point %d coord %d: analytic %.12g, finite difference %.12g (|Δ| %.3g > %.3g)",
							pt, i, grad[i], fd, math.Abs(grad[i]-fd), tol)
					}
				}
			}
		})
	}
}

// oracleLogPosterior is the posterior written out plainly from the model
// doc, with math.* (compared within a tolerance): the scaled data, the
// geometric adstock, the Hill curve, the ROI-derived β, the Normal
// likelihood with its constant, each prior's full density and each
// transform's log-Jacobian.
func oracleLogPosterior(y []float64, spend, controls [][]float64, maxLag int, pr bayesPriors, theta []float64) float64 {
	n, nc, nz := len(y), len(spend), len(controls)
	ymax := 0.0
	for _, v := range y {
		ymax = math.Max(ymax, math.Abs(v))
	}
	logistic := func(x float64) float64 { return 1 / (1 + math.Exp(-x)) }
	type mapped struct{ y, logJac float64 }
	tr := func(p prior, x float64) mapped {
		switch p.Family {
		case familyHalfNormal, familyLogNormal:
			return mapped{math.Exp(x), x}
		case familyBeta:
			u := logistic(x)
			return mapped{u, math.Log(u * (1 - u))}
		case familyTruncNormal:
			u := logistic(x)
			return mapped{p.Lo + float64((p.Hi-p.Lo)*u), math.Log((p.Hi - p.Lo) * u * (1 - u))}
		}
		return mapped{x, 0}
	}
	phi := func(z float64) float64 { return float64(0.5 * (1 + math.Erf(z/math.Sqrt2))) }
	dens := func(p prior, x float64) float64 {
		switch p.Family {
		case familyNormal:
			return float64(-0.5*math.Pow((x-p.A)/p.B, 2)) - math.Log(p.B*math.Sqrt(2*math.Pi))
		case familyHalfNormal:
			return math.Log(2) - float64(0.5*math.Pow(x/p.B, 2)) - math.Log(p.B*math.Sqrt(2*math.Pi))
		case familyLogNormal:
			return float64(-0.5*math.Pow((math.Log(x)-p.A)/p.B, 2)) - math.Log(x*p.B*math.Sqrt(2*math.Pi))
		case familyBeta:
			la, _ := math.Lgamma(p.A)
			lb, _ := math.Lgamma(p.B)
			lab, _ := math.Lgamma(p.A + p.B)
			return float64((p.A-1)*math.Log(x)) + float64((p.B-1)*math.Log(1-x)) - (la + lb - lab)
		case familyTruncNormal:
			return float64(-0.5*math.Pow((x-p.A)/p.B, 2)) - math.Log(p.B*math.Sqrt(2*math.Pi)) -
				math.Log(phi((p.Hi-p.A)/p.B)-phi((p.Lo-p.A)/p.B))
		}
		panic(p.Family)
	}
	lp := 0.0
	val := func(p prior, i int) float64 {
		m := tr(p, theta[i])
		lp += dens(p, m.y) + m.logJac
		return m.y
	}
	alpha := val(pr.Intercept, 0)
	mu := make([]float64, n)
	for t := range mu {
		mu[t] = alpha
	}
	for j, z := range controls {
		g := val(pr.Control, 1+j)
		mean, scale := 0.0, 0.0
		for _, v := range z {
			mean += v / float64(n)
		}
		for _, v := range z {
			scale = math.Max(scale, math.Abs(v-mean))
		}
		for t := range mu {
			mu[t] += g * (z[t] - mean) / scale
		}
	}
	for c, x := range spend {
		cp := pr.Channels[c]
		decay := val(cp.Decay, 1+nz+c)
		k := val(cp.K, 1+nz+nc+c)
		s := val(cp.S, 1+nz+2*nc+c)
		e := val(cp.Effect, 1+nz+3*nc+c)
		xmax, total := 0.0, 0.0
		for _, v := range x {
			xmax = math.Max(xmax, v)
			total += v
		}
		h := make([]float64, n)
		hsum := 0.0
		for t := range n {
			a := 0.0
			for l := 0; l <= maxLag && l <= t; l++ {
				a += float64(math.Pow(decay, float64(l)) * x[t-l])
			}
			if a > 0 {
				a /= xmax
				h[t] = math.Pow(a, s) / (math.Pow(a, s) + math.Pow(k, s))
			}
			hsum += h[t]
		}
		beta := e
		if !cp.Beta {
			beta = e * total / ymax / hsum
		}
		for t := range mu {
			mu[t] += float64(beta * h[t])
		}
	}
	sigma := val(pr.Sigma, 1+nz+4*nc)
	for t := range mu {
		r := y[t]/ymax - mu[t]
		lp += float64(-0.5*r*r)/(sigma*sigma) - math.Log(sigma)
	}
	return lp
}

// TestMMMBayes_DensityMatchesOracle: the model's log density equals the
// oracle's (both drop the likelihood's n·½·log 2π) at random points,
// under both prior sets — so the Jacobians, the derived
// ROI β, the scaling and every family's density are what the doc says.
func TestMMMBayes_DensityMatchesOracle(t *testing.T) {
	t.Parallel()

	y, spend, controls := jointModelData()
	for _, pr := range []bayesPriors{defaultBayesPriors(len(spend)), mixedPriors()} {
		m := jointModel(t, pr)
		rng := rand.New(rand.NewPCG(11, 11^0x9E3779B97F4A7C15))
		for pt := range 10 {
			theta := randomPoint(m, rng, 1)
			got := m.LogDensityGrad(theta, make([]float64, m.Dim()))
			want := oracleLogPosterior(y, spend, controls, defaultMaxLag, pr, theta)
			if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
				t.Errorf("point %d: model %.15g, oracle %.15g", pt, got, want)
			}
		}
	}
}

// TestMMMBayes_BackTransforms: constrain's original-unit coefficients
// rebuild the scaled model's mean in original units (μ_raw = max|y|·μ̃),
// and channelEffects' ROI and β agree with each other and, in ROI mode,
// return the sampled ROI.
func TestMMMBayes_BackTransforms(t *testing.T) {
	t.Parallel()

	y, spend, controls := jointModelData()
	m := jointModel(t, mixedPriors())
	rng := rand.New(rand.NewPCG(3, 3^0x9E3779B97F4A7C15))
	theta := randomPoint(m, rng, 1)
	out := make([]float64, m.Dim())
	m.constrain(theta, out)
	beta, roi := m.channelEffects(theta)

	// The scaled mean, from the model's own pieces.
	scaledMu := make([]float64, m.n)
	val := func(i int) float64 { return m.priors[i].transform().Forward(theta[i]).Y }
	for t := range scaledMu {
		scaledMu[t] = val(m.iIntercept())
		for j := range m.nz {
			scaledMu[t] += float64(val(m.iControl(j)) * m.zs[j][t])
		}
	}
	for c := range m.nc {
		st := m.curve(c, val(m.iDecay(c)), val(m.iK(c)), val(m.iS(c)), false)
		b := val(m.iEffect(c))
		if !m.isBeta[c] {
			b = b * m.spendShare[c] / st.hSum
		}
		for t := range scaledMu {
			scaledMu[t] += float64(b * st.h[t])
		}
	}
	// The raw mean, from original-unit coefficients and raw data only.
	for i := range y {
		raw := out[m.iIntercept()]
		for j := range controls {
			raw += float64(out[m.iControl(j)] * controls[j][i])
		}
		for c := range spend {
			w, _ := geometricWeights(out[m.iDecay(c)], defaultMaxLag)
			a := make([]float64, len(y))
			adstock(spend[c], w, nil, a, nil)
			h := 0.0
			if a[i] > 0 {
				h = 1 / (1 + math.Pow(out[m.iK(c)]/a[i], out[m.iS(c)]))
			}
			raw += float64(beta[c] * h)
		}
		if want := float64(m.yScale * scaledMu[i]); math.Abs(raw-want) > float64(1e-9*math.Abs(want)) {
			t.Fatalf("week %d: raw mean %.12g, scaled mean × max|y| %.12g", i+1, raw, want)
		}
	}
	for c := range m.nc {
		contrib := 0.0
		total := 0.0
		w, _ := geometricWeights(out[m.iDecay(c)], defaultMaxLag)
		a := make([]float64, len(y))
		adstock(spend[c], w, nil, a, nil)
		for t := range a {
			if a[t] > 0 {
				contrib += beta[c] / (1 + math.Pow(out[m.iK(c)]/a[t], out[m.iS(c)]))
			}
			total += spend[c][t]
		}
		if r := contrib / total; math.Abs(r-roi[c]) > 1e-9*roi[c] {
			t.Errorf("channel %d: ROI %v, contribution/spend %v", c, roi[c], r)
		}
		if m.isBeta[c] {
			if out[m.iEffect(c)] != beta[c] {
				t.Errorf("channel %d (β mode): constrained effect %v, β %v", c, out[m.iEffect(c)], beta[c])
			}
		} else if out[m.iEffect(c)] != roi[c] {
			t.Errorf("channel %d (ROI mode): constrained effect %v, ROI %v", c, out[m.iEffect(c)], roi[c])
		}
	}
}

// TestMMMBayes_PriorFamilies: each family's log density against a
// closed form, and the support rule (a family whose support is not
// inside the parameter's is refused).
func TestMMMBayes_PriorFamilies(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		p    prior
		s    support
		x    float64
		want float64
	}{
		{normalPrior(1, 2), supportReal, 0, math.Log(1 / (2 * math.Sqrt(2*math.Pi)) * math.Exp(-0.125))},
		{halfNormalPrior(1), supportPositive, 1, math.Log(2 / math.Sqrt(2*math.Pi) * math.Exp(-0.5))},
		{logNormalPrior(0.2, 0.9), supportPositive, 1.5, math.Log(1 / (1.5 * 0.9 * math.Sqrt(2*math.Pi)) * math.Exp(-0.5*math.Pow((math.Log(1.5)-0.2)/0.9, 2)))},
		{betaPrior(2, 3), supportUnit, 0.3, math.Log(12 * 0.3 * 0.49)},
		{betaPrior(1, 1), supportUnit, 0.7, 0},
		{truncNormalPrior(0, 1, -1, 1), supportReal, 0, math.Log(1 / math.Sqrt(2*math.Pi) / (math.Erf(1 / math.Sqrt2)))},
	} {
		cp, err := tc.p.compile(tc.s)
		if err != nil {
			t.Fatalf("%s: %v", tc.p.Family, err)
		}
		if got, _ := cp.logDensity(tc.x); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("%s(%v, %v) at %v: %.15g, want %.15g", tc.p.Family, tc.p.A, tc.p.B, tc.x, got, tc.want)
		}
	}
	for _, tc := range []struct {
		p  prior
		s  support
		ok bool
	}{
		{halfNormalPrior(1), supportReal, true},
		{normalPrior(0, 1), supportPositive, false},
		{logNormalPrior(0, 1), supportUnit, false},
		{betaPrior(1, 1), supportPositive, true},
		{truncNormalPrior(0, 1, -1, 1), supportPositive, false},
		{truncNormalPrior(0.5, 1, 0, 1), supportUnit, true},
		{truncNormalPrior(0, 1, 2, 1), supportReal, false},
		{normalPrior(0, 0), supportReal, false},
		{prior{Family: "cauchy", B: 1}, supportReal, false},
	} {
		if _, err := tc.p.compile(tc.s); (err == nil) != tc.ok {
			t.Errorf("%s(%v, %v) on %v: err %v, want ok=%v", tc.p.Family, tc.p.A, tc.p.B, tc.s, err, tc.ok)
		}
	}
}

// TestMMMBayes_Refusals: the constructor's coded refusals.
func TestMMMBayes_Refusals(t *testing.T) {
	t.Parallel()

	y, spend, controls := jointModelData()
	pr := defaultBayesPriors(len(spend))
	neg := slices.Clone(spend[0])
	neg[3] = -1
	constant := make([]float64, len(y))
	for i := range constant {
		constant[i] = 4
	}
	badPrior := defaultBayesPriors(len(spend))
	badPrior.Channels[0].Decay = logNormalPrior(0, 1)
	for _, tc := range []struct {
		name     string
		y        []float64
		spend    [][]float64
		controls [][]float64
		pr       bayesPriors
		code     perr.Code
	}{
		{"short", y[:20], [][]float64{spend[0][:20], spend[1][:20], spend[2][:20], spend[3][:20]}, [][]float64{controls[0][:20], controls[1][:20]}, pr, perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA},
		{"negative spend", y, [][]float64{neg, spend[1], spend[2], spend[3]}, controls, pr, perr.PROCESSING_CONFIG},
		{"never spends", y, [][]float64{make([]float64, len(y)), spend[1], spend[2], spend[3]}, controls, pr, perr.PROCESSING_REGRESSION_RANK_DEFICIENT},
		{"constant control", y, spend, [][]float64{constant, controls[1]}, pr, perr.PROCESSING_REGRESSION_RANK_DEFICIENT},
		{"zero outcome", make([]float64, len(y)), spend, controls, pr, perr.PROCESSING_REGRESSION_RANK_DEFICIENT},
		{"prior support", y, spend, controls, badPrior, perr.PROCESSING_CONFIG},
	} {
		if _, err := newBayesModel(tc.y, tc.spend, tc.controls, defaultMaxLag, tc.pr); !perr.HasCode(err, tc.code) {
			t.Errorf("%s: err %v, want %s", tc.name, err, tc.code)
		}
	}
}

// TestMMMBayes_MAPStart: the warm start on the joint fixture converges,
// beats the default start's density, and is the same bits on a second
// run; a search that cannot finish falls back to the default start.
func TestMMMBayes_MAPStart(t *testing.T) {
	t.Parallel()

	m := jointModel(t, defaultBayesPriors(len(mmmfixture.JointChannels)))
	theta, ok := m.mapStart(mapMaxIter)
	if !ok {
		t.Fatal("MAP start fell back on the joint fixture")
	}
	again, _ := m.mapStart(mapMaxIter)
	for i := range theta {
		if math.Float64bits(theta[i]) != math.Float64bits(again[i]) {
			t.Fatalf("coord %d: %v then %v", i, theta[i], again[i])
		}
	}
	init := m.defaultInit()
	scratch := make([]float64, m.Dim())
	if lpMAP, lp0 := m.LogDensityGrad(theta, scratch), m.LogDensityGrad(init, scratch); !(lpMAP > lp0) {
		t.Errorf("MAP density %v not above the default start's %v", lpMAP, lp0)
	}
	_, roi := m.channelEffects(theta)
	d := mmmfixture.Joint()
	for c, ch := range mmmfixture.JointChannels {
		t.Logf("%-8s MAP ROI %.3f, truth %.3f", ch.Name, roi[c], d.TrueROI(ch.Name))
	}

	fallback, ok := m.mapStart(1)
	if ok {
		t.Fatal("a one-iteration search reported success")
	}
	if !slices.Equal(fallback, init) {
		t.Errorf("fallback %v, want the default start %v", fallback, init)
	}
}

// TestMMMBayes_ConcurrentSafe: LogDensityGrad from many goroutines at
// once gives the serial bits (the model keeps no shared scratch).
func TestMMMBayes_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	m := jointModel(t, mixedPriors())
	rng := rand.New(rand.NewPCG(5, 5^0x9E3779B97F4A7C15))
	points := make([][]float64, 16)
	want := make([][]float64, len(points))
	for i := range points {
		points[i] = randomPoint(m, rng, 1)
		want[i] = make([]float64, m.Dim()+1)
		want[i][0] = m.LogDensityGrad(points[i], want[i][1:])
	}
	var wg sync.WaitGroup
	got := make([][]float64, len(points))
	for i := range points {
		wg.Go(func() {
			got[i] = make([]float64, m.Dim()+1)
			for range 20 {
				got[i][0] = m.LogDensityGrad(points[i], got[i][1:])
			}
		})
	}
	wg.Wait()
	for i := range points {
		for j := range got[i] {
			if math.Float64bits(got[i][j]) != math.Float64bits(want[i][j]) {
				t.Fatalf("point %d entry %d: concurrent %v, serial %v", i, j, got[i][j], want[i][j])
			}
		}
	}
}

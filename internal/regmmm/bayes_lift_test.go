package regmmm

import (
	"context"
	stderrors "errors"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/mmmfixture"
)

// bayes_lift_test.go holds REG_MMM_BAYES's lift-test likelihood
// (params.lift_tests, bayes_lift.go) to its contract: the changed spend,
// the model's Δ against an independently written oracle, the log
// density's lift term, its analytic gradient against finite differences,
// the spec and data refusals and the roi-prior overlap warning.

// TestMMMBayes_LiftSpend: spend_delta is spread over the window in
// proportion to the spend there (minus the window spend switches the
// channel off exactly), evenly over a window that never spent; rows
// before the window keep their spend; a change below zero spend is
// refused.
func TestMMMBayes_LiftSpend(t *testing.T) {
	t.Parallel()

	x := []float64{5, 10, 0, 30, 10, 7}
	got, err := liftSpend(x, 1, 3, 20) // window spend 40: ×1.5
	if err != nil {
		t.Fatal(err)
	}
	if want := []float64{5, 15, 0, 45}; !slices.Equal(got, want) {
		t.Errorf("+20 over [1, 3]: %v, want %v (rows past the window dropped)", got, want)
	}
	got, err = liftSpend(x, 1, 3, -40)
	if err != nil || !slices.Equal(got, []float64{5, 0, 0, 0}) {
		t.Errorf("switch-off: %v %v, want [5 0 0 0]", got, err)
	}
	// A switch-off spelled from a rounded window sum still lands on 0.
	got, err = liftSpend(x, 1, 3, -40*(1+1e-12))
	if err != nil || !slices.Equal(got, []float64{5, 0, 0, 0}) {
		t.Errorf("rounded switch-off: %v %v, want [5 0 0 0]", got, err)
	}
	got, err = liftSpend(x, 2, 2, 9)
	if err != nil || !slices.Equal(got, []float64{5, 10, 9}) {
		t.Errorf("dark window: %v %v, want [5 10 9]", got, err)
	}
	if _, err := liftSpend(x, 1, 3, -41); err == nil {
		t.Error("a change below zero spend was accepted")
	}
	if _, err := liftSpend(x, 2, 2, -1); err == nil {
		t.Error("a cut on a dark window was accepted")
	}
	if x[1] != 10 {
		t.Error("liftSpend wrote into the fitted spend")
	}
}

// liftModel is jointModel under pr with two lift tests: a switch-off of
// channel 0 over weeks 20–27 and a +50% test of channel 1 over 40–45,
// each with a measured lift near the model's scale.
func liftModel(t *testing.T, pr bayesPriors) *bayesModel {
	t.Helper()
	m := jointModel(t, pr)
	tot := func(c, from, to int) float64 {
		s := 0.0
		for i := from; i <= to; i++ {
			s += m.spend[c][i]
		}
		return s
	}
	for _, l := range []struct {
		c, from, to int
		delta, lift float64 // delta: a fraction of the window spend
		se          float64
	}{
		{0, 20, 27, -1, -0.04 * m.yScale, 0.01 * m.yScale},
		{1, 40, 45, 0.5, 0.02 * m.yScale, 0.015 * m.yScale},
	} {
		lt, err := m.newLiftTest(l.c, l.from, l.to, l.delta*tot(l.c, l.from, l.to), l.lift, l.se)
		if err != nil {
			t.Fatal(err)
		}
		m.lifts = append(m.lifts, lt)
	}
	return m
}

// oracleLiftDelta is lift test l's Δ in original units at θ, written
// from the contract with math.*: the changed spend (each window period
// scaled by 1 + frac, frac the change as a fraction of the window
// spend), each kernel's closed-form weights over the whole series, each
// curve's closed form on the channel's peak-scaled adstock, β derived
// from the ROI over the fitted spend, and the window-only sum.
func oracleLiftDelta(m *bayesModel, theta []float64, l liftTest, frac float64) float64 {
	v := make([]float64, m.Dim())
	for i := range v {
		v[i] = m.priors[i].transform().Forward(theta[i]).Y
	}
	c := l.channel
	ad := make([]float64, m.np)
	for i := range ad {
		ad[i] = v[m.iAdstock(i, c)]
	}
	q := make([]float64, m.nq)
	for i := range q {
		q[i] = v[m.iCurve(i, c)]
	}
	x := m.spend[c]
	xmax, total := 0.0, 0.0
	for _, s := range x {
		xmax, total = math.Max(xmax, s), total+s
	}
	changed := slices.Clone(x)
	for t := l.from; t <= l.to; t++ {
		changed[t] = x[t] * (1 + frac)
	}
	h := func(series []float64, t int) float64 {
		a := 0.0
		for lag := 0; lag <= m.maxLag && lag <= t; lag++ {
			a += float64(oracleKernelWeight(m.kernel.Name(), ad, lag) * series[t-lag])
		}
		if !(a > 0) {
			return 0
		}
		a /= xmax
		switch m.sat.Name() {
		case "hill":
			return math.Pow(a, q[1]) / (math.Pow(a, q[1]) + math.Pow(q[0], q[1]))
		case "logistic":
			return math.Tanh(q[0] * a / 2)
		}
		return a / (q[0] + a)
	}
	hsum, d := 0.0, 0.0
	for t := range x {
		hsum += h(x, t)
	}
	for t := l.from; t <= l.to; t++ {
		d += h(changed, t) - h(x, t)
	}
	beta := v[m.iEffect(c)]
	if !m.isBeta[c] {
		beta = beta * total / m.yScale / hsum
	}
	return m.yScale * beta * d
}

// liftSets are the prior sets the lift oracle and gradient tests run
// on: every kernel, every curve, ROI and β mode.
func liftSets() map[string]bayesPriors {
	return map[string]bayesPriors{
		"roi":                          defaultBayesPriors(len(mmmfixture.JointChannels)),
		"mixed":                        mixedPriors(),
		"delayed":                      kernelPriorSets()["delayed"],
		"weibull_cdf":                  kernelPriorSets()["weibull_cdf"],
		"delayed_logistic":             curvePriorSets()["delayed_logistic"],
		"weibull_pdf_michaelis_menten": curvePriorSets()["weibull_pdf_michaelis_menten"],
		"logistic_mixed":               curveMixedPriors("logistic"),
	}
}

// TestMMMBayes_LiftDensityMatchesOracle: with lift tests the log
// density is the lift-free oracle's plus each test's −½((lift − Δ)/se)²,
// Δ the oracle's; liftPrediction is that Δ — on every kernel and curve,
// ROI and β mode (the mixed sets put channel 1, a lift channel, in β
// mode).
func TestMMMBayes_LiftDensityMatchesOracle(t *testing.T) {
	t.Parallel()

	y, spend, controls := jointModelData()
	for name, pr := range liftSets() {
		m := liftModel(t, pr)
		deltas := []float64{-1, 0.5}
		rng := rand.New(rand.NewPCG(13, 13^0x9E3779B97F4A7C15))
		for pt := range 8 {
			theta := randomPoint(m, rng, 1)
			want := oracleLogPosterior(y, spend, controls, defaultMaxLag, pr, theta)
			for i, l := range m.lifts {
				d := oracleLiftDelta(m, theta, l, deltas[i])
				if got := m.liftPrediction(theta, i); math.Abs(got-d) > 1e-9*math.Max(1, math.Abs(d)) {
					t.Errorf("%s point %d test %d: Δ %.15g, oracle %.15g", name, pt, i, got, d)
				}
				if d == 0 {
					t.Errorf("%s point %d test %d: oracle Δ is 0 — the test exercises nothing", name, pt, i)
				}
				r := (l.lift - d) / l.se
				want -= float64(0.5 * r * r)
			}
			got := m.LogDensityGrad(theta, make([]float64, m.Dim()))
			if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
				t.Errorf("%s point %d: model %.15g, oracle %.15g", name, pt, got, want)
			}
		}
	}
}

// TestMMMBayes_LiftGradientMatchesFiniteDifferences: with lift tests the
// analytic gradient matches a fourth-order central difference to a
// relative 1e-6 on every kernel, curve and effect mode — the lift term's
// partials (through Σh in ROI mode) are chained right.
func TestMMMBayes_LiftGradientMatchesFiniteDifferences(t *testing.T) {
	t.Parallel()

	for name, pr := range liftSets() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := liftModel(t, pr)
			bare := jointModel(t, pr)
			dim := m.Dim()
			rng := rand.New(rand.NewPCG(17, 17^0x9E3779B97F4A7C15))
			f := func(x []float64) float64 { return m.LogDensityGrad(x, make([]float64, dim)) }
			moved := false
			for pt := range 6 {
				theta := randomPoint(m, rng, 1)
				grad, g0 := make([]float64, dim), make([]float64, dim)
				if lp := m.LogDensityGrad(theta, grad); math.IsInf(lp, 0) || math.IsNaN(lp) {
					t.Fatalf("point %d: density %v", pt, lp)
				}
				bare.LogDensityGrad(theta, g0)
				for i := range dim {
					moved = moved || grad[i] != g0[i]
					h := 1e-4
					at := func(k float64) float64 {
						x := slices.Clone(theta)
						x[i] += k * h
						return f(x)
					}
					fd := (float64(8*(at(1)-at(-1))) - (at(2) - at(-2))) / (12 * h)
					if tol := 1e-6 * max(1, math.Abs(grad[i])); math.Abs(grad[i]-fd) > tol {
						t.Errorf("point %d coord %d: analytic %.12g, finite difference %.12g", pt, i, grad[i], fd)
					}
				}
			}
			if !moved {
				t.Error("the lift tests moved no partial")
			}
		})
	}
}

// liftParams is the digest slot's params with a lift_tests list.
func liftParams(tests string) string {
	return `{"channels":["tv"],"time":"week","chains":1,"warmup":20,"draws":10,"lift_tests":` + tests + `}`
}

// TestMMMBayes_LiftFactoryRefusals: every spec refusal of a lift test is
// PROCESSING_CONFIG on slot params.lift_tests with its index and reason;
// an empty list builds as if absent.
func TestMMMBayes_LiftFactoryRefusals(t *testing.T) {
	t.Parallel()

	const ok = `"channel":"tv","start":10,"end":14,"spend_delta":-100,"lift":-30,"se":8`
	for _, tc := range []struct {
		tests, reason, msg string
	}{
		{`[{` + ok + `,"geo":"east"}]`, reasonLiftGeo, "geo applies only to a geo model"},
		{`[{"channel":"price","start":10,"end":14,"spend_delta":-1,"lift":-3,"se":1}]`, reasonLiftChannel, `channel "price" is not one of params.channels`},
		{`[{"channel":"","start":10,"end":14,"spend_delta":-1,"lift":-3,"se":1}]`, reasonLiftChannel, `channel "" is not`},
		{`[{"channel":"tv","end":14,"spend_delta":-1,"lift":-3,"se":1}]`, reasonLiftWindowMissing, "start and end"},
		{`[{"channel":"tv","start":15,"end":14,"spend_delta":-1,"lift":-3,"se":1}]`, reasonLiftWindowInvalid, "start 15 is after end 14"},
		{`[{"channel":"tv","start":10,"end":14,"lift":-3,"se":1}]`, reasonLiftSpendDeltaAbsent, "spend_delta"},
		{`[{"channel":"tv","start":10,"end":14,"spend_delta":0,"lift":-3,"se":1}]`, reasonLiftSpendDeltaZero, "changes no spend"},
		{`[{"channel":"tv","start":10,"end":14,"spend_delta":-1,"se":1}]`, reasonLiftMissing, "lift"},
		{`[{"channel":"tv","start":10,"end":14,"spend_delta":-1,"lift":-3}]`, reasonLiftSE, "se"},
		{`[{"channel":"tv","start":10,"end":14,"spend_delta":-1,"lift":-3,"se":0}]`, reasonLiftSE, "must be > 0"},
		{`[{` + ok + `},{"channel":"tv","start":10,"end":14,"spend_delta":-1,"lift":-3,"se":-2}]`, reasonLiftSE, "params.lift_tests[1]"},
	} {
		_, err := BayesFactory(BayesSpec(liftParams(tc.tests)), nil)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_CONFIG {
			t.Errorf("%s: %v, want PROCESSING_CONFIG", tc.tests, err)
			continue
		}
		if ce.Details["slot"] != "params.lift_tests" || ce.Details["reason"] != tc.reason || !strings.Contains(ce.Message, tc.msg) {
			t.Errorf("%s: %q %v, want reason %s and %q", tc.tests, ce.Message, ce.Details, tc.reason, tc.msg)
		}
	}
	// A typo inside a test is a params refusal, never ignored.
	if _, err := BayesFactory(BayesSpec(liftParams(`[{`+ok+`,"window":3}]`)), nil); err == nil || !strings.Contains(err.Error(), "window") {
		t.Errorf("unknown lift-test key: %v", err)
	}
	for _, tests := range []string{`[]`, `null`} {
		reg, err := BayesFactory(BayesSpec(liftParams(tests)), nil)
		if err != nil {
			t.Fatalf("%s: %v", tests, err)
		}
		if cfg := reg.(*bayesRegression).cfg; cfg.lifts != nil || cfg.liftOverlap != nil {
			t.Errorf("%s: lifts %v overlap %v, want none", tests, cfg.lifts, cfg.liftOverlap)
		}
	}
	reg, err := BayesFactory(BayesSpec(liftParams(`[{`+ok+`}]`)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.(*bayesRegression).cfg.lifts; len(got) != 1 || got[0] != (bayesLiftSpec{channel: 0, start: 10, end: 14, spendDelta: -100, lift: -30, se: 8}) {
		t.Errorf("lifts %+v", got)
	}
}

// TestMMMBayes_LiftPriorOverlap: only a channel with both an explicit
// roi prior and a lift test overlaps; a beta prior or a kernel prior
// does not.
func TestMMMBayes_LiftPriorOverlap(t *testing.T) {
	t.Parallel()

	spec := BayesSpec(`{"channels":["tv","radio"],"time":"week","lift_tests":[` +
		`{"channel":"tv","start":1,"end":2,"spend_delta":1,"lift":1,"se":1},` +
		`{"channel":"radio","start":1,"end":2,"spend_delta":1,"lift":1,"se":1}],` +
		`"priors":{"tv":{"roi":{"family":"lognormal","mu":0,"sigma":1}},"radio":{"decay":{"family":"beta","alpha":2,"beta":2}}}}`)
	spec.Predictors = []string{"tv", "radio", "price"}
	reg, err := BayesFactory(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.(*bayesRegression).cfg.liftOverlap; !slices.Equal(got, []string{"tv"}) {
		t.Errorf("overlap %v, want [tv]", got)
	}
	spec.Params = []byte(strings.Replace(string(spec.Params), `"roi":{"family":"lognormal","mu":0,"sigma":1}`, `"beta":{"family":"halfnormal","sigma":1}`, 1))
	reg, err = BayesFactory(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.(*bayesRegression).cfg.liftOverlap; got != nil {
		t.Errorf("a beta prior overlaps: %v", got)
	}
}

// TestMMMBayes_LiftDataRefusals: a window outside the fitted periods
// (before the first, past the last, or in the held-out rows) and a cut
// larger than the window's spend are PROCESSING_CONFIG before any
// sampling, slot params.lift_tests with the test's index.
func TestMMMBayes_LiftDataRefusals(t *testing.T) {
	t.Parallel()

	rows := digestRows()
	winSpend := 0.0
	for _, r := range rows[9:14] {
		winSpend += r["tv"]
	}
	for _, tc := range []struct {
		params, reason, msg string
	}{
		{liftParams(`[{"channel":"tv","start":0,"end":4,"spend_delta":-1,"lift":-3,"se":1}]`), reasonLiftWindowRange, "outside the fitted periods [1, 60]"},
		{liftParams(`[{"channel":"tv","start":58,"end":61,"spend_delta":-1,"lift":-3,"se":1}]`), reasonLiftWindowRange, "[58, 61]"},
		{strings.Replace(liftParams(`[{"channel":"tv","start":48,"end":52,"spend_delta":-1,"lift":-3,"se":1}]`), `"time":"week"`, `"time":"week","holdout_from":50`, 1),
			reasonLiftWindowRange, "fitted periods [1, 49]"},
		{liftParams(`[{"channel":"tv","start":10,"end":14,"spend_delta":` + formatFloat(-2*winSpend) + `,"lift":-3,"se":1}]`), reasonLiftSpendNegative, "more than the window's spend"},
	} {
		reg, err := BayesFactory(BayesSpec(tc.params), nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.params, err)
		}
		_, err = reg.(extend.ContextRegression).FitContext(context.Background(), rows)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_CONFIG || ce.Details["slot"] != "params.lift_tests" ||
			ce.Details["reason"] != tc.reason || ce.Details["index"] != 0 || !strings.Contains(ce.Message, tc.msg) {
			t.Errorf("%s: %v (details %v), want reason %s and %q", tc.params, err, detailsOf(ce), tc.reason, tc.msg)
		}
	}
}

func detailsOf(ce *perr.CodedError) map[string]any {
	if ce == nil {
		return nil
	}
	return ce.Details
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// TestMMMBayes_LiftPriorOverlapWarns: a fit whose lift-test channel also
// carries an roi prior runs and returns figures with a
// PULSE_MMM_LIFT_PRIOR_OVERLAP warning first (details.channels); the
// same fit without the prior carries none.
func TestMMMBayes_LiftPriorOverlapWarns(t *testing.T) {
	t.Parallel()

	const lift = `[{"channel":"tv","start":10,"end":14,"spend_delta":50,"lift":4,"se":3}]`
	withPrior := strings.Replace(liftParams(lift), `"time":"week"`, `"time":"week","priors":{"tv":{"roi":{"family":"lognormal","mu":0,"sigma":1}}}`, 1)
	for _, tc := range []struct {
		params string
		warn   bool
	}{{withPrior, true}, {liftParams(lift), false}} {
		reg, err := BayesFactory(BayesSpec(tc.params), nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := reg.(extend.ContextRegression).FitContext(context.Background(), digestRows())
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Coefficients) == 0 || out.Outputs["roi"] == nil {
			t.Fatalf("no figures: %+v", out)
		}
		var got []string
		for _, w := range out.Warnings {
			if w.Code == string(perr.PULSE_MMM_LIFT_PRIOR_OVERLAP) {
				got = append(got, w.Message)
				if ch, _ := w.Details["channels"].([]string); !slices.Equal(ch, []string{"tv"}) {
					t.Errorf("details %v, want channels [tv]", w.Details)
				}
			}
		}
		if tc.warn && (len(got) != 1 || out.Warnings[0].Code != string(perr.PULSE_MMM_LIFT_PRIOR_OVERLAP) || !strings.Contains(got[0], `channel(s) tv`)) {
			t.Errorf("with an roi prior: warnings %v, want the overlap warning first", out.Warnings)
		}
		if !tc.warn && len(got) != 0 {
			t.Errorf("without an roi prior: %v", got)
		}
	}
}

package regmmm

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/distlagparams"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/optimize"
	"github.com/frankbardon/pulse/types"
)

// hill_adstock_test.go pins REG_MMM_HILL's in-model adstock
// (params.adstock, mmm-optimize-depth E2-S2) on tiny generated series
// through the model's own Fit (no engine): the joint fit recovers a
// known geometric and delayed decay with finite standard errors and
// decay↔curve covariances, the analytic kernel Jacobian matches central
// differences, the factory's refusals, and the condition number that
// drives the ill-conditioning advisory. Every fit here runs in a few
// milliseconds.

// adstockTruth is one generated series' truth: two channels (raw spend)
// and a price control, each channel adstocked by kernel at its params.
type adstockTruth struct {
	kernel      string
	maxLag      int
	tvPhi       []float64 // the kernel's params, in Params' order
	srPhi       []float64
	tvBeta, tvK float64
	srBeta, srK float64
	noise       float64
}

// adstockSeries generates n weeks: bursty tv, always-on search with
// flights, uniform price; y = 50 + βtv·hill(a_tv, ktv, 2) + βsr·hill(a_sr,
// ksr, 1.5) − 3·price + noise, a the kernel's unnormalised adstock — the
// model's own. Every product feeding an add is barriered.
func adstockSeries(tr adstockTruth, n int, seed uint64) probeRows {
	rng := rand.New(rand.NewPCG(seed, 0xad57))
	k := kernelByName(tr.kernel)
	tvW, _ := k.Weights(tr.tvPhi, tr.maxLag, false)
	srW, _ := k.Weights(tr.srPhi, tr.maxLag, false)
	tv, sr, pr := make([]float64, n), make([]float64, n), make([]float64, n)
	for i := range n {
		if rng.Float64() < 0.45 {
			tv[i] = float64(rng.Float64()*300) + 20
		}
		sr[i] = float64(rng.Float64() * 100)
		pr[i] = float64(rng.Float64()*2) - 1
	}
	tvA, srA := make([]float64, n), make([]float64, n)
	adstock(tv, tvW, nil, tvA, nil)
	adstock(sr, srW, nil, srA, nil)
	rows := make(probeRows, n)
	for i := range n {
		y := 50 + float64(tr.tvBeta*hillCurve(tvA[i], tr.tvK, 2)) + float64(tr.srBeta*hillCurve(srA[i], tr.srK, 1.5)) +
			float64(-3*pr[i]) + float64(tr.noise*(float64(rng.Float64()*2)-1))
		rows[i] = probeRow{"y": y, "tv": tv[i], "search": sr[i], "price": pr[i]}
	}
	return rows
}

// adstockSpec is a REG_MMM_HILL slot over tv, search (channels) and
// price, with params extra merged in and an order.
func adstockSpec(extra string) *types.RegressionSpec {
	params := `{"channels":["tv","search"]` + extra + `}`
	return &types.RegressionSpec{Type: Name, Name: "mmm", Target: "y", Predictors: []string{"tv", "search", "price"},
		Params: json.RawMessage(params), Order: &types.RegressionOrder{Field: "week"}}
}

// fitAdstock builds and fits spec over rows.
func fitAdstock(t *testing.T, spec *types.RegressionSpec, rows probeRows) extend.RegressionOutput {
	t.Helper()
	reg, err := Factory(spec, nil)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	out, err := reg.(*model).Fit(rows)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	return out
}

// coefOf is the named coefficient of out.
func coefOf(t *testing.T, out extend.RegressionOutput, name string) extend.RegressionCoefficient {
	t.Helper()
	for _, c := range out.Coefficients {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no coefficient %q in %v", name, out.Coefficients)
	return extend.RegressionCoefficient{}
}

// TestMMMHillAdstock_RecoversGeometricDecay: the joint fit recovers each
// channel's geometric decay within three standard errors (and 10%), the
// table appends decay:<ch> after s:<ch>, every decay↔curve covariance is
// finite, and outputs carry a finite condition number.
func TestMMMHillAdstock_RecoversGeometricDecay(t *testing.T) {
	t.Parallel()

	tr := adstockTruth{kernel: distlagparams.KernelGeometric, maxLag: 8, tvPhi: []float64{0.6}, srPhi: []float64{0.3},
		tvBeta: 12, tvK: 250, srBeta: 6, srK: 60, noise: 0.2}
	out := fitAdstock(t, adstockSpec(`,"adstock":"geometric","max_lag":8`), adstockSeries(tr, 156, 3))

	var names []string
	for _, c := range out.Coefficients {
		names = append(names, c.Name)
	}
	want := []string{"(intercept)", "tv", "search", "price", "k:tv", "k:search", "s:tv", "s:search", "decay:tv", "decay:search"}
	if !slices.Equal(names, want) {
		t.Fatalf("coefficients %v, want %v", names, want)
	}
	for _, c := range []struct {
		name  string
		truth float64
	}{{"decay:tv", 0.6}, {"decay:search", 0.3}} {
		got := coefOf(t, out, c.name)
		if got.StdErr == nil || !(*got.StdErr > 0) || math.IsInf(*got.StdErr, 0) {
			t.Fatalf("%s std err %v, want finite > 0", c.name, got.StdErr)
		}
		if d := math.Abs(got.Estimate - c.truth); d > 3**got.StdErr || d > 0.1*c.truth {
			t.Errorf("%s = %v ± %v, truth %v", c.name, got.Estimate, *got.StdErr, c.truth)
		}
	}
	// decay:tv (index 8) against k:tv (4) and tv's β (1): the joint
	// Jacobian's covariances, not a profile's zeros.
	for _, j := range []int{1, 4, 6} {
		if v := out.Vcov[8][j]; math.IsNaN(v) || v == 0 {
			t.Errorf("vcov[decay:tv][%s] = %v, want a finite joint covariance", names[j], v)
		}
	}
	cond, ok := out.Outputs[pmodel.MMMConditionKey].(float64)
	if !ok || !(cond >= 1) || cond > pmodel.MMMConditionThreshold {
		t.Fatalf("condition_number = %v, want finite in [1, threshold] on a well-identified series", out.Outputs[pmodel.MMMConditionKey])
	}
}

// TestMMMHillAdstock_RecoversDelayedKernel: the delayed kernel's decay
// and peak are both recovered, appended decay:<ch> then peak:<ch>.
func TestMMMHillAdstock_RecoversDelayedKernel(t *testing.T) {
	t.Parallel()

	tr := adstockTruth{kernel: distlagparams.KernelDelayed, maxLag: 6, tvPhi: []float64{0.5, 2}, srPhi: []float64{0.4, 1},
		tvBeta: 12, tvK: 250, srBeta: 6, srK: 60, noise: 0.2}
	out := fitAdstock(t, adstockSpec(`,"adstock":"delayed","max_lag":6`), adstockSeries(tr, 156, 5))
	if n := out.Coefficients[len(out.Coefficients)-1].Name; n != "peak:search" {
		t.Fatalf("last coefficient %q, want peak:search", n)
	}
	for _, c := range []struct {
		name       string
		truth, tol float64
	}{{"decay:tv", 0.5, 0.05}, {"peak:tv", 2, 0.2}, {"decay:search", 0.4, 0.05}, {"peak:search", 1, 0.2}} {
		got := coefOf(t, out, c.name)
		if math.Abs(got.Estimate-c.truth) > c.tol || got.StdErr == nil || math.IsNaN(*got.StdErr) {
			t.Errorf("%s = %v (se %v), truth %v ± %v", c.name, got.Estimate, got.StdErr, c.truth, c.tol)
		}
	}
}

// TestMMMHillAdstock_JacobianMatchesFiniteDifferences: the analytic
// residual Jacobian's kernel columns (and the curve columns beside
// them) equal central differences of the residuals at an interior
// point, for both kernels.
func TestMMMHillAdstock_JacobianMatchesFiniteDifferences(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		kernel string
		phi    []float64
	}{{distlagparams.KernelGeometric, []float64{0.55}}, {distlagparams.KernelDelayed, []float64{0.45, 1.7}}} {
		tr := adstockTruth{kernel: c.kernel, maxLag: 5, tvPhi: c.phi, srPhi: c.phi, tvBeta: 12, tvK: 250, srBeta: 6, srK: 60, noise: 0.2}
		rows := adstockSeries(tr, 40, 9)
		k, _ := hillKernelFor(c.kernel, 5)
		p := &problem{y: make([]float64, len(rows)), a: [][]float64{make([]float64, len(rows)), make([]float64, len(rows))},
			z: [][]float64{make([]float64, len(rows))}, kRef: make([]float64, 2), priorIdx: -1,
			ad: &hillAdstock{kernel: k, spend: make([][]float64, 2), da: make([][][]float64, 2)}}
		for i, r := range rows {
			p.y[i], p.a[0][i], p.a[1][i], p.z[0][i] = r["y"], r["tv"], r["search"], r["price"]
		}
		p.initAdstock()
		x := make([]float64, p.dim())
		x[0], x[1], x[2], x[3] = 50, 10, 5, -3
		x[4], x[5], x[6], x[7] = 0.2, -0.1, 0.5, 0.3 // θk, θs
		for ch := range 2 {
			for i, v := range c.phi {
				x[p.phiIndex(i, ch)] = v
			}
		}
		n := len(p.y)
		jac := make([][]float64, n)
		for i := range jac {
			jac[i] = make([]float64, p.dim())
		}
		r := make([]float64, n)
		p.residuals(r, jac, x)
		rp, rm := make([]float64, n), make([]float64, n)
		for j := 4; j < p.dim(); j++ {
			h := 1e-6
			xp, xm := slices.Clone(x), slices.Clone(x)
			xp[j] += h
			xm[j] -= h
			p.residuals(rp, nil, xp)
			p.residuals(rm, nil, xm)
			for i := range n {
				fd := (rp[i] - rm[i]) / (2 * h)
				if math.Abs(fd-jac[i][j]) > 1e-5*math.Max(1, math.Abs(fd)) {
					t.Fatalf("%s: d r[%d]/d x[%d] analytic %v, central difference %v", c.kernel, i, j, jac[i][j], fd)
				}
			}
		}
	}
}

// TestMMMHillAdstock_FactoryRefusals: every params.adstock refusal is
// PROCESSING_CONFIG with its slot and reason, from the spec alone
// (SpecRefusal — what predict raises).
func TestMMMHillAdstock_FactoryRefusals(t *testing.T) {
	t.Parallel()

	partition := adstockSpec(`,"adstock":"geometric"`)
	partition.Order.Partition = "geo"
	collide := adstockSpec(`,"adstock":"geometric"`)
	collide.Predictors = append(collide.Predictors, "decay:x")
	cases := []struct {
		name         string
		spec         *types.RegressionSpec
		slot, reason string
	}{
		{"unknown", adstockSpec(`,"adstock":"hyperbolic"`), "params.adstock", reasonKernelUnknown},
		{"empty", adstockSpec(`,"adstock":""`), "params.adstock", reasonKernelUnknown},
		{"weibull_cdf", adstockSpec(`,"adstock":"weibull_cdf","max_lag":6`), "params.adstock", reasonKernelUnsupported},
		{"weibull_pdf", adstockSpec(`,"adstock":"weibull_pdf","max_lag":6`), "params.adstock", reasonKernelUnsupported},
		{"max_lag zero", adstockSpec(`,"adstock":"geometric","max_lag":0`), "params.max_lag", reasonMaxLagInvalid},
		{"max_lag ceiling", adstockSpec(`,"adstock":"geometric","max_lag":10001`), "params.max_lag", reasonMaxLagInvalid},
		{"delayed without max_lag", adstockSpec(`,"adstock":"delayed"`), "params.max_lag", reasonMaxLagRequired},
		{"max_lag without adstock", adstockSpec(`,"max_lag":4`), "params.max_lag", reasonMaxLagWithoutAdstock},
		{"partition", partition, "order", reasonOrderPartitionAdstock},
		{"decay prefix collision", collide, "predictors", ""},
	}
	for _, c := range cases {
		ce := SpecRefusal(c.spec)
		if ce == nil || ce.Code != perr.PROCESSING_CONFIG || ce.Details["slot"] != c.slot {
			t.Fatalf("%s: refusal %v, want PROCESSING_CONFIG slot %s", c.name, ce, c.slot)
		}
		if c.reason != "" && ce.Details["reason"] != c.reason {
			t.Fatalf("%s: reason %v, want %s", c.name, ce.Details["reason"], c.reason)
		}
	}
	// Accepted: geometric with the default window, delayed with one.
	for _, extra := range []string{`,"adstock":"geometric"`, `,"adstock":"delayed","max_lag":4`} {
		if ce := SpecRefusal(adstockSpec(extra)); ce != nil {
			t.Fatalf("%s refused: %v", extra, ce)
		}
	}
}

// TestMMMHillAdstock_IllConditionedFlagged: a channel whose spend barely
// moves cannot separate its decay from its curve — the adstock of a
// near-constant series is near-constant at every decay — so the fit
// still succeeds but its condition number lands above the advisory's
// threshold (or the joint block does not invert: +Inf), with the
// standard errors kept, never refused.
func TestMMMHillAdstock_IllConditionedFlagged(t *testing.T) {
	t.Parallel()

	tr := adstockTruth{kernel: distlagparams.KernelGeometric, maxLag: 8, tvPhi: []float64{0.6}, srPhi: []float64{0.3},
		tvBeta: 12, tvK: 250, srBeta: 6, srK: 60, noise: 0.2}
	rows := adstockSeries(tr, 120, 11)
	rng := rand.New(rand.NewPCG(2, 2))
	for _, r := range rows {
		r["search"] = 100 + float64(rng.Float64()*1e-3)
	}
	out := fitAdstock(t, adstockSpec(`,"adstock":"geometric","max_lag":8`), rows)
	cond, _ := out.Outputs[pmodel.MMMConditionKey].(float64)
	if !(cond > pmodel.MMMConditionThreshold) {
		t.Fatalf("condition_number = %v, want above %v on a near-constant channel", cond, pmodel.MMMConditionThreshold)
	}
}

// TestMMMHillAdstock_KernelWeightsAreTheWindows: the weights the fit
// adstocks with are WIN_DISTRIBUTED_LAG's (distlagparams.Resolve,
// normalize false) bit for bit, so the adstock record reproduces the
// fitted adstock.
func TestMMMHillAdstock_KernelWeightsAreTheWindows(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		kernel, params string
		phi            []float64
	}{
		{distlagparams.KernelGeometric, `{"kernel":"geometric","decay":0.37,"max_lag":7,"normalize":false}`, []float64{0.37}},
		{distlagparams.KernelDelayed, `{"kernel":"delayed","decay":0.62,"peak":2.3,"max_lag":7,"normalize":false}`, []float64{0.62, 2.3}},
	} {
		k, reason := hillKernelFor(c.kernel, 7)
		if reason != "" {
			t.Fatal(reason)
		}
		w, _ := k.Weights(c.phi, 7, true)
		spec, ref := distlagparams.Resolve(json.RawMessage(c.params))
		if ref != nil {
			t.Fatal(ref)
		}
		for l := range w {
			if math.Float64bits(w[l]) != math.Float64bits(spec.Weights[l]) {
				t.Fatalf("%s lag %d: fit weight %v, window %v", c.kernel, l, w[l], spec.Weights[l])
			}
		}
	}
}

// TestMMMHillAdstock_HeldBetaHoldsItsDecay: a channel whose β sits on
// its 0 bound takes its kernel parameters with it — like its k and s,
// their covariance rows are NaN (the data cannot inform the carry-over
// of a channel that adds nothing) — while the free channel's decay keeps
// a finite variance and its joint covariances.
func TestMMMHillAdstock_HeldBetaHoldsItsDecay(t *testing.T) {
	t.Parallel()

	k, _ := hillKernelFor(distlagparams.KernelGeometric, 4)
	p := &problem{y: make([]float64, 20), a: [][]float64{nil, nil}, z: [][]float64{nil}, priorIdx: -1, ad: &hillAdstock{kernel: k}}
	dim := p.dim() // 1 + 3·2 + 1 + 2 = 10
	rows := make([][]float64, dim)
	for i := range rows {
		rows[i] = make([]float64, dim)
		rows[i][i] = 2
		if i > 0 {
			rows[i][i-1], rows[i-1][i] = 0.5, 0.5
		}
	}
	jtj, err := linalg.NewSymFromRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	active := make([]optimize.ActiveBound, dim)
	active[2] = optimize.AtLower // the second channel's β
	res := &optimize.LeastSquaresResult{X: make([]float64, dim), JTJ: jtj, Active: active}
	vcov, cond := p.covariance(res, 1, []float64{1, 1}, []float64{1, 1})
	for _, i := range []int{2, 5, 7, p.phiIndex(0, 1)} {
		if !math.IsNaN(vcov[i][i]) {
			t.Errorf("vcov[%d][%d] = %v, want NaN (held with the second channel's β)", i, i, vcov[i][i])
		}
	}
	d := p.phiIndex(0, 0)
	if !(vcov[d][d] > 0) || math.IsNaN(vcov[d][4]) || !(cond >= 1) || math.IsInf(cond, 0) {
		t.Errorf("free decay variance %v, cov with k %v, condition %v", vcov[d][d], vcov[d][3], cond)
	}
}

// TestMMMHillAdstock_OutputReadsTheSolution: the output re-adstocks at
// the solution, so a last solver evaluation away from it (a rejected
// trial step) never leaks into the fit statistics.
func TestMMMHillAdstock_OutputReadsTheSolution(t *testing.T) {
	t.Parallel()

	tr := adstockTruth{kernel: distlagparams.KernelGeometric, maxLag: 8, tvPhi: []float64{0.6}, srPhi: []float64{0.3},
		tvBeta: 12, tvK: 250, srBeta: 6, srK: 60, noise: 0.2}
	rows := adstockSeries(tr, 156, 3)
	reg, err := Factory(adstockSpec(`,"adstock":"geometric","max_lag":8`), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := reg.(*model)
	want, err := m.Fit(rows)
	if err != nil {
		t.Fatal(err)
	}
	// Refit by hand, then move the adstock away from the solution
	// before the output mapping.
	k, _ := hillKernelFor(distlagparams.KernelGeometric, 8)
	p := &problem{y: make([]float64, len(rows)), a: [][]float64{make([]float64, len(rows)), make([]float64, len(rows))},
		z: [][]float64{make([]float64, len(rows))}, kRef: make([]float64, 2), priorIdx: -1,
		ad: &hillAdstock{kernel: k, spend: make([][]float64, 2), da: make([][][]float64, 2)}}
	for i, r := range rows {
		p.y[i], p.a[0][i], p.a[1][i], p.z[0][i] = r["y"], r["tv"], r["search"], r["price"]
		p.kRef[0] += r["tv"]
		p.kRef[1] += r["search"]
	}
	p.initAdstock()
	x0, err := p.startAdstock(true)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.solve(x0, true, maxIterations)
	if err != nil {
		t.Fatal(err)
	}
	away := slices.Clone(res.X)
	away[p.phiIndex(0, 0)] = 0.1
	p.adstockAt(away, false)
	got := m.output(p, res)
	if *got.Fit.R2 != *want.Fit.R2 || *got.Fit.Deviance != *want.Fit.Deviance {
		t.Fatalf("output after a stray evaluation: r2 %v deviance %v, want %v %v", *got.Fit.R2, *got.Fit.Deviance, *want.Fit.R2, *want.Fit.Deviance)
	}
}

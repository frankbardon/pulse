package sem

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"testing"
)

// The bootstrap references (testdata/brand_boot_*_lavaan.json) are
// lavaan's se = "bootstrap" fits (1000 replicates, percentile
// intervals) written by testdata/fit_lavaan_boot.R. A bootstrap is
// random and the draws differ (R's RNG against Pulse's PCG), so the
// parity is distributional. With B = 1000 a bootstrap SE carries a
// relative Monte-Carlo error of about 1/√(2B) ≈ 2.2%, so two
// independent ones differ by about 3.2% (1 SD): bootTolSE (15%) is
// ~4.7 SD (observed max 11.5% over 114 rows). A 2.5% / 97.5% percentile
// bound carries √(α(1−α)/B)/φ(z_α) ≈ 0.085·SE under normality, two
// independent ones ≈ 0.12·SE apart (observed max 0.36·SE over 228
// bounds), and more on a skewed ratio := (the mediated share: 0.57·SE):
// bootTolCI is 0.75·SE.
const (
	bootTolSE = 0.15
	bootTolCI = 0.75
	bootIters = 1000
	bootSeed  = 20261024
)

type lavaanBootRef struct {
	Lavaan     string `json:"lavaan"`
	Converged  bool   `json:"converged"`
	Successful int    `json:"bootstrap_successful"`
	Options    struct {
		Bootstrap  int      `json:"bootstrap"`
		GroupEqual []string `json:"group_equal"`
	} `json:"options"`
	Params []lavaanGroupParam `json:"params"`
}

func loadLavaanBootRef(t *testing.T, path string) lavaanBootRef {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r lavaanBootRef
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Lavaan == "" || !r.Converged || r.Options.Bootstrap != bootIters || r.Successful != bootIters {
		t.Fatalf("%s: lavaan %q converged %v bootstrap %d successful %d", path, r.Lavaan, r.Converged, r.Options.Bootstrap, r.Successful)
	}
	return r
}

// csvRows are the named columns of a CSV, row-major, split by group
// (group "" keeps one sample of every row) in labels order.
func csvRows(t *testing.T, path, group string, labels []string, cols []string) []BootstrapSample {
	t.Helper()
	if group == "" {
		_, rows := readCSVColumns(t, path, cols)
		var flat []float64
		for _, r := range rows {
			flat = append(flat, r...)
		}
		return []BootstrapSample{{Rows: flat}}
	}
	_, rows := readCSVColumns(t, path, append([]string{group}, cols...))
	out := make([]BootstrapSample, len(labels))
	for g, lab := range labels {
		out[g] = BootstrapSample{Label: lab, Level: g, Reference: g == 0}
		for _, r := range rows {
			if formatLevel(r[0]) == lab {
				out[g].Rows = append(out[g].Rows, r[1:]...)
			}
		}
	}
	return out
}

// checkBootAgainstLavaan holds every bootstrapped row (free and :=) of
// rows to the lavaan reference row with the same key (prefix: the
// group's key prefix) within the sampling tolerances.
func checkBootAgainstLavaan(t *testing.T, rows []Estimate, ref lavaanBootRef, group func(i int) string) {
	t.Helper()
	checkBootAgainstLavaanTol(t, rows, ref, group, bootTolSE, bootTolCI)
}

// checkBootAgainstLavaanTol is checkBootAgainstLavaan at the sampling
// tolerances tolSE (relative, on the SE) and tolCI (in SEs, on a bound).
func checkBootAgainstLavaanTol(t *testing.T, rows []Estimate, ref lavaanBootRef, group func(i int) string, tolSE, tolCI float64) {
	t.Helper()
	byKey := map[string]lavaanParam{}
	for _, p := range ref.Params {
		k := lavaanKey(p.lavaanParam)
		byKey[p.Group+"|"+k] = p.lavaanParam
		if p.Op == OpCovariance && p.LHS != p.RHS {
			byKey[p.Group+"|"+p.RHS+" "+p.Op+" "+p.LHS] = p.lavaanParam
		}
	}
	checked := 0
	for i, e := range rows {
		if !bootstrapped(e) {
			if !math.IsNaN(e.SE) && !e.Free {
				t.Errorf("%s: fixed row carries SE %g", e.Key(), e.SE)
			}
			continue
		}
		g := ""
		if e.Op != OpDefined {
			g = group(i)
		}
		p, ok := byKey[g+"|"+e.Key()]
		if !ok || p.SE == nil {
			t.Fatalf("[%s] %s: no lavaan bootstrap row", g, e.Key())
		}
		checked++
		if d := relTol(e.Value, *p.Est); d > lavaanEstTol {
			t.Errorf("[%s] %s estimate %g, lavaan %g: the point estimate must stay the full data's", g, e.Key(), e.Value, *p.Est)
		}
		if d := math.Abs(e.SE-*p.SE) / *p.SE; !(d <= tolSE) {
			t.Errorf("[%s] %s bootstrap SE %g, lavaan %g (rel %.3f > %.2f)", g, e.Key(), e.SE, *p.SE, d, tolSE)
		}
		for what, pair := range map[string][2]float64{"ci_lower": {e.CILower, *p.CILower}, "ci_upper": {e.CIUpper, *p.CIUpper}} {
			if d := math.Abs(pair[0]-pair[1]) / *p.SE; !(d <= tolCI) {
				t.Errorf("[%s] %s %s %g, lavaan %g (%.2f SE > %.2f)", g, e.Key(), what, pair[0], pair[1], d, tolCI)
			}
		}
		if z := e.Value / e.SE; e.Z != z || e.P != 2*(1-normCDF(math.Abs(z))) {
			t.Errorf("%s: z %g p %g, want the bootstrap SE's Wald z %g and its normal p", e.Key(), e.Z, e.P, z)
		}
		if !(e.CILower < e.CIUpper) {
			t.Errorf("%s: interval [%g, %g]", e.Key(), e.CILower, e.CIUpper)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d rows bootstrapped", checked)
	}
}

// TestSEM_BootstrapDeterministic: the same rows and seed give the same
// bits; another seed other draws.
func TestSEM_BootstrapDeterministic(t *testing.T) {
	desc, err := os.ReadFile("testdata/brand_def_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModelWithOptions(string(desc), ModelOptions{MeanStructure: true})
	if err != nil {
		t.Fatal(err)
	}
	mean, s, n := readCSVMoments(t, "testdata/brand_data.csv", m.Observed)
	fit, err := FitMoments(m, s, s, mean, n, Options{})
	if err != nil {
		t.Fatal(err)
	}
	samples := csvRows(t, "testdata/brand_data.csv", "", nil, m.Observed)
	run := func(seed uint64) *Bootstrap {
		b, err := RunBootstrap(m, samples, Options{}, BootstrapOptions{Iters: 25, Seed: seed, Level: 0.9}, BootstrapRows(fit))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	a, b, c := run(7), run(7), run(8)
	same := func(x, y []float64) bool {
		for i := range x {
			if math.Float64bits(x[i]) != math.Float64bits(y[i]) && !(math.IsNaN(x[i]) && math.IsNaN(y[i])) {
				return false
			}
		}
		return len(x) == len(y)
	}
	if !same(a.SE, b.SE) || !same(a.CILower, b.CILower) || !same(a.CIUpper, b.CIUpper) {
		t.Fatal("the same seed gave different bootstrap figures")
	}
	if same(a.SE, c.SE) {
		t.Fatal("another seed gave the same bootstrap SEs")
	}
}

// TestSEM_BootstrapWithinGroupDraws: a replicate draws each group's n_g
// rows from that group alone.
func TestSEM_BootstrapWithinGroupDraws(t *testing.T) {
	const p = 2
	samples := []BootstrapSample{
		{Rows: []float64{0, 0, 1, 1, 2, 2}},
		{Rows: []float64{10, 10, 11, 11, 12, 12, 13, 13, 14, 14}},
	}
	ns := []int{3, 5}
	draw := [][]float64{make([]float64, 6), make([]float64, 10)}
	rng := rand.New(rand.NewPCG(1, 2))
	for b := 0; b < 50; b++ {
		drawReplicate(rng, samples, ns, p, draw)
		for g, d := range draw {
			if len(d) != ns[g]*p {
				t.Fatalf("group %d drew %d values, want n_g·p = %d", g, len(d), ns[g]*p)
			}
			for i := 0; i < ns[g]; i++ {
				v := d[i*p]
				if d[i*p+1] != v || (g == 0 && v >= 10) || (g == 1 && v < 10) {
					t.Fatalf("group %d replicate %d drew row (%g, %g) from outside the group", g, b, v, d[i*p+1])
				}
			}
		}
	}
}

// TestSEM_BootstrapTooFewSuccesses: replicates that never converge
// leave fewer than BootstrapMinSuccesses — a *BootstrapError after the
// early stop, never figures.
func TestSEM_BootstrapTooFewSuccesses(t *testing.T) {
	desc, err := os.ReadFile("testdata/brand_def_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModelWithOptions(string(desc), ModelOptions{MeanStructure: true})
	if err != nil {
		t.Fatal(err)
	}
	mean, s, n := readCSVMoments(t, "testdata/brand_data.csv", m.Observed)
	opt := Options{MaxIter: 1}
	fit, err := FitMoments(m, s, s, mean, n, opt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = RunBootstrap(m, csvRows(t, "testdata/brand_data.csv", "", nil, m.Observed), opt,
		BootstrapOptions{Iters: 200, Seed: 1, Level: 0.95}, BootstrapRows(fit))
	var be *BootstrapError
	if !errors.As(err, &be) || be.Succeeded != 0 || be.Requested != 200 || be.Attempted != bootstrapEarlyStop {
		t.Fatalf("err %v (%+v), want a BootstrapError after %d attempts", err, be, bootstrapEarlyStop)
	}
}

// TestSEM_BootstrapNormInter pins the percentile endpoint to
// boot:::norm.inter (R 4.6.1, boot 1.3): an extreme rank, an
// interpolated one, an integral one and the top order statistic.
func TestSEM_BootstrapNormInter(t *testing.T) {
	x := []float64{3, 1, 4, 1.5, 9, 2.6, 5, 3.5, 8, 9.7, 7, 9.3}
	for alpha, want := range map[float64]float64{0.025: 1, 0.2: 2.191783, 0.5: 4.5, 0.975: 9.7} {
		if got := normInter(x, alpha); math.Abs(got-want) > 1e-6 {
			t.Errorf("normInter(%g) = %.7g, boot %g", alpha, got, want)
		}
	}
	if x[0] != 3 {
		t.Fatal("normInter sorted its input in place")
	}
}

// TestSEM_BootstrapStopsOnContext: a cancelled context stops the
// bootstrap before its next refit with the context's own error.
func TestSEM_BootstrapStopsOnContext(t *testing.T) {
	desc, err := os.ReadFile("testdata/brand_def_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModelWithOptions(string(desc), ModelOptions{MeanStructure: true})
	if err != nil {
		t.Fatal(err)
	}
	mean, s, n := readCSVMoments(t, "testdata/brand_data.csv", m.Observed)
	fit, err := FitMoments(m, s, s, mean, n, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = RunBootstrap(m, csvRows(t, "testdata/brand_data.csv", "", nil, m.Observed), Options{},
		BootstrapOptions{Iters: 1000, Seed: 1, Level: 0.95, Ctx: ctx}, BootstrapRows(fit))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
}

// TestSEM_BootstrapFiguresFromReplicates replays the replicate stream
// (the same PCG seeding, draws and refits) and recomputes every row's
// figures independently: the SE is the replicates' sample SD (divisor
// B − 1), the bounds norm.inter at (1 ∓ level)/2, z and p the bootstrap
// SE's; a fixed row is left alone.
func TestSEM_BootstrapFiguresFromReplicates(t *testing.T) {
	desc, err := os.ReadFile("testdata/brand_def_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModelWithOptions(string(desc), ModelOptions{MeanStructure: true})
	if err != nil {
		t.Fatal(err)
	}
	mean, s, n := readCSVMoments(t, "testdata/brand_data.csv", m.Observed)
	fit, err := FitMoments(m, s, s, mean, n, Options{})
	if err != nil {
		t.Fatal(err)
	}
	samples := csvRows(t, "testdata/brand_data.csv", "", nil, m.Observed)
	const iters, seed, level = 12, 99, 0.8
	full := BootstrapRows(fit)
	b, err := RunBootstrap(m, samples, Options{}, BootstrapOptions{Iters: iters, Seed: seed, Level: level}, full)
	if err != nil {
		t.Fatal(err)
	}
	p := len(m.Observed)
	ns := []int{len(samples[0].Rows) / p}
	draw := [][]float64{make([]float64, len(samples[0].Rows))}
	rng := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	var reps [][]Estimate
	for i := 0; i < iters; i++ {
		drawReplicate(rng, samples, ns, p, draw)
		rows, ok := bootstrapReplicate(m, samples, draw, ns, Options{}, BootstrapOptions{})
		if !ok {
			t.Fatalf("replicate %d failed", i)
		}
		reps = append(reps, rows)
	}
	rows := BootstrapRows(fit)
	b.Apply(rows)
	for j, e := range full {
		if !bootstrapped(e) {
			if !math.IsNaN(b.SE[j]) || rows[j].SE != e.SE && !math.IsNaN(e.SE) {
				t.Errorf("%s: a fixed row took bootstrap figures", e.Key())
			}
			continue
		}
		col := make([]float64, iters)
		mu := 0.0
		for i := range reps {
			col[i] = reps[i][j].Value
			mu += col[i]
		}
		mu /= iters
		ss := 0.0
		for _, v := range col {
			ss += (v - mu) * (v - mu)
		}
		sd := math.Sqrt(ss / (iters - 1))
		if math.Abs(b.SE[j]-sd) > 1e-12*sd {
			t.Errorf("%s SE %g, replicates' sample SD %g", e.Key(), b.SE[j], sd)
		}
		if lo, hi := normInter(col, (1-level)/2), normInter(col, (1+level)/2); b.CILower[j] != lo || b.CIUpper[j] != hi {
			t.Errorf("%s bounds [%g, %g], norm.inter [%g, %g]", e.Key(), b.CILower[j], b.CIUpper[j], lo, hi)
		}
		r := rows[j]
		if r.SE != b.SE[j] || r.Z != e.Value/b.SE[j] || r.CILower != b.CILower[j] || r.Value != e.Value || r.StdValue != e.StdValue {
			t.Errorf("%s: Apply wrote %+v", e.Key(), r)
		}
	}
}

// TestSEM_BootstrapSampleSD pins the replicate SD's divisor (B − 1).
func TestSEM_BootstrapSampleSD(t *testing.T) {
	if got, want := sampleSD([]float64{1, 2, 3, 4}), math.Sqrt(5.0/3); math.Abs(got-want) > 1e-15 {
		t.Fatalf("sampleSD = %g, want %g", got, want)
	}
}

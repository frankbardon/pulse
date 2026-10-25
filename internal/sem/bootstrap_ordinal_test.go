package sem

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
)

// The ordinal bootstrap reference (testdata/brand_boot_ord_lavaan.json,
// fit_lavaan_boot.R with ordered columns) is lavaan's se = "bootstrap"
// on the brand survey's measurement model with the nine ratings
// ordered: 500 replicates, estimator DWLS — lavaan refuses se =
// "bootstrap" under WLSMV, whose estimates are DWLS's, so a WLSMV
// bootstrap is DWLS's. With B = 500 a bootstrap SE carries ~3.2%
// relative Monte-Carlo error, two independent ones ~4.5% apart (1 SD):
// bootOrdTolSE (20%) is ~4.4 SD; a 2.5% / 97.5% bound ≈ 0.17·SE apart,
// so bootTolCI (0.75·SE) is ~4.4 SD too.
const (
	bootOrdIters = 500
	bootOrdTolSE = 0.20
)

// ordinalBootSample is a single-group ordinal fit's bootstrap input:
// the coded rows, row-major, as one sample.
func ordinalBootSample(in TwoStepInput) []BootstrapSample {
	var flat []float64
	for _, r := range in.Rows {
		flat = append(flat, r...)
	}
	return []BootstrapSample{{Rows: flat}}
}

// TestSEM_BootstrapOrdinalMatchesLavaanSampling holds a seeded
// 500-replicate bootstrap of the WLSMV measurement model (free rows
// and thresholds) to lavaan's DWLS bootstrap within the sampling
// tolerances: every replicate refits the two-step statistics from its
// own rows at the full fit's coding.
func TestSEM_BootstrapOrdinalMatchesLavaanSampling(t *testing.T) {
	b, err := os.ReadFile("testdata/brand_boot_ord_lavaan.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref lavaanBootRef
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatal(err)
	}
	if ref.Lavaan == "" || !ref.Converged || ref.Options.Bootstrap != bootOrdIters || ref.Successful != bootOrdIters {
		t.Fatalf("lavaan %q converged %v bootstrap %d successful %d", ref.Lavaan, ref.Converged, ref.Options.Bootstrap, ref.Successful)
	}
	var wref wlsRef
	readJSON(t, "testdata/brand_ord_wlsmv_lavaan.json", &wref)
	fit := fitWLSFixture(t, wref, "brand_ord")
	in := wlsFixtureInput(t, fit.Model)
	rows := BootstrapRows(fit)
	bs, err := RunBootstrap(fit.Model, ordinalBootSample(in), Options{}, BootstrapOptions{Iters: bootOrdIters, Seed: bootSeed, Level: 0.95,
		Ordinal: &BootstrapOrdinal{Estimator: EstimatorWLSMV, Kinds: in.Kinds, Levels: in.Levels}}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if bs.Requested != bootOrdIters || bs.Succeeded != bootOrdIters {
		t.Fatalf("requested %d succeeded %d", bs.Requested, bs.Succeeded)
	}
	bs.Apply(rows)
	th := 0
	for _, e := range rows {
		if e.Op == OpThreshold && bootstrapped(e) {
			th++
		}
	}
	if th != 36 {
		t.Fatalf("%d threshold rows bootstrapped, want 36", th)
	}
	checkBootAgainstLavaanTol(t, rows, ref, func(int) string { return "" }, bootOrdTolSE, bootTolCI)
}

// wlsFixtureInput is the brand survey's two-step input over m's
// observed order (fitWLSFixture's coding).
func wlsFixtureInput(t *testing.T, m *Model) TwoStepInput {
	t.Helper()
	cols := readCSV(t, "testdata/brand_data.csv")
	groups := ordinalGroupsFromCSV(t, "testdata/brand_data.csv", "wave", m)
	in := TwoStepInput{Kinds: groups[0].In.Kinds, Levels: groups[0].In.Levels}
	// The rows in file order (ordinalGroupsFromCSV split them by wave).
	at := make([]int, len(groups))
	for _, w := range cols["wave"] {
		g := 0
		if formatLevel(w) != groups[0].Label {
			g = 1
		}
		in.Rows = append(in.Rows, groups[g].In.Rows[at[g]])
		at[g]++
	}
	return in
}

// TestSEM_BootstrapOrdinalDeterministic: an ordinal bootstrap — single
// group, and the two-wave fit under threshold + loading invariance
// (rows drawn within each wave) — gives the same bits for the same
// seed and other draws for another.
func TestSEM_BootstrapOrdinalDeterministic(t *testing.T) {
	var ref wlsGroupRef
	readJSON(t, "testdata/brand_ordmg_thload_lavaan.json", &ref)
	gm, err := NewGroupModel(readModel(t, "testdata/brand_ord_model.txt"), ModelOptions{Ordered: ref.Options.Ordered, GroupEqual: ref.Options.GroupEqual})
	if err != nil {
		t.Fatal(err)
	}
	groups := ordinalGroupsFromCSV(t, "testdata/brand_data.csv", "wave", gm)
	groups[0].Reference = true
	gf, err := FitOrdinalGroups(gm, groups, EstimatorWLSMV, Options{})
	if err != nil {
		t.Fatal(err)
	}
	gsamples := make([]BootstrapSample, len(groups))
	for g, gr := range groups {
		gsamples[g] = ordinalBootSample(gr.In)[0]
		gsamples[g].Label, gsamples[g].Level, gsamples[g].Reference = gr.Label, gr.Level, gr.Reference
	}
	var wref wlsRef
	readJSON(t, "testdata/brand_ord_wlsmv_lavaan.json", &wref)
	fit := fitWLSFixture(t, wref, "brand_ord")
	in := wlsFixtureInput(t, fit.Model)
	for _, c := range []struct {
		name    string
		m       *Model
		samples []BootstrapSample
		rows    []Estimate
	}{
		{"single", fit.Model, ordinalBootSample(in), BootstrapRows(fit)},
		{"groups", gm, gsamples, BootstrapGroupRows(gf)},
	} {
		run := func(seed uint64) *Bootstrap {
			b, err := RunBootstrap(c.m, c.samples, Options{}, BootstrapOptions{Iters: 12, Seed: seed, Level: 0.9,
				Ordinal: &BootstrapOrdinal{Estimator: EstimatorWLSMV, Kinds: in.Kinds, Levels: in.Levels}}, c.rows)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		a, b, d := run(7), run(7), run(8)
		same := func(x, y []float64) bool {
			for i := range x {
				if math.Float64bits(x[i]) != math.Float64bits(y[i]) && !(math.IsNaN(x[i]) && math.IsNaN(y[i])) {
					return false
				}
			}
			return len(x) == len(y)
		}
		if !same(a.SE, b.SE) || !same(a.CILower, b.CILower) || !same(a.CIUpper, b.CIUpper) || a.Succeeded != 12 {
			t.Fatalf("%s: the same seed gave different bootstrap figures (succeeded %d)", c.name, a.Succeeded)
		}
		if same(a.SE, d.SE) {
			t.Fatalf("%s: another seed gave the same bootstrap SEs", c.name)
		}
		// Every scale-factor row of the reference wave is fixed (no
		// bootstrap figure); the other wave's are free.
		if c.name == "groups" {
			for j, e := range c.rows {
				if e.Op == OpScale && e.Free == math.IsNaN(a.SE[j]) {
					t.Errorf("%s: free %v, bootstrap SE %g", e.Key(), e.Free, a.SE[j])
				}
			}
		}
	}
}

// TestSEM_BootstrapOrdinalLostCategory: a replicate that loses a
// category of the full fit's coding fails (lavaan's empty-category
// refusal) and is skipped — a category held by one row is lost by most
// replicates, so fewer than BootstrapMinSuccesses refit.
func TestSEM_BootstrapOrdinalLostCategory(t *testing.T) {
	m, err := NewModelWithOptions("f =~ x1 + x2 + x3", ModelOptions{Ordered: []string{"x1", "x2", "x3"}})
	if err != nil {
		t.Fatal(err)
	}
	in := TwoStepInput{Kinds: []OrdinalKind{KindOrdered, KindOrdered, KindOrdered}, Levels: []int{3, 3, 3}}
	for i := 0; i < 300; i++ {
		a := float64(i % 2)
		in.Rows = append(in.Rows, []float64{a, float64((i / 2) % 2), float64((i/2 + i) % 2)})
	}
	for v := range in.Levels {
		in.Rows[v][v] = 2 // one row in each column's top category
	}
	fit, err := FitOrdinal(m, in, EstimatorWLSMV, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = RunBootstrap(m, ordinalBootSample(in), Options{}, BootstrapOptions{Iters: 60, Seed: 3, Level: 0.95,
		Ordinal: &BootstrapOrdinal{Estimator: EstimatorWLSMV, Kinds: in.Kinds, Levels: in.Levels}}, BootstrapRows(fit))
	var be *BootstrapError
	if !errors.As(err, &be) || be.Succeeded >= BootstrapMinSuccesses {
		t.Fatalf("err %v (%+v), want a BootstrapError: most replicates lose a category", err, be)
	}
	if !strings.Contains(err.Error(), "successful replicates") {
		t.Errorf("message %q", err)
	}
}

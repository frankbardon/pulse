//go:build heavy

package sem

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

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

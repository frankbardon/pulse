package sem

import (
	"errors"
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

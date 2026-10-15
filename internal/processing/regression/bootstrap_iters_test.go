package regression

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestBootstrap_ItersHonouredAndEchoed pins #305: bootstrap_iters is
// the replicate count as given (never floored to the default), <= 0
// means the default 1,000, and the result echoes the requested count
// beside the replicates that actually refit — a failed replicate is
// counted out of BootstrapSucceeded, never silently.
func TestBootstrap_ItersHonouredAndEchoed(t *testing.T) {
	records := makeLinearRecords(t, 60, []float64{1.5, -0.7}, 2, 1, 11)
	preds := predictorNames(2)
	base := fitBase(t, records, preds)
	cases := []struct {
		name          string
		iters         int
		failEvery     int
		wantRequested int
		wantSucceeded int
	}{
		{"as given", 200, 0, 200, 200},
		{"below the old floor", 20, 0, 20, 20},
		{"zero defaults", 0, 0, defaultBootstrapIters, defaultBootstrapIters},
		{"negative defaults", -5, 0, defaultBootstrapIters, defaultBootstrapIters},
		{"failed replicates counted", 200, 4, 200, 150},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: preds,
				Resample: "bootstrap", BootstrapIters: tc.iters, RNGSeed: 7}
			var reps [][]float64
			res, err := runBootstrap(spec, records, preds, base, recordingFitter(preds, spec, tc.failEvery, &reps), waldZTwoSidedP)
			if err != nil {
				t.Fatalf("runBootstrap: %v", err)
			}
			if len(reps) != tc.wantSucceeded {
				t.Fatalf("ran %d successful replicates, want %d", len(reps), tc.wantSucceeded)
			}
			if res.BootstrapRequested != tc.wantRequested || res.BootstrapSucceeded != tc.wantSucceeded {
				t.Errorf("echo requested=%d succeeded=%d, want %d / %d",
					res.BootstrapRequested, res.BootstrapSucceeded, tc.wantRequested, tc.wantSucceeded)
			}
		})
	}
}

// TestJackknife_NoBootstrapEcho: the bootstrap echo is bootstrap-only;
// a jackknife result leaves both counts zero (absent on the wire).
func TestJackknife_NoBootstrapEcho(t *testing.T) {
	records := makeLinearRecords(t, 30, []float64{1.5}, 0.3, 0.4, 3)
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}, Resample: "jackknife", BootstrapIters: 50}
	res := runOLSWithModifier(t, spec, records)
	if res.BootstrapRequested != 0 || res.BootstrapSucceeded != 0 {
		t.Errorf("jackknife carries a bootstrap echo: %d / %d", res.BootstrapRequested, res.BootstrapSucceeded)
	}
}

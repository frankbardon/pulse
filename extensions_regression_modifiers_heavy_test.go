//go:build heavy

package pulse_test

import (
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// TestExtensions_RegressionResampleDeterministic: a bootstrap at
// rng_seed 0 (the data-derived seed) and a jackknife are bit-identical
// across runs and across DecodeWorkers, streaming and buffered. Ten
// replicates: the bits under test are the seed's and the row order's,
// whatever the count (each refit is a full pass over the 50,000-row
// cohort the decode fan-out needs).
func TestExtensions_RegressionResampleDeterministic(t *testing.T) {
	t.Parallel()

	large := parityLargeCohort(t.TempDir())(t)
	specs := []*types.RegressionSpec{
		{Type: regParityOLS, Name: "s", Target: "score", Predictors: []string{"qty", "day"}, Resample: "bootstrap", BootstrapIters: 10},
		{Type: regParityOLSBuf, Name: "b", Target: "score", Predictors: []string{"qty"}, Resample: "jackknife", JackknifeGroups: 10},
	}
	run := func(t *testing.T, workers int) string {
		t.Helper()
		p, err := pulse.New(pulse.Options{DataDir: large, DecodeWorkers: workers, Extensions: pulse.Extensions{Regressions: modifierRegistrations()}})
		if err != nil {
			t.Fatal(err)
		}
		resp := processE2E(t, p, &types.Request{Cohort: &types.Cohort{Filename: "large.pulse"}, Regressions: specs})
		for _, r := range resp.Regressions {
			if r == nil || r.Error != nil || r.Resample == "" {
				t.Fatalf("regression failed: %+v", r)
			}
		}
		return mustJSON(t, resp.Regressions)
	}
	want := run(t, 1)
	if again := run(t, 1); again != want {
		t.Fatalf("a rerun differs:\n%s\n%s", again, want)
	}
	for _, w := range []int{2, 4, 7} {
		if got := run(t, w); got != want {
			t.Errorf("DecodeWorkers %d differs from 1:\n%s\n%s", w, got, want)
		}
	}
}

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestProcess_BootstrapItersEchoed pins #305 end to end: the
// bootstrap_iters a request asks for is the replicate count the fit
// runs and echoes (bootstrap_requested), beside the replicates that
// refit (bootstrap_succeeded), on the wire.
func TestProcess_BootstrapItersEchoed(t *testing.T) {
	t.Parallel()

	p, _, cohort := acceptanceCohort(t)
	for _, tc := range []struct {
		iters, want int
	}{{200, 200}, {0, 1000}} {
		body := `{"regressions":[{"type":"REG_OLS","name":"r","target":"x","predictors":["t_f64"],` +
			`"resample":"bootstrap","rng_seed":3,"bootstrap_iters":` + itoa(tc.iters) + `}]}`
		resp, err := p.Process(context.Background(), categoricalRequest(t, body, cohort))
		if err != nil {
			t.Fatalf("iters %d: %v", tc.iters, err)
		}
		r := resp.Regressions[0]
		if r.BootstrapRequested != tc.want || r.BootstrapSucceeded < 10 || r.BootstrapSucceeded > tc.want {
			t.Errorf("iters %d: requested %d succeeded %d, want %d requested", tc.iters, r.BootstrapRequested, r.BootstrapSucceeded, tc.want)
		}
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"bootstrap_requested":`+itoa(tc.want)) || !strings.Contains(string(b), `"bootstrap_succeeded":`) {
			t.Errorf("wire form lacks the echo: %s", b)
		}
		// bootstrap_skipped is absent exactly when no replicate was
		// skipped (U68 E2-S1): existing outputs stay byte-identical.
		if skipped := r.BootstrapRequested - r.BootstrapSucceeded; strings.Contains(string(b), `"bootstrap_skipped":`) != (skipped > 0) ||
			(skipped > 0 && !strings.Contains(string(b), `"bootstrap_skipped":`+itoa(skipped))) {
			t.Errorf("bootstrap_skipped on the wire disagrees with %d skipped: %s", skipped, b)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

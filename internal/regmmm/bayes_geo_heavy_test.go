//go:build heavy

package regmmm

import (
	"context"
	"math"
	"testing"

	pmodel "github.com/frankbardon/pulse/internal/model"
)

// TestMMMBayes_GeoRecovery: on the simulated three-geo panel (two
// channels, each geo with its own tv decay and both channels' effects,
// one shared curve per channel) the hierarchical fit recovers each
// geo's and the all-geo ROI per channel and orders the geos' tv decay
// as the truth does (geo 1 below geo 3, each within 0.25 – 0.75 around
// truths of 0.4 – 0.6) — the partial pooling neither collapses the geos
// to one value nor leaves them apart from the data. The sampler is
// healthy enough to read (at most 1% of transitions diverge). Tolerances:
// 10% on the all-geo ROI, 20% on a geo's.
func TestMMMBayes_GeoRecovery(t *testing.T) {
	t.Parallel()

	sim := simulateGeoPanel(7)
	reg := geoRegression(t, geoParams+`,"chains":2,"warmup":400,"draws":300}`)
	reg.cfg.seed = 11
	out, err := reg.FitContext(context.Background(), sim.probe())
	if err != nil {
		t.Fatal(err)
	}
	roi := out.Outputs["roi"].(map[string]any)
	mean := func(m map[string]any, key string) float64 { return m[key].(map[string]float64)["mean"] }
	for ch, name := range []string{"tv", "search"} {
		got, want := mean(roi, name), sim.pooledROI[ch]
		t.Logf("%s all-geo ROI %.4f, truth %.4f", name, got, want)
		if math.Abs(got-want) > 0.1*want {
			t.Errorf("%s all-geo ROI %.4f, truth %.4f (> 10%%)", name, got, want)
		}
		for g := range geoSimGeos {
			key := pmodel.MMMGeoTerm(name, string(rune('1'+g)))
			got, want := mean(roi, key), sim.roi[g][ch]
			t.Logf("%s ROI %.4f, truth %.4f", key, got, want)
			if math.Abs(got-want) > 0.2*want {
				t.Errorf("%s ROI %.4f, truth %.4f (> 20%%)", key, got, want)
			}
		}
	}
	for _, name := range []string{"tv", "search"} {
		if lo, hi := mean(roi, name+"[1]"), mean(roi, name+"[3]"); !(lo < hi) {
			t.Errorf("%s ROI geo 1 %.4f not below geo 3 %.4f (truth ordered)", name, lo, hi)
		}
	}
	coef := map[string]float64{}
	for _, c := range out.Coefficients {
		coef[c.Name] = c.Estimate
	}
	d1, d2, d3 := coef["decay:tv[1]"], coef["decay:tv[2]"], coef["decay:tv[3]"]
	t.Logf("tv decay %.3f %.3f %.3f (truth 0.4 0.5 0.6), population %.3f, tau %.3f", d1, d2, d3, coef["decay:tv"], coef["tau:decay:tv"])
	if !(d1 < d3) {
		t.Errorf("tv decay geo 1 %.3f not below geo 3 %.3f", d1, d3)
	}
	for _, d := range []float64{d1, d2, d3} {
		if d < 0.25 || d > 0.75 {
			t.Errorf("tv decay %.3f outside [0.25, 0.75]", d)
		}
	}
	diag := out.Outputs["diagnostics"].(map[string]any)
	if n := diag["divergences"].(int); n > 6 {
		t.Errorf("%d of 600 transitions diverged (> 1%%)", n)
	}
	t.Logf("max rhat %v, min ess bulk %v", diag["max_rhat"], diag["min_ess_bulk"])
	h := out.Outputs["hierarchy"].(map[string]any)
	if h["geo"] != "geo" {
		t.Errorf("hierarchy.geo %v", h["geo"])
	}
}

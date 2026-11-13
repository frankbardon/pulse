//go:build heavy

package regmmm

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"testing"

	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// TestMMMBayes_DrawsAbsentDigest: params.return_draws false is the
// draws-free fit — the geometric Hill digest of
// TestMMMBayes_GeometricDefaultDigest and the geo panel digest of
// TestMMMBayes_GeoPerCurvesAbsentDigest under the same explicit seed —
// so the key adds nothing, not even an empty output, when off. (Absent
// is those tests' own rows.)
func TestMMMBayes_DrawsAbsentDigest(t *testing.T) {
	t.Parallel()

	const one = `{"channels":["tv"],"time":"week","chains":1,"warmup":40,"draws":30,"return_draws":false}`
	if got, want := fitDigest(t, one, 99), "e1b68270343c09c00e6ceafdfd4c76aa2af90875bb6a6c648a4a7e05607662b0"; got != want {
		t.Errorf("one market: digest %s, want %s", got, want)
	}
	geo := geoParams + `,"chains":1,"warmup":20,"draws":10,"return_draws":false}`
	if got, want := geoFitDigest(t, geo, 99), "c49dfdc172bf47df36f360c17a39fd1d95c1afe06c1fda2856c38538e1695209"; got != want {
		t.Errorf("geo panel: digest %s, want %s", got, want)
	}
}

// TestMMMBayes_DrawsReproduceSummaries: with params.return_draws (draws
// thin 1) every output but outputs.draws is the draws-free fit, bit for
// bit (the pinned digests), and the returned draws, pooled chain order
// then draw order, summarise to exactly the reported outputs.params and
// outputs.roi entries — one market, and a geo panel's per-geo entries.
func TestMMMBayes_DrawsReproduceSummaries(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		spec   *types.RegressionSpec
		rows   extend.Rows
		digest string
	}{
		{"one market", BayesSpec(`{"channels":["tv"],"time":"week","chains":1,"warmup":40,"draws":30,"return_draws":true}`), digestRows(),
			"e1b68270343c09c00e6ceafdfd4c76aa2af90875bb6a6c648a4a7e05607662b0"},
		{"geo panel", geoSpec(geoParams + `,"chains":1,"warmup":20,"draws":10,"return_draws":true}`), geoShortRows(),
			"c49dfdc172bf47df36f360c17a39fd1d95c1afe06c1fda2856c38538e1695209"},
	} {
		tc.spec.RNGSeed = 99
		reg, err := BayesFactory(tc.spec, nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := reg.(extend.ContextRegression).FitContext(context.Background(), tc.rows)
		if err != nil {
			t.Fatal(err)
		}
		draws, ok := out.Outputs["draws"].(map[string]any)
		if !ok {
			t.Fatalf("%s: no outputs.draws", tc.name)
		}
		level := reg.(*bayesRegression).cfg.credibleLevel
		names := draws["names"].([]string)
		coefs := draws["draws"].([][][]float64)
		params := out.Outputs["params"].(map[string]any)
		if len(names) != len(out.Coefficients) || len(names) != len(params) {
			t.Fatalf("%s: %d names, %d coefficients, %d params summaries", tc.name, len(names), len(out.Coefficients), len(params))
		}
		for i, name := range names {
			if got, want := summarize(pool(paramChains(coefs, i)), level), params[name].(map[string]float64); !maps.Equal(got, want) {
				t.Errorf("%s: %s draws summarise to %v, outputs.params %v", tc.name, name, got, want)
			}
		}
		roi := out.Outputs["roi"].(map[string]any)
		roiDraws := draws["roi"].(map[string]any)
		if len(roiDraws) != len(roi) {
			t.Fatalf("%s: %d roi draw entries, %d roi summaries", tc.name, len(roiDraws), len(roi))
		}
		for name, d := range roiDraws {
			if got, want := summarize(pool(d.([][]float64)), level), roi[name].(map[string]float64); !maps.Equal(got, want) {
				t.Errorf("%s: roi %s draws summarise to %v, outputs.roi %v", tc.name, name, got, want)
			}
		}

		delete(out.Outputs, "draws")
		body, err := types.MarshalFinite(out)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(body)); got != tc.digest {
			t.Errorf("%s: the fit without its draws is digest %s, want the draws-free %s", tc.name, got, tc.digest)
		}
	}
}

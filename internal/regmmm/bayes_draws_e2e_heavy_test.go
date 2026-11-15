//go:build heavy

package regmmm_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/jsonfinite"
	"github.com/frankbardon/pulse/internal/regmmm"
)

// TestMMMBayes_DrawsEndToEnd: through the engine on a stock instance, a
// fit with params.return_draws and draws_thin 4 returns outputs.draws
// with 2 chains x every coefficient x ⌈100 / 4⌉ draws, the coefficient
// table's names and thin 4; the wire form (the Response's JSON) decodes
// back to the same values bit for bit; and a two-market panel's draws
// carry the per-geo roi entries beside the all-geo one.
func TestMMMBayes_DrawsEndToEnd(t *testing.T) {
	t.Parallel()

	p := bayesInstance(t, nil)
	writeBayesCohort(t, p, "panel.pulse", encoding.FieldTypeU16, panelRows())
	for _, tc := range []struct {
		file, params string
		roi          []string
	}{
		{regmmm.BayesCohort, `{"channels":["tv"],"time":"week","chains":2,"warmup":150,"draws":100,"return_draws":true,"draws_thin":4}`, []string{"tv"}},
		{"panel.pulse", `{"channels":["tv"],"time":"week","geo":"geo","chains":2,"warmup":150,"draws":100,"return_draws":true,"draws_thin":4}`, []string{"tv", "tv[east]", "tv[west]"}},
	} {
		resp := process(t, p, bayesRequest(tc.file, regmmm.BayesSpec(tc.params)))
		r := resp.Regressions[0]
		if r.Error != nil {
			t.Fatalf("%s: %s", tc.file, mustJSON(t, r))
		}
		draws := r.Outputs["draws"].(map[string]any)
		names := draws["names"].([]string)
		coefs := draws["draws"].([][][]float64)
		if len(coefs) != 2 || draws["thin"] != 4 {
			t.Fatalf("%s: %d chains, thin %v", tc.file, len(coefs), draws["thin"])
		}
		for ch := range coefs {
			if len(coefs[ch]) != len(names) {
				t.Fatalf("%s: chain %d holds %d coefficients, names %d", tc.file, ch, len(coefs[ch]), len(names))
			}
			for _, d := range coefs[ch] {
				if len(d) != 25 {
					t.Fatalf("%s: %d kept draws, want 25", tc.file, len(d))
				}
			}
		}
		var want []string
		for name := range r.Coefficients {
			want = append(want, name)
		}
		got := slices.Clone(names)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: draws names %v, coefficients %v", tc.file, got, want)
		}
		roi := draws["roi"].(map[string]any)
		var roiNames []string
		for name := range roi {
			roiNames = append(roiNames, name)
		}
		slices.Sort(roiNames)
		if !slices.Equal(roiNames, tc.roi) {
			t.Errorf("%s: roi draw entries %v, want %v", tc.file, roiNames, tc.roi)
		}

		body, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		var back struct {
			Regressions []struct {
				Outputs struct {
					Draws struct {
						Names []string               `json:"names"`
						Draws [][][]float64          `json:"draws"`
						ROI   map[string][][]float64 `json:"roi"`
						Thin  int                    `json:"thin"`
					} `json:"draws"`
				} `json:"outputs"`
			} `json:"regressions"`
		}
		if err := jsonfinite.Unmarshal(body, &back); err != nil {
			t.Fatal(err)
		}
		wire := back.Regressions[0].Outputs.Draws
		if !slices.Equal(wire.Names, names) || wire.Thin != 4 {
			t.Fatalf("%s: wire names %v thin %d", tc.file, wire.Names, wire.Thin)
		}
		for ch := range coefs {
			for i := range coefs[ch] {
				if !slices.Equal(wire.Draws[ch][i], coefs[ch][i]) {
					t.Fatalf("%s: chain %d %s: wire %v, engine %v", tc.file, ch, names[i], wire.Draws[ch][i], coefs[ch][i])
				}
			}
		}
		for name, d := range roi {
			for ch, v := range d.([][]float64) {
				if !slices.Equal(wire.ROI[name][ch], v) {
					t.Fatalf("%s: roi %s chain %d differs on the wire", tc.file, name, ch)
				}
			}
		}
	}
}

package service

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Mean overlays on a SLOT-ONLY weighted host (weighting-inferential
// E3-S1). The crosstab cell carries its own weight and nothing else in
// the request does — the host shape U11 let run on raw row counts. The
// weighted-host rule reads N* off the cell's floor keys, so under kind
// frequency every layer equals the physically expanded cohort's
// unweighted layer, and under kind probability rescaling every weight
// leaves the layer unchanged (N* is n_eff). E3-S3 owns the full parity
// and reference harness; this pins the slot-only fix.

const meanOverlayXT = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"h"}],"columns":[{"type":"GROUP_CATEGORY","field":"k"}],` +
	`"cell":{"type":"AGG_WELFORD","field":"y","label":"cell"%W}}`

func meanOverlayProcess(path, weight string) string {
	return `{"cohort":{"filename":"` + path + `"},` + strings.ReplaceAll(meanOverlayXT, "%W", weight) +
		`,"overlays":[{"name":"pw","kind":"OVERLAY_PAIRWISE_WELCH_T","scope":"row"}]}`
}

func meanOverlayCompose(path, weight string) string {
	xt := strings.ReplaceAll(meanOverlayXT, "%W", weight)
	return `{"requests":[{"label":"total","cohort":{"filename":"` + path + `"},` + xt + `},` +
		`{"label":"sub","cohort":{"filename":"` + path + `"},"filterers":[{"type":"FILTER_EXPRESSION","expression":"id % 5 != 0"}],` + xt + `}],` +
		`"overlays":[{"name":"t","kind":"OVERLAY_T_CELL","scope":"cell","reference":"total","targets":["sub"]},` +
		`{"name":"z","kind":"OVERLAY_Z_CELL","scope":"cell","reference":"total","targets":["sub"]}]}`
}

// meanOverlayLayers runs the Process host and the Compose host and
// returns their layers in order: PAIRWISE_WELCH_T, T_CELL, Z_CELL.
func meanOverlayLayers(t *testing.T, store *parityStore, weight string) []types.OverlayLayer {
	t.Helper()
	svc := New(store.mem)
	svc.SetShardWorkers(1)
	svc.SetDecodeWorkers(1)
	path := store.paths[paritySingleFile]
	var req types.Request
	if err := json.Unmarshal([]byte(meanOverlayProcess(path, weight)), &req); err != nil {
		t.Fatal(err)
	}
	resp, err := svc.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	var creq types.ComposedRequest
	if err := json.Unmarshal([]byte(meanOverlayCompose(path, weight)), &creq); err != nil {
		t.Fatal(err)
	}
	cresp, err := svc.Compose(context.Background(), &creq)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return append(append([]types.OverlayLayer{}, resp.Overlays...), cresp.Overlays...)
}

// layerPValues flattens a matrix layer's present cells (NaN for absent).
func layerPValues(t *testing.T, l types.OverlayLayer) []float64 {
	t.Helper()
	var out []float64
	for _, row := range l.Payload.Matrix.Cells {
		for _, c := range row {
			v := math.NaN()
			if c.Present {
				v = c.Value.(float64)
			}
			out = append(out, v)
		}
	}
	return out
}

func assertPValuesClose(t *testing.T, where string, got, want []float64, tol float64) (present int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d cells, want %d", where, len(got), len(want))
	}
	for i := range want {
		if math.IsNaN(want[i]) != math.IsNaN(got[i]) {
			t.Fatalf("%s[%d]: presence %v, want %v", where, i, got[i], want[i])
		}
		if math.IsNaN(want[i]) {
			continue
		}
		present++
		if math.Abs(got[i]-want[i]) > tol*math.Max(math.Abs(want[i]), 1e-300) {
			t.Errorf("%s[%d]: %.17g, want %.17g", where, i, got[i], want[i])
		}
	}
	return present
}

func TestWeightMeanOverlays_SlotOnlyHost(t *testing.T) {
	weighted := newParityStore(t, "mo_weighted", func(n int) []parityRow { return parityRows(n, freqWeight) })
	expanded := newParityStore(t, "mo_expanded", func(n int) []parityRow { return expandRows(parityRows(n, freqWeight)) })
	scaled := newParityStore(t, "mo_scaled", func(n int) []parityRow {
		return parityRows(n, func(i int) float64 { return freqWeight(i) * 0.37 })
	})
	const freq = `,"weight":{"field":"w","kind":"frequency"}`
	const prob = `,"weight":{"field":"w","kind":"probability"}`
	kinds := []types.OverlayKind{types.OverlayKindPairwiseWelchT, types.OverlayKindTCell, types.OverlayKindZCell}

	t.Run("frequency equals expansion", func(t *testing.T) {
		got := meanOverlayLayers(t, weighted, freq)
		want := meanOverlayLayers(t, expanded, "")
		raw := meanOverlayLayers(t, weighted, "")
		for i, k := range kinds {
			if got[i].Kind != k {
				t.Fatalf("layer %d is %s, want %s", i, got[i].Kind, k)
			}
			gp := layerPValues(t, got[i])
			if n := assertPValuesClose(t, string(k), gp, layerPValues(t, want[i]), 1e-9); n == 0 {
				t.Fatalf("%s: no p-value compared", k)
			}
			if equalFloats(gp, layerPValues(t, raw[i])) {
				t.Errorf("%s: the weighted layer equals the unweighted one", k)
			}
			if got[i].Summary.Parameters["sum_weights"] <= 0 || got[i].Summary.Parameters["n_eff"] != 0 {
				t.Errorf("%s: Parameters %v, want sum_weights only", k, got[i].Summary.Parameters)
			}
			if want[i].Summary.Parameters != nil {
				t.Errorf("%s: unweighted host carries Parameters %v", k, want[i].Summary.Parameters)
			}
		}
	})

	t.Run("probability scale invariance", func(t *testing.T) {
		got := meanOverlayLayers(t, scaled, prob)
		want := meanOverlayLayers(t, weighted, prob)
		freqLayers := meanOverlayLayers(t, weighted, freq)
		for i, k := range kinds {
			gp := layerPValues(t, got[i])
			if n := assertPValuesClose(t, string(k), gp, layerPValues(t, want[i]), 1e-9); n == 0 {
				t.Fatalf("%s: no p-value compared", k)
			}
			if equalFloats(layerPValues(t, want[i]), layerPValues(t, freqLayers[i])) {
				t.Errorf("%s: the probability layer equals the frequency one — n_eff never reached it", k)
			}
			gpar, wpar := got[i].Summary.Parameters, want[i].Summary.Parameters
			if math.Abs(gpar["sum_weights"]-0.37*wpar["sum_weights"]) > 1e-9*wpar["sum_weights"] ||
				math.Abs(gpar["n_eff"]-wpar["n_eff"]) > 1e-9*wpar["n_eff"] || wpar["n_eff"] <= 0 {
				t.Errorf("%s: Parameters %v vs %v, want sum_weights × 0.37 and n_eff unchanged", k, gpar, wpar)
			}
		}
	})
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] && !(math.IsNaN(a[i]) && math.IsNaN(b[i])) {
			return false
		}
	}
	return true
}

package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// TestComposeCellMoment_PopulationVarianceCellsMatchWelford (E4-S4):
// OVERLAY_T_CELL / _Z_CELL read {mean, variance, n} off each slot's
// crosstab cells. AGG_VARIANCE / AGG_STDDEV cells carry the POPULATION
// variance m2 / n, AGG_WELFORD the sample variance m2 / (n - 1) the
// tests are defined on; the Compose dispatcher now hands each handler
// its slots' cell types, so a population cell is converted
// (overlayhost.SampleVariance) and the layer equals the layer over
// AGG_WELFORD cells on the fused, buffered and shard-archive arms,
// serial and parallel Compose. It used to test on the population
// variance (p-values off by far more than rounding).
//
// Slot b drops ids, so its means differ from slot a's and its south/F
// and west/F cells hold one record each (n < 2: undefined, a null cell
// + PULSE_OVERLAY_REF_ZERO, exactly as over AGG_WELFORD).
//
//   - "contiguous": every slot-b cell is a run of consecutive ids, where
//     both accumulators' m2 is exact — the layers (p-values, warnings,
//     summary) are equal bit for bit.
//   - "scattered": AGG_WELFORD's running m2 and AGG_VARIANCE's m2 may
//     round apart in the last bit (different accumulation, not the
//     conversion), so p-values are compared to 1e-12 relative.
func TestComposeCellMoment_PopulationVarianceCellsMatchWelford(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	buffered, err := pulse.New(pulse.Options{FS: f.fs, DisableCrosstabFusion: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	slot := func(label, cohort string, cell types.AggregationType, exclude []string) *types.Request {
		r := &types.Request{
			Label:  label,
			Cohort: &types.Cohort{Filename: cohort},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}},
				Cell:    &types.Aggregation{Type: cell, Field: "score", Label: "v"},
			},
		}
		if exclude != nil {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXCLUDE, Field: "id", Values: exclude}}
		}
		return r
	}
	compose := func(kind types.OverlayKind, cell types.AggregationType, cohort string, exclude []string) *pulse.ComposedRequest {
		return &pulse.ComposedRequest{
			Requests: []*types.Request{slot("a", cohort, cell, nil), slot("b", cohort, cell, exclude)},
			Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: kind, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}}},
		}
	}
	cells := func(t *testing.T, out *pulse.ComposedResponse) [][]types.MatrixCell {
		t.Helper()
		if out == nil || len(out.Overlays) != 1 || out.Overlays[0].Payload.Matrix == nil {
			t.Fatalf("want one matrix layer, got %+v", out)
		}
		scored, undefined := 0, 0
		for _, row := range out.Overlays[0].Payload.Matrix.Cells {
			for _, c := range row {
				if !c.Present {
					continue
				}
				if v, ok := c.Value.(float64); ok && !math.IsNaN(v) {
					scored++
				} else {
					undefined++
				}
			}
		}
		if scored == 0 || undefined == 0 {
			t.Fatalf("fixture must score cells and hold an n < 2 cell: %d scored, %d undefined", scored, undefined)
		}
		return out.Overlays[0].Payload.Matrix.Cells
	}
	asJSON := func(t *testing.T, v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	fixtures := []struct {
		name    string
		exclude []string
		exact   bool
	}{
		{"contiguous", []string{"1", "12", "14", "15", "16", "22", "24", "25", "30"}, true},
		{"scattered", []string{"1", "9", "14", "15", "16", "18", "24", "25", "27"}, false},
	}
	arms := []struct {
		name   string
		inst   *pulse.Pulse
		cohort string
	}{
		{"fused", f.p, f.cohort},
		{"buffered", buffered, f.cohort},
		{"shards", f.shard, f.archive},
	}
	runs := []struct {
		name string
		run  func(p *pulse.Pulse, req *pulse.ComposedRequest) (*pulse.ComposedResponse, error)
	}{
		{"serial", func(p *pulse.Pulse, req *pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
			return p.Compose(ctx, req)
		}},
		{"parallel", func(p *pulse.Pulse, req *pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
			return p.ComposeParallel(ctx, req, pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
		}},
	}
	for _, fx := range fixtures {
		for _, kind := range []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindZCell} {
			for _, arm := range arms {
				for _, run := range runs {
					ref, err := run.run(arm.inst, compose(kind, types.AGG_WELFORD, arm.cohort, fx.exclude))
					if err != nil {
						t.Fatal(err)
					}
					wantCells := cells(t, ref)
					for _, cell := range []types.AggregationType{types.AGG_VARIANCE, types.AGG_STDDEV} {
						name := fx.name + " " + arm.name + " " + run.name + " " + string(kind) + " over " + string(cell)
						env, err := arm.inst.PredictCompose(ctx, compose(kind, cell, arm.cohort, fx.exclude))
						if err != nil || len(env.Errors) != 0 {
							t.Fatalf("%s: PredictCompose = %v %v", name, err, envCodes(env))
						}
						out, err := run.run(arm.inst, compose(kind, cell, arm.cohort, fx.exclude))
						if err != nil {
							t.Fatalf("%s: %v", name, err)
						}
						gotCells := cells(t, out)
						if fx.exact {
							if got, want := asJSON(t, out.Overlays[0]), asJSON(t, ref.Overlays[0]); got != want {
								t.Errorf("%s:\n got %s\nwant %s (AGG_WELFORD)", name, got, want)
							}
							continue
						}
						if got, want := asJSON(t, out.Overlays[0].Warnings), asJSON(t, ref.Overlays[0].Warnings); got != want {
							t.Errorf("%s warnings:\n got %s\nwant %s (AGG_WELFORD)", name, got, want)
						}
						for i := range wantCells {
							for j := range wantCells[i] {
								w, g := wantCells[i][j], gotCells[i][j]
								wv, _ := w.Value.(float64)
								gv, _ := g.Value.(float64)
								if w.Present != g.Present || math.IsNaN(wv) != math.IsNaN(gv) ||
									(!math.IsNaN(wv) && math.Abs(gv-wv) > 1e-12*math.Abs(wv)) {
									t.Errorf("%s cell (%d,%d) = %+v, want %+v (AGG_WELFORD)", name, i, j, g, w)
								}
							}
						}
					}
				}
			}
		}
	}
}

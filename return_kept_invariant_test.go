package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestReturnKeptNumberInvariant (U18 E2-S4, FR-29): a shaped run's kept
// paths are byte-identical to the full run's — the compute plan skips
// only what nothing kept reads. Covered: request overlays that READ the
// host's components (probability-weighted χ², pairwise proportion z),
// on the buffered and fused crosstab arms, and a Compose overlay naming
// two of three slots, serial and parallel. Every vetoed part is
// computed (its work counter moves) yet absent on the wire.
func TestReturnKeptNumberInvariant(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	prob := types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	standard := &types.Return{Preset: types.ReturnPresetStandard}
	ctx := context.Background()

	requests := map[string]func(ret *types.Return) *types.Request{
		"chisq_probability": func(ret *types.Return) *types.Request {
			r := floorCrosstab(cohort, &prob, types.SlotWeight{}, nil,
				types.OverlaySpec{Name: "m", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
				types.OverlaySpec{Name: "r", Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow})
			r.Return = ret
			return r
		},
		"pairwise_prop_z": func(ret *types.Return) *types.Request {
			r := floorCrosstab(cohort, nil, types.SlotWeight{}, nil,
				types.OverlaySpec{Name: "pz", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow})
			r.Return = ret
			return r
		},
	}
	for _, fusionOff := range []bool{false, true} {
		p := newReturnInstance(t, fs, pulse.Options{DisableCrosstabFusion: fusionOff})
		for name, build := range requests {
			t.Run(name+map[bool]string{false: "/fused", true: "/buffered"}[fusionOff], func(t *testing.T) {
				pr, err := p.Predict(ctx, build(nil))
				if err != nil || pr.CrosstabFusable == nil || *pr.CrosstabFusable == fusionOff {
					t.Fatalf("crosstab arm is not the one named (fusable=%v, err=%v)", pr.CrosstabFusable, err)
				}
				full, err := p.Process(ctx, build(nil))
				if err != nil {
					t.Fatalf("full: %v", err)
				}
				if len(full.Overlays) == 0 || full.Components == nil || full.Components.Crosstab == nil {
					t.Fatal("full run lacks the overlay or its components: the fixture proves nothing")
				}
				before := processing.WorkStats()
				shaped, err := p.Process(ctx, build(standard))
				if err != nil {
					t.Fatalf("shaped: %v", err)
				}
				if processing.WorkStats().Sub(before).CrosstabCellComponentMaps <= 0 {
					t.Error("vetoed components.crosstab was not computed")
				}
				assertKeptPathsIdentical(t, full, shaped, "components")
			})
		}
	}

	// U18 E3-S1: a `request` multiplicity family pools the test with the
	// inferential layers, so excluding the overlays still folds them —
	// the test's p_adjusted and m are the full run's — while the
	// descriptive layer, no member, is skipped. Overlays are absent.
	t.Run("multiplicity_family_vetoes_excluded_overlays", func(t *testing.T) {
		p := newReturnInstance(t, fs, pulse.Options{})
		build := func(ret *types.Return) *types.Request {
			r := floorCrosstab(cohort, nil, types.SlotWeight{}, nil,
				types.OverlaySpec{Name: "m", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
				types.OverlaySpec{Name: "d", Kind: types.OverlayKindShareOfRow, Scope: types.OverlayScopeRow,
					Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}})
			r.Tests = []*types.Test{{Type: types.TEST_T, Field: "x", Params: json.RawMessage(`{"mu":10}`), Label: "t"}}
			r.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyRequest}
			r.Return = ret
			return r
		}
		full, err := p.Process(ctx, build(nil))
		if err != nil {
			t.Fatalf("full: %v", err)
		}
		if len(full.Tests) != 1 || full.Tests[0].Multiplicity == nil || full.Tests[0].Multiplicity.M < 2 {
			t.Fatalf("full run's test is not pooled with the overlay: %+v", full.Tests)
		}
		before := processing.WorkStats()
		shaped, err := p.Process(ctx, build(&types.Return{Exclude: []string{"overlays"}}))
		if err != nil {
			t.Fatalf("shaped: %v", err)
		}
		if got := processing.WorkStats().Sub(before).OverlayLayerRuns; got != 1 {
			t.Errorf("%d layer folds; want the 1 member layer", got)
		}
		assertKeptPathsIdentical(t, full, shaped, "overlays")
	})

	t.Run("compose_overlay_names_slots", func(t *testing.T) {
		p := newReturnInstance(t, fs, pulse.Options{})
		fixture := func(ret *types.Return) *types.ComposedRequest {
			slot := func(label, expr string) *types.Request {
				r := floorCrosstab(cohort, &prob, types.SlotWeight{}, nil)
				r.Label = label
				r.Return = ret
				if expr != "" {
					r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}
				}
				return r
			}
			return &types.ComposedRequest{
				Requests: []*types.Request{slot("total", ""), slot("sub", "x >= 10"), slot("other", "x < 10")},
				Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: types.OverlayKindPropZCell, Scope: types.OverlayScopeCell, Reference: "total", Targets: []string{"sub"}}},
			}
		}
		full, _ := composeJSON(t, p, fixture(nil), false)
		if len(full.Overlays) != 1 || full.Overlays[0].Summary == nil {
			t.Fatalf("full overlay layer: %+v", full.Overlays)
		}
		for _, parallel := range []bool{false, true} {
			before := processing.WorkStats()
			shaped, _ := composeJSON(t, p, fixture(standard), parallel)
			if processing.WorkStats().Sub(before).CrosstabCellComponentMaps <= 0 {
				t.Errorf("parallel=%v: the named slots' components were not computed", parallel)
			}
			for i := range full.Responses {
				assertKeptPathsIdentical(t, full.Responses[i], shaped.Responses[i], "components")
			}
			want, _ := json.Marshal(full.Overlays)
			got, _ := json.Marshal(shaped.Overlays)
			if !bytes.Equal(got, want) {
				t.Errorf("parallel=%v: compose overlay layer moved\n got %s\nwant %s", parallel, got, want)
			}
		}
	})
}

// assertKeptPathsIdentical: every top-level key of the full response
// that the selection keeps is byte-identical on the shaped wire; every
// excluded key is ABSENT there (computed under a veto or not).
func assertKeptPathsIdentical(t *testing.T, full, shaped *types.Response, excluded ...string) {
	t.Helper()
	split := func(r *types.Response) map[string]json.RawMessage {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]json.RawMessage{}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	f, s := split(full), split(shaped)
	drop := map[string]bool{"returned": true}
	for _, k := range excluded {
		drop[k] = true
		if _, ok := s[k]; ok {
			t.Errorf("%s: excluded, yet present on the wire", k)
		}
	}
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if drop[k] {
			continue
		}
		if !bytes.Equal(f[k], s[k]) {
			t.Errorf("kept path %q moved\n got %s\nwant %s", k, s[k], f[k])
		}
	}
}

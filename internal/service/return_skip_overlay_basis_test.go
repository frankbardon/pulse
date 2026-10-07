package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// U18 follow-up: the χ² / Fisher components veto is weight-basis aware.
// OVERLAY_CHISQ_* and OVERLAY_FISHER_EXACT_CELL read the host's
// Components.Crosstab only through its weighted floor (CellFloor: the
// stamped sum_weights on a frequency cell, plus the Kish n_eff on a
// probability one), so over an UNWEIGHTED cell `return: standard` skips
// the component maps and the layers stay byte-identical, while over a
// weighted cell — request weight, cell slot weight or the instance
// default alone — the veto keeps components.crosstab so every kept
// number (summary Parameters' sum_weights / n_eff included) is the
// return-absent run's.

var basisVetoKinds = []types.OverlayKind{
	types.OverlayKindChiSqMatrix, types.OverlayKindChiSqRow, types.OverlayKindChiSqCol, types.OverlayKindFisherExactCell,
}

// basisVetoRequest is overlayCrosstabRequest's g×h host carrying the
// given floor kinds only, under the request weight w (nil: none).
func basisVetoRequest(w *types.WeightSpec, kinds []types.OverlayKind, ret *types.Return) *types.Request {
	r := overlayCrosstabRequest(false)
	r.Weight = w
	r.Overlays = nil
	for _, k := range kinds {
		scope := types.OverlayScopeMatrix
		switch k {
		case types.OverlayKindChiSqRow:
			scope = types.OverlayScopeRow
		case types.OverlayKindChiSqCol:
			scope = types.OverlayScopeColumn
		case types.OverlayKindFisherExactCell:
			scope = types.OverlayScopeCell
		}
		r.Overlays = append(r.Overlays, types.OverlaySpec{Name: string(k), Kind: k, Scope: scope})
	}
	r.Return = ret
	return r
}

// runBasisVeto runs req and returns the response and the component maps
// it built.
func runBasisVeto(t *testing.T, svc *Service, req *types.Request) (*types.Response, int64) {
	t.Helper()
	before := processing.WorkStats()
	resp := mustProcess(t, svc, req)
	return resp, processing.WorkStats().Sub(before).CrosstabCellComponentMaps
}

func assertFusionArm(t *testing.T, name string, svc *Service, req *types.Request) {
	t.Helper()
	if name != "fused" {
		return
	}
	if ok, why := processing.CanFuseCrosstab(req, multSchema(), svc.extensions); !ok {
		t.Fatalf("fusion gate declined (%s): the fused arm would silently run buffered", why)
	}
}

// hasFloorParameter reports whether any layer's summary (top-level or
// per-entry) carries the weighted floor's sum_weights — the figure only
// a components read produces.
func hasFloorParameter(layers []types.OverlayLayer) bool {
	for _, l := range layers {
		if l.Summary != nil {
			if _, ok := l.Summary.Parameters["sum_weights"]; ok {
				return true
			}
		}
		if l.Payload.Series != nil {
			for _, e := range l.Payload.Series.Entries {
				if _, ok := e.Summary.Parameters["sum_weights"]; ok {
					return true
				}
			}
		}
	}
	return false
}

// TestReturnSkipsComponents_UnweightedFloorOverlays: an unweighted host
// under `standard` builds no component map for its χ² / Fisher layers,
// and every layer equals the return-absent run's byte for byte.
func TestReturnSkipsComponents_UnweightedFloorOverlays(t *testing.T) {
	fused, buffered := overlayFoldService(t)
	standard := &types.Return{Preset: types.ReturnPresetStandard}
	for name, svc := range map[string]*Service{"fused": fused, "buffered": buffered} {
		t.Run(name, func(t *testing.T) {
			assertFusionArm(t, name, svc, basisVetoRequest(nil, basisVetoKinds, nil))
			base, baseMaps := runBasisVeto(t, svc, basisVetoRequest(nil, basisVetoKinds, nil))
			if baseMaps <= 0 || len(base.Overlays) != len(basisVetoKinds) {
				t.Fatalf("return absent: maps=%d layers=%d; the fixture proves nothing", baseMaps, len(base.Overlays))
			}
			shaped, maps := runBasisVeto(t, svc, basisVetoRequest(nil, basisVetoKinds, standard))
			if maps != 0 {
				t.Errorf("unweighted χ² / Fisher host under standard built %d component map(s); want 0", maps)
			}
			if !bytes.Equal(mustMarshal(t, base.Overlays), mustMarshal(t, shaped.Overlays)) {
				t.Errorf("an overlay layer moved when the components were skipped:\nbase   %s\nshaped %s",
					mustMarshal(t, base.Overlays), mustMarshal(t, shaped.Overlays))
			}
		})
	}
}

// TestReturnKeepsComponents_WeightedFloorOverlays: a weighted host —
// frequency or probability, by the request weight or by the instance
// default alone — keeps components.crosstab under `standard`, and the
// layers (their floor Parameters included) equal the return-absent
// run's byte for byte.
func TestReturnKeepsComponents_WeightedFloorOverlays(t *testing.T) {
	freq := &types.WeightSpec{Field: "id", Kind: types.WeightKindFrequency}
	prob := &types.WeightSpec{Field: "x", Kind: types.WeightKindProbability}
	chiSq := basisVetoKinds[:3] // Fisher is frequency-only: the resolver refuses it under prob
	standard := &types.Return{Preset: types.ReturnPresetStandard}
	cases := []struct {
		name     string
		reqW     *types.WeightSpec
		defaultW *types.WeightSpec
		kinds    []types.OverlayKind
	}{
		{"frequency", freq, nil, basisVetoKinds},
		{"probability", prob, nil, chiSq},
		{"frequency_default_only", nil, freq, basisVetoKinds},
		{"probability_default_only", nil, prob, chiSq},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fused, buffered := overlayFoldService(t)
			for name, svc := range map[string]*Service{"fused": fused, "buffered": buffered} {
				t.Run(name, func(t *testing.T) {
					svc.SetDefaultWeight(c.defaultW)
					assertFusionArm(t, name, svc, basisVetoRequest(c.reqW, c.kinds, nil))
					base, _ := runBasisVeto(t, svc, basisVetoRequest(c.reqW, c.kinds, nil))
					if len(base.Overlays) != len(c.kinds) || !hasFloorParameter(base.Overlays) {
						t.Fatalf("return absent: %d layers, floor Parameters %v; the fixture proves nothing",
							len(base.Overlays), hasFloorParameter(base.Overlays))
					}
					shaped, maps := runBasisVeto(t, svc, basisVetoRequest(c.reqW, c.kinds, standard))
					if maps <= 0 {
						t.Error("weighted χ² / Fisher host under standard built no component map: the veto did not hold")
					}
					if !bytes.Equal(mustMarshal(t, base.Overlays), mustMarshal(t, shaped.Overlays)) {
						t.Errorf("a kept overlay number moved under standard:\nbase   %s\nshaped %s",
							mustMarshal(t, base.Overlays), mustMarshal(t, shaped.Overlays))
					}
				})
			}
		})
	}
}

// TestReturnSkipsComponents_FisherProbabilityHostStillRefuses: Fisher
// over a cell weighted under kind probability by its slot alone refuses
// PULSE_WEIGHT_UNSUPPORTED with the components excluded too — the host
// basis rides the stamped cell weight, not the floor keys a skip would
// hide.
func TestReturnSkipsComponents_FisherProbabilityHostStillRefuses(t *testing.T) {
	fused, buffered := overlayFoldService(t)
	for name, svc := range map[string]*Service{"fused": fused, "buffered": buffered} {
		for _, ret := range []*types.Return{
			nil,
			{Preset: types.ReturnPresetStandard},
			{Exclude: []string{"components"}},
		} {
			req := basisVetoRequest(nil, []types.OverlayKind{types.OverlayKindFisherExactCell}, ret)
			req.Crosstab.Cell.Weight = types.SlotWeightOf(types.WeightSpec{Field: "x", Kind: types.WeightKindProbability})
			_, err := svc.Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED {
				t.Errorf("%s return %+v: err %v; want PULSE_WEIGHT_UNSUPPORTED", name, ret, err)
			}
		}
	}
}

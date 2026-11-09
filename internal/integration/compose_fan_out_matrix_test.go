package integration_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Compose fan-out, matrix half (U66 #443): T_CELL, Z_CELL, PROP_Z_CELL,
// CHISQ_VS_REF and RANK once scored Targets[0] and dropped every later
// target in silence; they now emit one layer per target, in Targets
// order, exactly as the series kinds do (compose_fan_out_test.go).

// foMatrixKinds are the matrix fan-out kinds.
var foMatrixKinds = []types.OverlayKind{
	types.OverlayKindTCell,
	types.OverlayKindZCell,
	types.OverlayKindPropZCell,
	types.OverlayKindChiSqVsRef,
	types.OverlayKindRank,
}

// foMatrixExclude drops ids per target slot: "B" and "C" keep
// different rows, so their cells (and every layer over them) differ.
var foMatrixExclude = map[string][]string{
	"A": nil,
	"B": {"1", "12", "14", "15", "16", "22", "24", "25", "30"},
	"C": {"2", "3", "7", "9", "18", "27"},
}

// foMatrixCell is the cell aggregation kind scores: the moment kinds
// read AGG_WELFORD's components, the count kinds read counts.
func foMatrixCell(kind types.OverlayKind) types.AggregationType {
	if kind == types.OverlayKindTCell || kind == types.OverlayKindZCell {
		return types.AGG_WELFORD
	}
	return types.AGG_COUNT
}

// foMatrixSlot is one region × sex crosstab slot scoring cell over
// score, dropping label's ids.
func foMatrixSlot(f *mgFixture, label string, cell types.AggregationType) *types.Request {
	r := &types.Request{
		Label:  label,
		Cohort: &types.Cohort{Filename: f.cohort},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}},
			Cell:    &types.Aggregation{Type: cell, Field: "score", Label: "v"},
		},
	}
	if cell == types.AGG_COUNT {
		// PROP_Z_CELL reads each cell's row margin as its sample size.
		r.Crosstab.Margins = types.CrosstabMargins{Rows: true, Columns: true, Grand: true}
	}
	if ex := foMatrixExclude[label]; ex != nil {
		r.Filterers = []*types.Filterer{{Type: types.FILTER_EXCLUDE, Field: "id", Values: ex}}
	}
	return r
}

func foMatrixSpec(kind types.OverlayKind, targets ...string) types.ComposeOverlaySpec {
	scope := types.OverlayScopeCell
	if kind == types.OverlayKindChiSqVsRef {
		scope = types.OverlayScopeMatrix
	}
	return types.ComposeOverlaySpec{Name: "o", Kind: kind, Scope: scope, Reference: "A", Targets: targets}
}

// foMatrixCompose is the A / B / C crosstab batch carrying specs, every
// slot scoring kind's cell.
func foMatrixCompose(f *mgFixture, kind types.OverlayKind, specs ...types.ComposeOverlaySpec) *types.ComposedRequest {
	cell := foMatrixCell(kind)
	return &types.ComposedRequest{
		Requests: []*types.Request{foMatrixSlot(f, "A", cell), foMatrixSlot(f, "B", cell), foMatrixSlot(f, "C", cell)},
		Overlays: specs,
	}
}

// foMatrixSingleTargetGolden is each kind's one-target layer over
// reference A and target B as the engine answered it BEFORE the
// fan-out (captured on the pre-U66-#443 matrix tree). The inferential
// kinds' p-values come from the incomplete-beta / gamma / normal tails,
// so foSameJSON holds their figures to 1e-12 relative (cross-arch) and
// everything else byte for byte; RANK is pinned byte for byte.
var foMatrixSingleTargetGolden = map[types.OverlayKind]string{
	types.OverlayKindTCell:      `[{"name":"o","kind":"OVERLAY_T_CELL","scope":"cell","ref":{},"payload":{"shape":"matrix","matrix":{"row_header":{"fields":["region"],"types":["GROUP_CATEGORY"]},"column_header":{"fields":["sex"],"types":["GROUP_CATEGORY"]},"row_keys":[["north"],["south"],["west"]],"column_keys":[["F"],["M"]],"cells":[[{"value":0.6634038351655083,"present":true},{"value":0.6176652970841958,"present":true}],[{"value":null,"present":true},{"value":0.6424284270186702,"present":true}],[{"value":null,"present":true},{"value":0.6176652970841958,"present":true}]],"grand_total":{"present":false},"cell_label":"o","normalize_applied":"none"}},"summary":{"min":0.6176652970841958,"max":0.6634038351655083,"count":4},"warnings":[{"code":"PULSE_OVERLAY_REF_ZERO","message":"overlay OVERLAY_T_CELL Welch t-test undefined for cell (zero SE OR insufficient n)","details":{"col_index":0,"col_key":"F","kind":"OVERLAY_T_CELL","overlay_index":0,"ref_value":14.5,"reference":"A","row_index":1,"row_key":"south","target_label":"B","target_value":13}},{"code":"PULSE_OVERLAY_REF_ZERO","message":"overlay OVERLAY_T_CELL Welch t-test undefined for cell (zero SE OR insufficient n)","details":{"col_index":0,"col_key":"F","kind":"OVERLAY_T_CELL","overlay_index":0,"ref_value":24,"reference":"A","row_index":2,"row_key":"west","target_label":"B","target_value":23}}]}]`,
	types.OverlayKindZCell:      `[{"name":"o","kind":"OVERLAY_Z_CELL","scope":"cell","ref":{},"payload":{"shape":"matrix","matrix":{"row_header":{"fields":["region"],"types":["GROUP_CATEGORY"]},"column_header":{"fields":["sex"],"types":["GROUP_CATEGORY"]},"row_keys":[["north"],["south"],["west"]],"column_keys":[["F"],["M"]],"cells":[[{"value":0.654720846018577,"present":true},{"value":0.6015081344405899,"present":true}],[{"value":null,"present":true},{"value":0.6309540411841709,"present":true}],[{"value":null,"present":true},{"value":0.6015081344405899,"present":true}]],"grand_total":{"present":false},"cell_label":"o","normalize_applied":"none"}},"summary":{"min":0.6015081344405899,"max":0.654720846018577,"count":4},"warnings":[{"code":"PULSE_OVERLAY_REF_ZERO","message":"overlay OVERLAY_Z_CELL z-test undefined for cell (zero SE OR insufficient n)","details":{"col_index":0,"col_key":"F","kind":"OVERLAY_Z_CELL","overlay_index":0,"ref_value":14.5,"reference":"A","row_index":1,"row_key":"south","target_label":"B","target_value":13}},{"code":"PULSE_OVERLAY_REF_ZERO","message":"overlay OVERLAY_Z_CELL z-test undefined for cell (zero SE OR insufficient n)","details":{"col_index":0,"col_key":"F","kind":"OVERLAY_Z_CELL","overlay_index":0,"ref_value":24,"reference":"A","row_index":2,"row_key":"west","target_label":"B","target_value":23}}]}]`,
	types.OverlayKindPropZCell:  `[{"name":"o","kind":"OVERLAY_PROP_Z_CELL","scope":"cell","ref":{},"payload":{"shape":"matrix","matrix":{"row_header":{"fields":["region"],"types":["GROUP_CATEGORY"]},"column_header":{"fields":["sex"],"types":["GROUP_CATEGORY"]},"row_keys":[["north"],["south"],["west"]],"column_keys":[["F"],["M"]],"cells":[[{"value":0.9368978212910839,"present":true},{"value":0.9368978212910837,"present":true}],[{"value":0.3296421755555329,"present":true},{"value":0.32964217555553277,"present":true}],[{"value":0.5059850436448927,"present":true},{"value":0.5059850436448926,"present":true}]],"grand_total":{"present":false},"cell_label":"o","normalize_applied":"none"}},"summary":{"min":0.32964217555553277,"max":0.9368978212910839,"count":6}}]`,
	types.OverlayKindChiSqVsRef: `[{"name":"o","kind":"OVERLAY_CHISQ_VS_REF","scope":"matrix","ref":{},"payload":{"shape":"scalar","scalar":0.8098569630604519},"summary":{"statistic":2.275510204081633,"p_value":0.8098569630604519,"parameters":{"df":5}},"warnings":[{"code":"PULSE_OVERLAY_EXPECTED_LOW","message":"overlay OVERLAY_CHISQ_VS_REF contingency table has cells with expected \u003c 5","details":{"kind":"OVERLAY_CHISQ_VS_REF","low_expected_cells":6,"overlay_index":0,"reference":"A","total_cells":6}}]}]`,
	types.OverlayKindRank:       `[{"name":"o","kind":"OVERLAY_RANK","scope":"cell","ref":{},"payload":{"shape":"matrix","matrix":{"row_header":{"fields":["region"],"types":["GROUP_CATEGORY"]},"column_header":{"fields":["sex"],"types":["GROUP_CATEGORY"]},"row_keys":[["north"],["south"],["west"]],"column_keys":[["F"],["M"]],"cells":[[{"value":1,"present":true},{"value":3.5,"present":true}],[{"value":5.5,"present":true},{"value":2,"present":true}],[{"value":5.5,"present":true},{"value":3.5,"present":true}]],"grand_total":{"present":false},"cell_label":"o","normalize_applied":"none"}},"summary":{"min":1,"max":5.5,"count":6,"baseline":1}}]`,
}

// TestComposeFanOutMatrix_SingleTargetByteIdentical: one target answers
// the pre-fan-out layer byte for byte, on every matrix fan-out kind.
func TestComposeFanOutMatrix_SingleTargetByteIdentical(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range foMatrixKinds {
		t.Run(string(kind), func(t *testing.T) {
			resp := foRun(t, f, foMatrixCompose(f, kind, foMatrixSpec(kind, "B")))
			got, want := foJSON(t, resp.Overlays), foMatrixSingleTargetGolden[kind]
			exact := kind == types.OverlayKindRank
			if (exact && got != want) || !foSameJSON(t, got, want) {
				t.Errorf("layers =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// foMatrixAsSingle is layer l renamed to the one-target layer's Name
// (and CellLabel, which follows it).
func foMatrixAsSingle(l types.OverlayLayer, name string) types.OverlayLayer {
	l.Name = name
	if l.Payload.Matrix != nil {
		m := *l.Payload.Matrix
		m.CellLabel = name
		l.Payload.Matrix = &m
	}
	return l
}

// TestComposeFanOutMatrix_OneLayerPerTarget: two targets emit two
// layers in Targets order, named `<name>__<target>` (CellLabel with
// it), each exactly the one-target layer for its target (the name
// aside) — warnings included; the two differ, so neither is a copy of
// the first target's. A later spec keeps its own layer.
func TestComposeFanOutMatrix_OneLayerPerTarget(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range foMatrixKinds {
		t.Run(string(kind), func(t *testing.T) {
			resp := foRun(t, f, foMatrixCompose(f, kind, foMatrixSpec(kind, "B", "C"), foMatrixSpec(kind, "C")))
			if len(resp.Overlays) != 3 {
				t.Fatalf("layers = %d, want 3 (two targets, then one)", len(resp.Overlays))
			}
			for i, want := range []string{"o__B", "o__C", "o"} {
				l := resp.Overlays[i]
				if l.Name != want || l.Kind != kind {
					t.Errorf("layer %d = %s / %s, want %s / %s", i, l.Name, l.Kind, want, kind)
				}
				if l.Payload.Matrix != nil && l.Payload.Matrix.CellLabel != want {
					t.Errorf("layer %d cell_label %q, want %q", i, l.Payload.Matrix.CellLabel, want)
				}
			}
			payloads := make([]string, 2)
			for i, target := range []string{"B", "C"} {
				single := foRun(t, f, foMatrixCompose(f, kind, foMatrixSpec(kind, target)))
				if len(single.Overlays) != 1 {
					t.Fatalf("one target: %d layers", len(single.Overlays))
				}
				got := foMatrixAsSingle(resp.Overlays[i], single.Overlays[0].Name)
				if g, w := foJSON(t, got), foJSON(t, single.Overlays[0]); g != w {
					t.Errorf("target %s layer =\n%s\nwant the one-target layer\n%s", target, g, w)
				}
				payloads[i] = foJSON(t, got.Payload)
			}
			if payloads[0] == payloads[1] {
				t.Errorf("both target layers carry the same payload %s", payloads[0])
			}
			if g, w := foJSON(t, foMatrixAsSingle(resp.Overlays[2], "o").Payload), payloads[1]; g != w {
				t.Errorf("third layer payload %s, want the C target's %s", g, w)
			}
		})
	}
}

// TestComposeFanOutMatrix_TargetCapParity: a matrix fan-out spec naming
// more targets than MaxPanelTargets is ONE
// PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP from predict-compose and from the
// runtime, message and Details equal; at the cap it runs.
func TestComposeFanOutMatrix_TargetCapParity(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range foMatrixKinds {
		t.Run(string(kind), func(t *testing.T) {
			spec := foMatrixSpec(kind, "B", "C")
			spec.Options = &types.OverlayOptions{MaxPanelTargets: 1}
			req := foMatrixCompose(f, kind, foMatrixSpec(kind, "B"), spec)
			env := csPredict(t, f.p, req)
			_, err := f.p.Compose(context.Background(), req)
			ce := requireCodedError(t, err, perr.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP)
			want := map[string]any{"index": 1, "kind": string(kind), "observed": 2, "cap": 1}
			if !reflect.DeepEqual(ce.Details, want) {
				t.Errorf("runtime details %#v, want %#v", ce.Details, want)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP) {
				t.Fatalf("predict-compose: want exactly one PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP, got %+v", env.Errors)
			}
			if !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
				t.Errorf("predict details %#v != runtime %#v", env.Errors[0].Details, ce.Details)
			}
			if env.Errors[0].Message != ce.Message {
				t.Errorf("message drift: predict %q, runtime %q", env.Errors[0].Message, ce.Message)
			}
			spec.Options.MaxPanelTargets = 2
			if resp := foRun(t, f, foMatrixCompose(f, kind, spec)); len(resp.Overlays) != 2 {
				t.Errorf("at the cap: %d layers, want 2", len(resp.Overlays))
			}
		})
	}
}

// TestComposeFanOutMatrix_PerTargetCellType: the moment cell kinds read
// each target's OWN authored cell type. Target C scores AGG_VARIANCE
// (a population variance, converted to the sample variance on read)
// beside an AGG_WELFORD reference and first target, so C's layer must
// equal the one-target layer over C — read with C's type, never the
// first target's.
func TestComposeFanOutMatrix_PerTargetCellType(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindZCell} {
		for _, cType := range []types.AggregationType{types.AGG_VARIANCE, types.AGG_STDDEV} {
			t.Run(string(kind)+"/"+string(cType), func(t *testing.T) {
				batch := func(spec types.ComposeOverlaySpec) *types.ComposedRequest {
					return &types.ComposedRequest{
						Requests: []*types.Request{foMatrixSlot(f, "A", types.AGG_WELFORD), foMatrixSlot(f, "B", types.AGG_WELFORD), foMatrixSlot(f, "C", cType)},
						Overlays: []types.ComposeOverlaySpec{spec},
					}
				}
				resp := foRun(t, f, batch(foMatrixSpec(kind, "B", "C")))
				if len(resp.Overlays) != 2 {
					t.Fatalf("layers = %d, want 2", len(resp.Overlays))
				}
				for i, target := range []string{"B", "C"} {
					single := foRun(t, f, batch(foMatrixSpec(kind, target)))
					got := foMatrixAsSingle(resp.Overlays[i], "o")
					if g, w := foJSON(t, got), foJSON(t, single.Overlays[0]); g != w {
						t.Errorf("target %s layer =\n%s\nwant the one-target layer\n%s", target, g, w)
					}
				}
			})
		}
	}
}

// TestComposeFanOutMatrix_TwoSampleInputsJudgeEveryTarget: the
// two-sample inputs rule judges every target, not only the first —
// target C's cells lose their moments (components disabled), so with no
// params its scalar side is refused PULSE_OVERLAY_PARAM_MISSING naming
// C, by predict-compose and the runtime alike, while B's moments pass.
func TestComposeFanOutMatrix_TwoSampleInputsJudgeEveryTarget(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	off := true
	for _, kind := range []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindZCell} {
		t.Run(string(kind), func(t *testing.T) {
			req := foMatrixCompose(f, kind, foMatrixSpec(kind, "B", "C"))
			req.Requests[2].DisableComponents = &off
			env := csPredict(t, f.p, req)
			_, err := f.p.Compose(context.Background(), req)
			ce := requireCodedError(t, err, perr.PULSE_OVERLAY_PARAM_MISSING)
			if ce.Details["slot"] != "C" || ce.Details["side"] != "target" {
				t.Errorf("runtime details %#v, want slot C, side target", ce.Details)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_PARAM_MISSING) {
				t.Fatalf("predict-compose: want exactly one PULSE_OVERLAY_PARAM_MISSING, got %+v", env.Errors)
			}
			if !reflect.DeepEqual(env.Errors[0].Details, ce.Details) || env.Errors[0].Message != ce.Message {
				t.Errorf("predict %q %#v != runtime %q %#v", env.Errors[0].Message, env.Errors[0].Details, ce.Message, ce.Details)
			}
		})
	}
}

// TestComposeFanOutMatrix_MultiplicityAcrossLayers: every target layer
// of an inferential matrix fan-out joins the Compose multiplicity fold.
// Under family `layer` each target layer is its own family (m = the
// one-target layer's own m); under `compose` one family spans both
// (m = the sum). Predict counts one p-value per target layer.
func TestComposeFanOutMatrix_MultiplicityAcrossLayers(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindZCell, types.OverlayKindPropZCell, types.OverlayKindChiSqVsRef} {
		for _, family := range []types.MultiplicityFamily{types.MultiplicityFamilyCompose, types.MultiplicityFamilyLayer} {
			t.Run(string(kind)+"/"+string(family), func(t *testing.T) {
				mult := &types.Multiplicity{Method: types.MultiplicityMethodBonferroni, Family: family}
				spec := foMatrixSpec(kind, "B", "C")
				spec.Multiplicity = mult
				req := foMatrixCompose(f, kind, spec)
				env := csPredict(t, f.p, req)
				res := env.Data.(*pulse.ComposePredictResult)
				if res.PValues == nil || res.PValues.Total != 2 || res.PValues.Uncorrected != 0 {
					t.Errorf("predict p_values = %+v, want 2 (one per target layer), all corrected", res.PValues)
				}
				resp := foRun(t, f, req)
				if len(resp.Overlays) != 2 {
					t.Fatalf("layers = %d, want 2", len(resp.Overlays))
				}
				own := make([]int, 2)
				for i, target := range []string{"B", "C"} {
					one := foMatrixSpec(kind, target)
					one.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodBonferroni, Family: types.MultiplicityFamilyLayer}
					single := foRun(t, f, foMatrixCompose(f, kind, one))
					if single.Overlays[0].Multiplicity == nil || single.Overlays[0].Multiplicity.M == 0 {
						t.Fatalf("one-target %s layer echoes no multiplicity: %+v", target, single.Overlays[0].Multiplicity)
					}
					own[i] = single.Overlays[0].Multiplicity.M
				}
				for i, l := range resp.Overlays {
					want := own[i]
					if family == types.MultiplicityFamilyCompose {
						want = own[0] + own[1]
					}
					if l.Multiplicity == nil || l.Multiplicity.M != want {
						t.Errorf("layer %s echo %+v, want m = %d", l.Name, l.Multiplicity, want)
					}
				}
				if kind == types.OverlayKindChiSqVsRef {
					for _, l := range resp.Overlays {
						s := l.Summary
						if s == nil || s.PValue == nil || s.PAdjusted == nil {
							t.Fatalf("layer %s summary %+v", l.Name, s)
						}
						if want := min(1, *s.PValue*float64(l.Multiplicity.M)); *s.PAdjusted != want {
							t.Errorf("layer %s p_adjusted %v, want %v", l.Name, *s.PAdjusted, want)
						}
					}
				}
			})
		}
	}
}

// TestComposeFanOutMatrix_WeightedHostPerLayer: over probability-
// weighted crosstab slots each fan-out layer of the
// floor-reading kinds is exactly the one-target layer for its target —
// the Kish-scaled sample sizes and the summary's weight parameters
// included — and carries those parameters.
func TestComposeFanOutMatrix_WeightedHostPerLayer(t *testing.T) {
	t.Parallel()

	p, _, cohort := acceptanceCohort(t)
	for _, wk := range []types.WeightKind{types.WeightKindProbability} {
		w := types.WeightSpec{Field: "y", Kind: wk}
		slot := func(label, expr string) *types.Request {
			r := floorCrosstab(cohort, &w, types.SlotWeight{}, nil)
			r.Label = label
			if expr != "" {
				r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}
			}
			return r
		}
		batch := func(spec types.ComposeOverlaySpec) *types.ComposedRequest {
			return &types.ComposedRequest{
				Requests: []*types.Request{slot("A", ""), slot("B", "week % 3 != 0"), slot("C", "week % 4 != 1")},
				Overlays: []types.ComposeOverlaySpec{spec},
			}
		}
		for _, kind := range []types.OverlayKind{types.OverlayKindPropZCell, types.OverlayKindChiSqVsRef} {
			t.Run(string(kind)+"/"+string(wk), func(t *testing.T) {
				run := func(req *types.ComposedRequest) *types.ComposedResponse {
					t.Helper()
					if env, err := p.PredictCompose(context.Background(), req); err != nil || len(env.Errors) > 0 {
						t.Fatalf("predict-compose: %v %v", err, envCodes(env))
					}
					out, err := p.Compose(context.Background(), req)
					if err != nil {
						t.Fatalf("compose: %v", err)
					}
					return out
				}
				resp := run(batch(foMatrixSpec(kind, "B", "C")))
				if len(resp.Overlays) != 2 {
					t.Fatalf("layers = %d, want 2", len(resp.Overlays))
				}
				for i, target := range []string{"B", "C"} {
					single := run(batch(foMatrixSpec(kind, target)))
					got := foMatrixAsSingle(resp.Overlays[i], "o")
					if g, w := foJSON(t, got), foJSON(t, single.Overlays[0]); g != w {
						t.Errorf("target %s layer =\n%s\nwant the one-target layer\n%s", target, g, w)
					}
					if got.Summary == nil || got.Summary.Parameters["sum_weights"] == 0 {
						t.Errorf("target %s: weighted layer carries no sum_weights: %+v", target, got.Summary)
					}
				}
			})
		}
	}
}

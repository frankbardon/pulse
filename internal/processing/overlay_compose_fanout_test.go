package processing

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// TestComposeFanOutKinds_MatchMultiLayerTable pins the multi-layer
// dispatch table to the shared rule predict and the multiplicity fold
// read (overlayhost.ComposeMultiLayerKind): a kind emits one layer per
// target iff it is in the table, and no kind sits in both tables.
func TestComposeFanOutKinds_MatchMultiLayerTable(t *testing.T) {
	t.Parallel()
	for _, k := range types.AllOverlayKinds() {
		_, multi := composeOverlayMultiLayerHandlers[k]
		if multi != overlayhost.ComposeMultiLayerKind(k) {
			t.Errorf("%s: in multi-layer table %v, overlayhost.ComposeMultiLayerKind %v", k, multi, overlayhost.ComposeMultiLayerKind(k))
		}
		if _, single := composeOverlayHandlers[k]; single && multi {
			t.Errorf("%s sits in both the single- and the multi-layer table", k)
		}
	}
}

// fanOutSingleTargetHandlers are the single-target handlers each
// fan-out kind ran before U66 #443 (its old composeOverlayHandlers
// entry).
var fanOutSingleTargetHandlers = map[types.OverlayKind]composeOverlayHandler{
	types.OverlayKindIndexVsRef: applyIndexVsRef,
	types.OverlayKindDeltaVsRef: applyDeltaVsRef,
	types.OverlayKindTVsRef:     applyTVsRef,
	types.OverlayKindZVsRef:     applyZVsRef,
	types.OverlayKindTCell:      applyTCell,
	types.OverlayKindZCell:      applyZCell,
	types.OverlayKindPropZCell:  applyPropZCell,
	types.OverlayKindChiSqVsRef: applyChiSqVsRef,
	types.OverlayKindRank:       applyRank,
}

// fanOutHosts are the slot shapes each fan-out kind runs on.
func fanOutHosts(kind types.OverlayKind) []string {
	switch kind {
	case types.OverlayKindIndexVsRef, types.OverlayKindDeltaVsRef:
		return []string{"series", "matrix"}
	case types.OverlayKindTVsRef, types.OverlayKindZVsRef:
		return []string{"series"}
	}
	return []string{"matrix"}
}

// fanOutSpec is a fan-out spec over reference "a"; the two-sample kinds
// carry both sides' variance and sample size.
func fanOutSpec(kind types.OverlayKind, scope types.OverlayScope, targets ...string) types.ComposeOverlaySpec {
	spec := types.ComposeOverlaySpec{Name: "test", Kind: kind, Scope: scope, Reference: "a", Targets: targets}
	switch kind {
	case types.OverlayKindTVsRef, types.OverlayKindZVsRef, types.OverlayKindTCell, types.OverlayKindZCell:
		spec.Params = map[string]any{"variance": 4.0, "sample_size": 9.0}
	}
	if kind == types.OverlayKindChiSqVsRef {
		spec.Scope = types.OverlayScopeMatrix
	}
	return spec
}

func fanOutJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestComposeFanOut_SingleTargetMatchesHandler: through the dispatcher,
// one target answers exactly the single-target handler's layer and
// warnings (bar the dispatcher's overlay_index stamp), on every fan-out
// kind and host shape — a zero reference value raises a warning on the
// index kind, so the warning path is compared too.
func TestComposeFanOut_SingleTargetMatchesHandler(t *testing.T) {
	t.Parallel()
	series := []*types.Response{
		makeSeriesResponse([]string{"north", "south", "east"}, []float64{0, 10, 20}),
		makeSeriesResponse([]string{"north", "south", "east"}, []float64{5, 50, 100}),
	}
	matrix := []*types.Response{
		makeMatrixFromValues([3][3]float64{{0, 20, 30}, {40, 50, 60}, {70, 80, 90}}),
		makeMatrixFromValues([3][3]float64{{15, 30, 45}, {60, 75, 90}, {105, 120, 135}}),
	}
	shapes := map[string][]*types.Response{"series": series, "matrix": matrix}
	for _, kind := range overlayhost.ComposeFanOutKinds() {
		for _, name := range fanOutHosts(kind) {
			slots := shapes[name]
			t.Run(string(kind)+"/"+name, func(t *testing.T) {
				scope := types.OverlayScopeGroup
				if name == "matrix" {
					scope = types.OverlayScopeCell
				}
				spec := fanOutSpec(kind, scope, "b")
				layers, ws, err := ApplyComposeOverlays([]types.ComposeOverlaySpec{spec}, slots, []string{"a", "b"})
				if err != nil {
					t.Fatalf("ApplyComposeOverlays: %v", err)
				}
				direct := spec
				wantLayer, wantWs, err := fanOutSingleTargetHandlers[kind](&direct, slots[0], slots[1:], 0, []int{1})
				if err != nil {
					t.Fatalf("handler: %v", err)
				}
				for i := range wantWs {
					if wantWs[i].Details == nil {
						wantWs[i].Details = map[string]any{}
					}
					wantWs[i].Details["overlay_index"] = 0
				}
				if len(layers) != 1 {
					t.Fatalf("layers = %d, want 1", len(layers))
				}
				if g, w := fanOutJSON(t, layers[0]), fanOutJSON(t, wantLayer); g != w {
					t.Errorf("layer =\n%s\nwant\n%s", g, w)
				}
				if g, w := fanOutJSON(t, ws), fanOutJSON(t, wantWs); g != w {
					t.Errorf("warnings =\n%s\nwant\n%s", g, w)
				}
			})
		}
	}
}

// TestComposeFanOut_RoutesWarningsToTargetLayer: each warning's layer
// position (ApplyComposeOverlaysRouted) is the layer its own target
// emitted — after a panel and a fan-out spec shift every later layer —
// while Details.overlay_index stays the spec's index. Every target
// meets the zero reference, so each layer raises exactly one warning.
func TestComposeFanOut_RoutesWarningsToTargetLayer(t *testing.T) {
	t.Parallel()
	slots := []*types.Response{
		makeSeriesResponse([]string{"north", "south"}, []float64{0, 10}),
		makeSeriesResponse([]string{"north", "south"}, []float64{5, 50}),
		makeSeriesResponse([]string{"north", "south"}, []float64{7, 70}),
	}
	specs := []types.ComposeOverlaySpec{
		fanOutSpec(types.OverlayKindPanelIndexVsRef, types.OverlayScopeGroup, "b", "c"),
		fanOutSpec(types.OverlayKindIndexVsRef, types.OverlayScopeGroup, "c", "b"),
		fanOutSpec(types.OverlayKindIndexVsRef, types.OverlayScopeGroup, "b"),
	}
	layers, ws, at, err := ApplyComposeOverlaysRouted(specs, slots, []string{"a", "b", "c"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("ApplyComposeOverlaysRouted: %v", err)
	}
	names := make([]string, len(layers))
	for i, l := range layers {
		names[i] = l.Name
	}
	if want := []string{"test__b", "test__c", "test__c", "test__b", "test"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("layer names = %v, want %v", names, want)
	}
	if want := []int{0, 1, 2, 3, 4}; !reflect.DeepEqual(at, want) {
		t.Errorf("warning layer positions = %v, want %v", at, want)
	}
	wantSpec := []int{0, 0, 1, 1, 2}
	wantTarget := []string{"b", "c", "c", "b", "b"}
	if len(ws) != len(wantSpec) {
		t.Fatalf("warnings = %d, want %d: %+v", len(ws), len(wantSpec), ws)
	}
	for i, w := range ws {
		if w.Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) || w.Details["overlay_index"] != wantSpec[i] || w.Details["target_label"] != wantTarget[i] {
			t.Errorf("warning %d = %s overlay_index %v target %v, want REF_ZERO %d %s", i, w.Code, w.Details["overlay_index"], w.Details["target_label"], wantSpec[i], wantTarget[i])
		}
	}
	// The positions follow from the specs alone — the map the
	// multiplicity fold reads.
	specOf, _ := overlayhost.ComposeLayerSpecs(specs, nil)
	if want := []int{0, 0, 1, 1, 2}; !reflect.DeepEqual(specOf, want) || len(specOf) != len(layers) {
		t.Errorf("ComposeLayerSpecs = %v, want %v over %d layers", specOf, want, len(layers))
	}
}

// TestComposeFanOut_MatrixCellLabelFollowsName: two targets on a MATRIX
// host name each layer and its CellLabel `<name>__<target>`.
func TestComposeFanOut_MatrixCellLabelFollowsName(t *testing.T) {
	t.Parallel()
	m := makeMatrixFromValues([3][3]float64{{10, 20, 30}, {40, 50, 60}, {70, 80, 90}})
	spec := fanOutSpec(types.OverlayKindDeltaVsRef, types.OverlayScopeCell, "b", "c")
	layers, _, err := ApplyComposeOverlays([]types.ComposeOverlaySpec{spec}, []*types.Response{m, m, m}, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("ApplyComposeOverlays: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("layers = %d, want 2", len(layers))
	}
	for i, want := range []string{"test__b", "test__c"} {
		if layers[i].Name != want || layers[i].Payload.Matrix == nil || layers[i].Payload.Matrix.CellLabel != want || layers[i].Kind != spec.Kind {
			t.Errorf("layer %d = %s / %+v, want %s on Name and CellLabel", i, layers[i].Name, layers[i].Payload.Matrix, want)
		}
	}
}

// TestComposeFanOut_WeightedHostPerLayer: over weighted Welford series
// slots each fan-out layer is exactly the single-target handler's layer
// for its target — the weighted legs (N*, Σw) and the summary's weight
// parameters included — under both weight bases.
func TestComposeFanOut_WeightedHostPerLayer(t *testing.T) {
	t.Parallel()
	series := func(cell map[string]any) *types.Response {
		return &types.Response{Data: []map[string]any{{"region": "north", "v": cell}}}
	}
	for _, basis := range []weighting.Basis{weighting.Frequency, weighting.Probability} {
		for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
			t.Run(string(kind)+"/"+map[weighting.Basis]string{weighting.Frequency: "frequency", weighting.Probability: "probability"}[basis], func(t *testing.T) {
				slots := []*types.Response{
					series(weightedWelfordCell(legB, basis)),
					series(weightedWelfordCell(legA, basis)),
					series(weightedWelfordCell(legB, basis)),
				}
				spec := types.ComposeOverlaySpec{Name: "w", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b", "c"}}
				layers, _, err := ApplyComposeOverlays([]types.ComposeOverlaySpec{spec}, slots, []string{"a", "b", "c"})
				if err != nil {
					t.Fatalf("ApplyComposeOverlays: %v", err)
				}
				if len(layers) != 2 {
					t.Fatalf("layers = %d, want 2", len(layers))
				}
				for i, target := range []string{"b", "c"} {
					single := spec
					single.Targets = []string{target}
					want, _, err := fanOutSingleTargetHandlers[kind](&single, slots[0], slots[i+1:i+2], 0, []int{i + 1})
					if err != nil {
						t.Fatal(err)
					}
					got := layers[i]
					got.Name = want.Name
					if g, w := fanOutJSON(t, got), fanOutJSON(t, want); g != w {
						t.Errorf("target %s layer =\n%s\nwant\n%s", target, g, w)
					}
					if got.Summary == nil || got.Summary.Parameters == nil {
						t.Errorf("target %s: weighted layer carries no weight parameters: %+v", target, got.Summary)
					}
				}
			})
		}
	}
}

// composeAllTargetKinds are the documented Compose kinds that read every
// target in ONE layer and so never fan out: OVERLAY_FORMULA (host = the
// first target, every other slot read as `slot.<label>`) and the two
// panel kinds (OVERLAY_PANEL_INDEX_VS_REF emits its own layer per
// target; OVERLAY_PROP_Z_PANEL tests all targets jointly).
var composeAllTargetKinds = map[types.OverlayKind]bool{
	types.OverlayKindFormula:         true,
	types.OverlayKindPanelIndexVsRef: true,
	types.OverlayKindPropZPanel:      true,
}

// TestComposeKinds_NoSilentTargetDrop (U66 #443): every built-in kind
// that runs on ComposedRequest.Overlays either reads all its targets
// (composeAllTargetKinds, the documented exceptions) or is a fan-out
// kind in the multi-layer table that, handed two targets, answers one
// layer per target — none still scores Targets[0] (composeFirstTarget)
// and drops the rest.
func TestComposeKinds_NoSilentTargetDrop(t *testing.T) {
	t.Parallel()
	series := makeSeriesResponse([]string{"north", "south"}, []float64{4, 10})
	matrix := makeMatrixFromValues([3][3]float64{{10, 20, 30}, {40, 50, 60}, {70, 80, 90}})
	matrix2 := makeMatrixFromValues([3][3]float64{{12, 21, 33}, {41, 55, 61}, {72, 84, 93}})
	shapes := map[string][]*types.Response{
		"series": {series, makeSeriesResponse([]string{"north", "south"}, []float64{5, 50}), makeSeriesResponse([]string{"north", "south"}, []float64{7, 70})},
		"matrix": {matrix, matrix2, makeMatrixFromValues([3][3]float64{{11, 25, 31}, {44, 52, 66}, {71, 88, 97}})},
	}
	seen := 0
	for _, kind := range types.AllOverlayKinds() {
		if !slices.Contains(overlayhost.Hosts(kind), overlayhost.Compose) {
			continue
		}
		seen++
		if composeAllTargetKinds[kind] {
			if overlayhost.ComposeFanOutKind(kind) {
				t.Errorf("%s reads every target, yet is a fan-out kind", kind)
			}
			continue
		}
		if !overlayhost.ComposeFanOutKind(kind) {
			t.Errorf("%s runs on Compose, reads one target and does not fan out", kind)
			continue
		}
		if _, single := composeOverlayHandlers[kind]; single {
			t.Errorf("%s is still in the single-layer table", kind)
		}
		for _, name := range fanOutHosts(kind) {
			spec := fanOutSpec(kind, types.OverlayScopeGroup, "b", "c")
			if name == "matrix" && kind != types.OverlayKindChiSqVsRef {
				spec.Scope = types.OverlayScopeCell
			}
			layers, _, err := ApplyComposeOverlays([]types.ComposeOverlaySpec{spec}, shapes[name], []string{"a", "b", "c"})
			if err != nil {
				t.Errorf("%s/%s: %v", kind, name, err)
				continue
			}
			if len(layers) != 2 || layers[0].Name != "test__b" || layers[1].Name != "test__c" {
				t.Errorf("%s/%s: %d layers, want test__b and test__c", kind, name, len(layers))
			}
		}
	}
	if seen != len(overlayhost.ComposeFanOutKinds())+len(composeAllTargetKinds) {
		t.Errorf("%d Compose kinds, want %d fan-out + %d all-target", seen, len(overlayhost.ComposeFanOutKinds()), len(composeAllTargetKinds))
	}
}

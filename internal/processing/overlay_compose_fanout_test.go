package processing

import (
	"encoding/json"
	"reflect"
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
}

// fanOutSpec is a fan-out spec over reference "a"; the two-sample kinds
// carry both sides' variance and sample size.
func fanOutSpec(kind types.OverlayKind, scope types.OverlayScope, targets ...string) types.ComposeOverlaySpec {
	spec := types.ComposeOverlaySpec{Name: "test", Kind: kind, Scope: scope, Reference: "a", Targets: targets}
	if kind == types.OverlayKindTVsRef || kind == types.OverlayKindZVsRef {
		spec.Params = map[string]any{"variance": 4.0, "sample_size": 9.0}
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
	for _, kind := range overlayhost.ComposeFanOutKinds() {
		hosts := map[string][]*types.Response{"series": series}
		if kind == types.OverlayKindIndexVsRef || kind == types.OverlayKindDeltaVsRef {
			hosts["matrix"] = matrix
		}
		for name, slots := range hosts {
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

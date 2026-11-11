package integration_test

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// Compose fan-out (U66 #443): INDEX_VS_REF, DELTA_VS_REF, T_VS_REF and
// Z_VS_REF once scored Targets[0] and dropped every later target in
// silence; they now emit one layer per target, in Targets order. The
// matrix fan-out kinds: compose_fan_out_matrix_test.go.

// foSeriesKinds are the series fan-out kinds.
var foSeriesKinds = []types.OverlayKind{
	types.OverlayKindIndexVsRef,
	types.OverlayKindDeltaVsRef,
	types.OverlayKindTVsRef,
	types.OverlayKindZVsRef,
}

// foSlot is one Compose slot over the multi-grouper cohort grouped by
// region, scoring the mean of y; sex "" keeps every row.
func foSlot(f *mgFixture, label, sex string) *types.Request {
	req := mgRequest(f, csRegion(), &types.Aggregation{Type: types.AGG_AVERAGE, Field: "y", Label: "m"})
	req.Label = label
	if sex != "" {
		req.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "sex", Values: []string{sex}}}
	}
	return req
}

// foSpec is a fan-out spec of kind over reference "F" and targets; the
// two-sample kinds score a scalar mean, so they carry both sides'
// variance and sample size.
func foSpec(kind types.OverlayKind, targets ...string) types.ComposeOverlaySpec {
	spec := types.ComposeOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "F", Targets: targets}
	if kind == types.OverlayKindTVsRef || kind == types.OverlayKindZVsRef {
		spec.Params = map[string]any{"variance": 5000.0, "sample_size": 9.0}
	}
	return spec
}

// foCompose is the F / M / ALL batch (ALL keeps both sexes, so its
// means differ from M's) carrying specs.
func foCompose(f *mgFixture, specs ...types.ComposeOverlaySpec) *types.ComposedRequest {
	return &types.ComposedRequest{
		Requests: []*types.Request{foSlot(f, "F", "F"), foSlot(f, "M", "M"), foSlot(f, "ALL", "")},
		Overlays: specs,
	}
}

func foRun(t *testing.T, f *mgFixture, req *types.ComposedRequest) *types.ComposedResponse {
	t.Helper()
	if env := csPredict(t, f.p, req); len(env.Errors) > 0 {
		t.Fatalf("predict-compose: %+v", env.Errors)
	}
	resp, err := f.p.Compose(context.Background(), req)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	return resp
}

func sortedKinds(ks []types.OverlayKind) []types.OverlayKind {
	out := append([]types.OverlayKind{}, ks...)
	slices.Sort(out)
	return out
}

func foJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// foSingleTargetGolden is each kind's one-target layer over reference F
// and target M as the engine answered it BEFORE the fan-out (captured
// on the pre-U66-#443 tree). The descriptive kinds are pinned byte for
// byte; the two-sample kinds' p-values come from the incomplete-beta /
// normal tails, so foSameJSON holds their figures to 1e-12 relative
// (cross-arch) and everything else byte for byte.
var foSingleTargetGolden = map[types.OverlayKind]string{
	types.OverlayKindIndexVsRef: `[{"name":"o","kind":"OVERLAY_INDEX_VS_REF","scope":"group","ref":{},"payload":{"shape":"series","series":{"entries":[{"key":["region=north"],"summary":{"statistic":100}},{"key":["region=south"],"summary":{"statistic":6766.666666666667}},{"key":["region=west"],"summary":{"statistic":100}}]}},"summary":{"min":100,"max":6766.666666666667,"count":3,"baseline":100}}]`,
	types.OverlayKindDeltaVsRef: `[{"name":"o","kind":"OVERLAY_DELTA_VS_REF","scope":"group","ref":{},"payload":{"shape":"series","series":{"entries":[{"key":["region=north"],"summary":{"statistic":0}},{"key":["region=south"],"summary":{"statistic":66.66666666666667}},{"key":["region=west"],"summary":{"statistic":0}}]}},"summary":{"min":0,"max":66.66666666666667,"count":3,"baseline":0}}]`,
	types.OverlayKindTVsRef:     `[{"name":"o","kind":"OVERLAY_T_VS_REF","scope":"group","ref":{},"payload":{"shape":"series","series":{"entries":[{"key":["region=north"],"summary":{"statistic":1}},{"key":["region=south"],"summary":{"statistic":0.06277196351460337}},{"key":["region=west"],"summary":{"statistic":1}}]}},"summary":{"min":0.06277196351460337,"max":1,"count":3}}]`,
	types.OverlayKindZVsRef:     `[{"name":"o","kind":"OVERLAY_Z_VS_REF","scope":"group","ref":{},"payload":{"shape":"series","series":{"entries":[{"key":["region=north"],"summary":{"statistic":1}},{"key":["region=south"],"summary":{"statistic":0.04550026389635844}},{"key":["region=west"],"summary":{"statistic":1}}]}},"summary":{"min":0.04550026389635844,"max":1,"count":3}}]`,
}

// foSameJSON reports whether two JSON documents are equal, numbers to
// 1e-12 relative.
func foSameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatal(err)
	}
	var same func(x, y any) bool
	same = func(x, y any) bool {
		switch xv := x.(type) {
		case float64:
			yv, ok := y.(float64)
			return ok && math.Abs(xv-yv) <= 1e-12*math.Max(1, math.Abs(yv))
		case []any:
			yv, ok := y.([]any)
			if !ok || len(xv) != len(yv) {
				return false
			}
			for i := range xv {
				if !same(xv[i], yv[i]) {
					return false
				}
			}
			return true
		case map[string]any:
			yv, ok := y.(map[string]any)
			if !ok || len(xv) != len(yv) {
				return false
			}
			for k := range xv {
				if !same(xv[k], yv[k]) {
					return false
				}
			}
			return true
		}
		return reflect.DeepEqual(x, y)
	}
	return same(x, y)
}

// TestComposeFanOut_SingleTargetByteIdentical: one target answers the
// pre-fan-out layer byte for byte, on every fan-out kind.
func TestComposeFanOut_SingleTargetByteIdentical(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range foSeriesKinds {
		t.Run(string(kind), func(t *testing.T) {
			resp := foRun(t, f, foCompose(f, foSpec(kind, "M")))
			got, want := foJSON(t, resp.Overlays), foSingleTargetGolden[kind]
			exact := kind == types.OverlayKindIndexVsRef || kind == types.OverlayKindDeltaVsRef
			if (exact && got != want) || !foSameJSON(t, got, want) {
				t.Errorf("layers =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// TestComposeFanOut_OneLayerPerTarget: two targets emit two layers in
// Targets order, named `<name>__<target>`, each exactly the one-target
// layer for its target (the Name aside); the two differ, so neither is
// a copy of the first target's.
func TestComposeFanOut_OneLayerPerTarget(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	// The series and matrix halves together are every fan-out kind.
	all := append(append([]types.OverlayKind{}, foSeriesKinds...), foMatrixKinds...)
	if got := overlayhost.ComposeFanOutKinds(); !reflect.DeepEqual(sortedKinds(got), sortedKinds(all)) {
		t.Fatalf("ComposeFanOutKinds() = %v, want the series and matrix halves %v", got, all)
	}
	for _, kind := range foSeriesKinds {
		t.Run(string(kind), func(t *testing.T) {
			resp := foRun(t, f, foCompose(f, foSpec(kind, "M", "ALL"), foSpec(kind, "ALL")))
			if len(resp.Overlays) != 3 {
				t.Fatalf("layers = %d, want 3 (two targets, then one)", len(resp.Overlays))
			}
			for i, want := range []string{"o__M", "o__ALL", "o"} {
				if l := resp.Overlays[i]; l.Name != want || l.Kind != kind {
					t.Errorf("layer %d = %s / %s, want %s / %s", i, l.Name, l.Kind, want, kind)
				}
			}
			payloads := make([]string, 2)
			for i, target := range []string{"M", "ALL"} {
				single := foRun(t, f, foCompose(f, foSpec(kind, target)))
				if len(single.Overlays) != 1 {
					t.Fatalf("one target: %d layers", len(single.Overlays))
				}
				got := resp.Overlays[i]
				got.Name = single.Overlays[0].Name
				if g, w := foJSON(t, got), foJSON(t, single.Overlays[0]); g != w {
					t.Errorf("target %s layer =\n%s\nwant the one-target layer\n%s", target, g, w)
				}
				payloads[i] = foJSON(t, got.Payload)
			}
			if payloads[0] == payloads[1] {
				t.Errorf("both target layers carry the same payload %s", payloads[0])
			}
			// A later spec keeps its own layer after a fan-out.
			if g, w := foJSON(t, resp.Overlays[2].Payload), payloads[1]; g != w {
				t.Errorf("third layer payload %s, want the ALL target's %s", g, w)
			}
		})
	}
}

// TestComposeFanOut_TargetCapParity: a fan-out spec naming more targets
// than MaxPanelTargets is ONE PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP from
// predict-compose and from the runtime, message and Details equal; at
// the cap it runs.
func TestComposeFanOut_TargetCapParity(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range foSeriesKinds {
		t.Run(string(kind), func(t *testing.T) {
			spec := foSpec(kind, "M", "ALL")
			spec.Options = &types.OverlayOptions{MaxPanelTargets: 1}
			req := foCompose(f, foSpec(kind, "M"), spec)
			_, err := f.p.Compose(context.Background(), req)
			ce := requireCodedError(t, err, perr.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP)
			want := map[string]any{"index": 1, "kind": string(kind), "observed": 2, "cap": 1}
			if !reflect.DeepEqual(ce.Details, want) {
				t.Errorf("runtime details %#v, want %#v", ce.Details, want)
			}
			env := csPredict(t, f.p, req)
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
			if resp := foRun(t, f, foCompose(f, spec)); len(resp.Overlays) != 2 {
				t.Errorf("at the cap: %d layers, want 2", len(resp.Overlays))
			}
		})
	}
}

// foEntryPs is every series entry's p-value (Summary.Statistic) and
// p_adjusted across layers, in layer then entry order.
func foEntryPs(t *testing.T, layers []types.OverlayLayer) (ps, adj []float64) {
	t.Helper()
	for _, l := range layers {
		for _, e := range l.Payload.Series.Entries {
			if e.Summary.Statistic == nil || e.Summary.PAdjusted == nil {
				t.Fatalf("layer %s entry %v: p %v, p_adjusted %v", l.Name, e.Key, e.Summary.Statistic, e.Summary.PAdjusted)
			}
			ps = append(ps, *e.Summary.Statistic)
			adj = append(adj, *e.Summary.PAdjusted)
		}
	}
	return ps, adj
}

// TestComposeFanOut_MultiplicityAcrossLayers: every target layer of a
// T / Z fan-out joins the Compose multiplicity fold. Under family
// `compose` one Bonferroni family spans both layers (m = every entry of
// both); under `layer` each target layer is its own family (m = its own
// entries). Predict counts one p-value per target layer.
func TestComposeFanOut_MultiplicityAcrossLayers(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
		for _, family := range []types.MultiplicityFamily{types.MultiplicityFamilyCompose, types.MultiplicityFamilyLayer} {
			t.Run(string(kind)+"/"+string(family), func(t *testing.T) {
				spec := foSpec(kind, "M", "ALL")
				spec.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodBonferroni, Family: family}
				req := foCompose(f, spec)
				env := csPredict(t, f.p, req)
				res := env.Data.(*pulse.ComposePredictResult)
				if res.PValues == nil || res.PValues.Total != 2 || res.PValues.Uncorrected != 0 {
					t.Errorf("predict p_values = %+v, want 2 (one per target layer), all corrected", res.PValues)
				}
				resp := foRun(t, f, req)
				if len(resp.Overlays) != 2 {
					t.Fatalf("layers = %d, want 2", len(resp.Overlays))
				}
				ps, adj := foEntryPs(t, resp.Overlays)
				perLayer := len(resp.Overlays[0].Payload.Series.Entries)
				m := len(ps)
				if family == types.MultiplicityFamilyLayer {
					m = perLayer
				}
				for i, p := range ps {
					if want := math.Min(1, p*float64(m)); math.Abs(adj[i]-want) > 1e-12 {
						t.Errorf("entry %d: p_adjusted %v, want min(1, %v x %d) = %v", i, adj[i], p, m, want)
					}
				}
				for _, l := range resp.Overlays {
					if l.Multiplicity == nil || l.Multiplicity.M != m {
						t.Errorf("layer %s echo %+v, want m = %d", l.Name, l.Multiplicity, m)
					}
				}
			})
		}
	}
}

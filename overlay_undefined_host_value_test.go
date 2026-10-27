package pulse_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// An undefined host value is MISSING on every series overlay host
// (matrix-extensions-hardening E4-S5). AGG_AVERAGE of b1 over e.pulse
// is undefined (NaN, JSON null) at qty 5 — no row answered b1 there —
// and the request (grouped Process), Compose and whole-chain hosts read
// it the same way: the entry keeps its key, carries NO statistic (the
// key absent on the wire, never null), raises no warning for that row
// and stays out of the layer summary count. Before, the Compose and
// chain pairs scored it as a null statistic, and the chain index raised
// a spurious zero-reference warning for it.

const undefMissingQty = "5"

// undefCheckLayer asserts layer's entry for qty 5 is present with no
// statistic key, every other entry's statistic is reported, and the
// summary counts exactly the entries with a non-null statistic.
func undefCheckLayer(t *testing.T, layer types.OverlayLayer) {
	t.Helper()
	b, err := types.MarshalFinite(layer)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Summary struct {
			Count *int `json:"count"`
		} `json:"summary"`
		Payload struct {
			Series struct {
				Entries []struct {
					Key     []string                   `json:"key"`
					Summary map[string]json.RawMessage `json:"summary"`
				} `json:"entries"`
			} `json:"series"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	seen, counted := false, 0
	for _, e := range wire.Payload.Series.Entries {
		k := e.Key[len(e.Key)-1]
		k = strings.TrimPrefix(k, "qty=")
		st, has := e.Summary["statistic"]
		if k == undefMissingQty {
			seen = true
			if has {
				t.Errorf("%s: qty %s (undefined host value) statistic = %s, want the key absent", layer.Kind, k, st)
			}
			continue
		}
		if has && string(st) != "null" {
			counted++
		}
	}
	if !seen {
		t.Fatalf("%s: no entry for qty %s in %s", layer.Kind, undefMissingQty, b)
	}
	if wire.Summary.Count == nil || *wire.Summary.Count != counted {
		t.Errorf("%s: summary count %v, want %d (the missing entry excluded)", layer.Kind, wire.Summary.Count, counted)
	}
}

// undefNoRowWarning asserts no warning anywhere in v names the qty 5
// row (a row_key ending in its key).
func undefNoRowWarning(t *testing.T, v any) {
	t.Helper()
	b, err := types.MarshalFinite(v)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(x any) {
		switch m := x.(type) {
		case map[string]any:
			if rk, ok := m["row_key"].(string); ok && (rk == undefMissingQty || strings.HasSuffix(rk, "="+undefMissingQty)) {
				t.Errorf("a warning names the undefined row: %v", m)
			}
			for _, e := range m {
				walk(e)
			}
		case []any:
			for _, e := range m {
				walk(e)
			}
		}
	}
	walk(doc)
}

// TestOverlayHosts_UndefinedValueIsMissing: one rule on the three hosts.
func TestOverlayHosts_UndefinedValueIsMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := elemComposeFixture(t)

	t.Run("request", func(t *testing.T) {
		for _, kind := range []types.OverlayKind{types.OverlayKindIndexVsTotal, types.OverlayKindShareOfTotal, types.OverlayKindDeltaVsPrior} {
			resp, err := p.Process(ctx, elemProcess("e.pulse", false, false, kind, "x"))
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			undefCheckLayer(t, resp.Overlays[0])
			undefNoRowWarning(t, resp)
		}
	})

	twoSample := map[string]any{"field": "x", "variance_target": 4.0, "variance_ref": 4.0, "sample_size_target": 10.0, "sample_size_ref": 10.0}
	compose := func(kind types.OverlayKind, params map[string]any, ref, target string) *pulse.ComposedRequest {
		r := elemCompose(false, false, kind, params)
		r.Overlays[0].Reference, r.Overlays[0].Targets = ref, []string{target}
		return r
	}
	for _, side := range []struct{ name, ref, target string }{
		{"undefined reference", "a", "b"},
		{"undefined target", "b", "a"},
	} {
		t.Run("compose/"+side.name, func(t *testing.T) {
			for _, c := range []struct {
				kind   types.OverlayKind
				params map[string]any
			}{
				{types.OverlayKindIndexVsRef, map[string]any{"field": "x"}},
				{types.OverlayKindDeltaVsRef, map[string]any{"field": "x"}},
				{types.OverlayKindPanelIndexVsRef, map[string]any{"field": "x"}},
				{types.OverlayKindTVsRef, twoSample},
				{types.OverlayKindZVsRef, twoSample},
			} {
				env, err := p.PredictCompose(ctx, compose(c.kind, c.params, side.ref, side.target))
				if err != nil || len(env.Errors) != 0 {
					t.Fatalf("%s: PredictCompose = %v %+v", c.kind, err, env)
				}
				got, err := p.Compose(ctx, compose(c.kind, c.params, side.ref, side.target))
				if err != nil {
					t.Fatalf("%s: Compose: %v", c.kind, err)
				}
				if len(got.Overlays) != 1 {
					t.Fatalf("%s: %d layers", c.kind, len(got.Overlays))
				}
				undefCheckLayer(t, got.Overlays[0])
				undefNoRowWarning(t, got)
			}
		})
	}

	t.Run("chain", func(t *testing.T) {
		for _, kind := range []types.OverlayKind{types.OverlayKindIndexVsStage, types.OverlayKindDeltaVsStage} {
			got, err := p.ProcessChain(ctx, elemChain(false, false, kind, "x"))
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			undefCheckLayer(t, *got.Overlays[0])
			undefNoRowWarning(t, got)
		}
	})
}

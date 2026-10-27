package returnshape_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/returnshape"
	"github.com/frankbardon/pulse/types"
)

// partsLayer is a multi-output layer: a scalar primary plus two parts.
func partsLayer() types.OverlayLayer {
	primary, exp, p := 2.0, 4.0, 0.03
	return types.OverlayLayer{
		Name: "m", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeCell,
		Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &primary,
			Parts: map[string]*types.OverlayPayload{
				"expected": {Shape: types.OverlayShapeScalar, Scalar: &exp},
				"p":        {Shape: types.OverlayShapeScalar, Scalar: &p},
			}},
	}
}

func partsResponse() *types.Response {
	return &types.Response{Data: []map[string]any{{"x": 1.0}}, Overlays: []types.OverlayLayer{partsLayer()}}
}

// payloadOf decodes the first layer's wire payload (nil when absent).
func payloadOf(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var doc struct {
		Overlays []map[string]any `json:"overlays"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Overlays) == 0 {
		return nil
	}
	p, _ := doc.Overlays[0]["payload"].(map[string]any)
	return p
}

// TestReturn_OverlayParts: `overlays[*].payload.parts.<part>` addresses
// one part. An excluded part is ABSENT (never null) on the wire and its
// key is gone from the Go map; excluding every part drops the `parts`
// key, never leaving `{}`; an include selects just that part; an
// unknown part is an Open path — an include of it warns
// PULSE_RETURN_PATH_UNMATCHED and, like any unmatched Open include,
// leaves only the empty ancestor containers; an exclude of it changes
// nothing; the
// standard preset (the MCP default) keeps parts whole and minimal drops
// the payload with them.
func TestReturn_OverlayParts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		ret         types.Return
		wantParts   []string // part keys on the wire; nil = no `parts` key
		wantPrimary bool
		unmatched   string
		noPayload   bool
	}{
		{name: "exclude one part", ret: types.Return{Exclude: []string{"overlays[*].payload.parts.p"}},
			wantParts: []string{"expected"}, wantPrimary: true},
		{name: "exclude every part", ret: types.Return{Exclude: []string{"overlays[*].payload.parts.p", "overlays[*].payload.parts.expected"}},
			wantPrimary: true},
		{name: "exclude parts", ret: types.Return{Exclude: []string{"overlays[*].payload.parts"}},
			wantPrimary: true},
		{name: "include one part", ret: types.Return{Include: []string{"overlays[*].payload.parts.expected"}},
			wantParts: []string{"expected"}},
		{name: "include unknown part", ret: types.Return{Include: []string{"data", "overlays[*].payload.parts.nope"}},
			unmatched: "overlays[*].payload.parts.nope"},
		{name: "exclude unknown part", ret: types.Return{Exclude: []string{"overlays[*].payload.parts.nope"}},
			wantParts: []string{"expected", "p"}, wantPrimary: true},
		{name: "standard preset", ret: types.Return{Preset: types.ReturnPresetStandard},
			wantParts: []string{"expected", "p"}, wantPrimary: true},
		{name: "minimal preset", ret: types.Return{Preset: types.ReturnPresetMinimal}, noPayload: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := partsResponse()
			returnshape.Apply(resp, planFor(t, &c.ret))
			b, err := json.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "null") {
				t.Errorf("a shaped slot was written as null: %s", b)
			}
			payload := payloadOf(t, b)
			if c.noPayload {
				if payload != nil {
					t.Errorf("payload present: %s", b)
				}
			} else {
				parts, has := payload["parts"].(map[string]any)
				if c.wantParts == nil && has {
					t.Errorf("parts key present: %s", b)
				}
				if c.wantParts != nil {
					if len(parts) != len(c.wantParts) {
						t.Errorf("wire parts = %v, want %v", parts, c.wantParts)
					}
					for _, k := range c.wantParts {
						if _, ok := parts[k]; !ok {
							t.Errorf("part %q missing: %s", k, b)
						}
					}
				}
				if _, ok := payload["scalar"]; ok != c.wantPrimary {
					t.Errorf("primary present = %v, want %v: %s", ok, c.wantPrimary, b)
				}
			}
			// Go mirrors the wire: an excluded part's key is gone.
			if !c.noPayload && c.ret.Include == nil {
				goParts := resp.Overlays[0].Payload.Parts
				if len(goParts) != len(c.wantParts) {
					t.Errorf("Go parts = %v, want %v", goParts, c.wantParts)
				}
			}
			var got string
			for _, w := range resp.Warnings {
				if w.Code == string(errors.PULSE_RETURN_PATH_UNMATCHED) {
					got = w.Details["path"].(string)
				}
			}
			if got != c.unmatched {
				t.Errorf("unmatched warning = %q, want %q", got, c.unmatched)
			}
		})
	}
}

// TestReturn_ComposeOverlayParts: the Compose-level `return` (rooted at
// ComposedResponse) addresses a top-level layer's part the same way.
func TestReturn_ComposeOverlayParts(t *testing.T) {
	t.Parallel()

	top, err := descx.ResolveComposeReturn(&types.ComposedRequest{
		Return: &types.Return{Exclude: []string{"overlays[*].payload.parts.p"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := &types.ComposedResponse{Overlays: []types.OverlayLayer{partsLayer()}}
	returnshape.ApplyComposed(out, nil, top)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	parts, _ := payloadOf(t, b)["parts"].(map[string]any)
	if _, ok := parts["p"]; ok || parts["expected"] == nil {
		t.Errorf("compose part exclude: parts = %v (%s)", parts, b)
	}
}

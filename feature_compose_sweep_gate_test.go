package pulse

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestHiddenComposeSweep_RefusedAsUnknownField: an instance offering
// Compose but not capability:compose_sweep refuses a set `sweep` slot
// as PULSE_REQUEST_UNKNOWN_FIELD on both Compose entry points, Go struct
// and JSON-decoded alike; the unprofiled control does not.
func TestHiddenComposeSweep_RefusedAsUnknownField(t *testing.T) {
	fsys := parityFS(t)
	control := newParityHost(t, fsys, "")
	feats := []string{"capability:compose", string(types.AGG_COUNT), string(types.GROUP_CATEGORY)}
	h := newParityHostWith(t, fsys, "compose-no-sweep", parityHostConfig{
		profiles: map[string][]string{"compose-no-sweep": feats},
	})
	hidden := descx.HiddenSlotKeys(&types.ComposedRequest{}, h.p.svc.InstanceSnapshot())
	if !slices.Contains(hidden, "sweep") {
		t.Fatalf("vacuous: the profile does not hide sweep (hidden %v)", hidden)
	}
	control.base = h.base
	ctx := context.Background()
	build := func(h *parityHost) *ComposedRequest {
		return &ComposedRequest{
			Requests: []*Request{withCohort(h)},
			Sweep: &types.SweepSpec{
				Axes:    []types.SweepAxis{{Name: "k", Values: []any{1}}},
				Label:   "k{{k}}",
				Request: json.RawMessage(`{"cohort":{"filename":"` + parityCohort + `"},"aggregations":[{"type":"AGG_COUNT","field":"age","label":"n"}]}`),
			},
		}
	}
	entries := map[string]func(h *parityHost, req *ComposedRequest) *slotGateOutcome{
		"Compose": func(h *parityHost, req *ComposedRequest) *slotGateOutcome {
			_, err := h.p.Compose(ctx, req)
			return slotGateFromErr(err)
		},
		"ComposeParallel": func(h *parityHost, req *ComposedRequest) *slotGateOutcome {
			_, err := h.p.ComposeParallel(ctx, req, ComposeOptions{MaxWorkers: 2})
			return slotGateFromErr(err)
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			if open := run(control, build(control)); open != nil && open.code == perr.PULSE_REQUEST_UNKNOWN_FIELD {
				t.Fatalf("default instance refuses sweep: %+v", open)
			}
			assertSlotRefusal(t, run(h, build(h)), "sweep", hidden)

			raw, err := json.Marshal(build(h))
			if err != nil {
				t.Fatal(err)
			}
			var decoded ComposedRequest
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			assertSlotRefusal(t, run(h, &decoded), "sweep", hidden)
		})
	}
}

package pulse

import (
	"context"
	"encoding/json"
	"reflect"
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

// TestSweepRandom_HiddenSamplingParity: an instance offering the sweep
// but not capability:compose_sweep_sampling refuses a sampled sweep —
// its `samples` key as PULSE_REQUEST_UNKNOWN_FIELD, a bare lhs mode as
// the unknown-mode PULSE_SWEEP_INVALID naming grid / zip only — with
// the same code and details on Compose, ComposeParallel and
// PredictCompose; the unprofiled control runs the sampled sweep.
func TestSweepRandom_HiddenSamplingParity(t *testing.T) {
	fsys := parityFS(t)
	control := newParityHost(t, fsys, "")
	feats := []string{"capability:compose", "capability:compose_sweep", string(types.AGG_COUNT), string(types.GROUP_CATEGORY)}
	h := newParityHostWith(t, fsys, "sweep-no-sampling", parityHostConfig{
		profiles: map[string][]string{"sweep-no-sampling": feats},
	})
	control.base = h.base
	ctx := context.Background()
	body := json.RawMessage(`{"cohort":{"filename":"` + parityCohort + `"},"aggregations":[{"type":"AGG_COUNT","field":"age","label":"n"}]}`)
	lo, hi := 1.0, 2.0
	cases := map[string]struct {
		spec func() *types.SweepSpec
		code perr.Code
	}{
		"samples": {func() *types.SweepSpec {
			return &types.SweepSpec{Mode: types.SweepModeLHS, Samples: 3,
				Axes: []types.SweepAxis{{Name: "k", Range: &types.SweepRange{Min: &lo, Max: &hi}}}, Label: "k{{k}}", Request: body}
		}, perr.PULSE_REQUEST_UNKNOWN_FIELD},
		"mode": {func() *types.SweepSpec {
			return &types.SweepSpec{Mode: types.SweepModeLHS, Axes: []types.SweepAxis{{Name: "k", Values: []any{1}}}, Label: "k{{k}}", Request: body}
		}, perr.PULSE_SWEEP_INVALID},
	}
	build := func(h *parityHost, spec *types.SweepSpec) *ComposedRequest {
		return &ComposedRequest{Requests: []*Request{withCohort(h)}, Sweep: spec}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := h.p.Compose(ctx, build(h, c.spec()))
			serial := slotGateFromErr(err)
			if serial == nil || serial.code != c.code {
				t.Fatalf("Compose: want %s, got %+v (%v)", c.code, serial, err)
			}
			if c.code == perr.PULSE_SWEEP_INVALID {
				if serial.details["reason"] != "mode_unknown" || !slices.Equal(toStrings(serial.details["valid"]), []string{"grid", "zip"}) {
					t.Fatalf("mode refusal details %v", serial.details)
				}
			}
			_, err = h.p.ComposeParallel(ctx, build(h, c.spec()), ComposeOptions{MaxWorkers: 2})
			if par := slotGateFromErr(err); par == nil || par.code != serial.code || !reflect.DeepEqual(par.details, serial.details) {
				t.Fatalf("ComposeParallel %+v, Compose %+v", par, serial)
			}
			env, err := h.p.PredictCompose(ctx, build(h, c.spec()))
			if err != nil {
				t.Fatalf("PredictCompose: %v", err)
			}
			pred := slotGateFromEnv(env)
			if pred == nil || pred.code != serial.code || !reflect.DeepEqual(toStrings(pred.details["unknown_keys"]), toStrings(serial.details["unknown_keys"])) ||
				pred.details["reason"] != serial.details["reason"] || !reflect.DeepEqual(toStrings(pred.details["valid"]), toStrings(serial.details["valid"])) {
				t.Fatalf("PredictCompose %+v, Compose %+v", pred, serial)
			}
		})
	}
	// The control samples: the sweep is valid there.
	lhs := cases["samples"].spec()
	out, err := control.p.Compose(ctx, build(control, lhs))
	if err != nil {
		t.Fatalf("control sampled sweep: %v", err)
	}
	if len(out.Responses) != 4 || out.SweepSeed == nil {
		t.Fatalf("control sampled sweep: %d responses, seed %v; want 1 + 3 and an echoed seed", len(out.Responses), out.SweepSeed)
	}
}

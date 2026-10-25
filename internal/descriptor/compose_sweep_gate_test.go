package descriptor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestSlotRefusal_SweepHidden: without capability:compose_sweep a set
// `sweep` slot on a ComposedRequest is an unknown field, refused before
// its slot requests; with it, nothing is refused.
func TestSlotRefusal_SweepHidden(t *testing.T) {
	req := &types.ComposedRequest{
		Requests: []*types.Request{{}},
		Sweep:    &types.SweepSpec{Axes: []types.SweepAxis{{Name: "k", Values: []any{1}}}, Request: json.RawMessage(`{}`)},
	}
	ce := asCoded(t, SlotRefusal(req, scopedOnly(featCompose)))
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"sweep"}) {
		t.Fatalf("got %s %v", ce.Code, ce.Details)
	}
	for _, k := range ce.Details["valid_keys"].([]string) {
		if k == "sweep" {
			t.Errorf("valid_keys names the hidden slot: %v", ce.Details["valid_keys"])
		}
	}
	if err := SlotRefusal(req, scopedOnly(featCompose, featComposeSweep)); err != nil {
		t.Errorf("enabled instance refuses: %v", err)
	}
	// An unset sweep is never refused.
	if err := SlotRefusal(&types.ComposedRequest{Requests: []*types.Request{{}}}, scopedOnly(featCompose)); err != nil {
		t.Errorf("sweep-free request refused: %v", err)
	}
}

// TestPayloadSchema_ComposeSweepHidden: the `sweep` request slot, the
// `ranking` response slot and every def reachable only through them are
// absent from the payload schema of an instance without
// capability:compose_sweep, and present with it.
func TestPayloadSchema_ComposeSweepHidden(t *testing.T) {
	for _, c := range []struct {
		name string
		inst *InstanceSnapshot
		want bool
	}{
		{"full registry", nil, true},
		{"enabled", scopedOnly(featCompose, featComposeSweep), true},
		{"hidden", scopedOnly(featCompose), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := PayloadSchemaForInstance(c.inst)
			if err != nil {
				t.Fatal(err)
			}
			s := string(b)
			for _, tok := range []string{`"sweep"`, `"ranking"`, `"SweepSpec"`, `"SweepAxis"`,
				`"SweepRank"`, `"SweepMode"`, `"SweepRankOrder"`, `"RankEntry"`} {
				if got := strings.Contains(s, tok); got != c.want {
					t.Errorf("%s present = %v, want %v", tok, got, c.want)
				}
			}
		})
	}
}

// TestPayloadSchema_SweepShape pins the hand-shaped parts of the sweep
// defs: the open request body, the overlays array, the scalar values
// list and the two closed vocabularies.
func TestPayloadSchema_SweepShape(t *testing.T) {
	var doc struct {
		Defs map[string]struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
			Enum       []string       `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(BuildPayloadSchema(), &doc); err != nil {
		t.Fatal(err)
	}
	prop := func(def, key string) map[string]any {
		m, _ := doc.Defs[def].Properties[key].(map[string]any)
		return m
	}
	spec := doc.Defs["SweepSpec"]
	if prop("SweepSpec", "request")["type"] != "object" || prop("SweepSpec", "overlays")["type"] != "array" {
		t.Errorf("SweepSpec request / overlays: %v / %v", spec.Properties["request"], spec.Properties["overlays"])
	}
	if !reflect.DeepEqual(spec.Required, []string{"axes", "request"}) {
		t.Errorf("SweepSpec required %v", spec.Required)
	}
	vals := prop("SweepAxis", "values")
	if vals["minItems"] != float64(1) || !reflect.DeepEqual(vals["items"], map[string]any{"type": []any{"number", "string", "boolean"}}) {
		t.Errorf("SweepAxis values %v", vals)
	}
	if got := doc.Defs["SweepMode"].Enum; !reflect.DeepEqual(got, []string{"grid", "lhs", "random", "zip"}) {
		t.Errorf("SweepMode enum %v", got)
	}
	if got := doc.Defs["SweepScale"].Enum; !reflect.DeepEqual(got, []string{"linear", "log"}) {
		t.Errorf("SweepScale enum %v", got)
	}
	if prop("SweepSpec", "samples")["type"] != "integer" || prop("SweepAxis", "range") == nil {
		t.Errorf("SweepSpec samples / SweepAxis range: %v / %v", spec.Properties["samples"], prop("SweepAxis", "range"))
	}
	if got := doc.Defs["SweepRankOrder"].Enum; !reflect.DeepEqual(got, []string{"asc", "desc"}) {
		t.Errorf("SweepRankOrder enum %v", got)
	}
}

// TestErrorOwners_SweepCodes: both sweep codes ride the capability, so
// an instance hiding it hides them.
func TestErrorOwners_SweepCodes(t *testing.T) {
	for _, c := range []errors.Code{errors.PULSE_SWEEP_INVALID, errors.PULSE_SWEEP_RANK_PATH} {
		if !reflect.DeepEqual(errorOwners[c], []string{featComposeSweep}) {
			t.Errorf("%s owners %v", c, errorOwners[c])
		}
	}
}

// TestSweepRandom_SamplingHiddenRefusal: an instance offering the sweep
// but not capability:compose_sweep_sampling refuses a set `samples` /
// `rng_seed` as unknown keys at the sweep, an axis `range` as an
// unknown key at that axis, and a random / lhs mode as an unknown mode
// whose `valid` names grid and zip only; grid / zip pass, and the
// enabled instance refuses nothing.
func TestSweepRandom_SamplingHiddenRefusal(t *testing.T) {
	lo, hi := 0.0, 1.0
	sampled := func(edit func(*types.SweepSpec)) *types.ComposedRequest {
		spec := &types.SweepSpec{
			Mode: types.SweepModeLHS, Samples: 4, RNGSeed: 7,
			Axes:    []types.SweepAxis{{Name: "k", Range: &types.SweepRange{Min: &lo, Max: &hi}}},
			Request: json.RawMessage(`{}`),
		}
		if edit != nil {
			edit(spec)
		}
		return &types.ComposedRequest{Requests: []*types.Request{{}}, Sweep: spec}
	}
	hidden := scopedOnly(featCompose, featComposeSweep)
	enabled := scopedOnly(featCompose, featComposeSweep, featSweepSampling)

	ce := asCoded(t, SlotRefusal(sampled(nil), hidden))
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || ce.Details["path"] != "sweep.rng_seed" ||
		!reflect.DeepEqual(ce.Details["unknown_keys"], []string{"rng_seed", "samples"}) {
		t.Fatalf("samples + rng_seed: got %s %v", ce.Code, ce.Details)
	}
	for _, k := range ce.Details["valid_keys"].([]string) {
		if k == "samples" || k == "rng_seed" {
			t.Errorf("valid_keys names a hidden key: %v", ce.Details["valid_keys"])
		}
	}
	ce = asCoded(t, SlotRefusal(sampled(func(s *types.SweepSpec) { s.Samples, s.RNGSeed = 0, 0 }), hidden))
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || ce.Details["path"] != "sweep.axes[0].range" {
		t.Fatalf("range: got %s %v", ce.Code, ce.Details)
	}
	for _, mode := range []types.SweepMode{types.SweepModeRandom, types.SweepModeLHS, "spiral"} {
		req := sampled(func(s *types.SweepSpec) {
			s.Mode, s.Samples, s.RNGSeed = mode, 0, 0
			s.Axes = []types.SweepAxis{{Name: "k", Values: []any{1}}}
		})
		ce = asCoded(t, SlotRefusal(req, hidden))
		if ce.Code != errors.PULSE_SWEEP_INVALID || ce.Details["reason"] != "mode_unknown" ||
			!reflect.DeepEqual(ce.Details["valid"], []string{"grid", "zip"}) {
			t.Fatalf("mode %s: got %s %v", mode, ce.Code, ce.Details)
		}
	}
	for _, mode := range []types.SweepMode{"", types.SweepModeGrid, types.SweepModeZip} {
		req := sampled(func(s *types.SweepSpec) {
			s.Mode, s.Samples, s.RNGSeed = mode, 0, 0
			s.Axes = []types.SweepAxis{{Name: "k", Values: []any{1}}}
		})
		if err := SlotRefusal(req, hidden); err != nil {
			t.Errorf("mode %q refused on the hiding instance: %v", mode, err)
		}
	}
	if err := SlotRefusal(sampled(nil), enabled); err != nil {
		t.Errorf("enabled instance refuses: %v", err)
	}
}

// TestSweepRandom_SamplingHiddenSchema: the payload schema of an
// instance hiding capability:compose_sweep_sampling keeps the sweep but
// offers grid / zip only, with no `samples`, `rng_seed`, axis `range`,
// SweepRange def or `sweep_seed` echo; the full registry and the
// enabled instance carry them all. An item for_each's mode is grid /
// zip and its axes carry values, never a range, on every instance.
func TestSweepRandom_SamplingHiddenSchema(t *testing.T) {
	type def struct {
		Properties map[string]any `json:"properties"`
		Enum       []string       `json:"enum"`
	}
	for _, c := range []struct {
		name string
		inst *InstanceSnapshot
		want bool
	}{
		{"full registry", nil, true},
		{"enabled", scopedOnly(featCompose, featComposeSweep, featSweepSampling), true},
		{"hidden", scopedOnly(featCompose, featComposeSweep, featForEach, "ATTR_FORMULA"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := PayloadSchemaForInstance(c.inst)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Defs map[string]def `json:"$defs"`
			}
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			modes := []string{"grid", "zip"}
			if c.want {
				modes = []string{"grid", "lhs", "random", "zip"}
			}
			if got := doc.Defs["SweepMode"].Enum; !reflect.DeepEqual(got, modes) {
				t.Errorf("SweepMode enum %v, want %v", got, modes)
			}
			_, samples := doc.Defs["SweepSpec"].Properties["samples"]
			_, seed := doc.Defs["SweepSpec"].Properties["rng_seed"]
			_, rng := doc.Defs["SweepAxis"].Properties["range"]
			_, rangeDef := doc.Defs["SweepRange"]
			_, echo := doc.Defs["ComposedResponse"].Properties["sweep_seed"]
			if samples != c.want || seed != c.want || rng != c.want || rangeDef != c.want || echo != c.want {
				t.Errorf("samples %v rng_seed %v range %v SweepRange %v sweep_seed %v; want all %v", samples, seed, rng, rangeDef, echo, c.want)
			}
			if fe, ok := doc.Defs["ForEach"]; ok {
				mode, _ := fe.Properties["mode"].(map[string]any)
				if got := mode["enum"]; !reflect.DeepEqual(got, []any{"grid", "zip"}) {
					t.Errorf("ForEach mode enum %v, want [grid zip]", got)
				}
				axes, _ := fe.Properties["axes"].(map[string]any)
				items, _ := axes["items"].(map[string]any)
				props, _ := items["properties"].(map[string]any)
				if _, rng := props["range"]; rng || props["values"] == nil {
					t.Errorf("ForEach axis items %v, want values and no range", items)
				}
			} else if c.name != "enabled" {
				t.Errorf("no ForEach def")
			}
		})
	}
}

// TestErrorOwners_SweepSamplingCodes: the codes only a sampled sweep
// reaches ride capability:compose_sweep_sampling.
func TestErrorOwners_SweepSamplingCodes(t *testing.T) {
	for _, c := range []errors.Code{errors.PULSE_SWEEP_MODE_MISMATCH, errors.PULSE_SWEEP_RANGE_INVALID, errors.PULSE_SWEEP_SAMPLES_INVALID} {
		if !reflect.DeepEqual(errorOwners[c], []string{featSweepSampling}) {
			t.Errorf("%s owners %v", c, errorOwners[c])
		}
	}
}

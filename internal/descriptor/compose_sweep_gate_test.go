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
	if got := doc.Defs["SweepMode"].Enum; !reflect.DeepEqual(got, []string{"grid", "zip"}) {
		t.Errorf("SweepMode enum %v", got)
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

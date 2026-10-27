package pulse

import (
	"context"
	"encoding/json"
	"testing"
)

// TestCompose_ResponsesEchoSlotLabel (U44 #310): every Compose slot's
// response carries the slot's final label — the caller's, or the
// synthesized request_<index+1> — on both orchestrators, sweep-expanded
// slots included, and the label survives the standard / minimal
// presets. A standalone Process response has no label key.
func TestCompose_ResponsesEchoSlotLabel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := sweepPulse(t, Options{})
	slot := func(label, ret string) string {
		body := `{"cohort":{"filename":"` + parityCohort + `"},"aggregations":[{"type":"AGG_COUNT","field":"age","label":"v"}]`
		if label != "" {
			body += `,"label":"` + label + `"`
		}
		if ret != "" {
			body += `,"return":` + ret
		}
		return body + "}"
	}
	raw := `{"requests":[` + slot("north", "") + `,` + slot("", `{"preset":"standard"}`) + `,` +
		slot("tiny", `{"preset":"minimal"}`) + `,` + slot("only_data", `{"include":["data"]}`) + `],
		"sweep":{"axes":[{"name":"op","values":["AGG_SUM"]}],"request":` + sweepSlotBody + `}}`
	want := []string{"north", "request_2", "tiny", "", "op=AGG_SUM"}

	entries := map[string]func(c *ComposedRequest) (*ComposedResponse, error){
		"Compose": func(c *ComposedRequest) (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func(c *ComposedRequest) (*ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 3})
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			resp, err := run(decodeComposedJSON(t, raw))
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Responses []map[string]json.RawMessage `json:"responses"`
			}
			if err := json.Unmarshal(wire, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Responses) != len(want) {
				t.Fatalf("%d responses, want %d", len(got.Responses), len(want))
			}
			for i, w := range want {
				l, present := got.Responses[i]["label"]
				switch {
				case w == "" && present:
					t.Errorf("slot %d (include data only): label %s present, want absent", i, l)
				case w != "" && string(l) != `"`+w+`"`:
					t.Errorf("slot %d label = %s, want %q", i, l, w)
				}
			}
			// A shaped response is pruned in place: the Go value
			// matches the wire.
			if resp.Responses[3].Label != "" || resp.Responses[0].Label != "north" {
				t.Errorf("Go Labels slot0 %q slot3 %q, want north / empty", resp.Responses[0].Label, resp.Responses[3].Label)
			}
		})
	}

	single, err := p.Process(ctx, decodeComposedJSON(t, `{"requests":[`+slot("solo", "")+`]}`).Requests[0])
	if err != nil {
		t.Fatal(err)
	}
	if single.Label != "" {
		t.Errorf("standalone Process Label = %q, want empty", single.Label)
	}
}

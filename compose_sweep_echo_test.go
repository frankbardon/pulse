package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// TestComposeSweep_EchoRequestCarriesExpandedRequest: with EchoRequest
// on, both Compose entry points hand back the effective request they
// ran on ComposedResponse.NormalizedRequest — a sweep expanded into its
// labelled slots with no `sweep` left, a sweep-free request as passed —
// and never put it on the wire. Off, the field stays nil.
func TestComposeSweep_EchoRequestCarriesExpandedRequest(t *testing.T) {
	ctx := context.Background()
	sweepJSON := `{"requests":[` + sweepSlotBodyWith("AGG_COUNT") + `],
		"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX"]}],"request":` + sweepSlotBody + `}}`
	freeJSON := `{"requests":[` + sweepSlotBodyWith("AGG_COUNT") + `]}`

	on := sweepPulse(t, Options{EchoRequest: true})
	off := sweepPulse(t, Options{})
	entries := map[string]func(p *Pulse, c *ComposedRequest) (*ComposedResponse, error){
		"Compose": func(p *Pulse, c *ComposedRequest) (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func(p *Pulse, c *ComposedRequest) (*ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2})
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			resp, err := run(on, decodeComposedJSON(t, sweepJSON))
			if err != nil {
				t.Fatal(err)
			}
			norm := resp.NormalizedRequest
			if norm == nil {
				t.Fatal("EchoRequest on: NormalizedRequest is nil")
			}
			if norm.Sweep != nil {
				t.Fatal("echoed request still carries the sweep block")
			}
			want := []string{"", "op=AGG_SUM", "op=AGG_MAX"}
			if len(norm.Requests) != len(want) {
				t.Fatalf("echoed %d slots, want %d", len(norm.Requests), len(want))
			}
			for i, l := range want {
				if norm.Requests[i].Label != l {
					t.Fatalf("slot %d label %q, want %q", i, norm.Requests[i].Label, l)
				}
			}
			wire, err := json.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(wire, []byte("normalized")) {
				t.Fatalf("the echo leaked onto the wire: %s", wire)
			}

			free := decodeComposedJSON(t, freeJSON)
			resp, err = run(on, free)
			if err != nil {
				t.Fatal(err)
			}
			if resp.NormalizedRequest != free {
				t.Fatal("sweep-free echo is not the request as passed")
			}

			resp, err = run(off, decodeComposedJSON(t, sweepJSON))
			if err != nil {
				t.Fatal(err)
			}
			if resp.NormalizedRequest != nil {
				t.Fatal("EchoRequest off: NormalizedRequest populated")
			}
		})
	}
}

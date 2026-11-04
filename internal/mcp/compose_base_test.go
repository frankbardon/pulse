package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// TestPulseCompose_RunsBase: pulse_compose accepts a `base` through its
// strict decode and merges it into every slot; the MCP `return` default
// lands on the base, so a slot without a `return` is shaped by the
// base's own (the default here, or one the caller set) and a slot's own
// `return` still wins.
func TestPulseCompose_RunsBase(t *testing.T) {
	t.Parallel()

	p, _ := newImportTestPulse(t)
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	base := `{"cohort":{"filename":"imports/data.pulse"},"aggregations":[{"type":"AGG_SUM","field":"amount","label":"total"}]}`
	run := func(args string) []any {
		t.Helper()
		out, err := invokeSweepTool(t, p, Config{}, toolmeta.ToolCompose, args)
		if err != nil {
			t.Fatalf("pulse_compose base: %v", err)
		}
		resps, _ := out["responses"].([]any)
		if len(resps) != 2 {
			t.Fatalf("%d responses, want 2: %v", len(resps), out)
		}
		return resps
	}
	returned := func(r any) string {
		b, _ := json.Marshal(r.(map[string]any)["returned"])
		return string(b)
	}

	resps := run(`{"base":` + base + `,"requests":[{"label":"a"},{"label":"b","aggregations":[{"type":"AGG_COUNT","field":"amount","label":"n"}]}]}`)
	for i, r := range resps {
		data, _ := r.(map[string]any)["data"].([]any)
		if len(data) != 1 || data[0].(map[string]any)["total"] == nil {
			t.Fatalf("slot %d lacks the base aggregation: %v", i, r)
		}
		if returned(r) == "null" {
			t.Fatalf("vacuous: slot %d carries no MCP return default: %v", i, r)
		}
	}
	if returned(resps[0]) != returned(resps[1]) {
		t.Errorf("slots shaped differently: %s vs %s", returned(resps[0]), returned(resps[1]))
	}

	// A base `return` reaches the slot that sets none; a slot's own wins.
	full := `{"base":{"cohort":{"filename":"imports/data.pulse"},"return":{"preset":"full"},"aggregations":[{"type":"AGG_SUM","field":"amount","label":"total"}]},` +
		`"requests":[{"label":"a"},{"label":"b","return":{"preset":"minimal"}}]}`
	resps = run(full)
	// The full preset shapes nothing, so it leaves no `returned` marker;
	// the MCP default on slot a would have left one.
	if got := returned(resps[0]); got != "null" {
		t.Errorf("slot a returned %s, want the base's full preset (no marker)", got)
	}
	if got := returned(resps[1]); got == "null" {
		t.Errorf("slot b's own minimal return lost to the base's: %s", got)
	}
}

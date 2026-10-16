package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// TestPulseCompose_RunsBanner: pulse_compose accepts a `banner` through
// its strict decode, returns one response per section × block plus the
// stitched `banner`, and every banner slot is shaped by the MCP `return`
// default exactly as an explicit slot sent without a `return` is (the
// default lands on the banner's base request).
func TestPulseCompose_RunsBanner(t *testing.T) {
	p, _ := newImportTestPulse(t)
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	slot := `{"cohort":{"filename":"imports/data.pulse"},"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"name"}],"cell":{"type":"AGG_SUM","field":"amount"}}}`
	args := `{"requests":[` + slot + `],"banner":{"request":` + slot + `,"sections":[{"label":"s"}],"total":true,` +
		`"groups":[{"label":"Name","columns":[{"type":"GROUP_CATEGORY","field":"name"}]}]}}`
	out, err := invokeSweepTool(t, p, Config{}, toolmeta.ToolCompose, args)
	if err != nil {
		t.Fatalf("pulse_compose banner: %v", err)
	}
	resps, _ := out["responses"].([]any)
	if len(resps) != 3 {
		t.Fatalf("%d responses, want 3 (1 explicit + 2 banner): %v", len(resps), out)
	}
	explicit, _ := json.Marshal(resps[0].(map[string]any)["returned"])
	if string(explicit) == "null" {
		t.Fatalf("vacuous: the explicit slot carries no MCP return default: %v", resps[0])
	}
	for i, r := range resps[1:] {
		got, _ := json.Marshal(r.(map[string]any)["returned"])
		if string(got) != string(explicit) {
			t.Errorf("banner slot %d returned %s, want the explicit slot's %s", i+1, got, explicit)
		}
	}
	banner, _ := out["banner"].(map[string]any)
	secs, _ := banner["sections"].([]any)
	if len(secs) != 1 {
		t.Fatalf("banner = %v, want one stitched section", out["banner"])
	}
	blocks, _ := secs[0].(map[string]any)["blocks"].([]any)
	if len(blocks) != 2 || blocks[0].(map[string]any)["slot"] != "s:Total" {
		t.Fatalf("blocks = %v", blocks)
	}
}

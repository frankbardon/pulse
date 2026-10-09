package gosdk_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// TestRegister_ExplainFollowsItsFeature: pulse_explain mounts iff the
// instance enables capability:explain, and a mounted one answers over
// the wire with the explain result — a null statistic still null.
func TestRegister_ExplainFollowsItsFeature(t *testing.T) {
	cfg := gosdk.Config{Version: "9.9.9", DisableCohortScan: true}

	c, cancel := profiledServer(t, afero.NewMemMapFs(), &pulse.FeatureProfile{Features: []string{"capability:process"}}, cfg)
	if slices.Contains(toolNames(t, c), "pulse_explain") {
		t.Error("pulse_explain mounted on an instance hiding capability:explain")
	}
	cancel()

	c, cancel = profiledServer(t, afero.NewMemMapFs(), &pulse.FeatureProfile{Features: []string{"capability:explain", "capability:process", "TEST_T"}}, cfg)
	defer cancel()
	if !slices.Contains(toolNames(t, c), "pulse_explain") {
		t.Fatal("pulse_explain not mounted on an instance enabling capability:explain")
	}
	res, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "pulse_explain", Arguments: map[string]any{"response": map[string]any{
			"tests": []any{map[string]any{"type": "TEST_T", "statistic": nil, "p_value": nil, "alpha": 0.05}},
		}},
	})
	if err != nil || res.IsError {
		t.Fatalf("CallTool: err=%v result=%+v", err, res)
	}
	var out struct {
		Mode     string `json:"mode"`
		Findings []struct {
			Verdict string              `json:"verdict"`
			Numbers map[string]*float64 `json:"numbers"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*mcpsdk.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "response" || len(out.Findings) != 1 || out.Findings[0].Verdict != "not_computable" || out.Findings[0].Numbers["statistic"] != nil {
		t.Fatalf("result = %+v", out)
	}
}

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

// TestRegister_RecommendFollowsItsFeature: pulse_recommend mounts iff
// the instance enables capability:recommend, and a mounted one answers
// over the wire with the recommend result.
func TestRegister_RecommendFollowsItsFeature(t *testing.T) {
	cfg := gosdk.Config{Version: "9.9.9", DisableCohortScan: true}

	c, cancel := profiledServer(t, afero.NewMemMapFs(), &pulse.FeatureProfile{Features: []string{"capability:process"}}, cfg)
	if slices.Contains(toolNames(t, c), "pulse_recommend") {
		t.Error("pulse_recommend mounted on an instance hiding capability:recommend")
	}
	cancel()

	c, cancel = profiledServer(t, afero.NewMemMapFs(), &pulse.FeatureProfile{Features: []string{"capability:recommend", "capability:process", "AGG_SUM"}}, cfg)
	defer cancel()
	if !slices.Contains(toolNames(t, c), "pulse_recommend") {
		t.Fatal("pulse_recommend not mounted on an instance enabling capability:recommend")
	}
	res, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "pulse_recommend", Arguments: map[string]any{"intent": "describe"},
	})
	if err != nil || res.IsError {
		t.Fatalf("CallTool: err=%v result=%+v", err, res)
	}
	var out struct {
		Intent          string `json:"intent"`
		Recommendations []struct {
			Operator string `json:"operator"`
		} `json:"recommendations"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*mcpsdk.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Intent != "describe" || len(out.Recommendations) == 0 {
		t.Fatalf("result = %+v", out)
	}
	for _, r := range out.Recommendations {
		if r.Operator != "AGG_SUM" {
			t.Errorf("hidden operator %s recommended", r.Operator)
		}
	}
}

package gosdk_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// TestRegister_RakeFollowsItsFeature pins pulse_rake's binding over the
// wire: a profile without capability:rake does not mount it, one with
// it does, and a call through the go-sdk writes the weighted cohort and
// returns the RakeResult.
func TestRegister_RakeFollowsItsFeature(t *testing.T) {
	fs := afero.NewMemMapFs()
	seed, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	b, err := seed.NewCohortBuilder(context.Background(), "src.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "grp", Type: encoding.FieldTypeU8, Description: "Sample stratum the respondent was drawn from"},
	}}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []uint64{1, 1, 1, 2} {
		if err := b.Append(pulse.CohortRow{g}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}

	without, cancel := profiledServer(t, fs, &pulse.FeatureProfile{Features: []string{"capability:process"}}, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel()
	if slices.Contains(toolNames(t, without), toolmeta.ToolRake) {
		t.Fatal("pulse_rake mounted without capability:rake")
	}

	with, cancel2 := profiledServer(t, fs, &pulse.FeatureProfile{Features: []string{"capability:rake"}}, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel2()
	if !slices.Contains(toolNames(t, with), toolmeta.ToolRake) {
		t.Fatal("pulse_rake not mounted with capability:rake")
	}
	res, err := with.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: toolmeta.ToolRake, Arguments: map[string]any{
		"cohort":  "src.pulse",
		"out":     "weighted.pulse",
		"margins": []any{map[string]any{"field": "grp", "targets": map[string]any{"1": 0.5, "2": 0.5}}},
	}})
	if err != nil || res.IsError {
		t.Fatalf("call: err %v result %+v", err, res)
	}
	var out struct {
		Records     int64  `json:"records"`
		WeightField string `json:"weight_field"`
		Converged   bool   `json:"converged"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*mcpsdk.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Records != 4 || out.WeightField != "rake_weight" || !out.Converged {
		t.Fatalf("result = %+v", out)
	}
	if ok, _ := afero.Exists(fs, "weighted.pulse"); !ok {
		t.Fatal("weighted.pulse not written")
	}
}

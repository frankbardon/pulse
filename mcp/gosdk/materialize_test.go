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

// TestRegister_MaterializeFollowsItsFeature pins pulse_materialize's
// binding over the wire: a profile without capability:materialize does
// not mount it, one with it does, and a call through the go-sdk writes
// the cohort and returns the result object.
func TestRegister_MaterializeFollowsItsFeature(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	seed, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	b, err := seed.NewCohortBuilder(context.Background(), "src.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16, Description: "Week number of the season"},
		{Name: "spend", Type: encoding.FieldTypeF64, Description: "Media spend in thousands"},
	}}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for w := 1; w <= 4; w++ {
		if err := b.Append(pulse.CohortRow{uint64(w), float64(w * 10)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}

	without, cancel := profiledServer(t, fs, &pulse.FeatureProfile{Features: []string{"capability:process"}}, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel()
	if slices.Contains(toolNames(t, without), toolmeta.ToolMaterialize) {
		t.Fatal("pulse_materialize mounted without capability:materialize")
	}

	with, cancel2 := profiledServer(t, fs, &pulse.FeatureProfile{Features: []string{"capability:process", "capability:materialize", "FEAT_LOG"}}, gosdk.Config{Version: "9.9.9", DisableCohortScan: true})
	defer cancel2()
	if !slices.Contains(toolNames(t, with), toolmeta.ToolMaterialize) {
		t.Fatal("pulse_materialize not mounted with capability:materialize")
	}
	res, err := with.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: toolmeta.ToolMaterialize, Arguments: map[string]any{
		"request": map[string]any{
			"cohort":   map[string]any{"filename": "src.pulse"},
			"features": []any{map[string]any{"type": "FEAT_LOG", "field": "spend", "label": "log_spend"}},
		},
		"out": "derived.pulse",
	}})
	if err != nil || res.IsError {
		t.Fatalf("call: err %v result %+v", err, res)
	}
	var out struct {
		Records        int64    `json:"records"`
		DerivedColumns []string `json:"derived_columns"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*mcpsdk.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Records != 4 || !slices.Equal(out.DerivedColumns, []string{"log_spend"}) {
		t.Fatalf("result = %+v", out)
	}
	if ok, _ := afero.Exists(fs, "derived.pulse"); !ok {
		t.Fatal("derived.pulse not written")
	}
}

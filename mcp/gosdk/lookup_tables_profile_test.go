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

// TestRegister_LookupTablesToolFollowsCapability pins pulse_lookup_tables'
// binding to capability:lookup_tables: an instance hiding the capability
// does not mount the tool, one enabling it mounts it and the call lists
// the table's metadata — while gosdk.RegisteredTools stays the GLOBAL
// list either way.
func TestRegister_LookupTablesToolFollowsCapability(t *testing.T) {
	t.Parallel()

	const tool = "pulse_lookup_tables"
	if !slices.Contains(gosdk.RegisteredTools(), tool) {
		t.Fatalf("gosdk.RegisteredTools() lacks %s", tool)
	}
	for _, tc := range []struct {
		name    string
		profile []string
		mounted bool
	}{
		{name: "hidden", profile: []string{"capability:process"}, mounted: false},
		{name: "enabled", profile: []string{"capability:process", "capability:lookup_tables"}, mounted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := pulse.New(pulse.Options{
				FS:             afero.NewMemMapFs(),
				FeatureProfile: &pulse.FeatureProfile{Features: tc.profile},
				Extensions: pulse.Extensions{LookupTables: map[string]pulse.LookupTable{
					"rates": {Description: "rate grid", Rows: map[string]float64{"a": 1.5, "b": 2.5}},
				}},
			})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			srv := newServer()
			if err := gosdk.Register(srv, p, gosdk.Config{Version: "9.9.9", DisableCohortScan: true}); err != nil {
				t.Fatalf("Register: %v", err)
			}
			c, cancel := connect(t, srv)
			defer cancel()
			if got := slices.Contains(toolNames(t, c), tool); got != tc.mounted {
				t.Fatalf("%s mounted = %v, want %v", tool, got, tc.mounted)
			}
			if !tc.mounted {
				return
			}
			res, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: tool, Arguments: map[string]any{}})
			if err != nil || res.IsError {
				t.Fatalf("CallTool: err=%v result=%+v", err, res)
			}
			text := res.Content[0].(*mcpsdk.TextContent).Text
			var out struct {
				Tables []pulse.LookupTableInfo `json:"tables"`
			}
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatalf("decode %s: %v", text, err)
			}
			if len(out.Tables) != 1 || out.Tables[0].Name != "rates" ||
				out.Tables[0].CellCount == nil || *out.Tables[0].CellCount != 2 {
				t.Errorf("tables = %+v; want rates with cell_count 2 (wire %s)", out.Tables, text)
			}
		})
	}
}

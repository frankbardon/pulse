package mcp

import (
	"encoding/json"
	"slices"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolCall builds a go-sdk CallToolRequest carrying the given arguments as the
// raw JSON the handler unmarshals itself (the low-level Server.AddTool path
// does no auto-decode). Under the canonical structured contract the arguments
// map IS the typed tool input at the top level.
func toolCall(t *testing.T, name string, args map[string]any) *mcpsdk.CallToolRequest {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return &mcpsdk.CallToolRequest{
		Params: &mcpsdk.CallToolParamsRaw{
			Name:      name,
			Arguments: raw,
		},
	}
}

func TestRegisteredTools_Stable(t *testing.T) {
	got := RegisteredTools()
	want := []string{
		ToolInspect,
		ToolPredict,
		ToolProcess,
		ToolProcessChain,
		ToolCompose,
		ToolSample,
		ToolFacet,
		ToolFacetSchema,
		ToolSkillsList,
		ToolSkillsGet,
		ToolManifest,
		ToolExamplesSearch,
		ToolExamplesGet,
		ToolErrorsLookup,
		ToolImport,
		ToolDrop,
		ToolImportsList,
		ToolLabelTables,
		ToolLabelResolve,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("RegisteredTools mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestRegisteredTools_AllPrefixed(t *testing.T) {
	for _, name := range RegisteredTools() {
		if len(name) < 6 || name[:6] != "pulse_" {
			t.Errorf("tool %q must use pulse_ prefix", name)
		}
	}
}

// TestManifest_HasMCPTool verifies that pulse_manifest is registered as
// an MCP tool. Bootstrap clients call this tool first.
func TestManifest_HasMCPTool(t *testing.T) {
	if !slices.Contains(RegisteredTools(), ToolManifest) {
		t.Fatalf("pulse_manifest tool missing from RegisteredTools(): %v", RegisteredTools())
	}
}

// TestRegisteredToolsMeta_MatchesRegisteredTools verifies the meta and
// name-only views stay aligned. Source of truth for the descriptor
// manifest mirror.
func TestRegisteredToolsMeta_MatchesRegisteredTools(t *testing.T) {
	meta := RegisteredToolsMeta()
	names := RegisteredTools()
	if len(meta) != len(names) {
		t.Fatalf("meta len %d != names len %d", len(meta), len(names))
	}
	for i, m := range meta {
		if m.Name != names[i] {
			t.Errorf("meta[%d].Name = %q, want %q", i, m.Name, names[i])
		}
		if m.Description == "" {
			t.Errorf("meta[%d] %q missing description", i, m.Name)
		}
	}
}

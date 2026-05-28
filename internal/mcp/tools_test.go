package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/afero"
)

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
		ToolAsk,
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

// TestAsk_MCPHandler exercises the pulse_ask MCP handler end-to-end:
// marshal an AskRequest into the tool args, call the handler, and
// assert the response carries the predict envelope. Uses predict-only
// mode so the test cohort can stay header+schema-only (no row data).
func TestAsk_MCPHandler(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Header+schema-only cohort: predict reads only headers, so this is
	// sufficient for the round-trip without standing up a full row set.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Score for an observation"},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	if err := afero.WriteFile(fs, "demo.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	// Build the inner AskRequest, marshal it, and ship it through the
	// handler the same way the MCP server would.
	askBody, err := json.Marshal(map[string]any{
		"predict": true,
		"request": map[string]any{
			"cohort": map[string]any{"filename": "demo.pulse"},
			"aggregations": []map[string]any{
				{"type": "AGG_AVERAGE", "field": "score", "label": "avg"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal AskRequest: %v", err)
	}

	handler := handleAsk(nil, p, false, boundHandlers{})
	call := mcpgo.CallToolRequest{}
	call.Params.Name = ToolAsk
	call.Params.Arguments = map[string]any{"request": string(askBody)}

	out, err := handler(context.Background(), call)
	if err != nil {
		t.Fatalf("handleAsk: %v", err)
	}
	if out.IsError {
		t.Fatalf("handleAsk returned error result: %+v", out.Content)
	}
	if len(out.Content) == 0 {
		t.Fatal("handleAsk returned no content")
	}
	text, ok := out.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", out.Content[0])
	}

	var decoded pulse.AskResponse
	if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
		t.Fatalf("unmarshal AskResponse: %v\n%s", err, text.Text)
	}
	if decoded.FormatVersion != "1.0" {
		t.Errorf("FormatVersion = %q, want \"1.0\"", decoded.FormatVersion)
	}
	if decoded.Predict == nil {
		t.Fatal("Predict missing from AskResponse")
	}
	if !decoded.Predict.Valid {
		t.Errorf("expected valid predict, got: %+v", decoded.Predict)
	}
	if decoded.Process != nil {
		t.Error("expected Process nil in predict-only mode")
	}
	if !strings.Contains(text.Text, "\"format_version\"") {
		t.Errorf("response missing format_version envelope key: %s", text.Text)
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

// TestMCPErrorsLookup_RoundTrip drives handleErrorsLookup with each of
// the three argument shapes (code / domain / query) and asserts the
// JSON-encoded result decodes to the expected LookupResult shape.
func TestMCPErrorsLookup_RoundTrip(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	handler := handleErrorsLookup(p)

	call := func(args map[string]any) []perr.LookupResult {
		t.Helper()
		req := mcpgo.CallToolRequest{}
		req.Params.Name = ToolErrorsLookup
		req.Params.Arguments = args
		out, err := handler(context.Background(), req)
		if err != nil {
			t.Fatalf("handler: %v", err)
		}
		if out.IsError {
			t.Fatalf("handler returned error result: %+v", out.Content)
		}
		if len(out.Content) == 0 {
			t.Fatal("handler returned no content")
		}
		text, ok := out.Content[0].(mcpgo.TextContent)
		if !ok {
			t.Fatalf("expected TextContent, got %T", out.Content[0])
		}
		var decoded []perr.LookupResult
		if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
			t.Fatalf("decode: %v\n%s", err, text.Text)
		}
		return decoded
	}

	// code → 1-element array on hit.
	hit := call(map[string]any{"code": string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL)})
	if len(hit) != 1 {
		t.Fatalf("code lookup hit: len=%d want 1", len(hit))
	}
	if hit[0].Domain != "PULSE" {
		t.Errorf("Domain=%q want PULSE", hit[0].Domain)
	}
	if hit[0].Message == "" {
		t.Errorf("Message is empty")
	}

	// code → empty array on miss.
	miss := call(map[string]any{"code": "NOT_A_REAL_CODE"})
	if len(miss) != 0 {
		t.Errorf("miss returned %d, want 0", len(miss))
	}

	// domain → every PULSE code.
	dom := call(map[string]any{"domain": "PULSE"})
	if len(dom) == 0 {
		t.Errorf("domain=PULSE returned 0")
	}
	for _, r := range dom {
		if r.Domain != "PULSE" {
			t.Errorf("PULSE-domain result has Domain=%q", r.Domain)
		}
	}

	// query → at least one match for known substring.
	q := call(map[string]any{"query": "numeric aggregation"})
	if len(q) == 0 {
		t.Errorf("query returned 0 results")
	}

	// all-empty → SERVICE_VALIDATION error.
	req := mcpgo.CallToolRequest{}
	req.Params.Name = ToolErrorsLookup
	req.Params.Arguments = map[string]any{}
	out, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler with empty args: %v", err)
	}
	if !out.IsError {
		t.Errorf("expected IsError=true for empty args")
	}

	// Intersection: code + domain match (both filter to same code).
	both := call(map[string]any{
		"code":   string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL),
		"domain": "PULSE",
	})
	if len(both) != 1 {
		t.Errorf("code+domain intersection len=%d want 1", len(both))
	}

	// Intersection: code + domain disagree (code is PULSE, domain is CLI).
	none := call(map[string]any{
		"code":   string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL),
		"domain": "CLI",
	})
	if len(none) != 0 {
		t.Errorf("disjoint intersection len=%d want 0", len(none))
	}
}

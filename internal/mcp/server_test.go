//go:build ignore

// TODO(E1-S2/S3/S4): not yet ported to github.com/modelcontextprotocol/go-sdk.
// This file still targets mark3labs/mcp-go and is excluded from the build by
// the constraint above. Handler logic is preserved verbatim; the per-file
// migration stories (tools/strict_decode/import_tools -> E1-S2; resources/
// prompts -> E1-S3; schema_bind -> E1-S4) remove this constraint as they port.

package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/afero"
)

// writeTestCohort writes a header+schema-only .pulse file at path.
func writeTestCohort(t *testing.T, fs afero.Fs, path string) {
	t.Helper()
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
	if err := afero.WriteFile(fs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// startInProcessClient spins up an MCP client wired to the Pulse server in
// process. It returns the initialized client and a cleanup function.
func startInProcessClient(t *testing.T, p *pulse.Pulse) (*client.Client, context.CancelFunc) {
	t.Helper()
	srv := mcp.New(p)
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := c.Start(ctx); err != nil {
		cancel()
		t.Fatalf("client.Start: %v", err)
	}

	initReq := mcpgo.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpgo.Implementation{Name: "pulse-test", Version: "1.0.0"}
	initReq.Params.Capabilities = mcpgo.ClientCapabilities{}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		cancel()
		t.Fatalf("client.Initialize: %v", err)
	}

	return c, cancel
}

func TestServer_ListTools_RoundTrip(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	c, cancel := startInProcessClient(t, p)
	defer cancel()

	ctx := context.Background()
	out, err := c.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := make([]string, len(out.Tools))
	for i, tool := range out.Tools {
		got[i] = tool.Name
	}
	slices.Sort(got)

	want := append([]string(nil), mcp.RegisteredTools()...)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("server tool list mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestServer_ListResources_IncludesCohortAndSkill(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeTestCohort(t, fs, "demo.pulse")

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	c, cancel := startInProcessClient(t, p)
	defer cancel()

	ctx := context.Background()
	out, err := c.ListResources(ctx, mcpgo.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	uris := make(map[string]bool, len(out.Resources))
	for _, r := range out.Resources {
		uris[r.URI] = true
	}

	if !uris["pulse://demo.pulse"] {
		t.Errorf("expected cohort resource pulse://demo.pulse, got %v", uris)
	}
	if !uris["pulse-skill://session-bootstrap"] {
		t.Errorf("expected skill resource pulse-skill://session-bootstrap, got %v", uris)
	}
	if !uris["pulse://schema"] {
		t.Errorf("expected payload-schema resource pulse://schema, got %v", uris)
	}
}

func TestServer_ReadSchemaResource(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	c, cancel := startInProcessClient(t, p)
	defer cancel()

	req := mcpgo.ReadResourceRequest{}
	req.Params.URI = "pulse://schema"

	out, err := c.ReadResource(context.Background(), req)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(out.Contents) == 0 {
		t.Fatal("expected schema contents")
	}
	text, ok := out.Contents[0].(mcpgo.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", out.Contents[0])
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(text.Text), &doc); err != nil {
		t.Fatalf("schema resource is not valid JSON: %v", err)
	}
	if _, ok := doc["$schema"]; !ok {
		t.Errorf("schema resource missing $schema key")
	}
	if _, ok := doc["$defs"]; !ok {
		t.Errorf("schema resource missing $defs key")
	}
}

func TestServer_CallPredict_RoundTrip(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeTestCohort(t, fs, "demo.pulse")

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	c, cancel := startInProcessClient(t, p)
	defer cancel()

	requestBody, err := json.Marshal(map[string]any{
		"cohort": map[string]any{"filename": "demo.pulse"},
		"aggregations": []map[string]any{
			{"type": "AGG_AVERAGE", "field": "score", "label": "avg_score"},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := mcpgo.CallToolRequest{}
	req.Params.Name = mcp.ToolPredict
	req.Params.Arguments = map[string]any{"request": string(requestBody)}

	ctx := context.Background()
	out, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool predict: %v", err)
	}
	if out.IsError {
		t.Fatalf("predict reported error: %+v", out.Content)
	}
	if len(out.Content) == 0 {
		t.Fatal("predict returned no content")
	}

	text, ok := out.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", out.Content[0])
	}
	if !strings.Contains(text.Text, "valid") && !strings.Contains(text.Text, "Valid") {
		t.Errorf("predict text missing validity field: %s", text.Text)
	}
}

func TestServer_ReadCohortResource(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeTestCohort(t, fs, "demo.pulse")

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	c, cancel := startInProcessClient(t, p)
	defer cancel()

	ctx := context.Background()
	req := mcpgo.ReadResourceRequest{}
	req.Params.URI = "pulse://demo.pulse"

	out, err := c.ReadResource(ctx, req)
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(out.Contents) == 0 {
		t.Fatal("expected contents")
	}
	tc, ok := out.Contents[0].(mcpgo.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", out.Contents[0])
	}
	if !strings.Contains(tc.Text, "score") {
		t.Errorf("inspect JSON should mention field name 'score', got: %s", tc.Text)
	}
}

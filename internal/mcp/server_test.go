package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
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

// startInProcessClient spins up an MCP client wired to the Pulse server over an
// in-memory transport pair. It returns the initialized client session and a
// cleanup function. The go-sdk in-memory transports (NewInMemoryTransports)
// replace mark3labs' client.NewInProcessClient; client.Connect performs the
// initialize handshake automatically, so the returned session is ready to use.
func startInProcessClient(t *testing.T, p *pulse.Pulse) (*mcpsdk.ClientSession, func()) {
	t.Helper()
	return connectInProcess(t, mcp.New(p))
}

// connectInProcess connects an in-memory client session to the given server.
func connectInProcess(t *testing.T, srv *mcpsdk.Server) (*mcpsdk.ClientSession, func()) {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()

	ss, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}

	c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "pulse-test", Version: "1.0.0"}, nil)
	cs, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = ss.Close()
		t.Fatalf("client.Connect: %v", err)
	}

	cleanup := func() {
		_ = cs.Close()
		_ = ss.Close()
	}
	return cs, cleanup
}

// callText returns the first text-content body from a tool-call result,
// failing the test if the result is an error or carries no text content.
func callText(t *testing.T, out *mcpsdk.CallToolResult) string {
	t.Helper()
	if out.IsError {
		t.Fatalf("tool reported error: %+v", out.Content)
	}
	if len(out.Content) == 0 {
		t.Fatal("tool returned no content")
	}
	text, ok := out.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("expected *TextContent, got %T", out.Content[0])
	}
	return text.Text
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
	out, err := c.ListTools(ctx, nil)
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
	out, err := c.ListResources(ctx, nil)
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

// TestServer_ListResourceTemplates asserts that the two URI schemes are
// exposed as RFC 6570 resource templates in addition to the concrete static
// resources. Under go-sdk, pulse:// and pulse-skill:// are registered via
// AddResourceTemplate so reads of un-enumerated URIs still resolve.
func TestServer_ListResourceTemplates(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeTestCohort(t, fs, "demo.pulse")

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	c, cancel := startInProcessClient(t, p)
	defer cancel()

	ctx := context.Background()
	out, err := c.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}

	templates := make(map[string]bool, len(out.ResourceTemplates))
	for _, rt := range out.ResourceTemplates {
		templates[rt.URITemplate] = true
	}

	if !templates[mcp.CohortURITemplate] {
		t.Errorf("expected cohort template %q, got %v", mcp.CohortURITemplate, templates)
	}
	if !templates[mcp.SkillURITemplate] {
		t.Errorf("expected skill template %q, got %v", mcp.SkillURITemplate, templates)
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

	out, err := c.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "pulse://schema"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(out.Contents) == 0 {
		t.Fatal("expected schema contents")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out.Contents[0].Text), &doc); err != nil {
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

	ctx := context.Background()
	out, err := c.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      mcp.ToolPredict,
		Arguments: map[string]any{"request": string(requestBody)},
	})
	if err != nil {
		t.Fatalf("CallTool predict: %v", err)
	}
	text := callText(t, out)
	if !strings.Contains(text, "valid") && !strings.Contains(text, "Valid") {
		t.Errorf("predict text missing validity field: %s", text)
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

	out, err := c.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "pulse://demo.pulse"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(out.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if !strings.Contains(out.Contents[0].Text, "score") {
		t.Errorf("inspect JSON should mention field name 'score', got: %s", out.Contents[0].Text)
	}
}

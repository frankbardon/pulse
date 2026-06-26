package gosdk_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/frankbardon/pulse/mcp/toolmeta"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// newServer builds a bare go-sdk server (the caller-owned object) so the test
// exercises the documented mount-onto-a-server-you-provide contract.
func newServer() *mcpsdk.Server {
	return mcpsdk.NewServer(&mcpsdk.Implementation{Name: "host-under-test", Version: "9.9.9"}, nil)
}

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

// connect wires an in-memory client session to the given server.
func connect(t *testing.T, srv *mcpsdk.Server) (*mcpsdk.ClientSession, func()) {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()

	ss, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "client-under-test", Version: "1.0.0"}, nil)
	cs, err := c.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = ss.Close()
		t.Fatalf("client.Connect: %v", err)
	}
	return cs, func() {
		_ = cs.Close()
		_ = ss.Close()
	}
}

func newPulse(t *testing.T, fs afero.Fs) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

func TestRegister_NilGuards(t *testing.T) {
	p := newPulse(t, afero.NewMemMapFs())
	if err := gosdk.Register(nil, p, gosdk.Config{}); err == nil {
		t.Error("Register(nil server) should error")
	}
	if err := gosdk.Register(newServer(), nil, gosdk.Config{}); err == nil {
		t.Error("Register(nil pulse) should error")
	}
}

// TestRegister_MountsAllSurfaces asserts Register mounts every built-in tool,
// both resource schemes + the static schema resource, and both prompts onto a
// caller-supplied bare server.
func TestRegister_MountsAllSurfaces(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeTestCohort(t, fs, "demo.pulse")
	p := newPulse(t, fs)

	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "9.9.9", BindOnInspect: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()
	ctx := context.Background()

	// All 19 tools.
	toolsOut, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := make([]string, len(toolsOut.Tools))
	for i, tool := range toolsOut.Tools {
		got[i] = tool.Name
	}
	slices.Sort(got)
	want := append([]string(nil), gosdk.RegisteredTools()...)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("tool list mismatch\n got: %v\nwant: %v", got, want)
	}
	if len(want) != 19 {
		t.Fatalf("expected 19 registered tools, got %d", len(want))
	}

	// Resource schemes (templates) + static schema resource.
	rtOut, err := c.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}
	templates := map[string]bool{}
	for _, rt := range rtOut.ResourceTemplates {
		templates[rt.URITemplate] = true
	}
	if !templates[gosdk.CohortURITemplate] {
		t.Errorf("missing cohort template %q: %v", gosdk.CohortURITemplate, templates)
	}
	if !templates[gosdk.SkillURITemplate] {
		t.Errorf("missing skill template %q: %v", gosdk.SkillURITemplate, templates)
	}

	resOut, err := c.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	uris := map[string]bool{}
	for _, r := range resOut.Resources {
		uris[r.URI] = true
	}
	if !uris[gosdk.SchemaResourceURI] {
		t.Errorf("missing static schema resource %q", gosdk.SchemaResourceURI)
	}
	if !uris["pulse://demo.pulse"] {
		t.Errorf("missing cohort resource pulse://demo.pulse")
	}
	if !uris["pulse-skill://session-bootstrap"] {
		t.Errorf("missing skill resource pulse-skill://session-bootstrap")
	}

	// Both prompts.
	promptsOut, err := c.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	prompts := map[string]bool{}
	for _, pr := range promptsOut.Prompts {
		prompts[pr.Name] = true
	}
	for _, want := range gosdk.RegisteredPrompts() {
		if !prompts[want] {
			t.Errorf("missing prompt %q: %v", want, prompts)
		}
	}
}

// TestRegister_ToolCallWorks exercises one round-trip tool call through the
// mounted catalog.
func TestRegister_ToolCallWorks(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeTestCohort(t, fs, "demo.pulse")
	p := newPulse(t, fs)

	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "1.0.0"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()

	out, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: toolmeta.ToolPredict,
		Arguments: map[string]any{
			"cohort": map[string]any{"filename": "demo.pulse"},
			"aggregations": []map[string]any{
				{"type": "AGG_AVERAGE", "field": "score", "label": "avg_score"},
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool predict: %v", err)
	}
	if out.IsError {
		t.Fatalf("predict reported error: %+v", out.Content)
	}
	text, ok := out.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", out.Content[0])
	}
	if !strings.Contains(text.Text, "valid") && !strings.Contains(text.Text, "Valid") {
		t.Errorf("predict text missing validity field: %s", text.Text)
	}
}

// processToolBound reports whether the pulse_process tool currently advertised
// by the server carries the schema-bound description suffix.
func processToolBound(t *testing.T, c *mcpsdk.ClientSession) bool {
	t.Helper()
	out, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range out.Tools {
		if tool.Name == toolmeta.ToolProcess {
			return strings.Contains(tool.Description, "(schema-bound)")
		}
	}
	t.Fatalf("pulse_process not in tool list")
	return false
}

// TestRegister_BindOnInspect asserts a successful pulse_inspect re-registers
// the action tools with schema-bound variants when BindOnInspect is true.
func TestRegister_BindOnInspect(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeTestCohort(t, fs, "demo.pulse")
	p := newPulse(t, fs)

	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "1.0.0", BindOnInspect: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()
	ctx := context.Background()

	if processToolBound(t, c) {
		t.Fatal("pulse_process should be unbound before any inspect")
	}

	out, err := c.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      toolmeta.ToolInspect,
		Arguments: map[string]any{"path": "demo.pulse"},
	})
	if err != nil {
		t.Fatalf("CallTool inspect: %v", err)
	}
	if out.IsError {
		t.Fatalf("inspect reported error: %+v", out.Content)
	}

	if !processToolBound(t, c) {
		t.Fatal("pulse_process should be schema-bound after a successful inspect")
	}
}

// TestRegister_BindDisabled asserts BindOnInspect=false leaves the unbound
// global tools in place even after a successful inspect.
func TestRegister_BindDisabled(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeTestCohort(t, fs, "demo.pulse")
	p := newPulse(t, fs)

	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "1.0.0", BindOnInspect: false}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()
	ctx := context.Background()

	out, err := c.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      toolmeta.ToolInspect,
		Arguments: map[string]any{"path": "demo.pulse"},
	})
	if err != nil {
		t.Fatalf("CallTool inspect: %v", err)
	}
	if out.IsError {
		t.Fatalf("inspect reported error: %+v", out.Content)
	}
	if processToolBound(t, c) {
		t.Fatal("pulse_process must stay unbound when BindOnInspect is false")
	}
}

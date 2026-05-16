package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/mcp/mcptools"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/afero"
)

// makeSchema constructs a schema with one numeric and one categorical
// field plus a date to cover the primary classification slots.
// Description left empty — Inspect synthesizes.
func makeSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	dict.Add("alpha")
	dict.Add("beta")
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64},
			{Name: "category", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
			{Name: "observed_on", Type: encoding.FieldTypeDate},
		},
	}
}

// writeBoundTestCohort persists a header+schema-only .pulse file in fs.
func writeBoundTestCohort(t *testing.T, fs afero.Fs, path string, schema *encoding.Schema) {
	t.Helper()
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

// decodeRequestSchema pulls the request sub-schema out of the bound JSON
// Schema body produced by Bind for action tools.
func decodeRequestSchema(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var outer map[string]any
	if err := json.Unmarshal(raw, &outer); err != nil {
		t.Fatalf("unmarshal outer: %v", err)
	}
	props, _ := outer["properties"].(map[string]any)
	req, _ := props["request"].(map[string]any)
	return req
}

// enumOf navigates request.<key>.items.properties.field.enum (the common
// case) and returns the enum as a sorted slice. Missing path returns nil.
func enumOf(req map[string]any, arrayKey, innerKey string) []string {
	arr, _ := req["properties"].(map[string]any)
	if arr == nil {
		return nil
	}
	a, _ := arr[arrayKey].(map[string]any)
	if a == nil {
		return nil
	}
	items, _ := a["items"].(map[string]any)
	if items == nil {
		return nil
	}
	ps, _ := items["properties"].(map[string]any)
	if ps == nil {
		return nil
	}
	field, _ := ps[innerKey].(map[string]any)
	if field == nil {
		return nil
	}
	enum, _ := field["enum"].([]any)
	if enum == nil {
		return nil
	}
	out := make([]string, 0, len(enum))
	for _, v := range enum {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// TestMCPSchemaBinding_RemovesInvalidFields verifies that
// request.attributes[].field is constrained to numeric fields only
// (categorical "category" must not appear). For aggregations, the v1
// cut applies a union (all-fields) because per-element Type→Field
// correlation is not expressible in JSON Schema; we still assert the
// numeric "score" is in the aggregator field enum.
func TestMCPSchemaBinding_RemovesInvalidFields(t *testing.T) {
	schemas, err := Bind(makeSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	raw, ok := schemas[mcptools.ToolProcess]
	if !ok {
		t.Fatal("missing process schema")
	}
	req := decodeRequestSchema(t, raw)

	attrFields := enumOf(req, "attributes", "field")
	if !slices.Contains(attrFields, "score") {
		t.Errorf("attributes.field enum missing numeric field 'score': %v", attrFields)
	}
	if slices.Contains(attrFields, "category") {
		t.Errorf("attributes.field enum must not contain categorical 'category': %v", attrFields)
	}

	aggFields := enumOf(req, "aggregations", "field")
	if !slices.Contains(aggFields, "score") {
		t.Errorf("aggregations.field enum missing 'score': %v", aggFields)
	}
}

// TestMCPSchemaBinding_AllFieldsInFiltererEnum verifies that the
// filterer field enum is the full set of cohort field names.
func TestMCPSchemaBinding_AllFieldsInFiltererEnum(t *testing.T) {
	schemas, err := Bind(makeSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	raw := schemas[mcptools.ToolProcess]
	req := decodeRequestSchema(t, raw)

	filterFields := enumOf(req, "filterers", "field")
	want := []string{"score", "category", "observed_on"}
	for _, w := range want {
		if !slices.Contains(filterFields, w) {
			t.Errorf("filterer field enum missing %q: %v", w, filterFields)
		}
	}
	if len(filterFields) != len(want) {
		t.Errorf("filterer field enum size = %d, want %d", len(filterFields), len(want))
	}
}

// TestMCPSchemaBinding_SampleAndFacetFieldEnum verifies the standalone
// sample/facet bound schemas. Sample has no field arg; facet's field
// must be enum-constrained.
func TestMCPSchemaBinding_SampleAndFacetFieldEnum(t *testing.T) {
	schemas, err := Bind(makeSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// pulse_facet: field has an enum of all field names.
	facetRaw, ok := schemas[mcptools.ToolFacet]
	if !ok {
		t.Fatal("missing facet schema")
	}
	var facetSchema map[string]any
	if err := json.Unmarshal(facetRaw, &facetSchema); err != nil {
		t.Fatalf("unmarshal facet: %v", err)
	}
	props, _ := facetSchema["properties"].(map[string]any)
	field, _ := props["field"].(map[string]any)
	enum, _ := field["enum"].([]any)
	if len(enum) != 3 {
		t.Errorf("facet field enum size = %d, want 3", len(enum))
	}

	// pulse_sample: no enum on field; path is present.
	sampleRaw, ok := schemas[mcptools.ToolSample]
	if !ok {
		t.Fatal("missing sample schema")
	}
	var sampleSchema map[string]any
	if err := json.Unmarshal(sampleRaw, &sampleSchema); err != nil {
		t.Fatalf("unmarshal sample: %v", err)
	}
	sprops, _ := sampleSchema["properties"].(map[string]any)
	if _, has := sprops["path"]; !has {
		t.Error("sample schema missing 'path' property")
	}
}

// fakeSessionWithTools implements server.SessionWithTools so we can drive
// AddSessionTools end-to-end without depending on transport-specific
// session implementations (stdio and in-process sessions don't implement
// SessionWithTools in mcp-go v0.52.0; sse and streamable_http do). The
// fake keeps test surface narrow and stable across mcp-go versions.
type fakeSessionWithTools struct {
	id            string
	tools         map[string]server.ServerTool
	notifications chan mcpgo.JSONRPCNotification
	initialized   bool
}

func newFakeSession(id string) *fakeSessionWithTools {
	return &fakeSessionWithTools{
		id:            id,
		tools:         map[string]server.ServerTool{},
		notifications: make(chan mcpgo.JSONRPCNotification, 100),
		initialized:   true,
	}
}

func (s *fakeSessionWithTools) Initialize()       {}
func (s *fakeSessionWithTools) Initialized() bool { return s.initialized }
func (s *fakeSessionWithTools) NotificationChannel() chan<- mcpgo.JSONRPCNotification {
	return s.notifications
}
func (s *fakeSessionWithTools) SessionID() string                                  { return s.id }
func (s *fakeSessionWithTools) GetSessionTools() map[string]server.ServerTool      { return s.tools }
func (s *fakeSessionWithTools) SetSessionTools(tools map[string]server.ServerTool) { s.tools = tools }

// TestMCPSchemaBinding_InspectSucceedsRegistersBindings drives the
// handleInspect path against a registered SessionWithTools and asserts
// that the bound action tools land in the session's tool map. The
// in-process MCP client used elsewhere in the test suite uses a session
// type that does not implement SessionWithTools, so we register a
// minimal fake directly with the server.
func TestMCPSchemaBinding_InspectSucceedsRegistersBindings(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeBoundTestCohort(t, fs, "bound.pulse", makeSchema())

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	srv := NewWithOptions(p, Options{BindOnOpen: true})

	sess := newFakeSession("test-bind")
	ctx := context.Background()
	if err := srv.RegisterSession(ctx, sess); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	defer srv.UnregisterSession(ctx, sess.SessionID())

	// Trigger handleInspect via direct tool dispatch on the server, with
	// the session attached to the context (handleInspect reads it via
	// server.ClientSessionFromContext).
	ctx = srv.WithContext(ctx, sess)
	req := mcpgo.CallToolRequest{}
	req.Params.Name = ToolInspect
	req.Params.Arguments = map[string]any{"path": "bound.pulse"}
	out := srv.HandleMessage(ctx, marshalToolCall(t, req))
	if out == nil {
		t.Fatal("HandleMessage returned nil response")
	}

	// Session must now carry bound variants of the action tools.
	sessionTools := sess.GetSessionTools()
	if len(sessionTools) == 0 {
		t.Fatal("session has no bound tools after inspect")
	}
	for _, name := range []string{ToolProcess, ToolPredict, ToolCompose, ToolSample, ToolFacet} {
		entry, ok := sessionTools[name]
		if !ok {
			t.Errorf("session missing bound tool %q", name)
			continue
		}
		raw, err := json.Marshal(entry.Tool)
		if err != nil {
			t.Fatalf("marshal bound tool %q: %v", name, err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("unmarshal bound tool %q: %v", name, err)
		}
		sch, _ := decoded["inputSchema"].(map[string]any)
		props, _ := sch["properties"].(map[string]any)
		if len(props) == 0 {
			t.Errorf("bound tool %q has empty input schema properties", name)
		}
	}
}

// TestMCPSchemaBinding_BindOnOpenFalse verifies that BindOnOpen=false
// suppresses session-tool registration on inspect.
func TestMCPSchemaBinding_BindOnOpenFalse(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeBoundTestCohort(t, fs, "unbound.pulse", makeSchema())

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	srv := NewWithOptions(p, Options{BindOnOpen: false})

	sess := newFakeSession("test-unbound")
	ctx := context.Background()
	if err := srv.RegisterSession(ctx, sess); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	defer srv.UnregisterSession(ctx, sess.SessionID())

	ctx = srv.WithContext(ctx, sess)
	req := mcpgo.CallToolRequest{}
	req.Params.Name = ToolInspect
	req.Params.Arguments = map[string]any{"path": "unbound.pulse"}
	_ = srv.HandleMessage(ctx, marshalToolCall(t, req))

	if got := sess.GetSessionTools(); len(got) != 0 {
		t.Errorf("BindOnOpen=false: session tools should be empty, got %d", len(got))
	}
}

// marshalToolCall encodes a CallToolRequest as a JSON-RPC tools/call
// message so we can drive srv.HandleMessage directly.
func marshalToolCall(t *testing.T, req mcpgo.CallToolRequest) json.RawMessage {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      req.Params.Name,
			"arguments": req.Params.Arguments,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal tool call: %v", err)
	}
	return raw
}

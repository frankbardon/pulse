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
	"github.com/frankbardon/pulse/internal/mcp/mcptools"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
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

// connectBindClient connects an in-memory client session to srv. Mirrors the
// mcp_test harness but lives in-package so the bind-on-inspect tests can drive
// the single-Server post-serve AddTool swap and observe it via tools/list.
func connectBindClient(t *testing.T, srv *mcpsdk.Server) (*mcpsdk.ClientSession, func()) {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	c := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "pulse-bind-test", Version: "1.0.0"}, nil)
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

// processToolFromList returns the pulse_process tool descriptor from a
// tools/list result, or nil if absent.
func processToolFromList(t *testing.T, cs *mcpsdk.ClientSession) *mcpsdk.Tool {
	t.Helper()
	out, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tl := range out.Tools {
		if tl.Name == ToolProcess {
			return tl
		}
	}
	return nil
}

// requestSubSchema extracts the inner request object schema from a tool's
// InputSchema (a map[string]any once round-tripped over the wire).
func requestSubSchema(tool *mcpsdk.Tool) map[string]any {
	sch, _ := tool.InputSchema.(map[string]any)
	props, _ := sch["properties"].(map[string]any)
	req, _ := props["request"].(map[string]any)
	return req
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

// TestMCPSchemaBinding_CrosstabNormalizeLevel verifies the bound
// process schema exposes Crosstab.normalize_level with the documented
// integer constraint and a non-empty description.
func TestMCPSchemaBinding_CrosstabNormalizeLevel(t *testing.T) {
	schemas, err := Bind(makeSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	raw, ok := schemas[mcptools.ToolProcess]
	if !ok {
		t.Fatal("missing process schema")
	}
	req := decodeRequestSchema(t, raw)
	props, _ := req["properties"].(map[string]any)
	cross, _ := props["crosstab"].(map[string]any)
	if cross == nil {
		t.Fatal("crosstab schema missing from request")
	}
	cprops, _ := cross["properties"].(map[string]any)
	level, _ := cprops["normalize_level"].(map[string]any)
	if level == nil {
		t.Fatal("crosstab.normalize_level property missing")
	}
	if typ, _ := level["type"].(string); typ != "integer" {
		t.Errorf("normalize_level.type = %q, want integer", typ)
	}
	if mn, ok := level["minimum"]; !ok {
		t.Error("normalize_level.minimum missing")
	} else if v, _ := mn.(int); v != 0 {
		// JSON loads numbers as float64 in some paths; accept either.
		if f, _ := mn.(float64); f != 0 {
			t.Errorf("normalize_level.minimum = %v, want 0", mn)
		}
	}
	if desc, _ := level["description"].(string); desc == "" {
		t.Error("normalize_level.description should be non-empty")
	}
}

// TestMCPSchemaBinding_CrosstabNormalizeWithin verifies the bound
// process schema exposes Crosstab.normalize_within with the documented
// integer constraint and a non-empty description.
func TestMCPSchemaBinding_CrosstabNormalizeWithin(t *testing.T) {
	schemas, err := Bind(makeSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	raw, ok := schemas[mcptools.ToolProcess]
	if !ok {
		t.Fatal("missing process schema")
	}
	req := decodeRequestSchema(t, raw)
	props, _ := req["properties"].(map[string]any)
	cross, _ := props["crosstab"].(map[string]any)
	if cross == nil {
		t.Fatal("crosstab schema missing from request")
	}
	cprops, _ := cross["properties"].(map[string]any)
	within, _ := cprops["normalize_within"].(map[string]any)
	if within == nil {
		t.Fatal("crosstab.normalize_within property missing")
	}
	if typ, _ := within["type"].(string); typ != "integer" {
		t.Errorf("normalize_within.type = %q, want integer", typ)
	}
	if mn, ok := within["minimum"]; !ok {
		t.Error("normalize_within.minimum missing")
	} else if v, _ := mn.(int); v != 0 {
		if f, _ := mn.(float64); f != 0 {
			t.Errorf("normalize_within.minimum = %v, want 0", mn)
		}
	}
	if desc, _ := within["description"].(string); desc == "" {
		t.Error("normalize_within.description should be non-empty")
	}
}

func TestMCPSchemaBinding_OverlayKindEnum(t *testing.T) {
	schemas, err := Bind(makeSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	raw, ok := schemas[mcptools.ToolProcess]
	if !ok {
		t.Fatal("missing process schema")
	}
	req := decodeRequestSchema(t, raw)
	props, _ := req["properties"].(map[string]any)
	overlays, _ := props["overlays"].(map[string]any)
	if overlays == nil {
		t.Fatal("request.overlays property missing")
	}
	if typ, _ := overlays["type"].(string); typ != "array" {
		t.Errorf("overlays.type = %q, want array", typ)
	}
	items, _ := overlays["items"].(map[string]any)
	if items == nil {
		t.Fatal("overlays.items missing")
	}
	itemProps, _ := items["properties"].(map[string]any)
	if itemProps == nil {
		t.Fatal("overlays.items.properties missing")
	}
	kind, _ := itemProps["kind"].(map[string]any)
	if kind == nil {
		t.Fatal("overlays.items.properties.kind missing")
	}
	if typ, _ := kind["type"].(string); typ != "string" {
		t.Errorf("overlays.items.properties.kind.type = %q, want string", typ)
	}
	enumAny, ok := kind["enum"].([]any)
	if !ok {
		t.Fatal("overlays.items.properties.kind.enum missing or wrong shape")
	}
	got := make([]string, 0, len(enumAny))
	for _, v := range enumAny {
		s, _ := v.(string)
		got = append(got, s)
	}

	// Source of truth: overlayKindEnumForFacade(overlayFacadeRequest, nil).
	// Per-facade narrowing excludes Compose-only, Chain-only, and
	// Facet-only kinds — those surface on their own MCP tools.
	want := overlayKindEnumForFacade(overlayFacadeRequest, nil)
	if !slices.Equal(got, want) {
		t.Errorf("overlay_kind enum = %v, want %v", got, want)
	}
	// Catalog ground-truth: OVERLAY_INDEX_VS_MARGIN is always present
	// on the Request facade.
	if !slices.Contains(got, "OVERLAY_INDEX_VS_MARGIN") {
		t.Errorf("overlay_kind enum missing OVERLAY_INDEX_VS_MARGIN: %v", got)
	}
	// Facade isolation invariants: per-facade kinds must NOT leak onto
	// the Request enum.
	for _, leak := range []string{
		"OVERLAY_INDEX_VS_POP",       // FACET-only
		"OVERLAY_INDEX_VS_STAGE",     // CHAIN-only
		"OVERLAY_INDEX_VS_REF",       // COMPOSE-only
		"OVERLAY_PROP_Z_PANEL",       // COMPOSE-only multi-ref
		"OVERLAY_PANEL_INDEX_VS_REF", // COMPOSE-only multi-ref
	} {
		if slices.Contains(got, leak) {
			t.Errorf("Request facade overlay_kind enum leaks %q (belongs to another facade): %v", leak, got)
		}
	}
}

// TestMCPSchemaBinding_InspectSucceedsRegistersBindings drives the
// handleInspect path end-to-end over an in-memory client/session and
// asserts the action tools are re-registered with the enum-constrained
// (schema-bound) variants. go-sdk has no per-session tool map; over the
// single in-memory session the inspect handler's same-name Server.AddTool
// swap replaces the base tools and the change is observable via tools/list.
func TestMCPSchemaBinding_InspectSucceedsRegistersBindings(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeBoundTestCohort(t, fs, "bound.pulse", makeSchema())

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	srv := NewWithOptions(p, Options{BindOnOpen: true})
	cs, cancel := connectBindClient(t, srv)
	defer cancel()

	ctx := context.Background()

	// Before inspect: the base pulse_process tool is unbound (its request
	// argument is a plain JSON string, not an enum-constrained object).
	if before := processToolFromList(t, cs); before == nil {
		t.Fatal("pulse_process missing from initial tools/list")
	} else if strings.Contains(before.Description, "(schema-bound)") {
		t.Fatalf("pulse_process should be unbound before inspect: %q", before.Description)
	}

	// Trigger the bind-on-inspect hook.
	if _, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      ToolInspect,
		Arguments: map[string]any{"path": "bound.pulse"},
	}); err != nil {
		t.Fatalf("CallTool inspect: %v", err)
	}

	// After inspect: every action tool carries the (schema-bound) variant.
	out, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools after inspect: %v", err)
	}
	byName := map[string]*mcpsdk.Tool{}
	for _, tl := range out.Tools {
		byName[tl.Name] = tl
	}
	for _, name := range []string{
		ToolProcess, ToolPredict, ToolCompose, ToolSample, ToolFacet, ToolFacetSchema, ToolProcessChain,
	} {
		tl, ok := byName[name]
		if !ok {
			t.Errorf("action tool %q missing after inspect", name)
			continue
		}
		if !strings.Contains(tl.Description, "(schema-bound)") {
			t.Errorf("action tool %q not re-registered as schema-bound: %q", name, tl.Description)
		}
	}

	// The bound pulse_process exposes the cohort's field names as an enum
	// on aggregations[].field — the load-bearing constraint of the feature.
	req := requestSubSchema(byName[ToolProcess])
	aggFields := enumOf(req, "aggregations", "field")
	if !slices.Contains(aggFields, "score") {
		t.Errorf("bound pulse_process aggregations.field enum missing 'score': %v", aggFields)
	}
}

// TestMCPSchemaBinding_BindOnOpenFalse verifies that BindOnOpen=false
// suppresses the schema-bound re-registration on inspect: the action
// tools keep their base (unbound) descriptors after a successful inspect.
func TestMCPSchemaBinding_BindOnOpenFalse(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeBoundTestCohort(t, fs, "unbound.pulse", makeSchema())

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	srv := NewWithOptions(p, Options{BindOnOpen: false})
	cs, cancel := connectBindClient(t, srv)
	defer cancel()

	ctx := context.Background()
	if _, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      ToolInspect,
		Arguments: map[string]any{"path": "unbound.pulse"},
	}); err != nil {
		t.Fatalf("CallTool inspect: %v", err)
	}

	tl := processToolFromList(t, cs)
	if tl == nil {
		t.Fatal("pulse_process missing from tools/list")
	}
	if strings.Contains(tl.Description, "(schema-bound)") {
		t.Errorf("BindOnOpen=false: pulse_process should stay unbound, got %q", tl.Description)
	}
	// The unbound request argument is a plain string, so there is no
	// aggregations field enum.
	if req := requestSubSchema(tl); enumOf(req, "aggregations", "field") != nil {
		t.Errorf("BindOnOpen=false: pulse_process must not carry a bound field enum")
	}
}

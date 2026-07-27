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
	writeCohortSchema(t, fs, path, schema)
}

// writeRichCohort writes a header+schema-only .pulse file carrying one numeric,
// one categorical, and one date field so the schema-bind path exercises every
// primary classification slot (numeric vs categorical vs date).
func writeRichCohort(t *testing.T, fs afero.Fs, path string) {
	t.Helper()
	dict := encoding.NewDictionary()
	dict.Add("alpha")
	dict.Add("beta")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Score for an observation"},
			{Name: "category", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Description: "Categorical label"},
			{Name: "observed_on", Type: encoding.FieldTypeDate, Description: "Observation date"},
		},
	}
	writeCohortSchema(t, fs, path, schema)
}

// writeCohortSchema persists a header+schema-only .pulse file at path.
func writeCohortSchema(t *testing.T, fs afero.Fs, path string, schema *encoding.Schema) {
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

	// All registered tools.
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
	if len(want) != len(toolmeta.Names()) {
		t.Fatalf("registered tool count %d != toolmeta.Names() %d", len(want), len(toolmeta.Names()))
	}

	// Self-description tools MUST be present in the mounted catalog. A
	// downstream host that runs its own go-sdk server typically filters these
	// out (it already serves its own manifest / skills surface), but Register
	// mounts the FULL catalog — the host filters; the adapter never withholds.
	// We assert they are present, then prove a host could enumerate-and-filter
	// them out without touching the rest of the surface.
	selfDescription := []string{
		toolmeta.ToolManifest,
		toolmeta.ToolSkillsList,
		toolmeta.ToolSkillsGet,
		toolmeta.ToolExamplesSearch,
		toolmeta.ToolExamplesGet,
		toolmeta.ToolErrorsLookup,
	}
	present := map[string]bool{}
	for _, n := range got {
		present[n] = true
	}
	for _, n := range selfDescription {
		if !present[n] {
			t.Errorf("self-description tool %q missing from mounted catalog: %v", n, got)
		}
	}
	// Downstream enumerate-and-filter: removing the self-description tools must
	// leave the action / data tools intact (i.e. the host keeps a usable
	// surface after filtering).
	selfSet := map[string]bool{}
	for _, n := range selfDescription {
		selfSet[n] = true
	}
	var afterFilter []string
	for _, n := range got {
		if !selfSet[n] {
			afterFilter = append(afterFilter, n)
		}
	}
	if len(afterFilter) != len(got)-len(selfDescription) {
		t.Fatalf("filter removed %d tools, want exactly %d", len(got)-len(afterFilter), len(selfDescription))
	}
	for _, n := range []string{toolmeta.ToolProcess, toolmeta.ToolPredict, toolmeta.ToolInspect} {
		if !slices.Contains(afterFilter, n) {
			t.Errorf("filtering self-description tools wrongly removed action tool %q", n)
		}
	}

	// The self-description tools must remain callable (not merely listed): a
	// pulse_manifest round-trip proves the bootstrap tool a host filters is
	// still wired to a live handler.
	manOut, err := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolmeta.ToolManifest})
	if err != nil {
		t.Fatalf("CallTool manifest: %v", err)
	}
	if manOut.IsError {
		t.Fatalf("pulse_manifest reported error: %+v", manOut.Content)
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

// inputSchemaOf returns the bound InputSchema (a map[string]any from the client
// side) for the named tool out of the current tools/list.
func inputSchemaOf(t *testing.T, c *mcpsdk.ClientSession, name string) map[string]any {
	t.Helper()
	out, err := c.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range out.Tools {
		if tool.Name == name {
			sch, ok := tool.InputSchema.(map[string]any)
			if !ok {
				t.Fatalf("%s InputSchema = %T, want map[string]any", name, tool.InputSchema)
			}
			return sch
		}
	}
	t.Fatalf("tool %q not in tools/list", name)
	return nil
}

// enumAt navigates schema.properties.<arrayKey>.items.properties.<innerKey>.enum
// and returns the enum as a string slice (nil when the path is absent).
func enumAt(schema map[string]any, arrayKey, innerKey string) []string {
	props, _ := schema["properties"].(map[string]any)
	a, _ := props[arrayKey].(map[string]any)
	items, _ := a["items"].(map[string]any)
	ips, _ := items["properties"].(map[string]any)
	field, _ := ips[innerKey].(map[string]any)
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

// TestRegister_BindOnInspect_EnumPayload is the load-bearing bind assertion: it
// is not enough that the bound variants carry the "(schema-bound)" description
// toggle — the re-registered tools must advertise the correct field-name enum
// sets per classification on the right JSON paths. After a pulse_inspect of a
// numeric+categorical+date cohort the bound pulse_process must constrain
// aggregations[].field / filterers[].field to all fields, attributes[].field to
// numeric-only (no categorical), and pulse_facet's field to all fields.
func TestRegister_BindOnInspect_EnumPayload(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeRichCohort(t, fs, "rich.pulse")
	p := newPulse(t, fs)

	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "1.0.0", BindOnInspect: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()
	ctx := context.Background()

	out, err := c.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      toolmeta.ToolInspect,
		Arguments: map[string]any{"path": "rich.pulse"},
	})
	if err != nil {
		t.Fatalf("CallTool inspect: %v", err)
	}
	if out.IsError {
		t.Fatalf("inspect reported error: %+v", out.Content)
	}

	proc := inputSchemaOf(t, c, toolmeta.ToolProcess)

	// aggregations[].field: all fields present.
	aggFields := enumAt(proc, "aggregations", "field")
	for _, w := range []string{"score", "category", "observed_on"} {
		if !slices.Contains(aggFields, w) {
			t.Errorf("bound aggregations.field enum missing %q: %v", w, aggFields)
		}
	}

	// attributes[].field: numeric-for-analytics only — the categorical must be
	// excluded. (Date IS numeric-for-analytics per
	// encoding.FieldType.IsNumericForAnalytics, so it is intentionally present.)
	attrFields := enumAt(proc, "attributes", "field")
	if !slices.Contains(attrFields, "score") {
		t.Errorf("bound attributes.field enum missing numeric 'score': %v", attrFields)
	}
	if slices.Contains(attrFields, "category") {
		t.Errorf("bound attributes.field enum must exclude categorical 'category': %v", attrFields)
	}

	// filterers[].field: every cohort field name, exactly.
	filterFields := enumAt(proc, "filterers", "field")
	if len(filterFields) != 3 {
		t.Errorf("bound filterers.field enum size = %d, want 3: %v", len(filterFields), filterFields)
	}

	// pulse_facet's standalone field arg carries an all-fields enum.
	facet := inputSchemaOf(t, c, toolmeta.ToolFacet)
	fprops, _ := facet["properties"].(map[string]any)
	ffield, _ := fprops["field"].(map[string]any)
	fenum, _ := ffield["enum"].([]any)
	if len(fenum) != 3 {
		t.Errorf("bound pulse_facet field enum size = %d, want 3: %v", len(fenum), fenum)
	}
}

// promptText fetches a prompt by name (with optional args) and returns the text
// of its first message.
func promptText(t *testing.T, c *mcpsdk.ClientSession, name string, args map[string]string) string {
	t.Helper()
	res, err := c.GetPrompt(context.Background(), &mcpsdk.GetPromptParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("GetPrompt(%s): %v", name, err)
	}
	if len(res.Messages) == 0 {
		t.Fatalf("prompt %s returned no messages", name)
	}
	tc, ok := res.Messages[0].Content.(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("prompt %s message content = %T, want *TextContent", name, res.Messages[0].Content)
	}
	return tc.Text
}

// TestRegister_BootstrapPromptContent asserts the bootstrap prompt body itself
// (not just its presence) references the canonical discovery tools.
func TestRegister_BootstrapPromptContent(t *testing.T) {
	p := newPulse(t, afero.NewMemMapFs())
	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "1.0.0"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()

	text := promptText(t, c, gosdk.PromptBootstrap, nil)
	for _, want := range []string{
		"pulse_manifest", "pulse_examples_search", "pulse_examples_get",
		"pulse_skills_get", "pulse_errors_lookup",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("bootstrap prompt body missing reference to %s", want)
		}
	}
}

// TestRegister_AuthorRequestPromptContent asserts the author-request prompt
// interpolates its `question` argument into the message body and still surfaces
// the discovery flow.
func TestRegister_AuthorRequestPromptContent(t *testing.T) {
	p := newPulse(t, afero.NewMemMapFs())
	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "1.0.0"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()

	const question = "regression of revenue on advertising spend by region"
	text := promptText(t, c, gosdk.PromptAuthorRequest, map[string]string{"question": question})
	if !strings.Contains(text, question) {
		t.Errorf("author-request prompt did not interpolate the question argument; got:\n%s", text)
	}
	if !strings.Contains(text, "pulse_examples_search") {
		t.Errorf("author-request prompt missing pulse_examples_search reference")
	}
}

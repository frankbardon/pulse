package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/mcptools"
	"github.com/frankbardon/pulse/skills"
	"github.com/frankbardon/pulse/types"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool name and description constants are sourced from the mcptools
// sub-package so the descriptor manifest can mirror them without taking
// a dependency on this package (which imports the root pulse package
// and would create an import cycle).
const (
	ToolInspect        = mcptools.ToolInspect
	ToolPredict        = mcptools.ToolPredict
	ToolProcess        = mcptools.ToolProcess
	ToolProcessChain   = mcptools.ToolProcessChain
	ToolCompose        = mcptools.ToolCompose
	ToolSample         = mcptools.ToolSample
	ToolFacet          = mcptools.ToolFacet
	ToolFacetSchema    = mcptools.ToolFacetSchema
	ToolSkillsList     = mcptools.ToolSkillsList
	ToolSkillsGet      = mcptools.ToolSkillsGet
	ToolManifest       = mcptools.ToolManifest
	ToolExamplesSearch = mcptools.ToolExamplesSearch
	ToolExamplesGet    = mcptools.ToolExamplesGet
	ToolErrorsLookup   = mcptools.ToolErrorsLookup
	ToolImport         = mcptools.ToolImport
	ToolDrop           = mcptools.ToolDrop
	ToolImportsList    = mcptools.ToolImportsList
	ToolLabelTables    = mcptools.ToolLabelTables
	ToolLabelResolve   = mcptools.ToolLabelResolve

	DescInspect        = mcptools.DescInspect
	DescPredict        = mcptools.DescPredict
	DescProcess        = mcptools.DescProcess
	DescProcessChain   = mcptools.DescProcessChain
	DescCompose        = mcptools.DescCompose
	DescSample         = mcptools.DescSample
	DescFacet          = mcptools.DescFacet
	DescFacetSchema    = mcptools.DescFacetSchema
	DescSkillsList     = mcptools.DescSkillsList
	DescSkillsGet      = mcptools.DescSkillsGet
	DescManifest       = mcptools.DescManifest
	DescExamplesSearch = mcptools.DescExamplesSearch
	DescExamplesGet    = mcptools.DescExamplesGet
	DescErrorsLookup   = mcptools.DescErrorsLookup
	DescImport         = mcptools.DescImport
	DescDrop           = mcptools.DescDrop
	DescImportsList    = mcptools.DescImportsList
	DescLabelTables    = mcptools.DescLabelTables
	DescLabelResolve   = mcptools.DescLabelResolve
)

// ToolMeta is the canonical (name, description) record for one registered
// MCP tool. Alias of mcptools.ToolMeta so callers do not need to import
// the sub-package directly.
type ToolMeta = mcptools.ToolMeta

// RegisteredToolsMeta returns the canonical list of MCP tools with their
// description strings.
func RegisteredToolsMeta() []ToolMeta {
	return mcptools.Meta()
}

// RegisteredTools returns the canonical list of MCP tool names exposed by
// this server. Order is stable for deterministic documentation scans.
func RegisteredTools() []string {
	return mcptools.Names()
}

// registerTools mounts every built-in MCP tool onto the go-sdk server via the
// low-level Server.AddTool path: each tool carries a precomputed input schema
// (json.RawMessage) and a handler that performs its own unmarshal/validate and
// builds the CallToolResult by hand. The generic AddTool[In,Out] reflection
// path is deliberately avoided — it panics on the recursive request types
// (Crosstab / overlay nesting). Typed-struct schema reflection is a later epic.
func registerTools(s *mcpsdk.Server, p *pulse.Pulse, bindOnOpen bool) {
	predict := handlePredict(p)
	process := handleProcess(p)
	compose := handleCompose(p)
	sample := handleSample(p)
	facet := handleFacet(p)
	facetSchema := handleFacetSchema(p)

	s.AddTool(
		tool(ToolInspect, DescInspect, objectSchema(
			prop{Name: "path", Type: "string", Description: "Filesystem path to the .pulse file", Required: true},
		)),
		handleInspect(s, p, bindOnOpen),
	)

	s.AddTool(
		tool(ToolPredict, DescPredict, objectSchema(
			prop{Name: "request", Type: "string", Description: "JSON-encoded types.Request", Required: true},
		)),
		predict,
	)

	s.AddTool(
		tool(ToolProcess, DescProcess, objectSchema(
			prop{Name: "request", Type: "string", Description: "JSON-encoded types.Request", Required: true},
		)),
		process,
	)

	s.AddTool(
		tool(ToolCompose, DescCompose, objectSchema(
			prop{Name: "request", Type: "string", Description: "JSON-encoded types.ComposedRequest", Required: true},
		)),
		compose,
	)

	s.AddTool(
		tool(ToolProcessChain, DescProcessChain, objectSchema(
			prop{Name: "request", Type: "string", Description: "JSON-encoded pulse.ChainRequest. Fields: cohort.filename (path to source for stage 0), stages ([]ChainStage with name + request).", Required: true},
		)),
		handleProcessChain(p),
	)

	s.AddTool(
		tool(ToolSample, DescSample, objectSchema(
			prop{Name: "path", Type: "string", Description: "Filesystem path to the .pulse file", Required: true},
			prop{Name: "count", Type: "number", Description: "Maximum rows to return (default 10)"},
		)),
		sample,
	)

	s.AddTool(
		tool(ToolFacet, DescFacet, objectSchema(
			prop{Name: "path", Type: "string", Description: "Filesystem path to the .pulse file", Required: true},
			prop{Name: "field", Type: "string", Description: "Field name to facet", Required: true},
		)),
		facet,
	)

	s.AddTool(
		tool(ToolFacetSchema, DescFacetSchema, objectSchema(
			prop{Name: "request", Type: "string", Description: "JSON-encoded pulse.FacetRequest. Fields: cohort.filename (path), fields (string[]), filterers (Filterer[]), additive_fields (string[]), discrete_top_k (int), numeric_percentiles (float[]), include_histogram (bool), histogram_bins (int), histogram_range ([min,max]).", Required: true},
		)),
		facetSchema,
	)

	s.AddTool(
		tool(ToolSkillsList, DescSkillsList, objectSchema()),
		handleSkillsList(),
	)

	s.AddTool(
		tool(ToolSkillsGet, DescSkillsGet, objectSchema(
			prop{Name: "name", Type: "string", Description: "Skill name (e.g. 'aggregation-guide')", Required: true},
		)),
		handleSkillsGet(),
	)

	s.AddTool(
		tool(ToolManifest, DescManifest, objectSchema()),
		handleManifest(p),
	)

	s.AddTool(
		tool(ToolExamplesSearch, DescExamplesSearch, objectSchema(
			prop{Name: "query", Type: "string", Description: "Optional case-insensitive substring (matched against name, description, operators)"},
			prop{Name: "tags", Type: "array", Items: "string", Description: "Optional list of canonical taxonomy tags; results must carry every tag (AND)"},
			prop{Name: "category", Type: "string", Description: "Optional exact directory: aggregations, attributes, features, filterers, groupers, tests, windows"},
		)),
		handleExamplesSearch(p),
	)

	s.AddTool(
		tool(ToolExamplesGet, DescExamplesGet, objectSchema(
			prop{Name: "name", Type: "string", Description: "Example name from the _meta.name field (e.g. 't_test_one_sample')", Required: true},
		)),
		handleExamplesGet(p),
	)

	s.AddTool(
		tool(ToolErrorsLookup, DescErrorsLookup, objectSchema(
			prop{Name: "code", Type: "string", Description: "Exact error code identifier (e.g. 'PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL'); returns a 1-element array on hit, empty on miss"},
			prop{Name: "domain", Type: "string", Description: "Domain prefix (PULSE, ENCODING, PROCESSING, SERVICE, DATA, CLI); case-insensitive; enumerates every code in that domain"},
			prop{Name: "query", Type: "string", Description: "Case-insensitive substring search across descriptions and fixup hints; ranks message hits above fixup hits"},
		)),
		handleErrorsLookup(p),
	)

	s.AddTool(
		tool(ToolImport, DescImport, objectSchema(
			prop{Name: "source", Type: "string", Description: "Filesystem path to the source file (relative to PULSE_DATA_DIR)", Required: true},
			prop{Name: "format", Type: "string", Description: "Optional format override: csv, tsv, ndjson, jsonarray, parquet, arrow, excel, pulse"},
			prop{Name: "handle", Type: "string", Description: "Optional managed handle name; defaults to source basename without extension"},
			prop{Name: "ttl", Type: "string", Description: "Optional TTL: Go duration (\"24h\", \"30m\", \"3600s\") or day form (\"7d\", \"30d\"); \"pin\" disables expiry. Default 7d."},
			prop{Name: "sheet", Type: "string", Description: "Optional Excel sheet name; ignored for non-Excel sources"},
			prop{Name: "overwrite", Type: "boolean", Description: "Replace an existing handle of the same name. Default false → PULSE_IMPORT_HANDLE_EXISTS on collision"},
		)),
		handleImport(s, p, bindOnOpen),
	)

	s.AddTool(
		tool(ToolDrop, DescDrop, objectSchema(
			prop{Name: "handle", Type: "string", Description: "Managed handle name to remove", Required: true},
		)),
		handleDrop(p),
	)

	s.AddTool(
		tool(ToolImportsList, DescImportsList, objectSchema()),
		handleImportsList(p),
	)

	s.AddTool(
		tool(ToolLabelTables, DescLabelTables, objectSchema()),
		handleLabelTables(p),
	)

	s.AddTool(
		tool(ToolLabelResolve, DescLabelResolve, objectSchema(
			prop{Name: "table", Type: "string", Description: "Label table name (from pulse_label_tables, e.g. 'brand')", Required: true},
			prop{Name: "query", Type: "string", Description: "Name to resolve, case-insensitive; empty returns the first rows (browse mode)"},
			prop{Name: "limit", Type: "number", Description: "Maximum matches to return. Default 10."},
		)),
		handleLabelResolve(p),
	)
}

// bindSessionFromPath is the schema-bind-on-inspect hook shared by the inspect
// and import handlers. Porting the session-scoped, enum-constrained tool
// variants (schema_bind.go) is E1-S4; until then this is a deliberate no-op so
// inspect/import still function against the global (unbound) tool schemas.
//
// TODO(E1-S4): port schema_bind.go and re-register bound action-tool variants
// here against the cohort schema at path, mirroring the legacy behavior.
func bindSessionFromPath(ctx context.Context, s *mcpsdk.Server, p *pulse.Pulse, bindOnOpen bool, path string) {
	_ = ctx
	_ = s
	_ = p
	_ = bindOnOpen
	_ = path
}

func handleInspect(s *mcpsdk.Server, p *pulse.Pulse, bindOnOpen bool) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := toolArgs(req)
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return errorResult("missing or invalid 'path'"), nil
		}
		result, err := p.Inspect(ctx, path)
		if err != nil {
			return errorResult(err.Error()), nil
		}

		// On a successful inspect, register session-scoped bound tool
		// variants whose JSON Schemas embed enum constraints on field-
		// name parameters. Best-effort: failure to bind degrades gracefully
		// to the global (unbound) tools. (Deferred to E1-S4.)
		bindSessionFromPath(ctx, s, p, bindOnOpen, path)

		return jsonResult(result)
	}
}

func handlePredict(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if ce := checkUnknownRequestKeys(body); ce != nil {
			return codedErrorResult(ce), nil
		}
		var typed types.Request
		if err := json.Unmarshal(body, &typed); err != nil {
			return errorResult(fmt.Sprintf("parse request: %v", err)), nil
		}
		result, err := p.Predict(ctx, &typed)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(result)
	}
}

func handleProcess(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if ce := checkUnknownRequestKeys(body); ce != nil {
			return codedErrorResult(ce), nil
		}
		var typed types.Request
		if err := json.Unmarshal(body, &typed); err != nil {
			return errorResult(fmt.Sprintf("parse request: %v", err)), nil
		}
		resp, err := p.Process(ctx, &typed)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(resp)
	}
}

func handleCompose(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if ce := checkUnknownKeysComposed(body); ce != nil {
			return codedErrorResult(ce), nil
		}
		var typed types.ComposedRequest
		if err := json.Unmarshal(body, &typed); err != nil {
			return errorResult(fmt.Sprintf("parse request: %v", err)), nil
		}
		resp, err := p.Compose(ctx, &typed)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(resp)
	}
}

func handleProcessChain(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if ce := checkUnknownKeysChain(body); ce != nil {
			return codedErrorResult(ce), nil
		}
		var typed types.ChainRequest
		if err := json.Unmarshal(body, &typed); err != nil {
			return errorResult(fmt.Sprintf("parse request: %v", err)), nil
		}
		resp, err := p.ProcessChain(ctx, &typed)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(resp)
	}
}

func handleSample(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := toolArgs(req)
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return errorResult("missing or invalid 'path'"), nil
		}
		count := 10
		if raw, ok := args["count"].(float64); ok && raw > 0 {
			count = int(raw)
		}
		rows, err := p.Sample(ctx, path, count)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(rows)
	}
}

func handleFacetSchema(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return errorResult(err.Error()), nil
		}
		var typed pulse.FacetRequest
		if err := json.Unmarshal(body, &typed); err != nil {
			return errorResult(fmt.Sprintf("parse request: %v", err)), nil
		}
		result, err := p.FacetSchema(ctx, &typed)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(result)
	}
}

func handleFacet(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := toolArgs(req)
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return errorResult("missing or invalid 'path'"), nil
		}
		field, ok := args["field"].(string)
		if !ok || field == "" {
			return errorResult("missing or invalid 'field'"), nil
		}
		values, err := p.Facet(ctx, path, field)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(values)
	}
}

// handleManifest serves the slim payload over MCP. Prose descriptions
// live in skills and are fetched separately via pulse_skills_get;
// duplicating them in the per-session bootstrap blob is the bloat we
// designed --slim to avoid. The CLI keeps both modes for human use.
func handleManifest(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return jsonResult(descriptor.SlimManifest(p.Manifest(ctx)))
	}
}

func handleSkillsList() mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return jsonResult(skills.List())
	}
}

func handleSkillsGet() mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := toolArgs(req)
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return errorResult("missing or invalid 'name'"), nil
		}
		body, found := skills.Get(name)
		if !found {
			return errorResult(fmt.Sprintf("skill %q not found", name)), nil
		}
		return textResult(body), nil
	}
}

// handleExamplesSearch wraps the embedded request-example library
// search facade. All three filters are optional and additive.
func handleExamplesSearch(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := toolArgs(req)
		query, _ := args["query"].(string)
		category, _ := args["category"].(string)
		var tags []string
		if raw, ok := args["tags"]; ok && raw != nil {
			switch v := raw.(type) {
			case []any:
				for _, item := range v {
					if s, ok := item.(string); ok {
						tags = append(tags, s)
					}
				}
			case []string:
				tags = v
			}
		}
		return jsonResult(p.ExamplesSearch(query, tags, category))
	}
}

// handleExamplesGet wraps the embedded request-example library single
// fetch facade. Returns the runnable Body with the _meta block already
// stripped.
func handleExamplesGet(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := toolArgs(req)
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return errorResult("missing or invalid 'name'"), nil
		}
		ex, found := p.ExampleGet(name)
		if !found {
			return errorResult(fmt.Sprintf("example %q not found", name)), nil
		}
		return jsonResult(ex)
	}
}

// handleErrorsLookup wraps the errors-package lookup surface. All three
// arguments are optional but at least one must be set; when more than
// one is set, the filters are ANDed against the union of matching
// codes.
//
// Return shape is always an array of perr.LookupResult so the LLM-side
// parsing stays uniform across hit/miss/multi-result paths.
func handleErrorsLookup(_ *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := toolArgs(req)
		code, _ := args["code"].(string)
		domain, _ := args["domain"].(string)
		query, _ := args["query"].(string)
		if code == "" && domain == "" && query == "" {
			return errorResult("specify at least one of code, domain, query"), nil
		}
		results := intersectErrorLookup(code, domain, query)
		return jsonResult(results)
	}
}

// intersectErrorLookup composes the three lookup axes. Each non-empty
// axis contributes a candidate set; the final result is the
// intersection. When only one axis is set, the result is that axis's
// natural output (preserving Search's ranking order; ByDomain's
// alphabetical order; Lookup's 0/1 element).
//
// Returns a non-nil empty slice when nothing matches.
func intersectErrorLookup(code, domain, query string) []perr.LookupResult {
	axes := make([][]perr.LookupResult, 0, 3)
	if code != "" {
		hit, ok := perr.Lookup(code)
		if ok {
			axes = append(axes, []perr.LookupResult{hit})
		} else {
			axes = append(axes, []perr.LookupResult{})
		}
	}
	if domain != "" {
		axes = append(axes, perr.ByDomain(domain))
	}
	if query != "" {
		axes = append(axes, perr.Search(query))
	}
	if len(axes) == 0 {
		return []perr.LookupResult{}
	}
	// Intersect: start with the first axis, drop entries not present in
	// each subsequent axis. Preserve the first axis's ordering so the
	// caller sees Search-ranked or ByDomain-alphabetized results
	// depending on which axis was supplied first.
	base := axes[0]
	for i := 1; i < len(axes); i++ {
		present := make(map[string]struct{}, len(axes[i]))
		for _, r := range axes[i] {
			present[r.Code] = struct{}{}
		}
		next := base[:0:0]
		for _, r := range base {
			if _, ok := present[r.Code]; ok {
				next = append(next, r)
			}
		}
		base = next
	}
	if base == nil {
		return []perr.LookupResult{}
	}
	return base
}

// handleLabelTables lists the registered label tables (name, row count,
// enumerable). The INPUT-direction discovery companion to
// pulse_label_resolve.
func handleLabelTables(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		tables := p.LabelTables()
		if tables == nil {
			tables = []pulse.LabelTableInfo{}
		}
		return jsonResult(tables)
	}
}

// handleLabelResolve reverse-resolves a human-readable name to the raw
// categorical key(s) a filter / grouper expects.
func handleLabelResolve(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := toolArgs(req)
		table, _ := args["table"].(string)
		if table == "" {
			return errorResult("missing or invalid 'table'"), nil
		}
		query, _ := args["query"].(string)
		limit := 0
		if raw, ok := args["limit"].(float64); ok {
			limit = int(raw)
		}
		matches, err := p.ResolveLabel(table, query, limit)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		if matches == nil {
			matches = []pulse.LabelMatch{}
		}
		return jsonResult(matches)
	}
}

// prop describes one property of a tool input schema. It is the minimal
// shape needed to reconstruct the schemas the legacy SDK's typed builder
// (string/number/boolean/array) emitted.
type prop struct {
	Name        string
	Type        string // "string" | "number" | "boolean" | "array"
	Items       string // element type for "array" props
	Description string
	Required    bool
}

// objectSchema builds a draft-2020-12 object JSON Schema carrying the given
// properties as a json.RawMessage. Built with encoding/json (never
// fmt.Sprintf) so the descriptor structural-defense ban holds. The low-level
// go-sdk Server.AddTool accepts any value that marshals to a JSON object with
// type "object" — this is exactly that.
func objectSchema(props ...prop) json.RawMessage {
	properties := make(map[string]any, len(props))
	var required []string
	for _, pr := range props {
		m := map[string]any{"type": pr.Type}
		if pr.Description != "" {
			m["description"] = pr.Description
		}
		if pr.Type == "array" && pr.Items != "" {
			m["items"] = map[string]any{"type": pr.Items}
		}
		properties[pr.Name] = m
		if pr.Required {
			required = append(required, pr.Name)
		}
	}
	obj := map[string]any{"type": "object"}
	if len(properties) > 0 {
		obj["properties"] = properties
	}
	if len(required) > 0 {
		obj["required"] = required
	}
	body, err := json.Marshal(obj)
	if err != nil {
		// Inputs are static literals; marshal cannot fail in practice.
		return json.RawMessage(`{"type":"object"}`)
	}
	return json.RawMessage(body)
}

// tool assembles a go-sdk Tool descriptor with a precomputed input schema.
func tool(name, desc string, schema json.RawMessage) *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name:        name,
		Description: desc,
		InputSchema: schema,
	}
}

// toolArgs decodes the raw tool-call arguments into a generic map. Mirrors the
// legacy CallToolRequest.GetArguments() helper. A nil/empty/invalid arguments
// blob yields an empty map so individual argument lookups fall through to the
// "missing or invalid" guards.
func toolArgs(req *mcpsdk.CallToolRequest) map[string]any {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(req.Params.Arguments, &m); err != nil {
		return map[string]any{}
	}
	if m == nil {
		return map[string]any{}
	}
	return m
}

// requestBytes pulls a request blob from a tool argument. The argument may
// be a JSON string (the common path: clients embed JSON in a string field)
// or a structured object that we re-marshal.
func requestBytes(req *mcpsdk.CallToolRequest, key string) ([]byte, error) {
	args := toolArgs(req)
	raw, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("missing %q argument", key)
	}
	if s, ok := raw.(string); ok {
		return []byte(s), nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode %q: %w", key, err)
	}
	return body, nil
}

// textResult builds a successful single-text-content tool result.
func textResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
	}
}

// errorResult builds a tool-error result (IsError set) carrying text. Tool
// errors ride in Content per the MCP contract so the LLM can self-correct;
// they are not surfaced as protocol-level errors.
func errorResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
		IsError: true,
	}
}

// jsonResult marshals v and returns it as text content.
func jsonResult(v any) (*mcpsdk.CallToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return errorResult(fmt.Sprintf("encode result: %v", err)), nil
	}
	return textResult(string(body)), nil
}

// codedErrorResult wraps a CodedError as an MCP error result, serialising
// the full {code, message, details} envelope so the caller receives the
// structured suggestion payload rather than a bare string.
func codedErrorResult(ce *perr.CodedError) *mcpsdk.CallToolResult {
	body, err := json.Marshal(ce)
	if err != nil {
		return errorResult(ce.Error())
	}
	return errorResult(string(body))
}

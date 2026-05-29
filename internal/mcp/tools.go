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
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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
	ToolAsk            = mcptools.ToolAsk
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
	DescAsk            = mcptools.DescAsk
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

func registerTools(s *server.MCPServer, p *pulse.Pulse, bindOnOpen bool) {
	predict := handlePredict(p)
	process := handleProcess(p)
	compose := handleCompose(p)
	sample := handleSample(p)
	facet := handleFacet(p)
	facetSchema := handleFacetSchema(p)

	handlers := boundHandlers{
		process:     process,
		predict:     predict,
		compose:     compose,
		sample:      sample,
		facet:       facet,
		facetSchema: facetSchema,
	}

	s.AddTool(
		mcpgo.NewTool(ToolInspect,
			mcpgo.WithDescription(DescInspect),
			mcpgo.WithString("path", mcpgo.Description("Filesystem path to the .pulse file"), mcpgo.Required()),
		),
		handleInspect(s, p, bindOnOpen, handlers),
	)

	s.AddTool(
		mcpgo.NewTool(ToolPredict,
			mcpgo.WithDescription(DescPredict),
			mcpgo.WithString("request", mcpgo.Description("JSON-encoded types.Request"), mcpgo.Required()),
		),
		predict,
	)

	s.AddTool(
		mcpgo.NewTool(ToolProcess,
			mcpgo.WithDescription(DescProcess),
			mcpgo.WithString("request", mcpgo.Description("JSON-encoded types.Request"), mcpgo.Required()),
		),
		process,
	)

	s.AddTool(
		mcpgo.NewTool(ToolCompose,
			mcpgo.WithDescription(DescCompose),
			mcpgo.WithString("request", mcpgo.Description("JSON-encoded types.ComposedRequest"), mcpgo.Required()),
		),
		compose,
	)

	s.AddTool(
		mcpgo.NewTool(ToolProcessChain,
			mcpgo.WithDescription(DescProcessChain),
			mcpgo.WithString("request", mcpgo.Description("JSON-encoded pulse.ChainRequest. Fields: cohort.filename (path to source for stage 0), stages ([]ChainStage with name + request)."), mcpgo.Required()),
		),
		handleProcessChain(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolSample,
			mcpgo.WithDescription(DescSample),
			mcpgo.WithString("path", mcpgo.Description("Filesystem path to the .pulse file"), mcpgo.Required()),
			mcpgo.WithNumber("count", mcpgo.Description("Maximum rows to return (default 10)")),
		),
		sample,
	)

	s.AddTool(
		mcpgo.NewTool(ToolFacet,
			mcpgo.WithDescription(DescFacet),
			mcpgo.WithString("path", mcpgo.Description("Filesystem path to the .pulse file"), mcpgo.Required()),
			mcpgo.WithString("field", mcpgo.Description("Field name to facet"), mcpgo.Required()),
		),
		facet,
	)

	s.AddTool(
		mcpgo.NewTool(ToolFacetSchema,
			mcpgo.WithDescription(DescFacetSchema),
			mcpgo.WithString("request", mcpgo.Description("JSON-encoded pulse.FacetRequest. Fields: cohort.filename (path), fields (string[]), filterers (Filterer[]), additive_fields (string[]), discrete_top_k (int), numeric_percentiles (float[]), include_histogram (bool), histogram_bins (int), histogram_range ([min,max])."), mcpgo.Required()),
		),
		facetSchema,
	)

	s.AddTool(
		mcpgo.NewTool(ToolSkillsList,
			mcpgo.WithDescription(DescSkillsList),
		),
		handleSkillsList(),
	)

	s.AddTool(
		mcpgo.NewTool(ToolSkillsGet,
			mcpgo.WithDescription(DescSkillsGet),
			mcpgo.WithString("name", mcpgo.Description("Skill name (e.g. 'aggregation-guide')"), mcpgo.Required()),
		),
		handleSkillsGet(),
	)

	s.AddTool(
		mcpgo.NewTool(ToolManifest,
			mcpgo.WithDescription(DescManifest),
		),
		handleManifest(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolAsk,
			mcpgo.WithDescription(DescAsk),
			mcpgo.WithString("request", mcpgo.Description("JSON-encoded pulse.AskRequest. Fields: `source` (path to csv/tsv/ndjson/jsonarray/parquet/arrow/excel/.pulse — auto-imported into the managed pool), `query` (natural-language question parsed against the cohort schema), `request` (structured types.Request for explicit control), `predict` (bool — validate without executing), `on_invalid` (\"abort\"|\"suggest\"), `source_format` / `source_handle` / `source_ttl` / `source_sheet` / `source_overwrite` (optional auto-import knobs; defaults: detect format from extension, 7d TTL). Most common shape: `{\"source\":\"data.csv\",\"query\":\"average X by Y\"}`."), mcpgo.Required()),
		),
		handleAsk(s, p, bindOnOpen, handlers),
	)

	s.AddTool(
		mcpgo.NewTool(ToolExamplesSearch,
			mcpgo.WithDescription(DescExamplesSearch),
			mcpgo.WithString("query", mcpgo.Description("Optional case-insensitive substring (matched against name, description, operators)")),
			mcpgo.WithArray("tags", mcpgo.Description("Optional list of canonical taxonomy tags; results must carry every tag (AND)"), mcpgo.WithStringItems()),
			mcpgo.WithString("category", mcpgo.Description("Optional exact directory: aggregations, attributes, features, filterers, groupers, tests, windows")),
		),
		handleExamplesSearch(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolExamplesGet,
			mcpgo.WithDescription(DescExamplesGet),
			mcpgo.WithString("name", mcpgo.Description("Example name from the _meta.name field (e.g. 't_test_one_sample')"), mcpgo.Required()),
		),
		handleExamplesGet(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolErrorsLookup,
			mcpgo.WithDescription(DescErrorsLookup),
			mcpgo.WithString("code", mcpgo.Description("Exact error code identifier (e.g. 'PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL'); returns a 1-element array on hit, empty on miss")),
			mcpgo.WithString("domain", mcpgo.Description("Domain prefix (PULSE, ENCODING, PROCESSING, SERVICE, DATA, CLI); case-insensitive; enumerates every code in that domain")),
			mcpgo.WithString("query", mcpgo.Description("Case-insensitive substring search across descriptions and fixup hints; ranks message hits above fixup hits")),
		),
		handleErrorsLookup(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolImport,
			mcpgo.WithDescription(DescImport),
			mcpgo.WithString("source", mcpgo.Description("Filesystem path to the source file (relative to PULSE_DATA_DIR)"), mcpgo.Required()),
			mcpgo.WithString("format", mcpgo.Description("Optional format override: csv, tsv, ndjson, jsonarray, parquet, arrow, excel, pulse")),
			mcpgo.WithString("handle", mcpgo.Description("Optional managed handle name; defaults to source basename without extension")),
			mcpgo.WithString("ttl", mcpgo.Description("Optional TTL: Go duration (\"24h\", \"30m\", \"3600s\") or day form (\"7d\", \"30d\"); \"pin\" disables expiry. Default 7d.")),
			mcpgo.WithString("sheet", mcpgo.Description("Optional Excel sheet name; ignored for non-Excel sources")),
			mcpgo.WithBoolean("overwrite", mcpgo.Description("Replace an existing handle of the same name. Default false → PULSE_IMPORT_HANDLE_EXISTS on collision")),
		),
		handleImport(s, p, bindOnOpen, handlers),
	)

	s.AddTool(
		mcpgo.NewTool(ToolDrop,
			mcpgo.WithDescription(DescDrop),
			mcpgo.WithString("handle", mcpgo.Description("Managed handle name to remove"), mcpgo.Required()),
		),
		handleDrop(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolImportsList,
			mcpgo.WithDescription(DescImportsList),
		),
		handleImportsList(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolLabelTables,
			mcpgo.WithDescription(DescLabelTables),
		),
		handleLabelTables(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolLabelResolve,
			mcpgo.WithDescription(DescLabelResolve),
			mcpgo.WithString("table", mcpgo.Description("Label table name (from pulse_label_tables, e.g. 'brand')"), mcpgo.Required()),
			mcpgo.WithString("query", mcpgo.Description("Name to resolve, case-insensitive; empty returns the first rows (browse mode)")),
			mcpgo.WithNumber("limit", mcpgo.Description("Maximum matches to return. Default 10.")),
		),
		handleLabelResolve(p),
	)
}

// bindSessionFromPath rebinds the session's tool schemas to the cohort
// at path. Shared by handleInspect and handleAsk so both touchpoints
// pick up enum constraints the same way. Best-effort: silently degrades
// when bindOnOpen is off, the server is nil, the session is nil, or the
// cohort cannot be re-opened.
func bindSessionFromPath(ctx context.Context, s *server.MCPServer, p *pulse.Pulse, bindOnOpen bool, path string, handlers boundHandlers) {
	if !bindOnOpen || s == nil || path == "" {
		return
	}
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		return
	}
	cohort, err := p.Open(ctx, path)
	if err != nil {
		return
	}
	_ = BindSessionToolsWithExtensions(s, session.SessionID(), cohort.Schema(), p.Service().ExtensionsSnapshot(), handlers)
}

func handleInspect(s *server.MCPServer, p *pulse.Pulse, bindOnOpen bool, handlers boundHandlers) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return mcpgo.NewToolResultError("missing or invalid 'path'"), nil
		}
		result, err := p.Inspect(ctx, path)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}

		// On a successful inspect, register session-scoped bound tool
		// variants whose JSON Schemas embed enum constraints on field-
		// name parameters. Best-effort: failure to bind degrades gracefully
		// to the global (unbound) tools. handleAsk reuses the same hook
		// so an Ask call after a fresh session also picks up bound enums.
		bindSessionFromPath(ctx, s, p, bindOnOpen, path, handlers)

		return jsonResult(result)
	}
}

// handleAsk wires the unified pulse_ask MCP tool. It accepts a JSON-encoded
// pulse.AskRequest (the same shape the facade consumes), forwards to
// p.Ask, and returns the AskResponse. On success it also fires the
// schema-binding hook so subsequent action tools in the session pick up
// typed enum constraints — the LLM just touched a file, so this is the
// natural moment to bind.
func handleAsk(s *server.MCPServer, p *pulse.Pulse, bindOnOpen bool, handlers boundHandlers) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if ce := checkUnknownKeysAsk(body); ce != nil {
			return codedErrorResult(ce), nil
		}
		var typed pulse.AskRequest
		if err := json.Unmarshal(body, &typed); err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("parse request: %v", err)), nil
		}
		resp, err := p.Ask(ctx, &typed)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}

		// Best-effort session binding when we know which cohort was touched.
		if typed.Request != nil && typed.Request.Cohort != nil {
			path := cohortPath(typed.Request.Cohort)
			bindSessionFromPath(ctx, s, p, bindOnOpen, path, handlers)
		}

		return jsonResult(resp)
	}
}

// cohortPath mirrors the service-internal resolution rule (DataDir + "/" + Filename)
// so the MCP handler can compute the same path the service used when
// reading the cohort.
func cohortPath(c *types.Cohort) string {
	if c == nil {
		return ""
	}
	if c.DataDir != "" {
		return c.DataDir + "/" + c.Filename
	}
	return c.Filename
}

func handlePredict(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if ce := checkUnknownRequestKeys(body); ce != nil {
			return codedErrorResult(ce), nil
		}
		var typed types.Request
		if err := json.Unmarshal(body, &typed); err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("parse request: %v", err)), nil
		}
		result, err := p.Predict(ctx, &typed)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return jsonResult(result)
	}
}

func handleProcess(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if ce := checkUnknownRequestKeys(body); ce != nil {
			return codedErrorResult(ce), nil
		}
		var typed types.Request
		if err := json.Unmarshal(body, &typed); err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("parse request: %v", err)), nil
		}
		resp, err := p.Process(ctx, &typed)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return jsonResult(resp)
	}
}

func handleCompose(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if ce := checkUnknownKeysComposed(body); ce != nil {
			return codedErrorResult(ce), nil
		}
		var typed types.ComposedRequest
		if err := json.Unmarshal(body, &typed); err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("parse request: %v", err)), nil
		}
		resp, err := p.Compose(ctx, &typed)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return jsonResult(resp)
	}
}

func handleProcessChain(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if ce := checkUnknownKeysChain(body); ce != nil {
			return codedErrorResult(ce), nil
		}
		var typed types.ChainRequest
		if err := json.Unmarshal(body, &typed); err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("parse request: %v", err)), nil
		}
		resp, err := p.ProcessChain(ctx, &typed)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return jsonResult(resp)
	}
}

func handleSample(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return mcpgo.NewToolResultError("missing or invalid 'path'"), nil
		}
		count := 10
		if raw, ok := args["count"].(float64); ok && raw > 0 {
			count = int(raw)
		}
		rows, err := p.Sample(ctx, path, count)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return jsonResult(rows)
	}
}

func handleFacetSchema(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		body, err := requestBytes(req, "request")
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		var typed pulse.FacetRequest
		if err := json.Unmarshal(body, &typed); err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("parse request: %v", err)), nil
		}
		result, err := p.FacetSchema(ctx, &typed)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return jsonResult(result)
	}
}

func handleFacet(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return mcpgo.NewToolResultError("missing or invalid 'path'"), nil
		}
		field, ok := args["field"].(string)
		if !ok || field == "" {
			return mcpgo.NewToolResultError("missing or invalid 'field'"), nil
		}
		values, err := p.Facet(ctx, path, field)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		return jsonResult(values)
	}
}

// handleManifest serves the slim payload over MCP. Prose descriptions
// live in skills and are fetched separately via pulse_skills_get;
// duplicating them in the per-session bootstrap blob is the bloat we
// designed --slim to avoid. The CLI keeps both modes for human use.
func handleManifest(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return jsonResult(descriptor.SlimManifest(p.Manifest(ctx)))
	}
}

func handleSkillsList() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return jsonResult(skills.List())
	}
}

func handleSkillsGet() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return mcpgo.NewToolResultError("missing or invalid 'name'"), nil
		}
		body, found := skills.Get(name)
		if !found {
			return mcpgo.NewToolResultError(fmt.Sprintf("skill %q not found", name)), nil
		}
		return mcpgo.NewToolResultText(body), nil
	}
}

// handleExamplesSearch wraps the embedded request-example library
// search facade. All three filters are optional and additive.
func handleExamplesSearch(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
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
func handleExamplesGet(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return mcpgo.NewToolResultError("missing or invalid 'name'"), nil
		}
		ex, found := p.ExampleGet(name)
		if !found {
			return mcpgo.NewToolResultError(fmt.Sprintf("example %q not found", name)), nil
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
func handleErrorsLookup(_ *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		code, _ := args["code"].(string)
		domain, _ := args["domain"].(string)
		query, _ := args["query"].(string)
		if code == "" && domain == "" && query == "" {
			return mcpgo.NewToolResultError("specify at least one of code, domain, query"), nil
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
func handleLabelTables(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		tables := p.LabelTables()
		if tables == nil {
			tables = []pulse.LabelTableInfo{}
		}
		return jsonResult(tables)
	}
}

// handleLabelResolve reverse-resolves a human-readable name to the raw
// categorical key(s) a filter / grouper expects.
func handleLabelResolve(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		table, _ := args["table"].(string)
		if table == "" {
			return mcpgo.NewToolResultError("missing or invalid 'table'"), nil
		}
		query, _ := args["query"].(string)
		limit := 0
		if raw, ok := args["limit"].(float64); ok {
			limit = int(raw)
		}
		matches, err := p.ResolveLabel(table, query, limit)
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		if matches == nil {
			matches = []pulse.LabelMatch{}
		}
		return jsonResult(matches)
	}
}

// requestBytes pulls a request blob from a tool argument. The argument may
// be a JSON string (the common path: clients embed JSON in a string field)
// or a structured object that we re-marshal.
func requestBytes(req mcpgo.CallToolRequest, key string) ([]byte, error) {
	args := req.GetArguments()
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

func jsonResult(v any) (*mcpgo.CallToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("encode result: %v", err)), nil
	}
	return mcpgo.NewToolResultText(string(body)), nil
}

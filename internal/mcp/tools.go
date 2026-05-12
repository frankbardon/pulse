package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
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
	ToolCompose        = mcptools.ToolCompose
	ToolSample         = mcptools.ToolSample
	ToolFacet          = mcptools.ToolFacet
	ToolSkillsList     = mcptools.ToolSkillsList
	ToolSkillsGet      = mcptools.ToolSkillsGet
	ToolManifest       = mcptools.ToolManifest
	ToolAsk            = mcptools.ToolAsk
	ToolExamplesSearch = mcptools.ToolExamplesSearch
	ToolExamplesGet    = mcptools.ToolExamplesGet

	DescInspect        = mcptools.DescInspect
	DescPredict        = mcptools.DescPredict
	DescProcess        = mcptools.DescProcess
	DescCompose        = mcptools.DescCompose
	DescSample         = mcptools.DescSample
	DescFacet          = mcptools.DescFacet
	DescSkillsList     = mcptools.DescSkillsList
	DescSkillsGet      = mcptools.DescSkillsGet
	DescManifest       = mcptools.DescManifest
	DescAsk            = mcptools.DescAsk
	DescExamplesSearch = mcptools.DescExamplesSearch
	DescExamplesGet    = mcptools.DescExamplesGet
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

	handlers := boundHandlers{
		process: process,
		predict: predict,
		compose: compose,
		sample:  sample,
		facet:   facet,
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
			mcpgo.WithString("request", mcpgo.Description("JSON-encoded pulse.AskRequest with either `request` (types.Request) or `query` (natural-language string parsed against the cohort's schema), optional `on_invalid` (\"abort\"|\"suggest\") and optional `predict` (bool)"), mcpgo.Required()),
		),
		handleAsk(s, p, bindOnOpen, handlers),
	)

	s.AddTool(
		mcpgo.NewTool(ToolExamplesSearch,
			mcpgo.WithDescription(DescExamplesSearch),
			mcpgo.WithString("query", mcpgo.Description("Optional case-insensitive substring (matched against name, description, operators)")),
			mcpgo.WithArray("tags", mcpgo.Description("Optional list of canonical taxonomy tags; results must carry every tag (AND)")),
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
	_ = BindSessionTools(s, session.SessionID(), cohort.Schema(), handlers)
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

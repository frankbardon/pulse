package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse"
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
	ToolInspect    = mcptools.ToolInspect
	ToolPredict    = mcptools.ToolPredict
	ToolProcess    = mcptools.ToolProcess
	ToolCompose    = mcptools.ToolCompose
	ToolSample     = mcptools.ToolSample
	ToolFacet      = mcptools.ToolFacet
	ToolSkillsList = mcptools.ToolSkillsList
	ToolSkillsGet  = mcptools.ToolSkillsGet
	ToolManifest   = mcptools.ToolManifest

	DescInspect    = mcptools.DescInspect
	DescPredict    = mcptools.DescPredict
	DescProcess    = mcptools.DescProcess
	DescCompose    = mcptools.DescCompose
	DescSample     = mcptools.DescSample
	DescFacet      = mcptools.DescFacet
	DescSkillsList = mcptools.DescSkillsList
	DescSkillsGet  = mcptools.DescSkillsGet
	DescManifest   = mcptools.DescManifest
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

func registerTools(s *server.MCPServer, p *pulse.Pulse) {
	s.AddTool(
		mcpgo.NewTool(ToolInspect,
			mcpgo.WithDescription(DescInspect),
			mcpgo.WithString("path", mcpgo.Description("Filesystem path to the .pulse file"), mcpgo.Required()),
		),
		handleInspect(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolPredict,
			mcpgo.WithDescription(DescPredict),
			mcpgo.WithString("request", mcpgo.Description("JSON-encoded types.Request"), mcpgo.Required()),
		),
		handlePredict(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolProcess,
			mcpgo.WithDescription(DescProcess),
			mcpgo.WithString("request", mcpgo.Description("JSON-encoded types.Request"), mcpgo.Required()),
		),
		handleProcess(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolCompose,
			mcpgo.WithDescription(DescCompose),
			mcpgo.WithString("request", mcpgo.Description("JSON-encoded types.ComposedRequest"), mcpgo.Required()),
		),
		handleCompose(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolSample,
			mcpgo.WithDescription(DescSample),
			mcpgo.WithString("path", mcpgo.Description("Filesystem path to the .pulse file"), mcpgo.Required()),
			mcpgo.WithNumber("count", mcpgo.Description("Maximum rows to return (default 10)")),
		),
		handleSample(p),
	)

	s.AddTool(
		mcpgo.NewTool(ToolFacet,
			mcpgo.WithDescription(DescFacet),
			mcpgo.WithString("path", mcpgo.Description("Filesystem path to the .pulse file"), mcpgo.Required()),
			mcpgo.WithString("field", mcpgo.Description("Field name to facet"), mcpgo.Required()),
		),
		handleFacet(p),
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
}

func handleInspect(p *pulse.Pulse) server.ToolHandlerFunc {
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
		return jsonResult(result)
	}
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

func handleManifest(p *pulse.Pulse) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return jsonResult(p.Manifest(ctx))
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

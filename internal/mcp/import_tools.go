package mcp

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/imports"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// handleImport wires the pulse_import MCP tool. It parses the
// tool arguments into an imports.Spec, forwards to p.ImportFile, and
// on success fires the schema-binding hook so subsequent action tools
// in the session pick up bound enum constraints against the freshly
// imported cohort. Pulse-format passthroughs and managed handles both
// trigger schema binding because both produce an addressable .pulse
// path the next call may reference.
func handleImport(s *mcpsdk.Server, p *pulse.Pulse, bindOnOpen bool) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := toolArgs(req)
		source, ok := args["source"].(string)
		if !ok || source == "" {
			return errorResult("missing or invalid 'source'"), nil
		}
		spec := imports.Spec{SourcePath: source}
		if v, ok := args["format"].(string); ok {
			spec.Format = v
		}
		if v, ok := args["handle"].(string); ok {
			spec.Handle = v
		}
		if v, ok := args["sheet"].(string); ok {
			spec.Sheet = v
		}
		if v, ok := args["overwrite"].(bool); ok {
			spec.Overwrite = v
		}
		if v, ok := args["ttl"].(string); ok && v != "" {
			d, err := imports.ParseTTL(v)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			spec.TTL = d
		}

		res, err := p.ImportFile(ctx, spec)
		if err != nil {
			return errorResult(err.Error()), nil
		}

		// Bind the session's action tools against the new handle's
		// schema, mirroring handleInspect. Best-effort: failures fall
		// back to the global (unbound) tools.
		bindSessionFromPath(ctx, s, p, bindOnOpen, res.Path)

		return jsonResult(res)
	}
}

// handleDrop wires the pulse_drop MCP tool. Bare path: validate the
// handle argument, forward to p.Drop, return a structured ack on
// success.
func handleDrop(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := toolArgs(req)
		handle, ok := args["handle"].(string)
		if !ok || handle == "" {
			return errorResult("missing or invalid 'handle'"), nil
		}
		if err := p.Drop(ctx, handle); err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(map[string]any{
			"handle":  handle,
			"dropped": true,
			"message": fmt.Sprintf("dropped managed import %q", handle),
		})
	}
}

// handleImportsList wires the pulse_imports_list MCP tool. Returns
// imports.Entry snapshots — Manager.List doesn't sweep, so callers see
// expired entries flagged via Expired and can choose to drop them.
func handleImportsList(p *pulse.Pulse) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		entries, err := p.Imports(ctx)
		if err != nil {
			return errorResult(err.Error()), nil
		}
		return jsonResult(entries)
	}
}

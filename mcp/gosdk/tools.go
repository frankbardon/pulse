package gosdk

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	core "github.com/frankbardon/pulse/mcp"
	"github.com/frankbardon/pulse/mcp/toolmeta"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// errNilServer / errNilPulse are the two Register guard failures.
var (
	errNilServer = errors.New("gosdk: Register requires a non-nil *mcp.Server")
	errNilPulse  = errors.New("gosdk: Register requires a non-nil *pulse.Pulse")
)

// RegisteredTools returns the canonical list of MCP tool names this adapter
// mounts. Order is stable for deterministic documentation scans. Mirrors
// toolmeta.Names().
func RegisteredTools() []string {
	return toolmeta.Names()
}

// registerTools mounts every built-in MCP tool onto the caller-supplied server
// by iterating the SDK-free core catalog (core.Tools). Each core descriptor
// carries a precomputed structured input schema (the reflected typed In) and a
// type-erased Invoke that strict-decodes the raw arguments and calls the typed
// handler. This adapter owns only the go-sdk wiring: it adapts the raw
// CallToolRequest into core.Invoke and renders the typed result (or coded
// error) back into a CallToolResult. Handler logic lives in the core package.
//
// The generic AddTool[In,Out] reflection path is deliberately avoided; it
// panics on the recursive request types (Crosstab / overlay nesting). The
// low-level Server.AddTool path accepts the json.RawMessage schema directly.
func registerTools(s *mcpsdk.Server, p *pulse.Pulse, cfg Config) {
	for _, d := range core.Tools(cfg.Core()) {
		s.AddTool(
			&mcpsdk.Tool{
				Name:        d.Name,
				Description: d.Description,
				InputSchema: d.InputSchema,
			},
			coreHandler(s, p, cfg, d),
		)
	}
}

// coreDescriptor returns the core catalog descriptor for the named tool. Used
// by the schema-bind path to reuse the core Invoke under a bound input schema.
func coreDescriptor(cfg Config, name string) (core.ToolDescriptor, bool) {
	for _, d := range core.Tools(cfg.Core()) {
		if d.Name == name {
			return d, true
		}
	}
	return core.ToolDescriptor{}, false
}

// coreHandler adapts one core.ToolDescriptor into a go-sdk ToolHandler. The raw
// tool arguments (the structured typed input) flow straight into the
// descriptor's Invoke. On success the typed Out is JSON-marshalled into a text
// result; on error a *errors.CodedError is rendered as the structured
// {code, message, details} envelope (verbatim — never string-flattened) and any
// other error becomes a plain tool-error result. The inspect and import tools
// additionally fire the schema-bind-on-inspect hook after a successful call.
func coreHandler(s *mcpsdk.Server, p *pulse.Pulse, cfg Config, d core.ToolDescriptor) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		raw := rawArguments(req)
		result, err := d.Invoke(ctx, p, raw)
		if err != nil {
			return errorResultFor(err), nil
		}
		// Schema-bind-on-inspect: re-register the action tools with
		// enum-constrained variants once a cohort's schema is known.
		// Best-effort; failures degrade to the global (unbound) tools.
		switch d.Name {
		case toolmeta.ToolInspect:
			bindSessionFromPath(ctx, s, p, cfg, inspectPath(raw))
		case toolmeta.ToolImport:
			bindSessionFromPath(ctx, s, p, cfg, importResultPath(result))
		}
		return jsonResult(result)
	}
}

// rawArguments returns the raw structured JSON arguments object the client
// sent. The canonical tool contract is the reflected typed input (fields at the
// top level), so the arguments blob is the typed In verbatim — no {request:...}
// unwrapping. A nil/empty blob yields an empty JSON object so decode falls
// through to the handler's own required-field guards.
func rawArguments(req *mcpsdk.CallToolRequest) json.RawMessage {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(req.Params.Arguments)
}

// inspectPath extracts the path argument from a pulse_inspect raw argument blob
// for the bind-on-inspect hook.
func inspectPath(raw json.RawMessage) string {
	var in struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(raw, &in)
	return in.Path
}

// importResultPath extracts the managed .pulse path from a pulse_import result
// for the bind-on-inspect hook. The result is the typed imports.Result.
func importResultPath(result any) string {
	body, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	var r struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(body, &r)
	return r.Path
}

// errorResultFor renders an Invoke error into a CallToolResult. A
// *errors.CodedError (from the facade or the strict-decode layer) is serialised
// verbatim as its {code, message, details} envelope so the caller receives the
// structured suggestion payload; any other error becomes a bare text error.
func errorResultFor(err error) *mcpsdk.CallToolResult {
	var ce *perr.CodedError
	if errors.As(err, &ce) {
		return codedErrorResult(ce)
	}
	return errorResult(err.Error())
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
		return errorResult("encode result: " + err.Error()), nil
	}
	return textResult(string(body)), nil
}

// codedErrorResult wraps a CodedError as an MCP error result, serialising the
// full {code, message, details} envelope so the caller receives the structured
// suggestion payload rather than a bare string.
func codedErrorResult(ce *perr.CodedError) *mcpsdk.CallToolResult {
	body, err := json.Marshal(ce)
	if err != nil {
		return errorResult(ce.Error())
	}
	return errorResult(string(body))
}

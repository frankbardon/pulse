package gosdk

// bind.go is the SDK half of schema-bind-on-inspect: the per-session server
// mutation that consumes the SDK-free per-tool schemas produced by the core
// (mcp.BindWithExtensions). The pure field-classification + schema-building
// logic lives in the core package; this file only re-registers tools on a live
// go-sdk Server.
//
// go-sdk has no per-session tool override — the Server holds one global tool
// set keyed by name. Over stdio that is exactly what we want: one process = one
// session = one Server, so re-registering a tool by name (Server.AddTool
// replaces same-name entries) mutates this session's view and go-sdk auto-emits
// notifications/tools/list_changed to the connected client. Multi-file binding
// is not supported in v1: the latest inspect wins.
//
// Concurrency note: divergent schemas across CONCURRENT sessions would require
// one Server per session (the HTTP StreamableHTTPHandler getServer factory
// pattern). That is an HTTP consumer's concern and is not handled here — the
// stdio server this adapter typically serves has a single session.

import (
	"context"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	core "github.com/frankbardon/pulse/mcp"
	"github.com/frankbardon/pulse/mcp/toolmeta"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// bindSessionFromPath is the schema-bind-on-inspect hook shared by the inspect
// and import handlers. On a successful inspect/import it re-opens the cohort at
// path, derives enum-constrained JSON Schemas from its field classification (in
// the SDK-free core), and re-registers the action tools on the server by name
// with those bound variants. Over stdio the server has a single session, so the
// same-name Server.AddTool swap replaces the base tools for this session and
// go-sdk auto-emits notifications/tools/list_changed.
//
// Best-effort: silently degrades to the global (unbound) tools when
// cfg.BindOnInspect is off, the server is nil, or the cohort cannot be
// re-opened. Multi-file is latest-inspect-wins (v1 limitation).
func bindSessionFromPath(ctx context.Context, s *mcpsdk.Server, p *pulse.Pulse, cfg Config, path string) {
	if !cfg.BindOnInspect || s == nil || p == nil || path == "" {
		return
	}
	cohort, err := p.Open(ctx, path)
	if err != nil {
		return
	}
	_ = bindSessionTools(s, p, cfg, cohort.Schema())
}

// bindSessionTools derives the per-tool bound schemas (via the SDK-free core)
// for the given cohort schema and re-registers the action tools on the server
// by name with those enum-constrained variants. The bound handlers wrap the
// same core descriptor Invoke as the global tools — only the advertised input
// schema changes on re-registration.
//
// Mechanism (go-sdk): Server.AddTool with an existing tool name replaces the
// prior registration in the server's single global tool set and fires
// notifications/tools/list_changed to the connected client. There is no
// per-session tool map in go-sdk; that is correct for stdio, where one Server
// serves exactly one session.
func bindSessionTools(s *mcpsdk.Server, p *pulse.Pulse, cfg Config, schema *encoding.Schema) error {
	if s == nil || schema == nil {
		return nil
	}
	schemas, err := core.BindWithExtensions(schema, p.Service().ExtensionsSnapshot())
	if err != nil {
		return err
	}
	handlers := boundHandlersFor(p, cfg)
	for _, entry := range []struct {
		name        string
		description string
		handler     mcpsdk.ToolHandler
	}{
		{toolmeta.ToolProcess, toolmeta.DescProcess + " (schema-bound)", handlers.process},
		{toolmeta.ToolPredict, toolmeta.DescPredict + " (schema-bound)", handlers.predict},
		{toolmeta.ToolCompose, toolmeta.DescCompose + " (schema-bound)", handlers.compose},
		{toolmeta.ToolSample, toolmeta.DescSample + " (schema-bound)", handlers.sample},
		{toolmeta.ToolFacet, toolmeta.DescFacet + " (schema-bound)", handlers.facet},
		{toolmeta.ToolFacetSchema, toolmeta.DescFacetSchema + " (schema-bound)", handlers.facetSchema},
		{toolmeta.ToolProcessChain, toolmeta.DescProcessChain + " (schema-bound)", handlers.processChain},
	} {
		raw, ok := schemas[entry.name]
		if !ok || entry.handler == nil {
			continue
		}
		s.AddTool(&mcpsdk.Tool{
			Name:        entry.name,
			Description: entry.description,
			InputSchema: raw,
		}, entry.handler)
	}
	return nil
}

// boundHandlers carries the per-tool handler closures so bindSessionTools can
// attach them to the bound variants. We reuse the existing global handlers
// verbatim — the wire-shape stays identical; only the input schema changes.
type boundHandlers struct {
	process      mcpsdk.ToolHandler
	predict      mcpsdk.ToolHandler
	compose      mcpsdk.ToolHandler
	sample       mcpsdk.ToolHandler
	facet        mcpsdk.ToolHandler
	facetSchema  mcpsdk.ToolHandler
	processChain mcpsdk.ToolHandler
}

// boundHandlersFor constructs the per-tool handler set for the bound variants
// from the live Pulse facade. The handlers are byte-identical to the globally
// registered ones (they wrap the same core descriptor Invoke) — only the
// advertised input schema changes on re-registration. The bound action tools
// never themselves trigger the bind hook, so a nil server and a BindOnInspect=
// false config are passed to coreHandler.
func boundHandlersFor(p *pulse.Pulse, cfg Config) boundHandlers {
	noBind := cfg
	noBind.BindOnInspect = false
	mk := func(name string) mcpsdk.ToolHandler {
		d, ok := coreDescriptor(cfg, name)
		if !ok {
			return nil
		}
		return coreHandler(nil, p, noBind, d)
	}
	return boundHandlers{
		process:      mk(toolmeta.ToolProcess),
		predict:      mk(toolmeta.ToolPredict),
		compose:      mk(toolmeta.ToolCompose),
		sample:       mk(toolmeta.ToolSample),
		facet:        mk(toolmeta.ToolFacet),
		facetSchema:  mk(toolmeta.ToolFacetSchema),
		processChain: mk(toolmeta.ToolProcessChain),
	}
}

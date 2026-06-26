// Package mcp wraps the Pulse library facade in the Model Context Protocol
// surface. The library has no dependency on this package; the CLI invokes
// New() and ServeStdio() to expose Pulse over stdio for MCP clients.
//
// Migration in progress (mcp-library-surface): the server is built on
// github.com/modelcontextprotocol/go-sdk. Tools (E1-S2), resources, and prompts
// (E1-S3) are ported; the schema-bind-on-inspect surface (schema_bind.go) lands
// in E1-S4 and still carries a `//go:build ignore` constraint until then.
package mcp

import (
	"context"

	"github.com/frankbardon/pulse"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerName is the MCP server identity reported during initialize.
const ServerName = "pulse"

// SpecVersion pins the supported MCP spec. Bump deliberately when upgrading the
// go-sdk dependency and confirming the wire format still works for our clients.
const SpecVersion = "1.0.0"

// New constructs an MCP server bound to the given Pulse instance. The caller is
// responsible for serving the returned server (typically via ServeStdio).
// Bind-on-inspect (the schema-bound tool enum variants) is enabled by default;
// use NewWithOptions to opt out.
func New(p *pulse.Pulse) *mcpsdk.Server {
	return NewWithOptions(p, Options{BindOnOpen: true})
}

// Options configures the MCP server.
type Options struct {
	// BindOnOpen toggles the session-scoped schema-bound tool variants
	// registered on successful pulse_inspect calls. Default (true via New)
	// gives LLM clients typed enum constraints on field-name parameters;
	// false leaves only the unbound global tools, which is useful for
	// embedders that bind themselves.
	BindOnOpen bool
}

// NewWithOptions is New with explicit configuration.
//
// NewServer panics on a nil Implementation or other bad config; the literal
// below is always valid.
func NewWithOptions(p *pulse.Pulse, opts Options) *mcpsdk.Server {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    ServerName,
		Version: SpecVersion,
	}, nil)

	// All 19 built-in tools register here on the go-sdk server (E1-S2).
	registerTools(s, p, opts.BindOnOpen)

	// Resource surface (payload schema + pulse:// / pulse-skill:// schemes) and
	// the prompt surface (pulse-bootstrap / pulse-author-request) — E1-S3.
	registerResources(s, p)
	registerPrompts(s)

	// TODO(E1-S4): schema-bind-on-inspect — port schema_bind.go. The
	//   bind-on-inspect hook (bindSessionFromPath) is a no-op until then;
	//   opts.BindOnOpen is threaded through registerTools so the wiring is
	//   ready when the binder lands.
	// The handler logic for the above lives in the build-ignored source files
	// in this package and is migrated verbatim by the cited stories.

	return s
}

// ServeStdio runs the given MCP server over stdio. Blocks until the client
// disconnects or an error occurs.
func ServeStdio(s *mcpsdk.Server) error {
	return s.Run(context.Background(), &mcpsdk.StdioTransport{})
}

// Package mcp wraps the Pulse library facade in the Model Context Protocol
// surface. The library has no dependency on this package; the CLI invokes
// New() and ServeStdio() to expose Pulse over stdio for MCP clients.
//
// Tool names are defined in tools.go and exported via RegisteredTools so the
// skills coverage gate can verify documentation parity.
package mcp

import (
	"github.com/frankbardon/pulse"
	"github.com/mark3labs/mcp-go/server"
)

// ServerName is the MCP server identity reported during initialize.
const ServerName = "pulse"

// SpecVersion pins the supported MCP spec. Bump deliberately when upgrading
// mark3labs/mcp-go and confirming the wire format still works for our clients.
const SpecVersion = "1.0.0"

// New constructs an MCP server bound to the given Pulse instance. Tools and
// resources are registered eagerly. The caller is responsible for serving the
// returned server (typically via server.ServeStdio). Bind-on-inspect (the
// schema-bound tool enum variants) is enabled by default; use NewWithOptions
// to opt out.
func New(p *pulse.Pulse) *server.MCPServer {
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
func NewWithOptions(p *pulse.Pulse, opts Options) *server.MCPServer {
	s := server.NewMCPServer(
		ServerName,
		SpecVersion,
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
	)
	registerTools(s, p, opts.BindOnOpen)
	registerResources(s, p)
	return s
}

// ServeStdio runs the given MCP server over stdio. Blocks until the client
// disconnects or an error occurs.
func ServeStdio(s *server.MCPServer) error {
	return server.ServeStdio(s)
}

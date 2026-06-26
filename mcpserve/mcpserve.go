// Package mcpserve exposes Pulse's Model Context Protocol server as a public
// entry point.
//
// The MCP wiring lives in internal/mcp (the library facade has no dependency
// on it; the CLI invokes it to expose Pulse over stdio). This package lets an
// embedder serve a Pulse MCP from its own process — including any operators,
// expression functions, or label tables registered via the constructed
// *pulse.Pulse's Options.Extensions — without shelling out to the `pulse`
// binary. That is the only way a domain layer (e.g. a BERA-flavored Pulse)
// can surface its in-process Go extensions over MCP, since the stock binary
// cannot load them.
package mcpserve

import (
	"context"
	"io"
	"os"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// nopWriteCloser adapts an io.Writer to io.WriteCloser; go-sdk's IOTransport
// owns its streams via Close, but Serve's caller owns the lifetime of out.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// Options configures the served MCP server. It mirrors internal/mcp.Options.
type Options struct {
	// BindOnOpen registers session-scoped schema-bound tool variants on a
	// successful pulse_inspect. True gives MCP clients typed enum constraints
	// on field-name parameters; false leaves only the unbound global tools,
	// which is useful for clients that bind tool schemas themselves.
	BindOnOpen bool
}

// Serve runs an MCP server bound to p, reading JSON-RPC requests from in and
// writing responses to out. It blocks until ctx is cancelled or a transport
// error occurs. Extensions registered when p was constructed are exposed
// verbatim.
func Serve(ctx context.Context, p *pulse.Pulse, opts Options, in io.Reader, out io.Writer) error {
	srv := mcp.NewWithOptions(p, mcp.Options{BindOnOpen: opts.BindOnOpen})
	transport := &mcpsdk.IOTransport{
		Reader: io.NopCloser(in),
		Writer: nopWriteCloser{out},
	}
	return srv.Run(ctx, transport)
}

// ServeStdio is Serve over the process's stdin/stdout — the transport MCP
// clients use when they spawn the server as a subprocess. It blocks until
// stdin closes or the client disconnects.
func ServeStdio(p *pulse.Pulse, opts Options) error {
	return Serve(context.Background(), p, opts, os.Stdin, os.Stdout)
}

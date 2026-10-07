// Package mcpserve exposes Pulse's Model Context Protocol server as a public
// entry point.
//
// The MCP wiring lives in the public mcp/ core (SDK-free catalog) plus the
// mcp/gosdk adapter (the only package importing the MCP SDK); the library
// facade has no dependency on either. This package lets an embedder serve a
// Pulse MCP from its own process — including any operators, expression
// functions, or label tables registered via the constructed *pulse.Pulse's
// Options.Extensions — without shelling out to the `pulse` binary. That is the
// only way a domain layer (e.g. a BERA-flavored Pulse) can surface its
// in-process Go extensions over MCP, since the stock binary cannot load them.
package mcpserve

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/frankbardon/pulse/types"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName is the MCP server identity reported during initialize.
const serverName = "pulse"

// nopWriteCloser adapts an io.Writer to io.WriteCloser; go-sdk's IOTransport
// owns its streams via Close, but Serve's caller owns the lifetime of out.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// Options configures the served MCP server.
type Options struct {
	// BindOnOpen registers session-scoped schema-bound tool variants on a
	// successful pulse_inspect. True gives MCP clients typed enum constraints
	// on field-name parameters; false leaves only the unbound global tools,
	// which is useful for clients that bind tool schemas themselves.
	BindOnOpen bool

	// Version is the server identity advertised during initialize and threaded
	// into the adapter Config. Defaults to pulse.Version() when empty.
	Version string

	// DisableCohortScan skips the startup filesystem walk that enumerates the
	// data root's .pulse files as exact-match pulse:// resources. Cohorts stay
	// readable through the pulse:// template either way — only the
	// resources/list enumeration is withheld. Leave it false (the zero value,
	// the pre-knob behaviour) unless walking the root at startup is itself the
	// cost you are avoiding. A feature profile on the served *pulse.Pulse
	// whose behaviour sets disable_cohort_scan ORs into this field.
	DisableCohortScan bool

	// FeatureProfileFile is a host OS path (absolute, or relative to the
	// process working directory) to a feature profile JSON file. It is
	// consumed by NewPulse, which reads it with the OS filesystem — never
	// the instance's afero Fs or DataDir — parses it with
	// pulse.ParseFeatureProfile and hands the value to pulse.New through
	// pulse.Options.FeatureProfile. Empty falls back to the
	// PULSE_FEATURE_PROFILE environment variable, which NewPulse ignores
	// when the pulse.Options it is given already carry a profile.
	//
	// Serve and ServeStdio ignore it: a profile is applied when the
	// instance is constructed, so serve an instance built by NewPulse
	// (or by pulse.New with a profile) to get one.
	FeatureProfileFile string

	// DefaultReturn is the `return` preset an MCP request without its own
	// `return` block is shaped by, threaded into gosdk.Config.DefaultReturn.
	// The zero value defers to the instance default (pulse.Options.DefaultReturn,
	// else the feature profile's `return`), else the built-in `standard`
	// preset. types.ReturnPresetFull serves the unshaped library output.
	// Serve refuses an unknown preset (PULSE_RETURN_INVALID).
	DefaultReturn types.ReturnPreset
}

// newServer builds a bare go-sdk server and mounts the full Pulse surface onto
// it through the single registration path (the gosdk adapter).
func newServer(p *pulse.Pulse, opts Options) (*mcpsdk.Server, error) {
	version := opts.Version
	if version == "" {
		version = pulse.Version()
	}
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    serverName,
		Version: version,
	}, nil)
	if err := gosdk.Register(srv, p, gosdk.Config{
		Version:           version,
		BindOnInspect:     opts.BindOnOpen,
		DisableCohortScan: opts.DisableCohortScan,
		DefaultReturn:     opts.DefaultReturn,
	}); err != nil {
		return nil, fmt.Errorf("registering mcp surface: %w", err)
	}
	return srv, nil
}

// Serve runs an MCP server bound to p, reading JSON-RPC requests from in and
// writing responses to out. It blocks until ctx is cancelled or a transport
// error occurs. Extensions registered when p was constructed are exposed
// verbatim.
func Serve(ctx context.Context, p *pulse.Pulse, opts Options, in io.Reader, out io.Writer) error {
	srv, err := newServer(p, opts)
	if err != nil {
		return err
	}
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

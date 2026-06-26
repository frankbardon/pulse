// Package gosdk is the thin, reusable adapter that mounts the SDK-free Pulse
// MCP catalog (github.com/frankbardon/pulse/mcp) onto a caller-supplied
// github.com/modelcontextprotocol/go-sdk server. It is the ONLY package in the
// module that imports the MCP SDK — the firewall test over the mcp/ core keeps
// the core SDK-free, and this adapter is where every SDK type is allowed.
//
// External programs that already run their own go-sdk server can mount the full
// Pulse surface — all built-in tools, the pulse:// / pulse-skill:// resource
// schemes, the static pulse://schema resource, both prompts, and the
// schema-bind-on-inspect behaviour — by calling Register with a constructed
// *pulse.Pulse and a Config:
//
//	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "my-host", Version: "..."}, nil)
//	if err := gosdk.Register(srv, p, gosdk.Config{Version: "1.0.0", BindOnInspect: true}); err != nil {
//	    return err
//	}
//	// caller owns serving: srv.Run(ctx, &mcpsdk.StdioTransport{}) — gosdk never serves.
//
// Register MOUNTS onto the server it is given; it never constructs or returns a
// finished server and never calls Serve/Run. There is no convenience "give me a
// ready server" constructor — server lifecycle (creation, transport, serving)
// stays entirely with the caller. All runtime configuration is threaded through
// Config; the adapter holds no process globals.
package gosdk

import (
	"github.com/frankbardon/pulse"
	core "github.com/frankbardon/pulse/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config carries the runtime configuration for one Register call. It is the
// single place runtime identity and behavioural toggles are injected — the
// adapter reads nothing from package-level state.
type Config struct {
	// Version is the server/build identity string. Threaded into the core
	// tool catalog (mcp.Config.Version) so config-dependent tool closures can
	// capture it. The caller is responsible for the go-sdk
	// Implementation.Version it passes to mcpsdk.NewServer; Register does not
	// touch the server's advertised identity.
	Version string

	// BindOnInspect toggles the session-scoped schema-bound tool variants.
	// When true, a successful pulse_inspect (or pulse_import) re-registers the
	// action tools by name with enum-constrained input schemas derived from the
	// cohort's fields, and go-sdk auto-emits notifications/tools/list_changed.
	// Over stdio the server has a single session, so the same-name AddTool swap
	// is exactly the per-session override we want. When false, only the unbound
	// global tools remain — useful for embedders that bind themselves.
	BindOnInspect bool
}

// Core projects the adapter Config onto the SDK-free core Config consumed by
// mcp.Tools. Only the fields the core catalog reads cross the boundary.
func (c Config) Core() core.Config {
	return core.Config{Version: c.Version}
}

// Register mounts the full Pulse MCP surface onto the caller-supplied server:
// every built-in tool, the resource schemes + static schema resource, both
// prompts, and (when cfg.BindOnInspect) the schema-bind-on-inspect behaviour.
// It returns an error only if a sub-registration fails; today every step is
// total, so a nil server or nil Pulse is the only failure mode.
//
// The server's lifecycle is the caller's: Register never serves. After this
// call returns, the caller drives srv.Run / srv.Connect with whatever transport
// it chooses.
func Register(server *mcpsdk.Server, p *pulse.Pulse, cfg Config) error {
	if server == nil {
		return errNilServer
	}
	if p == nil {
		return errNilPulse
	}

	registerTools(server, p, cfg)
	registerResources(server, p)
	registerPrompts(server)

	return nil
}

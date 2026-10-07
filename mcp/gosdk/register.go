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
//	if err := gosdk.Register(srv, p, gosdk.Config{Version: pulse.Version(), BindOnInspect: true}); err != nil {
//	    return err
//	}
//	// caller owns serving: srv.Run(ctx, &mcpsdk.StdioTransport{}) — gosdk never serves.
//
// A *pulse.Pulse built with a feature profile mounts only what that
// profile offers: hidden tools, prompts and the cohort enumeration are
// never registered.
//
// Register MOUNTS onto the server it is given; it never constructs or returns a
// finished server and never calls Serve/Run. There is no convenience "give me a
// ready server" constructor — server lifecycle (creation, transport, serving)
// stays entirely with the caller. All runtime configuration is threaded through
// Config; the adapter holds no process globals.
package gosdk

import (
	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/facadebridge"
	core "github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/types"
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
	// touch the server's advertised identity. Empty resolves to
	// pulse.Version() (see Core).
	Version string

	// BindOnInspect toggles the session-scoped schema-bound tool variants.
	// When true, a successful pulse_inspect (or pulse_import) re-registers the
	// action tools by name with enum-constrained input schemas derived from the
	// cohort's fields, and go-sdk auto-emits notifications/tools/list_changed.
	// Over stdio the server has a single session, so the same-name AddTool swap
	// is exactly the per-session override we want. When false, only the unbound
	// global tools remain — useful for embedders that bind themselves.
	BindOnInspect bool

	// DisableCohortScan skips the startup filesystem walk that enumerates
	// every .pulse file under the data root as an exact-match pulse://
	// resource. The pulse:// resource TEMPLATE is registered either way, so
	// every cohort stays READABLE by URI — what a disabled scan withholds is
	// the enumeration in resources/list, nothing else. Set it when the data
	// root is large, remote or volatile enough that walking it at startup
	// costs more than the listing is worth. The zero value keeps the scan, so
	// an existing Register call is unchanged.
	//
	// A feature profile on p whose behaviour sets disable_cohort_scan ORs
	// into this field: either one skips the scan, and false here cannot
	// turn a profile's true back off.
	DisableCohortScan bool

	// DefaultReturn is the `return` preset an MCP request (pulse_process,
	// pulse_predict, every pulse_compose slot, every pulse_process_chain
	// stage) WITHOUT its own `return` block is shaped by. The zero value
	// defers to the instance default (pulse.Options.DefaultReturn, else
	// the feature profile's `return`), else the built-in `standard`
	// preset — so an MCP agent gets lean responses by default while the
	// library default stays `full`. Set types.ReturnPresetFull to serve
	// the unshaped output. A request's own block always wins, and an
	// engine DisableComponents still keeps components off. Register
	// refuses an unknown preset with PULSE_RETURN_INVALID.
	DefaultReturn types.ReturnPreset
}

// coreConfig projects the adapter Config onto the SDK-free core Config consumed by
// mcp.Tools. Only the fields the core catalog reads cross the boundary.
// An empty Version resolves to pulse.Version(), so the catalog never
// carries a blank build identity.
func (c Config) coreConfig() core.Config {
	version := c.Version
	if version == "" {
		version = pulse.Version()
	}
	return core.Config{Version: version, DefaultReturn: c.DefaultReturn}
}

// Register mounts the Pulse MCP surface onto the caller-supplied server:
// every built-in tool, the resource schemes + static schema resource, both
// prompts, and (when cfg.BindOnInspect) the schema-bind-on-inspect behaviour.
// A feature profile on p scopes the mount: a tool, prompt or cohort
// enumeration whose feature the instance does not offer is never
// registered, so it is indistinguishable from one that does not exist.
// Without a feature profile the full surface is mounted.
// It returns an error for a nil server, a nil Pulse, or a
// cfg.DefaultReturn that is not a known preset (PULSE_RETURN_INVALID);
// every registration step is otherwise total.
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

	if err := cfg.coreConfig().Validate(instanceOf(p)); err != nil {
		return err
	}

	// A feature profile's behaviour.disable_cohort_scan ORs into the
	// caller's setting; the profile can turn the scan off, never on.
	if facadebridge.CohortScanDisabled != nil && facadebridge.CohortScanDisabled(p) {
		cfg.DisableCohortScan = true
	}
	// A feature profile that omits mcp_extra:cohort_resources withholds
	// the pulse:// enumeration through the same switch; the template
	// stays registered, so cohorts remain readable by URI.
	if !instanceOf(p).Enabled(cohortResourcesFeature) {
		cfg.DisableCohortScan = true
	}

	registerTools(server, p, cfg)
	registerResources(server, p, cfg)
	registerPrompts(server, p)

	return nil
}

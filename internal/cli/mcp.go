package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/obsprom"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/frankbardon/pulse/mcpserve"
	"github.com/frankbardon/pulse/types"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	cli "github.com/urfave/cli/v3"
	"go.opentelemetry.io/otel"
)

// mcpServerName is the MCP server identity reported during initialize. The
// version is threaded in from the build (see MCPCommand) rather than read from
// a package global.
const mcpServerName = "pulse"

// MCPCommand returns the mcp command leaf. Running it serves the MCP protocol
// over stdio so AI clients (Claude Desktop, Claude Code, etc.) can discover and
// call Pulse tools. version is the build identity, threaded into both the
// advertised server Implementation.Version and the adapter Config.
func MCPCommand(version string) *cli.Command {
	return &cli.Command{
		Name:  "mcp",
		Usage: "Run the Model Context Protocol server over stdio",
		Description: "Exposes Pulse as an MCP server. Resources and tools are " +
			"rooted at PULSE_DATA_DIR (or --data-dir). The library facade is " +
			"unchanged; the MCP server only translates between the protocol " +
			"and library entrypoints.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "data-dir",
				Usage:   "Directory of .pulse cohort files (overrides PULSE_DATA_DIR)",
				Sources: cli.EnvVars("PULSE_DATA_DIR"),
			},
			&cli.BoolFlag{
				Name: "no-cohort-scan",
				Usage: "Skip the startup walk of the data directory that enumerates .pulse files as pulse:// resources. " +
					"Cohorts stay readable by URI; only the resources/list enumeration is withheld.",
				Sources: cli.EnvVars("PULSE_MCP_NO_COHORT_SCAN"),
			},
			&cli.StringFlag{
				Name: "feature-profile",
				Usage: "Feature profile JSON file (OS path, relative to the working directory, not the data dir). " +
					"Falls back to PULSE_FEATURE_PROFILE. An invalid profile fails startup.",
			},
			&cli.StringFlag{
				Name: "return",
				Usage: "Response preset (" + pipeList(types.AllReturnPresets()) + ") for a tool request without its own return block. " +
					"Unset: the instance or feature-profile return default, else standard. full serves the unshaped library output.",
			},
			&cli.StringFlag{
				Name: "metrics-addr",
				Usage: "Serve Prometheus text-format metrics on GET /metrics at this host:port (e.g. 127.0.0.1:9090; :0 picks a free port). " +
					"Unset opens no port.",
			},
			&cli.BoolFlag{
				Name:  "bind-on-open",
				Usage: "Register session-scoped schema-bound tool variants on successful pulse_inspect (default true). Disable for clients that bind tool schemas themselves.",
				Value: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			dataDir := cmd.String("data-dir")
			if dataDir == "" {
				return fmt.Errorf("data directory required: set PULSE_DATA_DIR or pass --data-dir")
			}

			opts := mcpPulseOptions(ctx, dataDir)
			// Logs go to stderr only (stdout is the JSON-RPC transport);
			// metrics are recorded only when an exporter is asked for.
			metricsAddr := cmd.String("metrics-addr")
			var reg *obsprom.Registry
			if metricsAddr != "" {
				reg = obsprom.New()
				opts.Metrics = reg
			}

			// OTLP/HTTP export, only when the standard OTel environment
			// names an endpoint; unset builds nothing.
			otlp, err := startOTLP(ctx, os.LookupEnv)
			if err != nil {
				return err
			}
			var otlpSignals string
			if otlp != nil {
				h := otlpErrorHandler(opts.Logger, cmd.Root().ErrWriter)
				otel.SetErrorHandler(h)
				if otlp.ResourceWarning != nil {
					h.Handle(otlp.ResourceWarning)
				}
				defer func() {
					if err := otlp.Shutdown(ctx); err != nil {
						h.Handle(err)
					}
				}()
				if otlp.Hooks != nil {
					opts.Hooks = otlp.Hooks
				}
				opts.Metrics = fanMetrics(opts.Metrics, otlp.Metrics)
				otlpSignals = otlp.Signals
			}

			// mcpserve.NewPulse owns the profile resolution: the flag, else
			// PULSE_FEATURE_PROFILE, read as an OS path and parsed strictly.
			p, err := mcpserve.NewPulse(opts,
				mcpserve.Options{FeatureProfileFile: cmd.String("feature-profile")})
			if err != nil {
				return fmt.Errorf("constructing pulse: %w", err)
			}

			bindOnOpen := cmd.Bool("bind-on-open")
			noCohortScan := cmd.Bool("no-cohort-scan")
			defaultReturn := types.ReturnPreset(cmd.String("return"))

			// Construct a bare go-sdk server and mount the full Pulse surface
			// through the single registration path (the gosdk adapter), then
			// serve over stdio. NewServer panics on a nil Implementation; the
			// literal below is always valid.
			srv := mcpsdk.NewServer(&mcpsdk.Implementation{
				Name:    mcpServerName,
				Version: version,
			}, nil)
			if err := gosdk.Register(srv, p, gosdk.Config{
				Version:           version,
				BindOnInspect:     bindOnOpen,
				DisableCohortScan: noCohortScan,
				DefaultReturn:     defaultReturn,
			}); err != nil {
				return fmt.Errorf("registering mcp surface: %w", err)
			}

			// The exporter binds only after the surface registered, so a
			// refused configuration never opens a port; it stops with the
			// server.
			msrv, err := startMetrics(metricsAddr, reg)
			if err != nil {
				return err
			}
			if msrv != nil {
				defer func() {
					sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					_ = msrv.Shutdown(sctx)
				}()
				metricsAddr = msrv.Addr()
			}

			// The effective settings come from the library: a feature
			// profile can turn the cohort scan off behind the flag's back.
			info := mcpserve.Describe(p, mcpserve.Options{
				BindOnOpen: bindOnOpen, DisableCohortScan: noCohortScan, DefaultReturn: defaultReturn,
			})
			fmt.Fprintln(cmd.Root().ErrWriter, mcpStartupLine(dataDir, bindOnOpen, info, mcpObsSettings{
				LogLevel:    activeLogLevel(cmd.String(flagLogLevel)),
				MetricsAddr: metricsAddr,
				OTLP:        otlpSignals,
			}))

			if err := srv.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
				return fmt.Errorf("mcp server: %w", err)
			}
			return nil
		},
	}
}

// mcpObsSettings is the observability half of the startup notice: the
// active --log-level (empty when off), the bound --metrics-addr
// (empty when no exporter runs) and the OTLP signals.
type mcpObsSettings struct {
	LogLevel    string
	MetricsAddr string
	// OTLP names the signals exported over OTLP/HTTP (empty when the
	// OTel environment names no endpoint) — never the endpoint itself.
	OTLP string
}

// mcpStartupLine formats the one-line stderr startup notice from the
// effective serving settings. Formatting only: the values are decided by
// mcpserve.Describe. It names settings only — never a cohort, a request
// or a record.
func mcpStartupLine(dataDir string, bindOnOpen bool, info mcpserve.ServeInfo, obs mcpObsSettings) string {
	line := fmt.Sprintf("pulse mcp: serving over stdio (data dir: %s, bind-on-open: %v, cohort-scan: %v", dataDir, bindOnOpen, info.CohortScan)
	ret := string(info.DefaultReturn)
	if ret == "" {
		ret = "(custom)"
	}
	line += ", return: " + ret
	if info.FeatureProfileLoaded {
		name := info.FeatureProfile
		if name == "" {
			name = "(unnamed)"
		}
		line += fmt.Sprintf(", feature-profile: %s", name)
	}
	if tuned := tunedLimits(info.Limits); tuned != "" {
		line += ", limits: " + tuned
	}
	if obs.LogLevel != "" {
		line += ", log-level: " + obs.LogLevel
	}
	if obs.MetricsAddr != "" {
		line += ", metrics: " + obs.MetricsAddr
	}
	if obs.OTLP != "" {
		line += ", otlp: " + obs.OTLP
	}
	return line + ")"
}

// tunedLimits spells the effective limits that differ from the built-in
// defaults as space-separated name=value pairs, in the --limit grammar;
// empty when every limit is at its default.
func tunedLimits(effective []descriptor.LimitMeta) string {
	var parts []string
	for _, m := range effective {
		if m.Value != m.Default {
			parts = append(parts, m.Name+"="+limits.Spell(limits.Name(m.Name), m.Value))
		}
	}
	return strings.Join(parts, " ")
}

// mcpPulseOptions builds the pulse.Options `pulse mcp` constructs its
// instance from: the data directory and the root persistent flags in ctx
// (withGlobals: the stderr logger, the --shard-workers / --decode-workers
// caps and the --limit layer, which mcpserve.NewPulse then layers over
// the feature profile's limits section per key).
func mcpPulseOptions(ctx context.Context, dataDir string) pulse.Options {
	return withGlobals(ctx, pulse.Options{DataDir: dataDir})
}

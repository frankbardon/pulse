package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/frankbardon/pulse/mcpserve"
	"github.com/frankbardon/pulse/types"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	cli "github.com/urfave/cli/v3"
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
				Usage: "Response preset (full|standard|minimal) for a tool request without its own return block. " +
					"Unset: the instance or feature-profile return default, else standard. full serves the unshaped library output.",
			},
			&cli.StringSliceFlag{
				Name: "limit",
				Usage: "Instance resource limit as name=value (repeatable; snake_case names such as max_groups, request_timeout). " +
					"Values: a positive integer, a Go duration for request_timeout (30s), unlimited (or -1), or 0 for the default. " +
					"Overrides the feature profile's limits section per key; an unknown name or bad value fails startup with CLI_INPUT.",
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

			opts, err := mcpPulseOptions(dataDir, cmd.StringSlice("limit"))
			if err != nil {
				return err
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
			// The effective settings come from the library: a feature
			// profile can turn the cohort scan off behind the flag's back.
			info := mcpserve.Describe(p, mcpserve.Options{BindOnOpen: bindOnOpen, DisableCohortScan: noCohortScan})
			fmt.Fprintln(os.Stderr, mcpStartupLine(dataDir, bindOnOpen, info, p.Limits()))

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
				DefaultReturn:     types.ReturnPreset(cmd.String("return")),
			}); err != nil {
				return fmt.Errorf("registering mcp surface: %w", err)
			}
			if err := srv.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
				return fmt.Errorf("mcp server: %w", err)
			}
			return nil
		},
	}
}

// mcpStartupLine formats the one-line stderr startup notice from the
// effective serving settings. Formatting only: the values are decided by
// mcpserve.Describe.
func mcpStartupLine(dataDir string, bindOnOpen bool, info mcpserve.ServeInfo, effective pulse.Limits) string {
	line := fmt.Sprintf("pulse mcp: serving over stdio (data dir: %s, bind-on-open: %v, cohort-scan: %v", dataDir, bindOnOpen, info.CohortScan)
	if info.FeatureProfileLoaded {
		name := info.FeatureProfile
		if name == "" {
			name = "(unnamed)"
		}
		line += fmt.Sprintf(", feature-profile: %s", name)
	}
	if tuned := tunedLimits(effective); tuned != "" {
		line += ", limits: " + tuned
	}
	return line + ")"
}

// tunedLimits spells the effective limits that differ from the built-in
// defaults as space-separated name=value pairs, in the --limit grammar;
// empty when every limit is at its default.
func tunedLimits(effective pulse.Limits) string {
	var parts []string
	for _, n := range limits.Names() {
		if v := limits.Value(effective, n); v != limits.Default(n) {
			parts = append(parts, string(n)+"="+limits.Spell(n, v))
		}
	}
	return strings.Join(parts, " ")
}

// mcpPulseOptions builds the pulse.Options `pulse mcp` constructs its
// instance from: the data directory and the --limit layer.
func mcpPulseOptions(dataDir string, limitSpecs []string) (pulse.Options, error) {
	lims, err := parseLimitFlags(limitSpecs)
	if err != nil {
		return pulse.Options{}, err
	}
	return pulse.Options{DataDir: dataDir, Limits: lims}, nil
}

// parseLimitFlags turns repeated `--limit name=value` flags into the
// Options.Limits input layer (0 = default, so an unset key defers to the
// feature profile and then the built-in default). A later flag for the
// same name wins. A malformed pair, an unknown name or a bad value is
// CLI_INPUT with details {flag, value} (+ limit when the name parsed).
func parseLimitFlags(specs []string) (pulse.Limits, error) {
	var out pulse.Limits
	for _, spec := range specs {
		key, raw, ok := strings.Cut(spec, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return pulse.Limits{}, limitFlagError(spec, "",
				"--limit "+spec+": want name=value (e.g. max_groups=1000000, request_timeout=30s)")
		}
		n, err := limits.ParseName(key)
		if err != nil {
			return pulse.Limits{}, limitFlagError(spec, "",
				"--limit "+spec+": unknown limit "+key+"; known: "+limitNameList())
		}
		v, err := limits.ParseValue(n, raw)
		if err != nil {
			return pulse.Limits{}, limitFlagError(spec, n,
				"--limit "+spec+": bad value for "+key+": "+err.Error())
		}
		limits.Set(&out, n, v)
	}
	return out, nil
}

func limitNameList() string {
	names := limits.Names()
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = string(n)
	}
	return strings.Join(parts, ", ")
}

func limitFlagError(spec string, n limits.Name, msg string) error {
	details := map[string]any{"flag": "limit", "value": spec}
	if n != "" {
		details["limit"] = string(n)
	}
	return perrors.NewCodedErrorWithDetails(perrors.CLI_INPUT, msg, details)
}

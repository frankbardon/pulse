package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/gosdk"
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

			p, err := pulse.New(pulse.Options{DataDir: dataDir})
			if err != nil {
				return fmt.Errorf("constructing pulse: %w", err)
			}

			bindOnOpen := cmd.Bool("bind-on-open")
			fmt.Fprintf(os.Stderr, "pulse mcp: serving over stdio (data dir: %s, bind-on-open: %v)\n", dataDir, bindOnOpen)

			// Construct a bare go-sdk server and mount the full Pulse surface
			// through the single registration path (the gosdk adapter), then
			// serve over stdio. NewServer panics on a nil Implementation; the
			// literal below is always valid.
			srv := mcpsdk.NewServer(&mcpsdk.Implementation{
				Name:    mcpServerName,
				Version: version,
			}, nil)
			if err := gosdk.Register(srv, p, gosdk.Config{
				Version:       version,
				BindOnInspect: bindOnOpen,
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

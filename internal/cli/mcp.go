package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mcp"
	cli "github.com/urfave/cli/v3"
)

// MCPCommand returns the mcp command leaf. Running it serves the MCP
// protocol over stdio so AI clients (Claude Desktop, Claude Code, etc.)
// can discover and call Pulse tools.
func MCPCommand() *cli.Command {
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

			fmt.Fprintf(os.Stderr, "pulse mcp: serving over stdio (data dir: %s)\n", dataDir)
			if err := mcp.ServeStdio(mcp.New(p)); err != nil {
				return fmt.Errorf("mcp server: %w", err)
			}
			return nil
		},
	}
}

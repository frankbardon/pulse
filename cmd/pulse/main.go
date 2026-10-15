// Package main is the entry point for the pulse CLI binary.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	pcli "github.com/frankbardon/pulse/internal/cli"

	"github.com/frankbardon/pulse"
	cli "github.com/urfave/cli/v3"
)

func main() {
	os.Exit(run(context.Background(), os.Args, os.Stdout, os.Stderr))
}

// run executes the CLI and returns the process exit status: 0 success
// (warnings allowed), 1 a fatal error (in the --json envelope, or printed
// to stderr in text mode), 2 a usage error the flag parser refused.
// pcli.ExitStatus owns the mapping; main only applies it.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	app := buildApp()
	app.Writer, app.ErrWriter = stdout, stderr
	err := app.Run(ctx, args)
	status, printErr := pcli.ExitStatus(err)
	if printErr {
		fmt.Fprintf(stderr, "error: %v\n", err)
	}
	return status
}

func buildApp() *cli.Command {
	return pcli.InstallExitPolicy(&cli.Command{
		Name:    "pulse",
		Usage:   "High-performance tabular data processing engine",
		Version: pulse.Version(),
		Flags: append([]cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output self-describing manifest as JSON"},
			&cli.BoolFlag{Name: "slim", Usage: "Drop prose descriptions from the manifest payload (smaller for size-sensitive clients)"},
			&cli.StringFlag{Name: "intent", Usage: "With --json, scope the manifest to one intent-taxonomy ID (e.g. compare_groups): only its operators and skills"},
		}, pcli.LogFlags()...),
		// --log-level / --log-format are persistent: every leaf accepts
		// them, and Before hands the stderr logger to the leaf via ctx.
		Before: pcli.LogBefore,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("json") {
				return pcli.WriteManifest(cmd.Writer, cmd.Bool("slim"), cmd.String("intent"))
			}
			// Default: print usage.
			cli.ShowAppHelp(cmd)
			return nil
		},
		Commands: []*cli.Command{
			pcli.ImportCommand(),
			pcli.ExportCommand(),
			pcli.ConvertCommand(),
			pcli.CohortCommand(),
			pcli.APICommand(),
			pcli.SkillsCommand(),
			pcli.ExamplesCommand(),
			pcli.ErrorsCommand(),
			pcli.MCPCommand(pulse.Version()),
			pcli.SchemaCommand(),
			pcli.SynthCommand(),
			pcli.ProfileCommand(),
			pcli.ShardCommand(),
			pcli.IndexCommand(),
			pcli.WidenCommand(),
			pcli.DedupCommand(),
			pcli.RecommendCommand(),
			pcli.ExplainCommand(),
			pcli.VersionCommand(),
			pcli.FeaturesCommand(),
			pcli.DocsCommand(),
		},
	})
}

// Package main is the entry point for the pulse CLI binary.
package main

import (
	"context"
	"fmt"
	"os"

	pcli "github.com/frankbardon/pulse/internal/cli"

	"github.com/frankbardon/pulse"
	cli "github.com/urfave/cli/v3"
)

func main() {
	app := buildApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func buildApp() *cli.Command {
	return &cli.Command{
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
	}
}

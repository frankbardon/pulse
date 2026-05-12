// Package main is the entry point for the pulse CLI binary.
package main

import (
	"context"
	"fmt"
	"os"

	pcli "github.com/frankbardon/pulse/internal/cli"

	"github.com/frankbardon/pulse/descriptor"
	cli "github.com/urfave/cli/v3"
)

// version is set by the build system.
var version = "dev"

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
		Version: version,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output self-describing manifest as JSON"},
			&cli.BoolFlag{Name: "slim", Usage: "Drop prose descriptions from the manifest payload (smaller for size-sensitive clients)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("json") {
				manifest := descriptor.BuildManifest()
				if cmd.Bool("slim") {
					manifest = descriptor.SlimManifest(manifest)
				}
				env := descriptor.NewEnvelope(manifest)
				return pcli.WriteJSONPublic(cmd.Writer, env)
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
			pcli.MCPCommand(),
			pcli.SynthCommand(),
			pcli.ProfileCommand(),
		},
	}
}

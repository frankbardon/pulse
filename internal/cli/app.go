package cli

import (
	"context"

	cli "github.com/urfave/cli/v3"

	"github.com/frankbardon/pulse"
)

// App returns the pulse binary's root command: every leaf, every global
// and persistent flag, the exit policy and shell completion installed. The
// binary (cmd/pulse) runs it; documentation tooling (internal/doccoverage)
// walks it, so the tree the book is checked against is the tree the binary
// mounts.
func App() *cli.Command {
	return InstallExitPolicy(EnableCompletion(&cli.Command{
		Name:    "pulse",
		Usage:   "High-performance tabular data processing engine",
		Version: pulse.Version(),
		Flags: append([]cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output self-describing manifest as JSON"},
			&cli.BoolFlag{Name: "slim", Usage: "Drop prose descriptions from the manifest payload (smaller for size-sensitive clients)"},
			&cli.StringFlag{Name: "intent", Usage: "With --json, scope the manifest to one intent-taxonomy ID (e.g. compare_groups): only its operators and skills"},
		}, append(append(append(LogFlags(), WorkerFlags()...), LimitFlags()...), MemoryLimitFlags()...)...),
		// --log-level / --log-format, --shard-workers /
		// --decode-workers, --limit and --memory-limit are persistent:
		// every leaf accepts them, and Before hands the stderr logger, the
		// worker caps and the limits layer to the leaf via ctx, and sets
		// the process soft memory limit before any leaf runs.
		Before: GlobalBefore,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("json") {
				return WriteManifest(cmd.Writer, cmd.Bool("slim"), cmd.String("intent"))
			}
			// Default: print usage.
			cli.ShowAppHelp(cmd)
			return nil
		},
		Commands: []*cli.Command{
			ImportCommand(),
			ExportCommand(),
			ConvertCommand(),
			CohortCommand(),
			CohortsCommand(),
			APICommand(),
			SkillsCommand(),
			ExamplesCommand(),
			ErrorsCommand(),
			MCPCommand(pulse.Version()),
			SchemaCommand(),
			SynthCommand(),
			ProfileCommand(),
			ShardCommand(),
			IndexCommand(),
			WidenCommand(),
			DedupCommand(),
			RakeCommand(),
			RecommendCommand(),
			ExplainCommand(),
			VersionCommand(),
			FeaturesCommand(),
			DocsCommand(),
		},
	}))
}

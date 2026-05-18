package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	cli "github.com/urfave/cli/v3"
)

// ShardCommand returns the `pulse shard` command group covering
// archive create / add / remove / list / compact / verify / extract.
// Shard mutations use the atomic temp+rename pattern so partial writes
// never appear at the canonical archive path.
func ShardCommand() *cli.Command {
	return &cli.Command{
		Name:  "shard",
		Usage: "Manage Pulse shard archives (create, add, remove, list, compact, verify, extract)",
		Commands: []*cli.Command{
			shardCreateCmd(),
			shardAddCmd(),
			shardRemoveCmd(),
			shardListCmd(),
			shardCompactCmd(),
			shardVerifyCmd(),
			shardExtractCmd(),
		},
	}
}

func shardCreateCmd() *cli.Command {
	return &cli.Command{
		Name:      "create",
		Usage:     "Create a new shard archive from one or more single-file .pulse shards",
		ArgsUsage: "ARCHIVE",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{Name: "include", Aliases: []string{"i"}, Usage: "Single-file .pulse shard to include (repeatable)"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			jsonOut := cmd.Bool("json")
			if args.Len() < 1 {
				return cliError(cmd, jsonOut, "CLI_INPUT", "usage: pulse shard create ARCHIVE --include SHARD [--include SHARD ...]")
			}
			archive := args.First()
			includes := cmd.StringSlice("include")
			if len(includes) == 0 {
				return cliError(cmd, jsonOut, "CLI_INPUT", "pulse shard create requires at least one --include shard")
			}

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			if err := p.CreateShardArchive(ctx, archive, includes); err != nil {
				return cliError(cmd, jsonOut, "SHARD_CREATE_ERROR", err.Error())
			}
			shards, err := p.ListShards(ctx, archive)
			if err != nil {
				return cliError(cmd, jsonOut, "SHARD_LIST_ERROR", err.Error())
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, map[string]any{
					"archive": archive,
					"shards":  shards,
				})
			}
			writeText(cmd.Writer, "Created %s with %d shard(s)\n", archive, len(shards))
			return nil
		},
	}
}

func shardAddCmd() *cli.Command {
	return &cli.Command{
		Name:      "add",
		Usage:     "Append a shard to an existing archive",
		ArgsUsage: "ARCHIVE SHARD",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			jsonOut := cmd.Bool("json")
			if args.Len() < 2 {
				return cliError(cmd, jsonOut, "CLI_INPUT", "usage: pulse shard add ARCHIVE SHARD")
			}
			archive := args.Get(0)
			shard := args.Get(1)

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			if err := p.AddShard(ctx, archive, shard); err != nil {
				return cliError(cmd, jsonOut, "SHARD_ADD_ERROR", err.Error())
			}
			shards, err := p.ListShards(ctx, archive)
			if err != nil {
				return cliError(cmd, jsonOut, "SHARD_LIST_ERROR", err.Error())
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, map[string]any{
					"archive": archive,
					"added":   shard,
					"shards":  shards,
				})
			}
			writeText(cmd.Writer, "Added %s to %s (now %d shard(s))\n", shard, archive, len(shards))
			return nil
		},
	}
}

func shardRemoveCmd() *cli.Command {
	return &cli.Command{
		Name:      "remove",
		Usage:     "Remove a shard from an archive by basename",
		ArgsUsage: "ARCHIVE BASENAME",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			jsonOut := cmd.Bool("json")
			if args.Len() < 2 {
				return cliError(cmd, jsonOut, "CLI_INPUT", "usage: pulse shard remove ARCHIVE BASENAME")
			}
			archive := args.Get(0)
			basename := args.Get(1)

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			if err := p.RemoveShard(ctx, archive, basename); err != nil {
				return cliError(cmd, jsonOut, "SHARD_REMOVE_ERROR", err.Error())
			}
			shards, err := p.ListShards(ctx, archive)
			if err != nil {
				return cliError(cmd, jsonOut, "SHARD_LIST_ERROR", err.Error())
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, map[string]any{
					"archive": archive,
					"removed": basename,
					"shards":  shards,
				})
			}
			writeText(cmd.Writer, "Removed %s from %s (now %d shard(s))\n", basename, archive, len(shards))
			return nil
		},
	}
}

func shardListCmd() *cli.Command {
	return &cli.Command{
		Name:      "list",
		Usage:     "List shards inside an archive",
		ArgsUsage: "ARCHIVE",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			jsonOut := cmd.Bool("json")
			if args.Len() < 1 {
				return cliError(cmd, jsonOut, "CLI_INPUT", "usage: pulse shard list ARCHIVE")
			}
			archive := args.First()

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			shards, err := p.ListShards(ctx, archive)
			if err != nil {
				return cliError(cmd, jsonOut, "SHARD_LIST_ERROR", err.Error())
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, map[string]any{
					"archive": archive,
					"shards":  shards,
				})
			}
			if len(shards) == 0 {
				writeText(cmd.Writer, "(archive has no shards)\n")
				return nil
			}
			writeText(cmd.Writer, "%-40s  %12s\n", "BASENAME", "RECORDS")
			var total int64
			for _, sh := range shards {
				writeText(cmd.Writer, "%-40s  %12d\n", sh.Filename, sh.RecordCount)
				total += sh.RecordCount
			}
			writeText(cmd.Writer, "%-40s  %12d\n", "(total)", total)
			return nil
		},
	}
}

func shardCompactCmd() *cli.Command {
	return &cli.Command{
		Name:      "compact",
		Usage:     "Rewrite a shard archive to reclaim orphan bytes and refresh canonical metadata",
		ArgsUsage: "ARCHIVE",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			jsonOut := cmd.Bool("json")
			if args.Len() < 1 {
				return cliError(cmd, jsonOut, "CLI_INPUT", "usage: pulse shard compact ARCHIVE")
			}
			archive := args.First()

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			if err := p.CompactShardArchive(ctx, archive); err != nil {
				return cliError(cmd, jsonOut, "SHARD_COMPACT_ERROR", err.Error())
			}
			shards, err := p.ListShards(ctx, archive)
			if err != nil {
				return cliError(cmd, jsonOut, "SHARD_LIST_ERROR", err.Error())
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, map[string]any{
					"archive": archive,
					"shards":  shards,
				})
			}
			writeText(cmd.Writer, "Compacted %s (now %d shard(s))\n", archive, len(shards))
			return nil
		},
	}
}

func shardVerifyCmd() *cli.Command {
	return &cli.Command{
		Name:      "verify",
		Usage:     "Re-validate every shard's header + cohesion against the canonical schema",
		ArgsUsage: "ARCHIVE",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			jsonOut := cmd.Bool("json")
			if args.Len() < 1 {
				return cliError(cmd, jsonOut, "CLI_INPUT", "usage: pulse shard verify ARCHIVE")
			}
			archive := args.First()

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			result, err := p.VerifyShardArchive(ctx, archive)
			if err != nil {
				return cliError(cmd, jsonOut, "SHARD_VERIFY_ERROR", err.Error())
			}

			if jsonOut {
				// Re-shape into the envelope's structured {errors, warnings}
				// list so it merges cleanly with the standard envelope.
				env := newEnvelopeShardVerify(archive, result)
				return writeJSON(cmd.Writer, env)
			}

			writeText(cmd.Writer, "Verified %s: %d error(s), %d warning(s)\n",
				archive, len(result.Errors), len(result.Warnings))
			for _, e := range result.Errors {
				writeText(cmd.Writer, "  ERROR  [%s] %s\n", e.Code, e.Message)
			}
			for _, w := range result.Warnings {
				writeText(cmd.Writer, "  WARN   [%s] %s\n", w.Code, w.Message)
			}
			if len(result.Errors) > 0 {
				// Non-zero exit on error. The cli/v3 runtime treats any
				// non-nil error return as a non-zero exit code.
				return fmt.Errorf("verify found %d error(s) in %s", len(result.Errors), archive)
			}
			return nil
		},
	}
}

func shardExtractCmd() *cli.Command {
	return &cli.Command{
		Name:      "extract",
		Usage:     "Extract a shard's standalone .pulse bytes to stdout",
		ArgsUsage: "ARCHIVE BASENAME",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 2 {
				return fmt.Errorf("usage: pulse shard extract ARCHIVE BASENAME")
			}
			archive := args.Get(0)
			basename := args.Get(1)

			p, err := newPulse()
			if err != nil {
				return err
			}
			rc, err := p.ExtractShard(ctx, archive, basename)
			if err != nil {
				return err
			}
			defer rc.Close()
			if _, err := io.Copy(cmd.Writer, rc); err != nil {
				return err
			}
			return nil
		},
	}
}

// newEnvelopeShardVerify projects a VerifyResult into the standard
// descriptor.Envelope shape: archive path under data, structured
// errors/warnings flattened into the envelope's slots. The CLI never
// builds JSON via fmt.Sprintf — descriptor.NewEnvelope wraps the
// payload deterministically.
func newEnvelopeShardVerify(archive string, result *pulse.VerifyResult) *descriptor.Envelope {
	env := descriptor.NewEnvelope(map[string]any{
		"archive":   archive,
		"verified":  len(result.Errors) == 0,
		"error_n":   len(result.Errors),
		"warning_n": len(result.Warnings),
	})
	for _, e := range result.Errors {
		env.AddError(string(e.Code), e.Message, e.Details)
	}
	for _, w := range result.Warnings {
		env.AddWarning(w.Code, w.Message, w.Details)
	}
	return env
}

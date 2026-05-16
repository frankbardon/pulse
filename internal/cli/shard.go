package cli

import (
	"context"
	"fmt"
	"io"

	cli "github.com/urfave/cli/v3"
)

// ShardCommand returns the `pulse shard` command group covering
// archive create / add / remove / list / extract. The S7 phase
// implements the read+admin surface; S8 will add `compact` and
// `verify` leaves alongside these. Shard mutations use the atomic
// temp+rename pattern so partial writes never appear at the canonical
// archive path.
func ShardCommand() *cli.Command {
	return &cli.Command{
		Name:  "shard",
		Usage: "Manage Pulse shard archives (create, add, remove, list, extract)",
		Commands: []*cli.Command{
			shardCreateCmd(),
			shardAddCmd(),
			shardRemoveCmd(),
			shardListCmd(),
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

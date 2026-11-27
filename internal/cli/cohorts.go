package cli

import (
	"context"
	"os"
	"strings"

	"github.com/frankbardon/pulse"
	cli "github.com/urfave/cli/v3"
)

// CohortsCommand is `pulse cohorts [DIR]`: the cohorts under the data
// directory (Pulse.ListCohorts), header and schema only. The leaf only
// formats the library result.
func CohortsCommand() *cli.Command {
	return &cli.Command{
		Name:      "cohorts",
		Usage:     "List every .pulse cohort under the data directory (or under DIR, relative to it): path, record count, fields, shard-archive and managed-import flags; header and schema only",
		ArgsUsage: "[DIR]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			p, err := pulse.New(withGlobals(ctx, pulse.Options{DataDir: os.Getenv("PULSE_DATA_DIR")}))
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}
			entries, err := p.ListCohorts(ctx, pulse.ListCohortsOptions{Dir: cmd.Args().First()})
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, entries)
			}
			if len(entries) == 0 {
				writeText(cmd.Writer, "No cohorts.\n")
				return nil
			}
			for _, e := range entries {
				writeText(cmd.Writer, "%s\n", cohortListLine(e))
			}
			return nil
		},
	}
}

// cohortListLine renders one entry as one text line: path, record
// count, flags, then the field list (or the read error).
func cohortListLine(e pulse.CohortListEntry) string {
	var b strings.Builder
	b.WriteString(e.Path)
	if e.Error != nil {
		b.WriteString("  error " + e.Error.Code + ": " + e.Error.Message)
		return b.String()
	}
	writeText(&b, "  %d records", e.RecordCount)
	if e.ShardArchive {
		writeText(&b, "  archive (%d shards)", e.ShardCount)
	}
	if e.Managed {
		b.WriteString("  managed import " + e.Handle)
	}
	fields := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		fields[i] = f.Name + ":" + f.Type
		if f.Nullable {
			fields[i] += "?"
		}
	}
	b.WriteString("  fields " + strings.Join(fields, ", "))
	for _, w := range e.Warnings {
		b.WriteString("  warning " + w.Code)
	}
	return b.String()
}

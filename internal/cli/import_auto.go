package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/imports"
	cli "github.com/urfave/cli/v3"
)

// importAutoCmd wraps pulse.ImportFile as the auto-detect CLI leaf.
// Source format is inferred from the extension when --format is unset.
// On success the managed handle is written to PULSE_DATA_DIR/imports/.
func importAutoCmd() *cli.Command {
	return &cli.Command{
		Name:      "auto",
		Usage:     "Auto-detect a source format and import into the managed pool",
		ArgsUsage: "SOURCE",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Aliases: []string{"f"}, Usage: "Format override (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, pulse)"},
			&cli.StringFlag{Name: "handle", Usage: "Managed handle name (defaults to source basename)"},
			&cli.StringFlag{Name: "ttl", Value: "7d", Usage: "Lifetime in the managed pool: Go duration (24h, 30m), day form (7d), or \"pin\""},
			&cli.StringFlag{Name: "sheet", Usage: "Excel sheet name (ignored for non-Excel)"},
			&cli.BoolFlag{Name: "overwrite", Usage: "Replace an existing managed handle"},
			&cli.BoolFlag{Name: "json", Usage: "Emit the JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 1 {
				return fmt.Errorf("source file argument required")
			}
			source := args.First()
			jsonOut := cmd.Bool("json")

			ttl, err := imports.ParseTTL(cmd.String("ttl"))
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			p, err := pulse.New(pulse.Options{DataDir: os.Getenv("PULSE_DATA_DIR")})
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			res, err := p.ImportFile(ctx, pulse.ImportSpec{
				SourcePath: source,
				Format:     cmd.String("format"),
				Handle:     cmd.String("handle"),
				TTL:        ttl,
				Sheet:      cmd.String("sheet"),
				Overwrite:  cmd.Bool("overwrite"),
			})
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "IMPORT_ERROR", err.Error())
				}
				return err
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, res)
			}
			if res.Managed {
				writeText(cmd.Writer, "Imported %d rows into managed handle %q at %s\n", res.RowsImported, res.Handle, res.Path)
				if res.ExpiresAt != nil {
					writeText(cmd.Writer, "Expires: %s\n", res.ExpiresAt.Format("2006-01-02 15:04:05 MST"))
				} else {
					writeText(cmd.Writer, "Pinned (no expiry)\n")
				}
			} else {
				writeText(cmd.Writer, "Passthrough: %s (format=%s, no managed sidecar)\n", res.Path, res.Format)
			}
			return nil
		},
	}
}

// importsListCmd enumerates the managed-imports pool. Expired and
// pinned entries are flagged in the output so the operator can decide
// to drop them.
func importsListCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List managed-import handles and their TTL status",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Emit the JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			p, err := pulse.New(pulse.Options{DataDir: os.Getenv("PULSE_DATA_DIR")})
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}
			entries, err := p.Imports(ctx)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, entries)
			}
			if len(entries) == 0 {
				writeText(cmd.Writer, "No managed imports.\n")
				return nil
			}
			enc := json.NewEncoder(cmd.Writer)
			enc.SetIndent("", "  ")
			return enc.Encode(entries)
		},
	}
}

// importDropCmd removes one managed handle.
func importDropCmd() *cli.Command {
	return &cli.Command{
		Name:      "drop",
		Usage:     "Remove a managed-import handle from the pool",
		ArgsUsage: "HANDLE",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Emit the JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 1 {
				return fmt.Errorf("handle argument required")
			}
			handle := args.First()
			jsonOut := cmd.Bool("json")

			p, err := pulse.New(pulse.Options{DataDir: os.Getenv("PULSE_DATA_DIR")})
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}
			if err := p.Drop(ctx, handle); err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "DROP_ERROR", err.Error())
				}
				return err
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, map[string]any{"handle": handle, "dropped": true})
			}
			writeText(cmd.Writer, "Dropped %q\n", handle)
			return nil
		},
	}
}

package cli

import (
	"context"
	"fmt"

	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// exportFlags are the common flags for all export format subcommands.
var exportFlags = []cli.Flag{
	&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse file path", Required: true},
	&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output file path", Required: true},
	&cli.StringSliceFlag{Name: "labels", Usage: "Categorical label binding: field=table[:replace|augment]. Repeatable. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration."},
	&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
}

// ExportCommand returns the export command group.
func ExportCommand() *cli.Command {
	return &cli.Command{
		Name:  "export",
		Usage: "Export .pulse file to tabular format",
		Commands: []*cli.Command{
			exportFormatCmd("csv"),
			exportFormatCmd("tsv"),
			exportFormatCmd("ndjson"),
			exportFormatCmd("jsonarray"),
			exportFormatCmd("parquet"),
			exportFormatCmd("arrow"),
			exportFormatCmd("excel"),
			exportPredictCmd(),
		},
	}
}

func exportFormatCmd(format string) *cli.Command {
	return &cli.Command{
		Name:  format,
		Usage: fmt.Sprintf("Export .pulse to %s format", format),
		Flags: exportFlags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runExport(ctx, cmd, format)
		},
	}
}

func runExport(ctx context.Context, cmd *cli.Command, format string) error {
	input := cmd.String("input")
	output := cmd.String("output")
	labelArgs := cmd.StringSlice("labels")
	jsonOut := cmd.Bool("json")

	fs := afero.NewOsFs()

	writer, err := newWriterForFormat(format, fs, output)
	if err != nil {
		if jsonOut {
			return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
		}
		return err
	}

	job := pio.NewExportJob(input, writer)
	job.FS = fs

	if len(labelArgs) > 0 {
		bindings, perr := parseLabelBindings(labelArgs)
		if perr != nil {
			if jsonOut {
				return writeErrorEnvelope(cmd.Writer, "CLI_INPUT", perr.Error())
			}
			return perr
		}
		job.Labels = bindings
		// Route through the pulse facade so the resolver is built
		// from the Service's registered LabelTables (the env-var
		// loader runs at newPulse).
		p, perr := newPulse()
		if perr != nil {
			if jsonOut {
				return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", perr.Error())
			}
			return perr
		}
		report, perr := p.Export(ctx, job)
		if perr != nil {
			if jsonOut {
				return writeErrorEnvelope(cmd.Writer, "EXPORT_ERROR", perr.Error())
			}
			return perr
		}
		if err := writer.Close(); err != nil {
			if jsonOut {
				return writeErrorEnvelope(cmd.Writer, "EXPORT_ERROR", err.Error())
			}
			return err
		}
		if jsonOut {
			return writeEnvelope(cmd.Writer, report)
		}
		writeText(cmd.Writer, "Exported %d rows to %s\n", report.RowsExported, output)
		if len(report.RowErrors) > 0 {
			writeText(cmd.Writer, "Warnings: %d row errors\n", len(report.RowErrors))
		}
		return nil
	}

	report, err := job.Run(ctx)
	if err != nil {
		if jsonOut {
			return writeErrorEnvelope(cmd.Writer, "EXPORT_ERROR", err.Error())
		}
		return err
	}

	if err := writer.Close(); err != nil {
		if jsonOut {
			return writeErrorEnvelope(cmd.Writer, "EXPORT_ERROR", err.Error())
		}
		return err
	}

	if jsonOut {
		return writeEnvelope(cmd.Writer, report)
	}

	writeText(cmd.Writer, "Exported %d rows to %s\n", report.RowsExported, output)
	if len(report.RowErrors) > 0 {
		writeText(cmd.Writer, "Warnings: %d row errors\n", len(report.RowErrors))
	}
	return nil
}

func exportPredictCmd() *cli.Command {
	return &cli.Command{
		Name:  "predict",
		Usage: "Validate an export without writing output",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse file path", Required: true},
			&cli.StringFlag{Name: "format", Aliases: []string{"f"}, Usage: "Output format (csv, tsv, ndjson, jsonarray, parquet, arrow, excel)"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			jsonOut := cmd.Bool("json")

			fs := afero.NewOsFs()

			// ExportJob.Predict only needs the source file; target writer isn't used.
			job := &pio.ExportJob{
				Source: input,
				FS:     fs,
			}

			report, err := job.Predict(ctx)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "PREDICT_ERROR", err.Error())
				}
				return err
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, report)
			}

			writeText(cmd.Writer, "Schema: %d fields\n", len(report.Schema.Fields))
			writeText(cmd.Writer, "Estimated rows: %d\n", report.EstimatedRows)
			return nil
		},
	}
}

package cli

import (
	"context"
	"fmt"

	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/spss"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// exportFlags are the common flags for all export format subcommands.
var exportFlags = []cli.Flag{
	&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse file path", Required: true},
	&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output file path", Required: true},
	&cli.StringSliceFlag{Name: "include", Usage: "Export only the named source-schema field. Repeatable; omit to export every field. Output order always follows the source schema, not flag order."},
	&cli.StringSliceFlag{Name: "labels", Usage: "Categorical label binding: field=table[:replace|augment]. Repeatable. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration."},
	&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
}

// The `.sav` writer's four flags, each mapping onto one
// spss.WriterOptions field. The usage strings are constants so the
// wording cannot drift between the export leaf and the convert leaf,
// which is the other place a `.sav` can be the target.
const (
	ignoreSidecarFlagUsage = "Do not read the SPSS metadata sidecar beside the source cohort; synthesise the .sav dictionary from the .pulse schema alone. Suppresses the READ, not merely the staleness verdict — an unreadable sidecar cannot block the export either. Cannot round-trip a cohort whose derived set_* column is still present (see the docs)."

	uncompressedFlagUsage = "Write the .sav data section as flat 8-byte elements instead of SPSS's bytecode compression. Both encodings are losslessly equivalent; bytecode is the default because it is what SPSS's own SAVE writes. Does not select ZSAV — that is not emitted."

	writeCharsetFlagUsage = "Character encoding the emitted .sav is written in, and declared as, e.g. windows-1252 or utf-8. Default: the charset the SOURCE declared, or UTF-8 for a cohort with no SPSS provenance. Set it when the cohort now holds text the source's codepage cannot express."

	sanitiseNamesFlagUsage = "Rewrite cohort field names that cannot be SPSS variable names (a space, bracket, hyphen or leading digit) instead of refusing the export. Every rename is reported as a PULSE_SPSS_NAME_SANITISED warning. Only affects a synthesised dictionary — names from a sidecar came from SPSS and are already legal."
)

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
			exportSPSSCmd(),
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

// withExportFlags returns the common export flags plus the format-specific
// extras, without mutating the shared slice.
func withExportFlags(extra ...cli.Flag) []cli.Flag {
	flags := make([]cli.Flag, 0, len(exportFlags)+len(extra))
	flags = append(flags, exportFlags...)
	return append(flags, extra...)
}

// exportSPSSCmd is `pulse export spss`. It carries the four write-side
// knobs for the same reason `pulse import spss` carries --charset: the
// format asks questions the cohort cannot always answer for itself.
func exportSPSSCmd() *cli.Command {
	return &cli.Command{
		Name:  "spss",
		Usage: "Export .pulse to SPSS .sav format",
		Flags: withExportFlags(
			&cli.BoolFlag{Name: "ignore-sidecar", Usage: ignoreSidecarFlagUsage},
			&cli.BoolFlag{Name: "uncompressed", Usage: uncompressedFlagUsage},
			&cli.StringFlag{Name: "charset", Usage: writeCharsetFlagUsage},
			&cli.BoolFlag{Name: "sanitise-names", Usage: sanitiseNamesFlagUsage},
		),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runExport(ctx, cmd, "spss")
		},
	}
}

// writerOptionsFrom lifts the per-format writer knobs off whichever leaf
// is running. A leaf that does not declare a flag reads it as the zero
// value, which is the same as not setting the option, so one helper
// serves every leaf — mirroring readerOptionsFrom on the import side.
func writerOptionsFrom(cmd *cli.Command) writerOptions {
	return writerOptions{
		SPSS: spss.WriterOptions{
			IgnoreSidecar: cmd.Bool("ignore-sidecar"),
			Uncompressed:  cmd.Bool("uncompressed"),
			Charset:       cmd.String("charset"),
			SanitiseNames: cmd.Bool("sanitise-names"),
		},
	}
}

func runExport(ctx context.Context, cmd *cli.Command, format string) error {
	input := cmd.String("input")
	output := cmd.String("output")
	labelArgs := cmd.StringSlice("labels")
	includes := cmd.StringSlice("include")
	jsonOut := cmd.Bool("json")

	fs := afero.NewOsFs()

	writer, err := newWriterForFormat(format, fs, output, writerOptionsFrom(cmd))
	if err != nil {
		if jsonOut {
			return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
		}
		return err
	}

	job := pio.NewExportJob(input, writer)
	job.FS = fs
	job.Includes = includes

	if len(labelArgs) > 0 {
		bindings, perr := parseLabelBindings(labelArgs)
		if perr != nil {
			if jsonOut {
				return writeCodedErrorEnvelope(cmd.Writer, "CLI_INPUT", perr)
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
				return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", perr)
			}
			return perr
		}
		report, perr := p.Export(ctx, job)
		if perr != nil {
			if jsonOut {
				return writeCodedErrorEnvelope(cmd.Writer, "EXPORT_ERROR", perr)
			}
			return perr
		}
		if err := writer.Close(); err != nil {
			if jsonOut {
				return writeCodedErrorEnvelope(cmd.Writer, "EXPORT_ERROR", err)
			}
			return err
		}
		return finishExport(cmd, writer, report, output, jsonOut)
	}

	report, err := job.Run(ctx)
	if err != nil {
		if jsonOut {
			return writeCodedErrorEnvelope(cmd.Writer, "EXPORT_ERROR", err)
		}
		return err
	}

	if err := writer.Close(); err != nil {
		if jsonOut {
			return writeCodedErrorEnvelope(cmd.Writer, "EXPORT_ERROR", err)
		}
		return err
	}

	return finishExport(cmd, writer, report, output, jsonOut)
}

// finishExport emits the export result, lifting the target adapter's
// coded diagnostics onto the envelope's `warnings` array (or onto the
// text output) the way the import leaves already lift a source
// adapter's.
//
// It re-reads the warnings AFTER Close rather than trusting
// report.TargetWarnings, and that is load-bearing: a writer that
// buffers and encodes at Close — which the `.sav` writer does on the
// convert-style row path — has raised none of its diagnostics by the
// time the job builds its report. Reading again after Close is the only
// point at which the set is complete. pio.TargetWarningEmitter is a
// pure accessor, so asking twice cannot double the set.
func finishExport(cmd *cli.Command, writer pio.Writer, report *pio.ExportReport, output string, jsonOut bool) error {
	warnings := report.TargetWarnings
	if twe, ok := writer.(pio.TargetWarningEmitter); ok {
		warnings = twe.Warnings()
	}
	warnings = append(append([]*perrors.CodedError(nil), warnings...), report.OverlayWarnings...)

	if jsonOut {
		return writeEnvelopeWithWarnings(cmd.Writer, report, warnings)
	}
	writeText(cmd.Writer, "Exported %d rows to %s\n", report.RowsExported, output)
	if len(report.RowErrors) > 0 {
		writeText(cmd.Writer, "Warnings: %d row errors\n", len(report.RowErrors))
	}
	writeSourceWarnings(cmd.Writer, warnings)
	return nil
}

func exportPredictCmd() *cli.Command {
	return &cli.Command{
		Name:  "predict",
		Usage: "Validate an export without writing output",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse file path", Required: true},
			&cli.StringFlag{Name: "format", Aliases: []string{"f"}, Usage: "Output format (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss)"},
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
					return writeCodedErrorEnvelope(cmd.Writer, "PREDICT_ERROR", err)
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

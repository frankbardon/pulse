package cli

import (
	"context"
	"fmt"

	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// ConvertCommand returns the convert command.
func ConvertCommand() *cli.Command {
	return &cli.Command{
		Name:      "convert",
		Usage:     "Convert between tabular formats (auto-detects from extensions)",
		ArgsUsage: "INPUT OUTPUT",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "from", Usage: "Source format override"},
			&cli.StringFlag{Name: "to", Usage: "Target format override"},
			&cli.StringFlag{Name: "schema", Usage: "Schema JSON file path"},
			&cli.StringFlag{Name: "keep-pulse", Usage: "Also write intermediate .pulse file at this path"},
			&cli.IntFlag{Name: "sample-rows", Value: 500, Usage: "Rows to sample for schema inference (min 50)"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Commands: []*cli.Command{
			convertPredictCmd(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 2 {
				return fmt.Errorf("usage: pulse convert INPUT OUTPUT")
			}
			input := args.Get(0)
			output := args.Get(1)
			fromFmt := cmd.String("from")
			toFmt := cmd.String("to")
			schemaPath := cmd.String("schema")
			keepPulse := cmd.String("keep-pulse")
			sampleRows := int(cmd.Int("sample-rows"))
			jsonOut := cmd.Bool("json")

			if fromFmt == "" {
				fromFmt = formatFromExt(input)
			}
			if toFmt == "" {
				toFmt = formatFromExt(output)
			}
			if fromFmt == "" {
				msg := fmt.Sprintf("cannot detect source format from %q; use --from", input)
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", msg)
				}
				return fmt.Errorf("%s", msg)
			}
			if toFmt == "" {
				msg := fmt.Sprintf("cannot detect target format from %q; use --to", output)
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", msg)
				}
				return fmt.Errorf("%s", msg)
			}

			fs := afero.NewOsFs()

			reader, err := newReaderForFormat(fromFmt, fs, input, "")
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			writer, err := newWriterForFormat(toFmt, fs, output)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			job := pio.NewConvertJob(reader, writer)
			job.FS = fs
			job.SampleRows = sampleRows
			job.KeepPulseAt = keepPulse

			if schemaPath != "" {
				schema, loadErr := loadSchemaFromFile(fs, schemaPath)
				if loadErr != nil {
					if jsonOut {
						return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", loadErr.Error())
					}
					return loadErr
				}
				job.Schema = schema
			}

			report, err := job.Run(ctx)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CONVERT_ERROR", err.Error())
				}
				return err
			}

			if err := writer.Close(); err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CONVERT_ERROR", err.Error())
				}
				return err
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, report)
			}

			writeText(cmd.Writer, "Converted %d rows: %s -> %s\n", report.RowsConverted, input, output)
			if len(report.RowErrors) > 0 {
				writeText(cmd.Writer, "Warnings: %d row errors\n", len(report.RowErrors))
			}
			return nil
		},
	}
}

func convertPredictCmd() *cli.Command {
	return &cli.Command{
		Name:      "predict",
		Usage:     "Validate a conversion without writing output",
		ArgsUsage: "INPUT OUTPUT",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "from", Usage: "Source format override"},
			&cli.StringFlag{Name: "to", Usage: "Target format override"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
			&cli.IntFlag{Name: "sample-rows", Value: 500, Usage: "Rows to sample (min 50)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 2 {
				return fmt.Errorf("usage: pulse convert predict INPUT OUTPUT")
			}
			input := args.Get(0)
			output := args.Get(1)
			fromFmt := cmd.String("from")
			toFmt := cmd.String("to")
			jsonOut := cmd.Bool("json")
			sampleRows := int(cmd.Int("sample-rows"))

			if fromFmt == "" {
				fromFmt = formatFromExt(input)
			}
			if toFmt == "" {
				toFmt = formatFromExt(output)
			}
			if fromFmt == "" {
				return fmt.Errorf("cannot detect source format from %q; use --from", input)
			}
			if toFmt == "" {
				return fmt.Errorf("cannot detect target format from %q; use --to", output)
			}

			fs := afero.NewOsFs()

			reader, err := newReaderForFormat(fromFmt, fs, input, "")
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			writer, err := newWriterForFormat(toFmt, fs, output)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			job := pio.NewConvertJob(reader, writer)
			job.FS = fs
			job.SampleRows = sampleRows

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

package cli

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	pformat "github.com/frankbardon/pulse/io/format"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// charsetFlagUsage is the one-line help for --charset. It is shared by every
// leaf that can be handed a `.sav`, so the wording cannot drift between them.
const charsetFlagUsage = "Character encoding override for SPSS .sav input (e.g. windows-1252, latin1, utf-8)"

// spssMissingFlagUsage is the one-line help for --spss-missing, shared by
// every leaf a `.sav` can arrive through so the wording cannot drift.
const spssMissingFlagUsage = "How SPSS numeric user-missing values are represented: auto (default — null plus a <var>_missing sibling carrying the reason) or null (plain null, reason not preserved)"

// importFlags are the common flags for all import format subcommands.
var importFlags = []cli.Flag{
	&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input file path", Required: true},
	&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output .pulse file path", Required: true},
	&cli.StringFlag{Name: "schema", Usage: "Schema JSON file path"},
	&cli.IntFlag{Name: "sample-rows", Value: 500, Usage: "Rows to sample for schema inference (min 50)"},
	&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
}

// ImportCommand returns the import command group.
func ImportCommand() *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "Import tabular data into .pulse format",
		Commands: []*cli.Command{
			importFormatCmd("csv"),
			importFormatCmd("tsv"),
			importFormatCmd("ndjson"),
			importFormatCmd("jsonarray"),
			importFormatCmd("parquet"),
			importFormatCmd("arrow"),
			importSPSSCmd(),
			importExcelCmd(),
			importPredictCmd(),
			importSchemaTemplateCmd(),
			importAutoCmd(),
			importsListCmd(),
			importDropCmd(),
		},
	}
}

func importFormatCmd(format string) *cli.Command {
	return &cli.Command{
		Name:  format,
		Usage: fmt.Sprintf("Import %s file into .pulse format", format),
		Flags: importFlags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runImport(ctx, cmd, format)
		},
	}
}

// withImportFlags returns the common import flags plus the format-specific
// extras, without mutating the shared slice.
func withImportFlags(extra ...cli.Flag) []cli.Flag {
	flags := make([]cli.Flag, 0, len(importFlags)+len(extra))
	flags = append(flags, importFlags...)
	return append(flags, extra...)
}

func importExcelCmd() *cli.Command {
	return &cli.Command{
		Name:  "excel",
		Usage: "Import Excel file into .pulse format",
		Flags: withImportFlags(&cli.StringFlag{Name: "sheet", Usage: "Excel sheet name"}),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runImport(ctx, cmd, "excel")
		},
	}
}

// importSPSSCmd is `pulse import spss`. It carries --charset for the same
// reason the excel leaf carries --sheet: the format has one question the
// file cannot always answer for itself.
//
// Without it a legacy `.sav` that declares no encoding at all — no record
// 7/20, no record 7/3 character code — and carries any 8-bit byte fails
// PULSE_SPSS_CHARSET_INVALID with no recourse from the CLI, because
// spss.WithCharset was reachable only from the library. That is the gap this
// flag closes.
func importSPSSCmd() *cli.Command {
	return &cli.Command{
		Name:  "spss",
		Usage: "Import SPSS .sav / .zsav file into .pulse format",
		Flags: withImportFlags(
			&cli.StringFlag{Name: "charset", Usage: charsetFlagUsage},
			&cli.StringFlag{Name: "spss-missing", Value: "auto", Usage: spssMissingFlagUsage},
		),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runImport(ctx, cmd, "spss")
		},
	}
}

func runImport(ctx context.Context, cmd *cli.Command, format string) error {
	input := cmd.String("input")
	output := cmd.String("output")
	schemaPath := cmd.String("schema")
	sampleRows := int(cmd.Int("sample-rows"))
	jsonOut := cmd.Bool("json")

	fs := afero.NewOsFs()

	reader, err := makeImportReader(format, fs, input, readerOptionsFrom(cmd))
	if err != nil {
		if jsonOut {
			return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
		}
		return err
	}

	job := pio.NewImportJob(reader, output)
	job.FS = fs
	job.SampleRows = sampleRows

	if schemaPath != "" {
		schema, err := loadSchemaFromFile(fs, schemaPath)
		if err != nil {
			if jsonOut {
				return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
			}
			return err
		}
		job.Schema = schema
	}

	report, err := job.Run(ctx)
	if err != nil {
		if jsonOut {
			return writeCodedErrorEnvelope(cmd.Writer, "IMPORT_ERROR", err)
		}
		return err
	}

	if jsonOut {
		return writeEnvelopeWithWarnings(cmd.Writer, report, report.SourceWarnings)
	}

	writeText(cmd.Writer, "Imported %d rows to %s\n", report.RowsImported, output)
	if len(report.RowErrors) > 0 {
		writeText(cmd.Writer, "Warnings: %d row errors\n", len(report.RowErrors))
	}
	writeSourceWarnings(cmd.Writer, report.SourceWarnings)
	if len(report.PromotedFields) > 0 {
		writeText(cmd.Writer, "%s: fields promoted to nullable (null found past the inference sample): %s\n",
			errors.PULSE_IMPORT_NULL_PROMOTED, strings.Join(report.PromotedFields, ", "))
	}
	return nil
}

func importPredictCmd() *cli.Command {
	return &cli.Command{
		Name:  "predict",
		Usage: "Validate an import without writing output",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input file path", Required: true},
			&cli.StringFlag{Name: "schema", Usage: "Schema JSON file path"},
			&cli.StringFlag{Name: "format", Aliases: []string{"f"}, Usage: "Input format (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss)"},
			&cli.StringFlag{Name: "sheet", Usage: "Excel sheet name"},
			&cli.StringFlag{Name: "charset", Usage: charsetFlagUsage},
			&cli.StringFlag{Name: "spss-missing", Value: "auto", Usage: spssMissingFlagUsage},
			&cli.IntFlag{Name: "sample-rows", Value: 500, Usage: "Rows to sample for schema inference (min 50)"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			format := cmd.String("format")
			schemaPath := cmd.String("schema")
			sampleRows := int(cmd.Int("sample-rows"))
			jsonOut := cmd.Bool("json")

			if format == "" {
				format = formatFromExt(input)
			}
			if format == "" {
				msg := "cannot detect format; use --format"
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", msg)
				}
				return fmt.Errorf("%s", msg)
			}

			fs := afero.NewOsFs()
			reader, err := makeImportReader(format, fs, input, readerOptionsFrom(cmd))
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}

			job := pio.NewImportJob(reader, "/dev/null")
			job.FS = fs
			job.SampleRows = sampleRows

			if schemaPath != "" {
				schema, loadErr := loadSchemaFromFile(fs, schemaPath)
				if loadErr != nil {
					if jsonOut {
						return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", loadErr)
					}
					return loadErr
				}
				job.Schema = schema
			}

			report, err := job.Predict(ctx)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "PREDICT_ERROR", err)
				}
				return err
			}

			if jsonOut {
				return writeEnvelopeWithWarnings(cmd.Writer, report, report.SourceWarnings)
			}

			writeText(cmd.Writer, "Schema: %d fields\n", len(report.Schema.Fields))
			writeText(cmd.Writer, "Estimated rows: %d\n", report.EstimatedRows)
			for _, w := range report.Warnings {
				writeText(cmd.Writer, "Warning [%s]: %s\n", w.Column, w.Message)
			}
			writeSourceWarnings(cmd.Writer, report.SourceWarnings)
			return nil
		},
	}
}

func importSchemaTemplateCmd() *cli.Command {
	return &cli.Command{
		Name:      "schema-template",
		Usage:     "Generate an editable schema template from input data",
		ArgsUsage: "INPUT",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Aliases: []string{"f"}, Usage: "Input format (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss)"},
			&cli.StringFlag{Name: "sheet", Usage: "Excel sheet name"},
			&cli.StringFlag{Name: "charset", Usage: charsetFlagUsage},
			&cli.StringFlag{Name: "spss-missing", Value: "auto", Usage: spssMissingFlagUsage},
			&cli.IntFlag{Name: "sample-rows", Value: 500, Usage: "Rows to sample (min 50)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 1 {
				return fmt.Errorf("input file argument required")
			}
			input := args.First()
			format := cmd.String("format")
			sampleRows := int(cmd.Int("sample-rows"))

			if format == "" {
				format = formatFromExt(input)
			}
			if format == "" {
				return fmt.Errorf("cannot detect format from %q; use --format", input)
			}

			fs := afero.NewOsFs()
			reader, err := makeImportReader(format, fs, input, readerOptionsFrom(cmd))
			if err != nil {
				return err
			}

			job := pio.NewImportJob(reader, "/dev/null")
			job.FS = fs
			job.SampleRows = sampleRows

			report, err := job.Predict(ctx)
			if err != nil {
				return err
			}

			// Build template with empty descriptions.
			type fieldTemplate struct {
				Name        string `json:"name"`
				Type        string `json:"type"`
				Description string `json:"description"`
				Nullable    bool   `json:"nullable"`
			}
			tmpl := make([]fieldTemplate, len(report.Schema.Fields))
			for i, f := range report.Schema.Fields {
				tmpl[i] = fieldTemplate{
					Name:        f.Name,
					Type:        f.Type.String(),
					Description: "",
					Nullable:    f.Nullable,
				}
			}

			enc := json.NewEncoder(cmd.Writer)
			enc.SetIndent("", "  ")
			return enc.Encode(tmpl)
		},
	}
}

// readerOptionsFrom lifts the per-format reader knobs off whichever leaf is
// running. A leaf that does not declare a flag reads it as "", which is the
// same as not setting the option, so one helper serves every leaf.
func readerOptionsFrom(cmd *cli.Command) pformat.ReaderOptions {
	return pformat.ReaderOptions{
		Sheet:       cmd.String("sheet"),
		Charset:     cmd.String("charset"),
		SPSSMissing: cmd.String("spss-missing"),
	}
}

// makeImportReader builds the source reader for `pulse import`.
//
// It delegates to pformat.NewReader rather than keeping a second dispatch
// switch. The duplicate switch this replaces was a standing trap — a format
// registered in io/format and not here produced a subcommand that existed and
// immediately failed — and it is also where a new ReaderOptions field would
// have silently gone unread.
func makeImportReader(format string, fs afero.Fs, path string, opts pformat.ReaderOptions) (pio.Reader, error) {
	r, err := pformat.NewReader(format, fs, path, opts)
	if err != nil {
		// A CODED failure is a per-option refusal — an unrecognised
		// --spss-missing value, say — and is surfaced verbatim. Only the
		// dispatch's own "no such format" is reworded, because that one
		// is about the format identifier and nothing else. Flattening
		// both into "unsupported import format" told an operator who
		// typo'd a flag value that Pulse cannot read `.sav` at all.
		var coded *errors.CodedError
		if stderrors.As(err, &coded) {
			return nil, err
		}
		return nil, fmt.Errorf("unsupported import format: %s", format)
	}
	return r, nil
}

func loadSchemaFromFile(fs afero.Fs, path string) (*encoding.Schema, error) {
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file: %w", err)
	}

	type fieldDef struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
		Nullable    bool   `json:"nullable"`
		Precision   uint8  `json:"precision,omitempty"`
		Scale       uint8  `json:"scale,omitempty"`
	}
	var fields []fieldDef
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("parsing schema JSON: %w", err)
	}

	schema := &encoding.Schema{
		Fields: make([]encoding.Field, len(fields)),
	}

	byteOffset := 0
	for i, f := range fields {
		ft := parseFieldType(f.Type)
		field := encoding.Field{
			Name:         f.Name,
			Type:         ft,
			Nullable:     f.Nullable,
			ByteOffset:   byteOffset,
			CsvColumnIdx: i,
			Description:  f.Description,
		}
		if ft.IsDecimal() {
			field.Precision = f.Precision
			field.Scale = f.Scale
		}
		schema.Fields[i] = field
		byteOffset += ft.ByteSize()
	}

	return schema, nil
}

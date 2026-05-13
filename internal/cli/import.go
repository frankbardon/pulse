package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	parrow "github.com/frankbardon/pulse/io/arrow"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/frankbardon/pulse/io/excel"
	"github.com/frankbardon/pulse/io/jsonarray"
	"github.com/frankbardon/pulse/io/ndjson"
	"github.com/frankbardon/pulse/io/parquet"
	"github.com/frankbardon/pulse/io/tsv"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

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

func importExcelCmd() *cli.Command {
	flags := make([]cli.Flag, len(importFlags))
	copy(flags, importFlags)
	flags = append(flags, &cli.StringFlag{Name: "sheet", Usage: "Excel sheet name"})
	return &cli.Command{
		Name:  "excel",
		Usage: "Import Excel file into .pulse format",
		Flags: flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runImport(ctx, cmd, "excel")
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

	reader, err := makeImportReader(format, fs, input, cmd.String("sheet"))
	if err != nil {
		if jsonOut {
			return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
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
				return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
			}
			return err
		}
		job.Schema = schema
	}

	report, err := job.Run(ctx)
	if err != nil {
		if jsonOut {
			return writeErrorEnvelope(cmd.Writer, "IMPORT_ERROR", err.Error())
		}
		return err
	}

	if jsonOut {
		return writeEnvelope(cmd.Writer, report)
	}

	writeText(cmd.Writer, "Imported %d rows to %s\n", report.RowsImported, output)
	if len(report.RowErrors) > 0 {
		writeText(cmd.Writer, "Warnings: %d row errors\n", len(report.RowErrors))
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
			&cli.StringFlag{Name: "format", Aliases: []string{"f"}, Usage: "Input format (csv, tsv, ndjson, jsonarray, parquet, arrow, excel)"},
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
			reader, err := makeImportReader(format, fs, input, "")
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
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
						return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", loadErr.Error())
					}
					return loadErr
				}
				job.Schema = schema
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
			for _, w := range report.Warnings {
				writeText(cmd.Writer, "Warning [%s]: %s\n", w.Column, w.Message)
			}
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
			&cli.StringFlag{Name: "format", Aliases: []string{"f"}, Usage: "Input format (csv, tsv, ndjson, jsonarray, parquet, arrow, excel)"},
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
			reader, err := makeImportReader(format, fs, input, "")
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
			}
			tmpl := make([]fieldTemplate, len(report.Schema.Fields))
			for i, f := range report.Schema.Fields {
				tmpl[i] = fieldTemplate{
					Name:        f.Name,
					Type:        f.Type.String(),
					Description: "",
				}
			}

			enc := json.NewEncoder(cmd.Writer)
			enc.SetIndent("", "  ")
			return enc.Encode(tmpl)
		},
	}
}

// makeImportReader creates a reader for the given format.
func makeImportReader(format string, fs afero.Fs, path string, sheet string) (pio.Reader, error) {
	switch format {
	case "csv":
		return csv.NewReader(fs, path), nil
	case "tsv":
		return tsv.NewReader(fs, path), nil
	case "ndjson":
		return ndjson.NewReader(fs, path), nil
	case "jsonarray":
		return jsonarray.NewReader(fs, path), nil
	case "parquet":
		return parquet.NewReader(fs, path), nil
	case "arrow":
		return parrow.NewReader(fs, path), nil
	case "excel":
		opts := []excel.Option{}
		if sheet != "" {
			opts = append(opts, excel.WithSheet(sheet))
		}
		return excel.NewReader(fs, path, opts...), nil
	default:
		return nil, fmt.Errorf("unsupported import format: %s", format)
	}
}

// loadSchemaFromFile reads and parses a JSON schema file.
func loadSchemaFromFile(fs afero.Fs, path string) (*encoding.Schema, error) {
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file: %w", err)
	}

	type fieldDef struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		Description  string `json:"description"`
		Precision    uint8  `json:"precision,omitempty"`
		Scale        uint8  `json:"scale,omitempty"`
		H3Resolution *uint8 `json:"h3_resolution,omitempty"`
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
			ByteOffset:   byteOffset,
			CsvColumnIdx: i,
			Description:  f.Description,
		}
		if ft.IsDecimal() {
			field.Precision = f.Precision
			field.Scale = f.Scale
		}
		if ft == encoding.FieldTypeH3Cell {
			if f.H3Resolution != nil {
				field.H3Resolution = *f.H3Resolution
			} else {
				field.H3Resolution = 0xFF
			}
		}
		schema.Fields[i] = field
		byteOffset += ft.ByteSize()
	}

	return schema, nil
}

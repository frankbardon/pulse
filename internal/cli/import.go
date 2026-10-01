package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	iio "github.com/frankbardon/pulse/internal/io"
	pio "github.com/frankbardon/pulse/io"
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
	&cli.BoolFlag{Name: "elide-constants", Usage: "Store fields holding one value on every row once in the schema block instead of per row (writes format 0x02, unreadable by older pulse binaries)"},
	&cli.StringSliceFlag{Name: "group", Usage: "Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag (writes format 0x02, unreadable by older pulse binaries)"},
	&cli.FloatFlag{Name: "dedup-ratio-floor", Value: encx.DefaultDedupRatioFloor, Usage: "Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check"},
	&cli.BoolFlag{Name: "strict", Usage: "Treat parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) as errors: the import fails and writes nothing"},
}

// importLeaf finishes an import leaf. Slice flags are not split on ','
// here: a --group value carries its own commas (KEY,KEY:MEMBER,MEMBER),
// and one flag is one group.
func importLeaf(c *cli.Command) *cli.Command {
	c.DisableSliceFlagSeparator = true
	return c
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
			importTransferCmd(),
			importPredictCmd(),
			importSchemaTemplateCmd(),
			importAutoCmd(),
			importsListCmd(),
			importDropCmd(),
		},
	}
}

func importFormatCmd(format string) *cli.Command {
	return importLeaf(&cli.Command{
		Name:  format,
		Usage: fmt.Sprintf("Import %s file into .pulse format", format),
		Flags: importFlags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runImport(ctx, cmd, format)
		},
	})
}

// withImportFlags returns the common import flags plus the format-specific
// extras, without mutating the shared slice.
func withImportFlags(extra ...cli.Flag) []cli.Flag {
	flags := make([]cli.Flag, 0, len(importFlags)+len(extra))
	flags = append(flags, importFlags...)
	return append(flags, extra...)
}

func importExcelCmd() *cli.Command {
	return importLeaf(&cli.Command{
		Name:  "excel",
		Usage: "Import Excel file into .pulse format",
		Flags: withImportFlags(&cli.StringFlag{Name: "sheet", Usage: "Excel sheet name"}),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runImport(ctx, cmd, "excel")
		},
	})
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
	return importLeaf(&cli.Command{
		Name:  "spss",
		Usage: "Import SPSS .sav / .zsav file into .pulse format",
		Flags: withImportFlags(
			&cli.StringFlag{Name: "charset", Usage: charsetFlagUsage},
			&cli.StringFlag{Name: "spss-missing", Value: "auto", Usage: spssMissingFlagUsage},
		),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runImport(ctx, cmd, "spss")
		},
	})
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
	job.ElideConstants = cmd.Bool("elide-constants")
	job.DedupRatioFloor = cmd.Float("dedup-ratio-floor")
	job.StrictDedup = cmd.Bool("strict")
	for _, decl := range cmd.StringSlice("group") {
		g, err := iio.ParseGroupDecl(decl)
		if err != nil {
			if jsonOut {
				return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
			}
			return err
		}
		job.Groups = append(job.Groups, g)
	}

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
		return writeEnvelopeWithWarnings(cmd.Writer, report, append(append(append([]*errors.CodedError(nil), report.SourceWarnings...), report.GroupWarnings...), report.WidthWarnings...))
	}

	writeText(cmd.Writer, "Imported %d rows to %s\n", report.RowsImported, output)
	writeSourceWarnings(cmd.Writer, report.WidthWarnings)
	if len(report.ElidedConstants) > 0 {
		writeText(cmd.Writer, "Elided constant fields (stored once, format 0x02): %s\n", strings.Join(report.ElidedConstants, ", "))
	}
	writeGroupReports(cmd.Writer, report.Groups)
	writeSourceWarnings(cmd.Writer, report.GroupWarnings)
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

// writeGroupReports prints one line per declared parent group as written:
// the import leaves and `import auto` share it so the text cannot drift.
func writeGroupReports(w io.Writer, groups []pio.GroupReport) {
	for _, g := range groups {
		if g.Verdict == encx.GroupVerdictDroppedTooNarrow {
			writeText(w, "Parent %s: dropped, %d-byte members no wider than the %d-byte index\n", g.Label, g.MemberRowBytes, g.IndexWidth)
			continue
		}
		writeText(w, "Parent %s: %d distinct tuples of %s (format 0x02), ratio %.2fx, %d dictionary bytes resident, %+d bytes vs undeduped\n",
			g.Label, g.EntryCount, strings.Join(g.Fields, ", "), g.Ratio, g.DictionaryBytes, g.ByteDelta)
	}
}

func importPredictCmd() *cli.Command {
	return importLeaf(&cli.Command{
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
			&cli.BoolFlag{Name: "suggest-groups", Usage: "Detect candidate parent groups (a key and the fields it determines) and measure each over every row: ratio, resident dictionary bytes, projected file size and a ready-to-paste --group value. Suggests only; nothing is declared"},
			&cli.StringSliceFlag{Name: "group", Usage: "Evaluate a parent-group declaration exactly as 'import <format> --group' would apply it (same syntax, repeatable): its verdict and measured figures, or the error the import would fail with"},
			&cli.BoolFlag{Name: "elide-constants", Usage: "Report the fields 'import <format> --elide-constants' would elide and the bytes saved"},
			&cli.FloatFlag{Name: "dedup-ratio-floor", Value: encx.DefaultDedupRatioFloor, Usage: "Ratio floor the --group and --suggest-groups verdicts are judged against"},
			&cli.BoolFlag{Name: "strict", Usage: "Fail as 'import <format> --strict' would when a --group draws a viability warning"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			format := cmd.String("format")
			schemaPath := cmd.String("schema")
			sampleRows := int(cmd.Int("sample-rows"))
			jsonOut := cmd.Bool("json")

			if format == "" {
				format = pio.FormatFromPath(input).String()
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
			job.SuggestGroups = cmd.Bool("suggest-groups")
			job.ElideConstants = cmd.Bool("elide-constants")
			job.DedupRatioFloor = cmd.Float("dedup-ratio-floor")
			job.StrictDedup = cmd.Bool("strict")
			for _, decl := range cmd.StringSlice("group") {
				g, gerr := iio.ParseGroupDecl(decl)
				if gerr != nil {
					if jsonOut {
						return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", gerr)
					}
					return gerr
				}
				job.Groups = append(job.Groups, g)
			}

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
				return writeEnvelopeWithWarnings(cmd.Writer, report, append(append(append([]*errors.CodedError(nil), report.SourceWarnings...), report.GroupWarnings...), report.WidthWarnings...))
			}

			writeText(cmd.Writer, "Schema: %d fields\n", len(report.Schema.Fields))
			writeSourceWarnings(cmd.Writer, report.WidthWarnings)
			writeText(cmd.Writer, "Estimated rows: %d\n", report.EstimatedRows)
			for _, w := range report.Warnings {
				writeText(cmd.Writer, "Warning [%s]: %s\n", w.Column, w.Message)
			}
			writeSourceWarnings(cmd.Writer, report.SourceWarnings)
			writePredictMeasured(cmd, report)
			return nil
		},
	})
}

// writePredictMeasured prints the measured-pass sections of an import
// predict report (projection, declared groups, elision, candidates).
func writePredictMeasured(cmd *cli.Command, report *pio.PredictReport) {
	w := cmd.Writer
	if p := report.Projection; p != nil {
		writeText(w, "Rows that would import: %d (%d row errors)\n", p.RowsImported, p.RowErrors)
		writeText(w, "Flat cohort: %d bytes; as configured: %d bytes\n", p.FlatFileBytes, p.ProjectedFileBytes)
	}
	if len(report.ElidedConstants) > 0 {
		writeText(w, "Would elide constant fields (%d bytes saved): %s\n", report.Projection.ElisionBytesSaved, strings.Join(report.ElidedConstants, ", "))
	}
	for _, g := range report.Groups {
		if g.Verdict == encx.GroupVerdictDroppedTooNarrow {
			writeText(w, "Parent %s: would be dropped, %d-byte members no wider than the %d-byte index\n", g.Label, g.MemberRowBytes, g.IndexWidth)
			continue
		}
		writeText(w, "Parent %s: %s, %d distinct tuples, ratio %.2fx, %d dictionary bytes resident, %+d bytes vs undeduped\n",
			g.Label, g.Verdict, g.EntryCount, g.Ratio, g.DictionaryBytes, g.ByteDelta)
	}
	writeSourceWarnings(w, report.GroupWarnings)
	writeGroupCandidates(w, report.GroupCandidates)
}

// writeGroupCandidates prints a candidate parent-group detection report
// (import predict --suggest-groups, dedup --suggest-groups). Silent for
// nil.
func writeGroupCandidates(w io.Writer, d *pio.GroupDetection) {
	if d == nil {
		return
	}
	writeText(w, "Group detection: nominated over the first %d rows (bound %d), measured over all %d rows; %d key(s) evaluated\n",
		d.WindowRows, d.WindowBound, d.Rows, d.KeysEvaluated)
	for _, c := range d.Candidates {
		mark := " "
		if c.Suggested {
			mark = "*"
		}
		switch c.Verdict {
		case iio.CandidateVerdictUnmeasured:
			writeText(w, "%s %s: %s (%s) members %s\n", mark, c.Label, c.Verdict, c.Reason, strings.Join(c.Members, ","))
			continue
		case encx.GroupVerdictDroppedTooNarrow:
			writeText(w, "%s %s: %s, %d-byte members no wider than the %d-byte index\n", mark, c.Label, c.Verdict, c.MemberRowBytes, c.IndexWidth)
			continue
		}
		writeText(w, "%s %s: %s, ratio %.2fx, %d distinct, %d dictionary bytes, %+d bytes, file %d bytes\n",
			mark, c.Label, c.Verdict, c.Ratio, c.EntryCount, c.DictionaryBytes, c.ByteDelta, c.ProjectedFileBytes)
		if c.Declaration != "" {
			writeText(w, "    --group %s\n", c.Declaration)
		}
		if c.OverlapsWith != "" {
			writeText(w, "    overlaps %s\n", c.OverlapsWith)
		}
		if len(c.RejectedMembers) > 0 {
			writeText(w, "    varied within the key after the window: %s\n", strings.Join(c.RejectedMembers, ", "))
		}
	}
	if len(d.Suggested) == 0 {
		writeText(w, "No viable parent group found.\n")
		return
	}
	var flags []string
	for _, s := range d.Suggested {
		flags = append(flags, "--group "+s)
	}
	writeText(w, "Suggested: %s\n", strings.Join(flags, " "))
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
				format = pio.FormatFromPath(input).String()
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
// same as not setting the option, so one helper serves every leaf. Each
// knob lands in its format's sub-struct; the factory ignores the others.
func readerOptionsFrom(cmd *cli.Command) pio.ReaderOptions {
	return pio.ReaderOptions{
		Excel: pio.ExcelReaderOptions{Sheet: cmd.String("sheet")},
		SPSS: pio.SPSSReaderOptions{
			Charset:     cmd.String("charset"),
			MissingMode: pio.SPSSMissingMode(cmd.String("spss-missing")),
		},
	}
}

// makeImportReader builds the source reader for `pulse import` through the
// io factory, the one dispatch every surface shares — a second switch here
// was a standing trap (a format registered in one place and not the other
// produced a subcommand that existed and immediately failed). Every
// factory failure is coded and surfaced verbatim: PULSE_IO_FORMAT_UNSUPPORTED
// for the format identifier, a per-option code (an unrecognised
// --spss-missing value, say) otherwise.
func makeImportReader(format string, fs afero.Fs, path string, opts pio.ReaderOptions) (pio.Reader, error) {
	return pio.NewReader(pio.Format(format), fs, path, opts)
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

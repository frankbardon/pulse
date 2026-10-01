package io

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	parrow "github.com/frankbardon/pulse/io/arrow"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/frankbardon/pulse/io/excel"
	"github.com/frankbardon/pulse/io/jsonarray"
	"github.com/frankbardon/pulse/io/ndjson"
	"github.com/frankbardon/pulse/io/parquet"
	"github.com/frankbardon/pulse/io/spss"
	"github.com/frankbardon/pulse/io/tsv"
	"github.com/spf13/afero"
)

// ReaderOptions carries the per-format reader knobs. Each sub-struct is
// honoured only by its own format and ignored silently by every other, so
// one options value can be built once and handed to whichever format a
// path resolves to. The zero value is every format's default.
type ReaderOptions struct {
	// Excel applies when the format is FormatExcel.
	Excel ExcelReaderOptions
	// SPSS applies when the format is FormatSPSS.
	SPSS SPSSReaderOptions
}

// ExcelReaderOptions are the Excel reader's knobs.
type ExcelReaderOptions struct {
	// Sheet names the worksheet to read. Empty selects the first.
	Sheet string
}

// SPSSReaderOptions are the SPSS `.sav` reader's knobs.
type SPSSReaderOptions struct {
	// Charset overrides the character encoding the file declares about
	// itself, resolved by the same lookup the file's own record 7/20 name
	// goes through ("windows-1252", "cp1252" and "1252" are one request).
	// Empty leaves the file's declaration in force.
	//
	// It is the only recourse for a file that is wrong about itself: a
	// dictionary transcoded by one tool and re-saved by another keeps a
	// stale declaration, and a pre-Unicode file declares nothing and reads
	// as strict UTF-8, failing PULSE_SPSS_CHARSET_INVALID on its first
	// 8-bit byte. Decoding only; the file's own declaration is retained.
	Charset string

	// MissingMode selects how a numeric variable's USER-missing values are
	// represented. The zero value is SPSSMissingAuto. Any value other than
	// the declared constants fails construction with
	// PULSE_SPSS_MISSING_MODE_INVALID rather than falling back to the
	// default, because the two modes produce different schemas.
	MissingMode SPSSMissingMode
}

// SPSSMissingMode selects how an SPSS numeric variable's USER-missing
// values become cohort columns. The zero value is SPSSMissingAuto.
type SPSSMissingMode string

const (
	// SPSSMissingAuto is the default and fidelity-preserving: the analytic
	// column is null at every missing position, and a generated
	// `<var>_missing` sibling carries WHY each value is missing.
	SPSSMissingAuto SPSSMissingMode = "auto"

	// SPSSMissingNull suppresses the siblings: the nulls are identical,
	// and the reason is not represented in the cohort (the metadata
	// sidecar still records the full specification).
	SPSSMissingNull SPSSMissingMode = "null"
)

// WriterOptions carries the per-format writer knobs. Each sub-struct is
// honoured only by its own format and ignored silently by every other.
// The zero value is every format's default.
type WriterOptions struct {
	// SPSS applies when the format is FormatSPSS.
	SPSS SPSSWriterOptions
}

// SPSSWriterOptions are the SPSS `.sav` writer's knobs.
type SPSSWriterOptions struct {
	// IgnoreSidecar suppresses the cohort's metadata sidecar entirely:
	// the export proceeds on a dictionary synthesised from the `.pulse`
	// schema alone. It never causes a stale dictionary to be applied.
	IgnoreSidecar bool

	// Uncompressed writes the data section as flat 8-byte elements
	// instead of SPSS's bytecode compression (the default, and what SPSS
	// itself writes). The two are losslessly equivalent.
	Uncompressed bool

	// Charset overrides the character encoding the written file's strings
	// are encoded in and its record 7/20 declaration names. Empty writes
	// the charset the SOURCE declared (UTF-8 for a cohort with no SPSS
	// provenance or no declaration). A name the writer cannot encode in is
	// PULSE_SPSS_CHARSET_UNSUPPORTED, never a silent fall back.
	Charset string

	// SanitizeNames rewrites cohort field names that cannot be SPSS
	// variable names instead of refusing the export. Every rename is
	// reported as a PULSE_SPSS_NAME_SANITIZED warning; renames are
	// deterministic and collision-safe. No effect on sidecar-driven names.
	SanitizeNames bool
}

// BufferWriter is a Writer whose output is held in memory and read back
// with Bytes after Close. NewWriterToBuffer returns one for every
// writable format.
type BufferWriter interface {
	Writer
	// Bytes returns the encoded output. Call it after Close.
	Bytes() []byte
}

func unsupported(f Format, direction string) error {
	msg := "io: format " + strconv.Quote(string(f)) + " cannot be "
	if direction == "read" {
		msg += "read"
	} else {
		msg += "written"
	}
	switch {
	case f == "":
		msg += ": no format given"
	case f == FormatPulse:
		msg += " through the io factory: pulse is the native cohort format, opened directly"
	default:
		msg += ": not a supported tabular format"
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_IO_FORMAT_UNSUPPORTED, msg,
		map[string]any{"format": string(f), "direction": direction})
}

// spssReaderOptions resolves the SPSS reader knobs. MissingMode is
// validated HERE, at construction, because the adapter's option is a
// mutator with no error channel: a typo'd mode would otherwise fail at
// first read or silently fall back to the default.
func spssReaderOptions(o SPSSReaderOptions) ([]spss.Option, error) {
	var opts []spss.Option
	if o.Charset != "" {
		opts = append(opts, spss.WithCharset(o.Charset))
	}
	mode, err := spss.ParseMissingMode(string(o.MissingMode))
	if err != nil {
		return nil, err
	}
	return append(opts, spss.WithMissingMode(mode)), nil
}

func excelOptions(sheet string) []excel.Option {
	if sheet == "" {
		return nil
	}
	return []excel.Option{excel.WithSheet(sheet)}
}

func spssWriterOptions(o SPSSWriterOptions) spss.WriterOptions {
	return spss.WriterOptions{
		IgnoreSidecar: o.IgnoreSidecar,
		Uncompressed:  o.Uncompressed,
		Charset:       o.Charset,
		SanitizeNames: o.SanitizeNames,
	}
}

// NewReader builds a Reader for format f reading path on fs. The returned
// value is the format adapter itself, unwrapped, so every optional
// interface it implements (ResetReader, SchemaAwareReader, NullAwareReader,
// SourceWarningEmitter, SidecarEmitter) is reachable by type assertion.
// Every readable format's reader implements ResetReader.
//
// FormatPulse, the empty Format and unknown identifiers fail with
// PULSE_IO_FORMAT_UNSUPPORTED; an invalid SPSS MissingMode fails with
// PULSE_SPSS_MISSING_MODE_INVALID. Nothing is opened until the first read.
func NewReader(f Format, fs afero.Fs, path string, opts ReaderOptions) (Reader, error) {
	switch f {
	case FormatCSV:
		return csv.NewReader(fs, path), nil
	case FormatTSV:
		return tsv.NewReader(fs, path), nil
	case FormatNDJSON:
		return ndjson.NewReader(fs, path), nil
	case FormatJSONArray:
		return jsonarray.NewReader(fs, path), nil
	case FormatParquet:
		return parquet.NewReader(fs, path), nil
	case FormatArrow:
		return parrow.NewReader(fs, path), nil
	case FormatExcel:
		return excel.NewReader(fs, path, excelOptions(opts.Excel.Sheet)...), nil
	case FormatSPSS:
		so, err := spssReaderOptions(opts.SPSS)
		if err != nil {
			return nil, err
		}
		return spss.NewReader(fs, path, so...), nil
	}
	return nil, unsupported(f, "read")
}

// NewReaderFromBytes builds a Reader for format f over an in-memory
// encoding of the source. Same formats, options, errors and unwrapped
// return as NewReader.
func NewReaderFromBytes(f Format, data []byte, opts ReaderOptions) (Reader, error) {
	switch f {
	case FormatCSV:
		return csv.NewReaderFromBytes(data), nil
	case FormatTSV:
		return tsv.NewReaderFromBytes(data), nil
	case FormatNDJSON:
		return ndjson.NewReaderFromBytes(data), nil
	case FormatJSONArray:
		return jsonarray.NewReaderFromBytes(data), nil
	case FormatParquet:
		return parquet.NewReaderFromBytes(data), nil
	case FormatArrow:
		return parrow.NewReaderFromBytes(data), nil
	case FormatExcel:
		return excel.NewReaderFromBytes(data, excelOptions(opts.Excel.Sheet)...), nil
	case FormatSPSS:
		so, err := spssReaderOptions(opts.SPSS)
		if err != nil {
			return nil, err
		}
		return spss.NewReaderFromBytes(data, so...), nil
	}
	return nil, unsupported(f, "read")
}

// NewWriter builds a Writer for format f writing path on fs. The returned
// value is the format adapter itself, unwrapped, so every optional
// interface it implements (SchemaAwareWriter, NullAwareWriter,
// OverlayAwareWriter, DiscardableWriter, CohortWriter, CohortValidator,
// TargetWarningEmitter, OverlayWarningEmitter, SourceAwareWriter) is
// reachable by type assertion — the export and convert jobs discover them
// that way.
//
// FormatPulse, the empty Format and unknown identifiers fail with
// PULSE_IO_FORMAT_UNSUPPORTED. Nothing is written until the job runs.
func NewWriter(f Format, fs afero.Fs, path string, opts WriterOptions) (Writer, error) {
	switch f {
	case FormatCSV:
		return csv.NewWriter(fs, path), nil
	case FormatTSV:
		return tsv.NewWriter(fs, path), nil
	case FormatNDJSON:
		return ndjson.NewWriter(fs, path), nil
	case FormatJSONArray:
		return jsonarray.NewWriter(fs, path), nil
	case FormatParquet:
		return parquet.NewWriter(fs, path), nil
	case FormatArrow:
		return parrow.NewWriter(fs, path), nil
	case FormatExcel:
		return excel.NewWriter(fs, path), nil
	case FormatSPSS:
		return spss.NewWriter(fs, path, spssWriterOptions(opts.SPSS)), nil
	}
	return nil, unsupported(f, "write")
}

// NewWriterToBuffer builds an in-memory BufferWriter for format f; read
// the encoded output with Bytes after Close. Same formats, options,
// errors and unwrapped return as NewWriter.
func NewWriterToBuffer(f Format, opts WriterOptions) (BufferWriter, error) {
	switch f {
	case FormatCSV:
		return csv.NewWriterToBuffer(), nil
	case FormatTSV:
		return tsv.NewWriterToBuffer(), nil
	case FormatNDJSON:
		return ndjson.NewWriterToBuffer(), nil
	case FormatJSONArray:
		return jsonarray.NewWriterToBuffer(), nil
	case FormatParquet:
		return parquet.NewWriterToBuffer(), nil
	case FormatArrow:
		return parrow.NewWriterToBuffer(), nil
	case FormatExcel:
		return excel.NewWriterToBuffer(), nil
	case FormatSPSS:
		return spss.NewWriterToBuffer(spssWriterOptions(opts.SPSS)), nil
	}
	return nil, unsupported(f, "write")
}

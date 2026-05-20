package io

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Reader reads tabular data from a source format.
type Reader interface {
	// ReadHeader returns column names from the source.
	ReadHeader() ([]string, error)
	// ReadRows streams rows; calls fn for each row.
	// The context controls cancellation.
	ReadRows(ctx context.Context, fn func(row []string) error) error
	// Close releases underlying resources.
	Close() error
}

// ResetReader is an optional interface for readers that support rewinding
// to the beginning. This is needed for schema inference followed by import.
type ResetReader interface {
	Reader
	// Reset rewinds the reader to the beginning.
	Reset() error
}

// Writer writes tabular data to a target format.
type Writer interface {
	// WriteHeader writes column names to the target.
	WriteHeader(columns []string) error
	// WriteRow writes a single row of values.
	WriteRow(values []any) error
	// Close flushes and releases resources.
	Close() error
}

// SchemaAwareWriter is an optional extension of Writer for targets that
// emit native typed columns (Arrow, Parquet, Excel) and want the source
// .pulse schema to drive column-type selection. ExportJob calls
// SetPulseSchema before WriteHeader on writers that implement this
// interface, then passes typed values through WriteRow:
//
//   - encoding.Decimal128 for decimal128 / nullable_decimal128 columns
//   - canonical strings for narrow types (current behavior)
//
// Writers that do not implement SchemaAwareWriter receive only canonical
// strings, which is the prior text-only export contract.
type SchemaAwareWriter interface {
	Writer
	SetPulseSchema(s *encoding.Schema)
}

// ImportReport summarizes the result of an import operation.
type ImportReport struct {
	RowsImported int
	Schema       *encoding.Schema
	RowErrors    []RowError
}

// ExportReport summarizes the result of an export operation.
type ExportReport struct {
	RowsExported  int
	RowErrors     []RowError
	LabelWarnings []LabelWarning
}

// ConvertReport summarizes the result of a convert operation.
type ConvertReport struct {
	RowsConverted int
	Schema        *encoding.Schema
	RowErrors     []RowError
	LabelWarnings []LabelWarning
}

// RowError records a per-row error during import or export.
type RowError struct {
	Row int
	Err error
}

// PredictReport summarizes a validation-only run.
type PredictReport struct {
	Schema        *encoding.Schema
	EstimatedRows int
	Warnings      []InferenceWarning
}

// ImportJob converts tabular source data into a .pulse file.
type ImportJob struct {
	Source     Reader
	Target     string // output .pulse path
	Schema     *encoding.Schema
	SampleRows int // default 500, min 50
	FS         afero.Fs
}

// NewImportJob creates an ImportJob with default settings.
func NewImportJob(source Reader, target string) *ImportJob {
	return &ImportJob{
		Source:     source,
		Target:     target,
		SampleRows: 500,
	}
}

// ExportJob converts a .pulse file into tabular output.
type ExportJob struct {
	Source string // input .pulse path
	Target Writer
	FS     afero.Fs
	// Labels rewrites or augments categorical column values during
	// export using embedder-registered label tables. Bindings name a
	// categorical field plus a label table; mode=replace overwrites
	// the column value, mode=augment emits a sibling "<field>_label"
	// column. See types.LabelBinding for semantics. Nil/empty means
	// the exporter writes raw resolved categorical values, matching
	// pre-label behaviour.
	Labels []*types.LabelBinding
	// LabelResolver carries the runtime resolver built from Labels +
	// the pulse Service's registered LabelTables. The pulse.Export
	// facade builds the resolver and sets this field; callers using
	// io.ExportJob directly without a Pulse instance must supply a
	// satisfying implementation (typically wrapping
	// processing.BuildLabelResolver). Nil means no label translation.
	LabelResolver LabelResolver
}

// NewExportJob creates an ExportJob.
func NewExportJob(source string, target Writer) *ExportJob {
	return &ExportJob{
		Source: source,
		Target: target,
	}
}

// ConvertJob chains import and export with no intermediate file on disk
// (unless KeepPulseAt is set).
type ConvertJob struct {
	Source      Reader
	Target      Writer
	Schema      *encoding.Schema
	KeepPulseAt string // optional: also write intermediate .pulse
	SampleRows  int
	FS          afero.Fs
	// Labels apply to the export phase only — the import side reads
	// raw source bytes and has no use for label translation. See
	// ExportJob.Labels.
	Labels []*types.LabelBinding
	// LabelResolver carries the runtime resolver. See
	// ExportJob.LabelResolver.
	LabelResolver LabelResolver
}

// NewConvertJob creates a ConvertJob with default settings.
func NewConvertJob(source Reader, target Writer) *ConvertJob {
	return &ConvertJob{
		Source:     source,
		Target:     target,
		SampleRows: 500,
	}
}

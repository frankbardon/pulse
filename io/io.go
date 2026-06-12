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

// OverlayAwareWriter is an optional extension of Writer for targets that
// can embed Response.Overlays in the exported artefact (Arrow / Parquet /
// Excel / NDJSON per research/export-embedding-shape.md). The ExportJob
// dispatch wiring (future story) calls SetOverlays before WriteHeader on
// writers that implement this interface when ExportJob.IncludeOverlays
// resolves to true; the writer then emits the layers in its format-
// native sidecar shape at Close time (or earlier where the format
// allows). Writers that do not implement this interface receive no
// overlay slice — the layers are dropped, which is the correct behaviour
// for the CSV / TSV warn-and-skip family.
type OverlayAwareWriter interface {
	Writer
	SetOverlays(layers []*types.OverlayLayer)
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
	// SetInferenceMinPct configures the delimited-cell heuristic for
	// inferring set_* field types during the inference pass. A column
	// is classified as set_* when at least this percentage of non-
	// null sampled cells contain the inferred delimiter, the
	// post-split unique token count fits in set_u64 (≤64), and the
	// average post-split cardinality is > 1. Zero is treated as 30%.
	// Ignored when Schema is supplied.
	SetInferenceMinPct int
	// ColumnTypeOverrides bypasses inference for the named columns
	// (keyed by column name). Used by the managed-import sidecar's
	// force_type escape hatch. Ignored when Schema is supplied.
	ColumnTypeOverrides map[string]encoding.FieldType
	// SetDelimiters maps set-typed column name to the delimiter the
	// importer should use when splitting cell strings into tokens
	// for per-row mask packing. Populated by inference; absent
	// entries (explicit-schema imports or non-set columns) fall back
	// to DefaultSetDelimiter ("|").
	SetDelimiters map[string]string
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
	// Includes restricts the export to the named source-schema fields,
	// in source-schema order. Nil / empty means export every field
	// (prior behaviour). Names must match Schema.Fields[i].Name
	// exactly. Unknown names return PULSE_EXPORT_FIELD_UNKNOWN with
	// the offending name + list of known fields. Label augment
	// siblings ("<field>_label") are emitted only for included source
	// fields; replace mode applies to included fields as before.
	Includes []string
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
	// IncludeOverlays controls whether Response.Overlays land in the
	// exported artefact for adapters that can carry them. The slot is
	// a tri-state pointer:
	//
	//   - nil   ⇒ DEFAULT EMIT — when the host Response carries any
	//             overlay layers the adapter embeds them in the
	//             format-native sidecar (additive-by-default, per PRD
	//             §6 FR-L5). Output for an overlay-free Response is
	//             byte-identical to a pre-overlay ExportJob.
	//   - true  ⇒ explicit emit — same behaviour as nil for downstream
	//             adapters, but the explicit toggle distinguishes the
	//             intent in canonical-hash composition so cache keys
	//             differ between "default" and "explicit yes".
	//   - false ⇒ opt out — overlays are dropped on export even when
	//             the host Response carries layers. Output is byte-
	//             identical to a pre-overlay ExportJob against the
	//             same host result.
	//
	// Per-format semantics (research/export-embedding-shape.md):
	//
	//   - Arrow / Parquet — overlays ride as a top-level
	//     LIST<STRUCT> "overlays" field group emitted once in the
	//     first record-batch / row-group; the host record stream is
	//     byte-identical to the opt-out shape.
	//   - Excel — one sheet per layer named "__overlay_<layer_name>";
	//     the host workbook sheet is unchanged from the opt-out shape.
	//   - NDJSON — single trailing line {"_overlays": [...]} after the
	//     last host-record line; the host stream is byte-identical
	//     until the trailer.
	//   - CSV / TSV — warn-and-skip. The dispatcher emits one
	//     PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED warning and writes the
	//     host CSV verbatim; no overlay output lands. Setting
	//     IncludeOverlays=false suppresses the warning while keeping
	//     the same CSV body.
	//
	// The pointer shape mirrors the Includes-slot precedent (a nil
	// slice means "default": include every field) — nil here means
	// "default: emit overlays when present" rather than "explicit no"
	// because Go bool zero is false. Marshalling via encoding/json
	// honours `omitempty` so nil pointers do not appear in canonical
	// JSON; explicit *true and *false both serialise as booleans and
	// produce distinct canonical-hash keys via ExportJob.Hash().
	IncludeOverlays *bool
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
	// Includes restricts the export half to the named schema fields,
	// in schema order. Nil / empty means write every field. The
	// intermediate .pulse file (when KeepPulseAt is set) always
	// carries the full schema — projection is an output-time overlay,
	// not an on-disk schema change. See ExportJob.Includes.
	Includes []string
	// Labels apply to the export phase only — the import side reads
	// raw source bytes and has no use for label translation. See
	// ExportJob.Labels.
	Labels []*types.LabelBinding
	// LabelResolver carries the runtime resolver. See
	// ExportJob.LabelResolver.
	LabelResolver LabelResolver
	// IncludeOverlays controls whether the export half embeds
	// Response.Overlays in the format-native sidecar. Identical
	// tri-state semantics to ExportJob.IncludeOverlays — nil defaults
	// to emit, *true forces emit, *false drops overlays. The
	// intermediate .pulse file (when KeepPulseAt is set) is byte-
	// identical regardless of this flag because overlays are an
	// export-side concern only; the .pulse byte format never carries
	// overlay payloads. See ExportJob.IncludeOverlays for the per-
	// format wire shape.
	IncludeOverlays *bool
}

// NewConvertJob creates a ConvertJob with default settings.
func NewConvertJob(source Reader, target Writer) *ConvertJob {
	return &ConvertJob{
		Source:     source,
		Target:     target,
		SampleRows: 500,
	}
}

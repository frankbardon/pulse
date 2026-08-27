package io

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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

// SchemaAwareReader is an optional extension of Reader for sources that
// already carry an authoritative schema — a source-side dictionary that
// declares each column's type, nullability and category set as fact
// rather than as a guess (SPSS `.sav`, and in principle Parquet /
// Arrow). ImportJob.Run and ImportJob.Predict type-assert this
// interface and, when it yields a schema, skip io/infer.go entirely:
// no row sampling, no per-column type voting, no delimiter probing.
// Readers that do not implement SchemaAwareReader take the unchanged
// sample-then-infer path, byte-for-byte — pinned by
// TestImportJob_NonSchemaAwareReader_ByteIdentical.
//
// It is the read-side mirror of SchemaAwareWriter: ExportJob.Run pushes
// the .pulse schema INTO a writer via SetPulseSchema before WriteHeader;
// ImportJob pulls the .pulse schema OUT of a reader via PulseSchema
// before the row pass.
//
// # Implementer obligations
//
// The returned schema is used verbatim as the .pulse schema — Run does
// not adjust, widen or re-order it. Every field must therefore be fully
// specified:
//
//   - Name — the .pulse column name.
//   - Type — the storage type. Chosen by the implementer from the source
//     dictionary, never re-derived from cell text.
//   - Nullable — authoritative. See "No null promotion" below.
//   - CsvColumnIdx — the index into the []string row that ReadRows
//     yields. Fields are NOT positionally matched to the header; this
//     index is the only binding. Must be >= 0.
//   - Dictionary — required (and pre-populated) for every type where
//     Type.HasDictionary() reports true. This is the load-bearing slot:
//     dictionary ENTRY ORDER is the on-wire encoding — position i is the
//     categorical_* ID and the set_* mask bit — so handing over the
//     source's own ordering is what preserves the source codes. A nil
//     dictionary is allocated empty and filled in first-seen order from
//     the row text, which is exactly the re-guessing this interface
//     exists to prevent.
//   - Precision / Scale — required for decimal128.
//
// ReadRows still yields []string, so a set_* cell must arrive as its
// tokens joined by DefaultSetDelimiter ("|") — ImportJob.SetDelimiters is
// inert on this path and the constant is the fixed contract.
//
// Cell text still passes through the same conversion the inferred path
// uses, so an authoritative dictionary is pre-seeded, not sealed: a
// value absent from it is appended (subject to the type's width limit)
// rather than rejected. Source-declared entries keep their positions
// either way.
//
// # Precedence over the inference-steering slots
//
// ImportJob carries four slots that exist only to steer inference. With
// an authoritative schema there is no inference to steer, so all four
// are inert — Run reads none of them:
//
//   - SampleRows, SetInferenceMinPct — sampling knobs with nothing to
//     sample.
//   - SetDelimiters — set_* masks come from the source's own set
//     definitions, not from splitting delimited cell strings.
//   - ColumnTypeOverrides — the managed-import force_type escape hatch.
//     Inert deliberately, on two grounds. It is already documented as
//     "ignored when Schema is supplied", and a reader-supplied schema is
//     a supplied schema. More importantly, forcing a type onto a
//     dictionary-carrying column would discard the source's category
//     IDs / mask bit positions and silently rebuild them in first-seen
//     order — a quiet fidelity loss. A caller who genuinely needs a
//     different type sets ImportJob.Schema explicitly, which wins
//     outright (below).
//
// # No null promotion
//
// ImportJob.InferredSchema's out-of-sample-null tolerance does NOT
// apply. An authoritative schema is not inference-originated, so a null
// in a field the source dictionary declares non-nullable is a genuine
// data error: it stays a PULSE_IMPORT_ROW_ERROR and the field is not
// widened. ImportReport.PromotedFields is always empty for such an
// import. Setting InferredSchema alongside a SchemaAwareReader source
// does not re-enable promotion.
//
// # Precedence and failure
//
//   - An explicit ImportJob.Schema wins outright; PulseSchema is not
//     even called. The caller is the most specific instruction, and this
//     keeps every existing explicit-schema path unchanged.
//   - A non-nil error fails the import. There is no fallback to
//     inference: a source that has a dictionary and could not read it
//     must not quietly produce a differently-typed cohort.
//   - A (nil, nil) return is the deliberate opt-out — "this particular
//     source carries no authoritative schema" — and falls back to
//     inference exactly as if the interface were not implemented. It
//     then requires a ResetReader source like any inferred import.
//   - A non-nil schema with no fields, or a field whose CsvColumnIdx is
//     negative, is a malformed contract and fails the import.
//
// ConvertJob does not consult this interface; its import half runs off
// the schema ConvertJob itself resolved.
type SchemaAwareReader interface {
	Reader
	// PulseSchema returns the authoritative .pulse schema for this
	// source, or (nil, nil) to decline and fall back to inference.
	PulseSchema() (*encoding.Schema, error)
}

// OverlayAwareWriter is an optional extension of Writer for targets that
// can embed Response.Overlays in the exported artefact (Arrow / Parquet /
// Excel / NDJSON per research/export-embedding-shape.md). The ExportJob
// dispatch wiring calls SetOverlays before WriteHeader on writers that
// implement this interface when ExportJob.IncludeOverlays resolves to
// true; the writer then emits the layers in its format-native sidecar
// shape at Close time (or earlier where the format allows). Writers that
// do not implement this interface receive no overlay slice — the layers
// are dropped, which is the correct behaviour for the CSV / TSV warn-
// and-skip family.
type OverlayAwareWriter interface {
	Writer
	SetOverlays(layers []*types.OverlayLayer)
}

// OverlayWarningEmitter is an optional extension a Writer can implement
// to surface per-format overlay warnings the dispatcher should lift onto
// the ExportReport / ConvertReport. The canonical user is the CSV / TSV
// adapter which emits PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED via this
// surface whenever the dispatcher hands it a non-empty overlay slate.
// Writers that embed overlays natively (Arrow / Parquet / Excel /
// NDJSON) do not implement this interface; their overlay output rides
// in the format-native sidecar instead.
type OverlayWarningEmitter interface {
	OverlayWarnings() []*errors.CodedError
}

// ImportReport summarizes the result of an import operation.
//
// PromotedFields names the columns an inferred import widened to nullable
// because a null cell fell outside the bounded inference sample window
// (see ImportJob.InferredSchema). Empty for explicit-schema imports and
// for inferred imports with no out-of-sample nulls. Each promotion also
// surfaces a PULSE_IMPORT_NULL_PROMOTED warning to callers that thread
// coded warnings.
type ImportReport struct {
	RowsImported   int
	Schema         *encoding.Schema
	RowErrors      []RowError
	PromotedFields []string
}

// ExportReport summarizes the result of an export operation.
//
// OverlayWarnings carries the warn-and-skip codes the target Writer
// surfaced through the optional OverlayWarningEmitter contract (currently
// the CSV / TSV adapters via PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED).
// Empty / nil when the target adapter embedded the overlays natively
// (Arrow / Parquet / Excel / NDJSON), when no overlays were supplied,
// or when IncludeOverlays explicitly opted out.
type ExportReport struct {
	RowsExported    int
	RowErrors       []RowError
	LabelWarnings   []LabelWarning
	OverlayWarnings []*errors.CodedError
}

// ConvertReport summarizes the result of a convert operation.
//
// OverlayWarnings carries the warn-and-skip codes the target Writer
// surfaced through OverlayWarningEmitter — see ExportReport.OverlayWarnings.
type ConvertReport struct {
	RowsConverted   int
	Schema          *encoding.Schema
	RowErrors       []RowError
	LabelWarnings   []LabelWarning
	OverlayWarnings []*errors.CodedError
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
	Source Reader
	Target string // output .pulse path
	// Schema is the caller-authored .pulse schema. When non-nil it wins
	// over everything: inference is skipped AND a SchemaAwareReader
	// source is not consulted at all. It is therefore the escape hatch
	// for overriding an authoritative source dictionary.
	Schema *encoding.Schema
	// SampleRows caps the inference sample: default 500, min 50.
	// Inert when Schema is supplied or the Source is a
	// SchemaAwareReader that yields a schema — there is nothing to
	// sample.
	SampleRows int
	FS         afero.Fs
	// SetInferenceMinPct configures the delimited-cell heuristic for
	// inferring set_* field types during the inference pass. A column
	// is classified as set_* when at least this percentage of non-
	// null sampled cells contain the inferred delimiter, the
	// post-split unique token count fits in set_u64 (≤64), and the
	// average post-split cardinality is > 1. Zero is treated as 30%.
	// Ignored when Schema is supplied, and inert when the Source is a
	// SchemaAwareReader that yields a schema.
	SetInferenceMinPct int
	// ColumnTypeOverrides bypasses inference for the named columns
	// (keyed by column name). Used by the managed-import sidecar's
	// force_type escape hatch. Ignored when Schema is supplied, and
	// inert when the Source is a SchemaAwareReader that yields a
	// schema — an authoritative source dictionary is not a guess to
	// override, and forcing a type onto a dictionary-carrying column
	// would silently discard the source's category IDs / mask bit
	// positions. Override an authoritative schema by supplying Schema.
	// See SchemaAwareReader.
	ColumnTypeOverrides map[string]encoding.FieldType
	// SetDelimiters maps set-typed column name to the delimiter the
	// importer should use when splitting cell strings into tokens
	// for per-row mask packing. Populated by inference; absent
	// entries (explicit-schema imports or non-set columns) fall back
	// to DefaultSetDelimiter ("|"). Inert when the Source is a
	// SchemaAwareReader that yields a schema: such a source builds
	// set_* masks from its own set definitions, not by splitting
	// delimited cell strings.
	SetDelimiters map[string]string
	// InferredSchema marks a supplied Schema as inference-originated so
	// the row pass promotes non-nullable fields to nullable on an
	// out-of-sample null instead of failing the row (see Run). When
	// Schema is nil this is implied — Run infers the schema and always
	// promotes. Set it explicitly only when handing Run a pre-built
	// schema that came from inference (e.g. ConvertJob's KeepPulseAt
	// re-import) so it inherits the same tolerance. Leave false for a
	// user-authored explicit schema, where a null in a declared
	// non-nullable field must stay a PULSE_IMPORT_ROW_ERROR.
	//
	// Inert when the Source is a SchemaAwareReader that yields a
	// schema. Such a schema is authoritative, not inference-originated,
	// so its declared nullability is a contract: an unexpected null is
	// a row error, never a silent widening. See SchemaAwareReader.
	InferredSchema bool
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
	// Overlays carries the Response.Overlays layers the export should
	// embed in the target adapter's format-native sidecar (Arrow /
	// Parquet column family, Excel sheets, NDJSON trailer) per
	// research/export-embedding-shape.md. ExportJob.Run dispatches the
	// slice to writers satisfying OverlayAwareWriter when IncludeOverlays
	// does not explicitly opt out. The CSV / TSV warn-and-skip family
	// also accepts the slice through SetOverlays but emits a
	// PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED warning (surfaced on
	// ExportReport.OverlayWarnings) instead of writing the layers into
	// the CSV body. nil / empty leaves the export byte-identical to a
	// pre-overlay job. The slot is intentionally NOT part of the
	// canonical-hash composition — two jobs sharing IncludeOverlays /
	// Source / Includes / Labels but differing in the overlay payload
	// resolve through the SAME cache key because the cache identity is
	// the export REQUEST, not the response.
	Overlays []*types.OverlayLayer
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
	// Overlays carries the Response.Overlays layers the convert should
	// embed in the target adapter. Identical semantics to
	// ExportJob.Overlays — the export half of the convert dispatches the
	// slice via SetOverlays on writers satisfying OverlayAwareWriter
	// when IncludeOverlays does not explicitly opt out. The intermediate
	// .pulse file (when KeepPulseAt is set) never carries overlay
	// payloads.
	Overlays []*types.OverlayLayer
}

// NewConvertJob creates a ConvertJob with default settings.
func NewConvertJob(source Reader, target Writer) *ConvertJob {
	return &ConvertJob{
		Source:     source,
		Target:     target,
		SampleRows: 500,
	}
}

package io

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ImportReport summarizes the result of an import operation.
//
// RowErrors records the rows that did not make it. It is a PARTIAL-failure
// channel only: a report exists precisely because some rows did import.
// When a non-empty source yields zero importable rows, ImportJob.Run
// returns a coded error and no report at all, so a caller that checks only
// the error return can no longer read total failure as success.
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
	// WidthWarnings carries one PULSE_IMPORT_WIDTH_PROMOTED warning per
	// field the row pass promoted past its sample-inferred width
	// (categorical_* rung, integer width, or integer → f64), with
	// field / from / to / source_row in Details. Empty (and omitted from
	// JSON) when nothing outgrew its inferred width — always, for an
	// explicit or authoritative schema. See import_widen.go.
	WidthWarnings []*errors.CodedError `json:"WidthWarnings,omitempty"`
	// ZoneWarnings carries the one PULSE_IMPORT_DST_RESOLVED warning a
	// non-default ImportJob.DSTPolicy raises when it resolved at least
	// one ambiguous or nonexistent source-zone wall clock (details:
	// policy, ambiguous_n, nonexistent_n). Empty (and omitted from
	// JSON) otherwise — always, without a source zone.
	ZoneWarnings []*errors.CodedError `json:"ZoneWarnings,omitempty"`
	// SourceWarnings carries the non-fatal diagnostics the source
	// Reader surfaced through the optional SourceWarningEmitter
	// contract — today the PULSE_SPSS_* family raised by the `.sav`
	// dictionary walk, schema mapping and data pass. Nil for sources
	// that do not implement the interface and for sources that
	// implement it and raised nothing, so the report shape is
	// unchanged for every pre-existing adapter.
	SourceWarnings []*errors.CodedError
	// ElidedConstants names the fields ImportJob.ElideConstants stored
	// once in the schema block instead of per row, in schema order.
	// Empty (and omitted from JSON) when elision was off or elided
	// nothing, in which case the cohort is 0x01.
	ElidedConstants []string `json:"ElidedConstants,omitempty"`
	// Groups describes each ImportJob.Groups declaration as written, in
	// declaration order: its label, fields, distinct-tuple count and
	// the entry / member / index widths a viability check weighs. Empty
	// (and omitted from JSON) when no group was declared.
	Groups []GroupReport `json:"Groups,omitempty"`
	// GroupWarnings carries the viability gate's findings, one per
	// flagged group: PULSE_GROUP_TOO_NARROW (the group was dropped) and
	// PULSE_DEDUP_LOW_RATIO (the group was written anyway), each with
	// its numbers in Details. Empty (and omitted from JSON) when every
	// declared group passed. Under ImportJob.StrictDedup a finding is
	// returned as Run's error instead and there is no report.
	GroupWarnings []*errors.CodedError `json:"GroupWarnings,omitempty"`
}

// ExportReport summarizes the result of an export operation.
//
// RowErrors is a PARTIAL-failure channel, exactly as on ImportReport: when
// a non-empty cohort yields zero exported rows, ExportJob.Run returns a
// coded error and no report.
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
	// TargetWarnings carries the non-fatal diagnostics the target
	// Writer surfaced through the optional TargetWarningEmitter
	// contract — today the PULSE_SPSS_SIDECAR_* and
	// PULSE_SPSS_NAME_SANITIZED codes the `.sav` encode raises. Nil
	// for targets that do not implement the interface and for those
	// that implement it and raised nothing, so the report shape is
	// unchanged for every pre-existing adapter.
	TargetWarnings []*errors.CodedError
}

// ConvertReport summarizes the result of a convert operation.
//
// RowErrors is a PARTIAL-failure channel, exactly as on ImportReport and
// ExportReport: when a non-empty source yields zero converted rows,
// ConvertJob.Run returns a coded error and no report.
//
// OverlayWarnings carries the warn-and-skip codes the target Writer
// surfaced through OverlayWarningEmitter — see ExportReport.OverlayWarnings.
type ConvertReport struct {
	RowsConverted   int
	Schema          *encoding.Schema
	RowErrors       []RowError
	LabelWarnings   []LabelWarning
	OverlayWarnings []*errors.CodedError
	// SourceWarnings carries the source Reader's non-fatal diagnostics
	// — see ImportReport.SourceWarnings. Distinct from OverlayWarnings,
	// which come from the TARGET Writer.
	SourceWarnings []*errors.CodedError
	// TargetWarnings carries the target Writer's non-fatal diagnostics
	// — see ExportReport.TargetWarnings.
	TargetWarnings []*errors.CodedError
	// WidthWarnings carries one PULSE_IMPORT_WIDTH_PROMOTED warning per
	// field of an INFERRED schema the row pass promoted past its
	// sample-inferred width (see ImportReport.WidthWarnings); Schema
	// carries the promoted types. Nil for a declared schema, and when
	// nothing outgrew its width.
	WidthWarnings []*errors.CodedError `json:"WidthWarnings,omitempty"`
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
	// ZoneWarnings is what ImportReport.ZoneWarnings would carry: the
	// predict converts every source-zone datetime cell exactly as Run
	// does, so a DST refusal is Predict's error too.
	ZoneWarnings []*errors.CodedError `json:"ZoneWarnings,omitempty"`
	// SourceWarnings carries the source Reader's non-fatal coded
	// diagnostics — see ImportReport.SourceWarnings. Held apart from
	// Warnings, which is the inference pass's own untyped channel: a
	// predict against an authoritative source runs no inference, so
	// Warnings is empty there and SourceWarnings is the only signal.
	SourceWarnings []*errors.CodedError
	// TargetWarnings carries the non-fatal diagnostics the TARGET
	// Writer would raise if the export ran, lifted off the optional
	// CohortValidator contract — today the PULSE_SPSS_SIDECAR_ABSENT /
	// _IGNORED and PULSE_SPSS_NAME_SANITIZED codes a `.sav` export
	// raises before it reads a single record. Symmetric with
	// ExportReport.TargetWarnings, and held apart from SourceWarnings
	// for the same reason ConvertReport holds the two apart: one is
	// about what was read, the other about what would be written.
	//
	// Nil for a predict whose target does not implement
	// CohortValidator, which is every format but `.sav` today, and nil
	// for a validating target that raised nothing.
	TargetWarnings []*errors.CodedError
	// The fields below are filled only by an ImportJob.Predict that runs
	// the MEASURED pass — the job declares Groups, sets ElideConstants
	// or sets SuggestGroups — and are nil (omitted from JSON) otherwise,
	// so a plain predict's report is unchanged. The measured pass
	// converts every row exactly as Run does, so each figure is what the
	// import would produce, not a sample estimate.
	//
	// WidthWarnings are the PULSE_IMPORT_WIDTH_PROMOTED warnings Run
	// would raise (see ImportReport.WidthWarnings), and Schema carries
	// the promoted types. Only the measured pass converts values, so a
	// plain predict never reports a width promotion: its Schema is the
	// sample-inferred one.
	WidthWarnings []*errors.CodedError `json:"WidthWarnings,omitempty"`
	// Projection sizes the file the import would write.
	Projection *ImportProjection `json:"Projection,omitempty"`
	// Groups / GroupWarnings are what ImportReport.Groups /
	// GroupWarnings would carry for the declared Groups. A declaration
	// Run would refuse — an unknown field, or a member that varies
	// within its key (PULSE_GROUP_MEMBER_NOT_CONSTANT) — is Predict's
	// error too, and StrictDedup fails Predict as it fails Run.
	Groups        []GroupReport        `json:"Groups,omitempty"`
	GroupWarnings []*errors.CodedError `json:"GroupWarnings,omitempty"`
	// ElidedConstants names what ElideConstants would elide.
	ElidedConstants []string `json:"ElidedConstants,omitempty"`
	// GroupCandidates is the SuggestGroups detection report.
	GroupCandidates *GroupDetection `json:"GroupCandidates,omitempty"`
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
	// post-split unique token count fits the widest set rung (≤256),
	// and the average post-split cardinality is > 1. Zero is treated
	// as 30%.
	// Ignored when Schema is supplied, and inert when the Source is a
	// SchemaAwareReader that yields a schema.
	SetInferenceMinPct int
	// ColumnTypeOverrides bypasses inference for the named columns
	// (keyed by exact header name). Used by the managed-import
	// sidecar's force_type escape hatch. An override is applied exactly
	// or refused with PULSE_IMPORT_OVERRIDE_INVALID — never narrowed,
	// widened or ignored: an unknown column, a present value the forced
	// type cannot hold (on ANY row, not just the inference sample —
	// never a skipped row), and an override on a job with no inferred
	// schema (Schema supplied, or a SchemaAwareReader source whose
	// authoritative dictionary is not a guess to override) are all
	// refused. Re-type an authoritative column by supplying Schema.
	// See SchemaAwareReader and import_override.go.
	ColumnTypeOverrides map[string]encoding.FieldType
	// SourceTZ is the zone naive datetime literals are read in: an
	// IANA Area/Location name, "UTC", or a fixed offset "+HH:MM" /
	// "-HH:MM" (accepted here, never in a request). It applies to every
	// `datetime` field of the resolved schema and skips `date` fields
	// (a calendar day has no zone). A literal carrying its own `Z` or
	// offset names that instant and ignores the zone. Storage is
	// unchanged: the stored value is always UTC epoch seconds. Empty
	// (the default) reads naive literals as UTC, byte-identically to an
	// import without the field. An unknown zone is PULSE_TIMEZONE_UNKNOWN.
	SourceTZ string
	// ColumnSourceTZ sets the source zone per column (keyed by exact
	// field name, same spelling as SourceTZ) and wins over SourceTZ for
	// that column. A key naming no field, or a field that is not
	// `datetime` (a `date` column included), is refused with
	// SERVICE_VALIDATION before the row pass.
	ColumnSourceTZ map[string]string
	// DSTPolicy decides a naive literal a source zone's DST transition
	// makes ambiguous (shown twice) or nonexistent (skipped): DSTPolicyError
	// (the default; "" means it) refuses the import at the first such row
	// with PULSE_IMPORT_DST_AMBIGUOUS / PULSE_IMPORT_DST_NONEXISTENT;
	// DSTPolicyEarlier / DSTPolicyLater resolve with the pre- / post-
	// transition offset and report the counts in one
	// PULSE_IMPORT_DST_RESOLVED warning (ImportReport.ZoneWarnings).
	// Inert without a source zone and under a fixed-offset zone.
	DSTPolicy DSTPolicy
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
	// ElideConstants stores every field that holds exactly one value
	// (or is null) on EVERY imported row once, in the schema block, and
	// drops it from each record — a format 0x02 cohort, which binaries
	// older than 0x02 support cannot open. Constancy is decided over the
	// full row pass, never the inference sample. Nothing is elided (and
	// the output stays a byte-identical 0x01 cohort) for fewer than two
	// rows, when no field is constant, or when declaring the constants
	// would cost more schema bytes than it saves. When every field is
	// constant the lowest-index one stays in the row. Default false.
	// See encoding.PlanConstantElision.
	ElideConstants bool
	// Groups declares parent groups (see GroupDecl): each stores its
	// distinct member tuples ONCE in the schema block and every record a
	// u32 index into them — a format 0x02 cohort, which binaries older
	// than 0x02 support cannot open. Groups are independent; a field
	// belongs to at most one. Names are checked against the resolved
	// schema (inferred, authoritative or explicit alike) before the row
	// pass; the dictionaries are built in one pass over the imported
	// rows. Composes with ElideConstants: declared members are never
	// elided. Empty (the default) writes the 0x01 cohort unchanged.
	//
	// Every declared group passes the per-group viability gate
	// (encoding.DedupGate): one no wider than its u32 index is dropped
	// with PULSE_GROUP_TOO_NARROW; one below DedupRatioFloor rows per
	// distinct tuple, or that makes the file no smaller, is written with
	// PULSE_DEDUP_LOW_RATIO. Both land in ImportReport.GroupWarnings.
	Groups []GroupDecl
	// DedupRatioFloor is the rows-per-distinct-tuple floor below which
	// a declared group draws PULSE_DEDUP_LOW_RATIO. Zero (the default)
	// selects encoding.DefaultDedupRatioFloor (2); a value in (0, 1]
	// disables the floor, leaving only the grows-the-file check.
	DedupRatioFloor float64
	// StrictDedup turns the viability gate's warnings into errors: Run
	// fails with the finding's own code (PULSE_GROUP_TOO_NARROW or
	// PULSE_DEDUP_LOW_RATIO) and writes nothing. It governs the gate
	// only — other import warnings are unaffected.
	StrictDedup bool
	// SuggestGroups makes Predict detect candidate parent groups —
	// single-field keys and the fields they determine — and measure
	// each over the full row pass (PredictReport.GroupCandidates). It
	// SUGGESTS only: Run ignores it, and a candidate is formed only when
	// declared through Groups. Costs Predict a full conversion pass (as
	// Groups and ElideConstants do) plus the bounded detection work
	// documented on GroupDetection.
	SuggestGroups bool
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

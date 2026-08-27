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
// # The empty-mask cell, and the two rules that keep it distinct
//
// A set_* column has THREE states, not two: some elements selected, NO
// element selected, and null. CLAUDE.md's byte-layout invariants make
// the middle one real — "an empty mask is a valid no-selection,
// distinct from null" — and for a source that can tell them apart it is
// load-bearing data, not a nicety. io/spss is the canonical case: a
// survey respondent who worked through a "select all that apply"
// battery and ticked nothing gave an answer, and one who was never
// shown the battery did not.
//
// The row form for "no selection" is a cell of ONE BARE DELIMITER
// ("|"). The empty string cannot serve, because it is a null token and
// is consumed before any dictionary is consulted. That the bare
// delimiter works is not a trick but the composition of two documented
// behaviours in the shared import path, and BOTH are part of this
// contract:
//
//   - isNullToken (io/import.go) recognises exactly "", "na", "n/a" and
//     "null", case-insensitively. "|" is not among them, so the cell
//     reaches value conversion instead of being read as null.
//   - splitSetTokens (io/infer.go) trims each part and DROPS the empty
//     ones, so "|" yields zero tokens: mask 0, and no dictionary
//     mutation.
//
// Widening the null-token set to cover a lone delimiter, or making
// splitSetTokens retain empty tokens, would collapse the empty-mask and
// null states into one — SILENTLY, since both spellings would keep
// importing and only the meaning would change. An end-to-end
// FILTER_SET EQUALS-empty assertion in io/spss guards the composition;
// a change to either rule must keep that green rather than update it.
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
// ConvertJob consults this interface too, through the same precedence:
// an explicit ConvertJob.Schema wins outright, otherwise an
// authoritative source schema is adopted before inference is
// considered, and the intermediate .pulse file KeepPulseAt writes is
// built from it. Without that, a convert FROM an authoritative source
// would re-infer types from cell text and throw the source dictionary
// away — the exact loss this interface exists to prevent, reached
// through a different verb.
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

// SourceWarningEmitter is an optional extension a Reader can implement
// to surface non-fatal diagnostics the source parse raised, so the
// shared jobs can lift them onto the ImportReport / PredictReport /
// ConvertReport instead of leaving them stranded inside the adapter.
//
// It is the read-side mirror of OverlayWarningEmitter: the dispatcher
// type-asserts it after the row pass and copies whatever it yields.
// Readers that do not implement it contribute no warnings and their
// reports are byte-identical to the pre-interface shape.
//
// The canonical user is io/spss, whose `.sav` dictionary walk and
// schema mapping raise warnings that do not stop an import but change
// what the cohort means — an unrecognised record type 7 extension
// subtype, a temporal column demoted to raw seconds, a near-unique
// categorical, a value collision, a declared case count that disagrees
// with the cases actually present. Every one of those is a
// PULSE_SPSS_* code with a fixup, and a user who never sees them
// cannot act on them.
//
// # Timing
//
// Warnings become knowable progressively — the dictionary's at parse,
// the mapping's at schema resolution, the data pass's while reading
// cases — so the jobs collect AFTER the row pass, when the full set is
// available. An implementation must therefore be a pure accessor:
// calling it must not itself trigger a parse, and calling it twice must
// not double the set.
type SourceWarningEmitter interface {
	Reader
	// Warnings returns the non-fatal diagnostics raised so far. The
	// returned slice is the caller's to retain.
	Warnings() []*errors.CodedError
}

// SidecarEmitter is an optional extension a Reader can implement to
// persist source metadata that the `.pulse` format has nowhere to hold.
//
// It is the third member of the io/ optional-interface family
// (SchemaAwareReader, SourceWarningEmitter): the dispatcher
// type-asserts it and does nothing at all when the assertion fails, so
// a Reader that does not implement it produces a byte-identical import
// to the pre-interface shape — no extra file, no extra stat, no
// behaviour change. Verified by TestImportJob_NoSidecarEmitter_WritesNothing.
//
// The canonical user is io/spss. An SPSS dictionary declares measure
// levels, print formats, arbitrary value codes, missing-value
// specifications, declared string widths, multiple-response sets,
// document records and a source charset — none of which a `.pulse`
// header, schema block or null bitmap can express, and all of which a
// round trip needs. The adapter writes them to a JSON sidecar beside
// the cohort.
//
// # Timing
//
// ImportJob.Run calls this AFTER the cohort has been written, and the
// order is load-bearing: an implementation is expected to fingerprint
// the cohort it is describing, which requires the cohort's bytes to
// exist. cohortPath is ImportJob.Target and fs is ImportJob.FS, so the
// sidecar lands on the same filesystem as the cohort — never through
// os, so fs.NewMemMap() stays hermetic.
//
// # Failure
//
// A returned error FAILS the import. The cohort write on the same
// filesystem has just succeeded, so a sidecar write that then fails is
// a genuine fault rather than an expected condition, and a cohort
// silently missing the only surviving record of its source dictionary
// is precisely the quiet fidelity loss the sidecar exists to prevent.
// (The "absent sidecar is only a warning" rule is a READ-path rule
// about cohorts that never had one.) Implementations should return a
// coded error.
type SidecarEmitter interface {
	Reader
	// WriteSidecar writes the source-metadata sidecar describing the
	// cohort at cohortPath, onto fs. Implementations derive the
	// sidecar's own path from cohortPath by appending a suffix, per the
	// imports.Sidecar convention.
	WriteSidecar(fs afero.Fs, cohortPath string) error
}

// CohortSource describes the `.pulse` cohort an export is reading from,
// handed to a [CohortWriter] so it can encode from the cohort's own
// bytes instead of from the rendered row stream.
//
// It carries the two output-time transformations ExportJob.Run applies
// to that row stream — projection and label translation — not because a
// cohort writer is expected to implement them, but so it can REFUSE
// them. A writer that silently ignored Includes would answer
// `pulse export spss --include age` with a file carrying every column,
// which is the quiet wrong answer this whole surface exists to avoid.
type CohortSource struct {
	// FS is the filesystem the cohort lives on — ExportJob.FS, so a
	// hermetic MemMapFs export stays hermetic.
	FS afero.Fs

	// Path is the `.pulse` cohort path (ExportJob.Source). It is also
	// where a format-specific metadata sidecar rides: io/spss derives
	// `cohort.pulse.spss.json` from it, which is the only surviving
	// record of the source SPSS dictionary.
	Path string

	// Includes is ExportJob.Includes verbatim — nil / empty when the
	// export emits every field.
	Includes []string

	// Labelled reports that a label resolver is rewriting or augmenting
	// cells in the row stream (ExportJob.Labels / LabelResolver).
	Labelled bool
}

// CohortWriter is an optional extension of Writer for targets whose
// encoding is defined on the cohort's RAW STORAGE rather than on the
// rendered row stream ExportJob.Run produces.
//
// It is the one place the row-oriented Writer contract does not fit, and
// the fit is not close enough to fake. io/spss is the case that forced
// it: a `.sav` variable's on-wire value is derived from a categorical's
// dictionary ID, a set_*'s mask bit and the null bitmap, and every one
// of those is GONE by the time ExportJob has rendered a row —
// `formatFieldValue` resolves a categorical to its label text and a null
// to "", which a string categorical can hold legitimately. Rebuilding
// the storage from the text would be a guess in exactly the places SPSS
// fidelity is decided.
//
// # Dispatch
//
// ExportJob.Run type-asserts this interface AFTER SetPulseSchema and
// AFTER WriteHeader — so a cohort writer still receives both, and the
// header is still the projection-aware column list — and then calls
// WriteCohort INSTEAD of its own row loop. Nothing is double-decoded:
// the row loop does not run at all, and WriteRow is never called.
// Writers that do not implement it take the unchanged row path,
// byte-for-byte.
//
// # Obligations
//
// The returned count is ExportReport.RowsExported. An implementation
// that cannot honour some part of the job — a projection it does not
// implement, a label binding it cannot apply — must return a coded
// error rather than writing a file that ignores it.
type CohortWriter interface {
	Writer
	// WriteCohort encodes the cohort described by src, and returns the
	// number of records written.
	WriteCohort(ctx context.Context, src CohortSource) (int, error)
}

// CohortValidator is an optional extension of Writer for targets that
// can decide whether they COULD encode a cohort without encoding it.
//
// It exists because `pulse export predict` was target-blind. ExportJob.Predict
// read the source header and schema and answered "this export is fine" no
// matter what the target was, which was harmless only for as long as every
// writer was infallible at the target boundary — CSV / TSV / NDJSON /
// JSONArray stringify anything handed to them. io/spss is the first writer
// that can REFUSE, so predict was claiming an export would work and the real
// export was then failing. Predict has to be able to answer the question it
// appears to answer.
//
// # Dispatch, and the promise to every other format
//
// ExportJob.Predict type-asserts this interface. A Target that is nil, or one
// that does not implement it, is predicted EXACTLY as it was before the
// interface existed: no extra read, no extra failure mode, the same
// PredictReport. That is the whole compatibility contract and it is pinned by
// TestExportJob_Predict_NonValidatingTargetUnchanged.
//
// Unlike CohortWriter, a validator is called WITHOUT SetPulseSchema and
// WITHOUT WriteHeader — predict starts no write lifecycle on a writer it will
// never Close. Everything a validator needs is reachable from CohortSource:
// src.FS + src.Path locate the cohort (and any format sidecar riding beside
// it), and Includes / Labelled carry the output-time transformations so they
// can be refused here for the same reasons WriteCohort refuses them.
//
// # Return contract
//
// The returned slice is the non-fatal diagnostics the real export WOULD
// raise; they land on PredictReport.TargetWarnings. A non-nil error is the
// refusal the real export WOULD return, and must carry the same code the
// export itself would — a predicted PULSE_SPSS_NAME_INVALID that exported as
// something else would be worse than no prediction at all.
//
// # The one rule an implementation may not break
//
// A validator must never refuse something the real export would accept. A
// false refusal is worse than the current silence: it blocks work that would
// have succeeded, and there is no way for the caller to appeal it. Where a
// verdict is not reachable without reading records — a value whose width
// overflows, a character the target charset cannot encode, a dictionary ID
// with no source code behind it — a validator warns, or stays silent. It
// never guesses. Predict is therefore a sound but INCOMPLETE filter: passing
// it means no schema-level refusal was found, not that the export cannot
// fail.
//
// The refusal set is mostly reachable without records precisely because a
// `.pulse` cohort's records are fixed-width numerics — every string lives in
// the schema block's dictionaries. Name legality, charset encodability of the
// dictionary text, sidecar state, derived-column foldability and the
// Includes / Labels refusals are all schema + sidecar facts.
//
// # Obligations
//
// Validation must have no side effects the caller can observe: no output
// file, no mutation of the writer's own encode state, and it must stay safe
// to call before, after, or instead of a write pass.
type CohortValidator interface {
	Writer
	// ValidateCohort reports whether the cohort described by src could be
	// encoded, without encoding it. The slice is the warnings the real
	// export would raise; a non-nil error is the refusal it would return.
	ValidateCohort(ctx context.Context, src CohortSource) ([]*errors.CodedError, error)
}

// TargetWarningEmitter is an optional extension a Writer can implement
// to surface non-fatal diagnostics the ENCODE raised, so the shared jobs
// lift them onto ExportReport.TargetWarnings / ConvertReport.TargetWarnings
// instead of leaving them stranded inside the adapter.
//
// It is the write-side mirror of SourceWarningEmitter, and distinct from
// OverlayWarningEmitter, which answers the narrower question "were
// overlay layers dropped?". Writers that implement neither contribute no
// warnings and their reports are byte-identical to the pre-interface
// shape.
//
// The canonical user is io/spss, whose encode raises diagnostics that do
// not stop an export but change what the file MEANS: a metadata sidecar
// that was absent or deliberately ignored (so the dictionary was
// synthesised rather than reproduced), and every variable rename
// --sanitise-names performed. A user who never sees those cannot tell a
// faithful re-emission from a reconstruction.
//
// # Timing
//
// The jobs collect AFTER the write pass, when the full set is knowable.
// An implementation must be a pure accessor: calling it must not itself
// trigger work, and calling it twice must not double the set.
type TargetWarningEmitter interface {
	Writer
	// Warnings returns the non-fatal diagnostics raised so far. The
	// returned slice is the caller's to retain.
	Warnings() []*errors.CodedError
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
	// SourceWarnings carries the non-fatal diagnostics the source
	// Reader surfaced through the optional SourceWarningEmitter
	// contract — today the PULSE_SPSS_* family raised by the `.sav`
	// dictionary walk, schema mapping and data pass. Nil for sources
	// that do not implement the interface and for sources that
	// implement it and raised nothing, so the report shape is
	// unchanged for every pre-existing adapter.
	SourceWarnings []*errors.CodedError
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
	// TargetWarnings carries the non-fatal diagnostics the target
	// Writer surfaced through the optional TargetWarningEmitter
	// contract — today the PULSE_SPSS_SIDECAR_* and
	// PULSE_SPSS_NAME_SANITISED codes the `.sav` encode raises. Nil
	// for targets that do not implement the interface and for those
	// that implement it and raised nothing, so the report shape is
	// unchanged for every pre-existing adapter.
	TargetWarnings []*errors.CodedError
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
	// SourceWarnings carries the source Reader's non-fatal diagnostics
	// — see ImportReport.SourceWarnings. Distinct from OverlayWarnings,
	// which come from the TARGET Writer.
	SourceWarnings []*errors.CodedError
	// TargetWarnings carries the target Writer's non-fatal diagnostics
	// — see ExportReport.TargetWarnings.
	TargetWarnings []*errors.CodedError
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
	// SourceWarnings carries the source Reader's non-fatal coded
	// diagnostics — see ImportReport.SourceWarnings. Held apart from
	// Warnings, which is the inference pass's own untyped channel: a
	// predict against an authoritative source runs no inference, so
	// Warnings is empty there and SourceWarnings is the only signal.
	SourceWarnings []*errors.CodedError
	// TargetWarnings carries the non-fatal diagnostics the TARGET
	// Writer would raise if the export ran, lifted off the optional
	// CohortValidator contract — today the PULSE_SPSS_SIDECAR_ABSENT /
	// _IGNORED and PULSE_SPSS_NAME_SANITISED codes a `.sav` export
	// raises before it reads a single record. Symmetric with
	// ExportReport.TargetWarnings, and held apart from SourceWarnings
	// for the same reason ConvertReport holds the two apart: one is
	// about what was read, the other about what would be written.
	//
	// Nil for a predict whose target does not implement
	// CohortValidator, which is every format but `.sav` today, and nil
	// for a validating target that raised nothing.
	TargetWarnings []*errors.CodedError
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

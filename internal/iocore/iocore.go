package iocore

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

// DiscardableWriter is an optional extension of Writer for targets that
// hold resources a Go GC cannot reclaim — an OS temp file, a handle —
// before Close is ever reached.
//
// It exists because the io CLI leaves deliberately do NOT close the
// writer when the job returns an error. Every adapter buffers its output
// and emits it exactly once inside Close (afero.WriteFile, not an
// incrementally written handle), so skipping Close writes NOTHING, which
// is the correct outcome for a hard failure; adding a close-on-error
// would put a zero-row target next to the error instead. That reasoning
// is about DATA, and it is unchanged.
//
// Resources are the separate question, and exactly one adapter answers
// it differently: io/excel drives an excelize StreamWriter whose buffer
// spills to os.CreateTemp past excelize.StreamChunkSize (16 MiB), and
// only excelize.File.Close removes those files. Discard is the
// release-without-emitting half of Close — drop the buffers and any
// temp files, write nothing to the target, and stay safe to call before
// a Close that must also write nothing.
//
// An adapter that buffers purely on the Go heap needs no implementation;
// the absent interface is the correct answer and DiscardWriter leaves
// such a target alone.
type DiscardableWriter interface {
	Writer
	Discard() error
}

// DiscardWriter releases a writer's non-heap resources without emitting
// its target, for writers that implement DiscardableWriter. It is a
// no-op for every other writer — and specifically does NOT fall back to
// Close, which would write the file the caller is erroring out of.
func DiscardWriter(w Writer) error {
	if dw, ok := w.(DiscardableWriter); ok {
		return dw.Discard()
	}
	return nil
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
// ("|") — the exported constant EmptySetCell. The empty string cannot
// serve, because it is a null token and is consumed before any
// dictionary is consulted. It is no longer a SchemaAwareReader
// peculiarity: ExportJob.Run writes the same marker for every format,
// so the convention is one contract shared by the whole adapter set
// (see EmptySetCell and docs/src/internals/adding-io-format.md). That
// the bare delimiter works is not a trick but the composition of two
// documented behaviours in the shared import path, and BOTH are part
// of this contract:
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

// The null cell, and why a categorical needs a channel the text has not
//
// A `.pulse` categorical column has THREE states in the same sense a
// set_* column does: an ordinary value, the EMPTY STRING as a value,
// and null. The byte format already keeps them apart — null rides the
// per-record null bitmap and the empty string is an ordinary dictionary
// entry — so any collapse happens in this package, not in the format.
//
// set_* could solve its version of this with an in-band marker
// (EmptySetCell, a bare delimiter, which no legal selection can spell).
// A categorical cannot: ANY text is a legal categorical value, so every
// candidate sentinel makes some real value unrepresentable, which is a
// worse bug than the one it fixes. The distinction therefore has to
// ride OUT OF BAND, in a channel beside the cell text.
//
// # The row-level convention: nil is the null cell
//
// ExportJob.Run spells a bitmap-null cell as an untyped Go nil in the
// []any row, and a present empty string as "". That is the whole
// convention, and it is free for the adapters that cannot use it:
// io/csv and io/tsv already render nil as "", so their bytes are
// unchanged.
//
// ConvertJob does NOT write from a cohort — it copies source TEXT, in
// which "" is the null token isNullToken recognises and no nil is ever
// produced. The two verbs therefore disagree about what "" means, and a
// writer must not guess which one is calling it. NullAwareWriter is how
// it is told: ExportJob.Run calls SetExplicitNulls(true) and ConvertJob
// does not, so a writer's null policy follows its caller's provenance
// rather than the cell's spelling.
//
// # The read-back channel: NullAwareReader
//
// Reader.ReadRows yields []string, which has no null channel at all —
// a JSON null, an Arrow validity bit and an empty JSON string all
// arrive as "". NullAwareReader is the out-of-band half: a source whose
// format HAS a native null channel reports it per row, and ImportJob
// believes it over the text.
//
// # The adapter matrix, including the honest gaps
//
// Carries the distinction end to end (implements NullAwareReader):
// ndjson and jsonarray (JSON null vs ""), arrow and parquet (the
// validity bit vs a present empty string).
//
// Does NOT carry it, deliberately and documented rather than silently:
//
//   - csv, tsv — RFC 4180 has one spelling for an absent value. Go's
//     encoding/csv does not preserve the unquoted-empty vs
//     quoted-empty distinction on read (LazyQuotes and the default
//     reader both yield ""), so there is no channel to use even if the
//     writer emitted one. A null and an empty-string categorical both
//     export as an empty field and both re-import as NULL.
//   - excel — the WRITER already distinguishes them: a nil cell is
//     written as a genuinely blank cell (CellTypeUnset) and "" as an
//     empty string cell, so the artefact carries the distinction. The
//     READER loses it: excelize's row API (GetRows / Rows.Columns)
//     yields []string with no cell-presence channel, and the per-cell
//     GetCellType probe that would recover it is O(rows) per call —
//     quadratic over a sheet. Recovering it needs a different read
//     path, not a different interface, so excel collapses to NULL like
//     the delimited pair.
//   - spss — has its own fidelity model (the metadata sidecar and
//     pio.CohortWriter) and does not ride the row path in either
//     direction.
//
// The collapse direction matters and is asserted: a format that cannot
// carry the distinction reads BOTH states back as NULL. Losing the
// empty string into null is the pre-existing behaviour and is
// recoverable from the source; inventing an empty-string value where
// the cohort had a null would be new data.

// NullAwareReader is an optional extension of Reader for sources whose
// format carries an explicit null channel — JSON null, an Arrow or
// Parquet validity bit — that the []string row cannot express.
//
// ImportJob.Run and ImportJob.Predict type-assert it. A source that
// does not implement it takes the unchanged text path, where
// isNullToken alone decides, byte-for-byte.
//
// # Contract
//
// RowNulls describes the row MOST RECENTLY passed to the ReadRows
// callback and is only valid for the duration of that call —
// implementations reuse the slice. Its length is the row's length;
// entry i is true when column i was null IN THE SOURCE, which is a
// different question from whether the cell text is empty.
//
// # How ImportJob reads it
//
// A declared null is always null, whatever the text says. A cell the
// source declares PRESENT is a null only if the type cannot hold its
// text: the empty string is a legal value for a dictionary-bearing
// non-set type and nothing else, so that is the single case where an
// empty cell survives as a value. An empty cell in a u32 or a date
// column is still a null, because "" is not a number and reading it as
// one would turn a recoverable null into a per-row import error.
// set_* is excluded deliberately: its present-but-empty state already
// has the EmptySetCell spelling and must keep it, so that the set
// tri-state behaves identically on every adapter.
type NullAwareReader interface {
	Reader
	// RowNulls reports which columns of the current row were null in
	// the source. Valid only inside the ReadRows callback.
	RowNulls() []bool
}

// NullAwareWriter is an optional extension of Writer for targets whose
// format has a native null channel and which therefore need to know
// which caller is handing them rows.
//
// ExportJob.Run calls SetExplicitNulls(true) before WriteHeader: its
// rows come from a `.pulse` cohort, where the null bitmap is authority,
// nil is the ONLY null cell and "" is an ordinary value. ConvertJob
// never calls it: its rows are source text, where "" is the null token
// and no nil is produced. A writer that is never told stays on the text
// convention, which is what keeps every convert byte-identical.
//
// Use IsNullCell rather than testing the flag by hand, so the two
// conventions are spelled once.
type NullAwareWriter interface {
	Writer
	// SetExplicitNulls declares that the caller marks null cells with
	// an untyped nil and means "" literally.
	SetExplicitNulls(on bool)
}

// IsNullCell reports whether one cell of a row handed to Writer.WriteRow
// is the ABSENT cell, under whichever of the two conventions applies.
//
// explicit is the flag NullAwareWriter.SetExplicitNulls set: true on the
// export path (nil alone is null; "" is a value), false on the convert
// path (nil and "" are both the absent cell, matching isNullToken's
// reading of the source text).
//
// Only "" is treated as absent on the text path — never "na" / "null" —
// because that is exactly what the writers did before the null channel
// existed, and widening it here would change what a convert emits.
func IsNullCell(v any, explicit bool) bool {
	if v == nil {
		return true
	}
	if explicit {
		return false
	}
	s, ok := v.(string)
	return ok && s == ""
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
// --sanitize-names performed. A user who never sees those cannot tell a
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

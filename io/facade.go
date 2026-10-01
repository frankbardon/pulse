// Public alias facade over internal/io and internal/iocore. Every type is
// an alias, every function a one-line forward, every constant a re-export:
// the implementation lives in internal/io (jobs, inference, transfer) and
// internal/iocore (the Reader / Writer contracts the format adapters
// implement). Doc comments are the implementation's own.

package io

import (
	encoding "github.com/frankbardon/pulse/encoding"
	iio "github.com/frankbardon/pulse/internal/io"
	iocore "github.com/frankbardon/pulse/internal/iocore"
	types "github.com/frankbardon/pulse/types"
)

// CandidateReasonMemoryBound is the Reason on an unmeasured
// candidate.
const CandidateReasonMemoryBound = iio.CandidateReasonMemoryBound

// CandidateVerdictUnmeasured: the candidate outgrew its share of
// the detection memory budget before the pass ended; no figures.
const CandidateVerdictUnmeasured = iio.CandidateVerdictUnmeasured

// DefaultSetDelimiter is the delimiter assumed by convertValue when no
// per-column delimiter has been recorded (explicit-schema imports that
// skipped the inference pass). Always |.
const DefaultSetDelimiter = iio.DefaultSetDelimiter

// DefaultTransferLevel is the zstd level used when a job leaves Level at
// zero. Level 3 is zstd's own default and was the level measured during
// planning.
const DefaultTransferLevel = iio.DefaultTransferLevel

// EmptySetCell is the external form of a PRESENT set_* cell with no
// element selected — a bare DefaultSetDelimiter, carrying no token.
//
// A set column has three states, not two: some elements selected, NO
// element selected, and null. The empty string cannot spell the middle
// one, because isNullToken consumes it before any dictionary is
// consulted; a cell that means "answered, ticked nothing" would
// re-import as "never answered" and quietly shrink the denominator of
// every rate computed over the column.
//
// One marker serves every adapter — flat text (csv / tsv / excel) and
// JSON alike — so the convention is stated once and cannot drift
// between formats. It survives a third-party round trip because it is
// ordinary cell TEXT: unlike CSV's `,,` versus `,"",`, a spreadsheet or
// a generic CSV writer has nothing to normalise away. The structured
// adapters additionally carry the distinction natively (an Arrow or
// Parquet LIST cell is null via its validity bit and empty via a
// zero-length list; ndjson / jsonarray accept a JSON `[]`), and the
// readers map those spellings back onto this marker so the shared
// import path sees one form.
//
// It costs nothing at the other two states: a null cell is still "" and
// a NON-EMPTY cell is still its delimiter-joined tokens, byte for byte.
//
// The composition that makes it work is documented on SchemaAwareReader
// in io.go and must be kept: isNullToken does not recognise "|", and
// splitSetTokens drops empty tokens, so "|" yields zero tokens — mask
// 0, with no dictionary mutation.
const EmptySetCell = iio.EmptySetCell

const MaxTransferLevel = iio.MaxTransferLevel

const MinTransferLevel = iio.MinTransferLevel

// TransferCodec is the one codec the transfer artifact uses.
const TransferCodec = iio.TransferCodec

// TransferExtension is the conventional suffix appended to a cohort's
// file name for its transfer artifact.
const TransferExtension = iio.TransferExtension

const TransferLayoutShardArchive = iio.TransferLayoutShardArchive

const TransferLayoutSingleFile = iio.TransferLayoutSingleFile

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
type CohortSource = iocore.CohortSource

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
type CohortValidator = iocore.CohortValidator

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
type CohortWriter = iocore.CohortWriter

// ConvertJob chains import and export with no intermediate file on disk
// (unless KeepPulseAt is set).
type ConvertJob = iio.ConvertJob

// ConvertReport summarizes the result of a convert operation.
//
// RowErrors is a PARTIAL-failure channel, exactly as on ImportReport and
// ExportReport: when a non-empty source yields zero converted rows,
// ConvertJob.Run returns a coded error and no report.
//
// OverlayWarnings carries the warn-and-skip codes the target Writer
// surfaced through OverlayWarningEmitter — see ExportReport.OverlayWarnings.
type ConvertReport = iio.ConvertReport

// ConvertSource is what a convert knows about its SOURCE that the rendered
// row stream between the two halves cannot carry.
//
// # Why it exists
//
// ConvertJob is import-half then export-half with no cohort in between: the
// source yields `[]string` rows and the target consumes them. That is
// sufficient for a text target, and insufficient for a target that has to
// rebuild a cohort in order to emit at all — io/spss's `.sav` writer is the
// only one today (see its pio.CohortWriter row path). Such a target
// re-derives a schema by INFERRING it from the very text the source just
// rendered, and inference cannot recover what the source DECLARED:
//
//   - A multiple-dichotomy `set_*` column round-trips its selections as
//     "Q1A|Q1C" tokens, and a column whose observed tokens happen to be few
//     re-infers as a narrow rung — or, with few enough distinct cells, as a
//     categorical. The selections survive; the declared width does not, and
//     with it goes the dictionary entry per UNSELECTED member.
//   - The SPSS metadata sidecar is the only place the code / label /
//     dictionary-ID triple lives (.claude/reference/byte-layout.md). A
//     rebuilt intermediate cohort has none, so a derived column cannot be
//     recognised as derived, and the `.sav` writer's synthesise-a-default
//     path re-emits a multiple-dichotomy set as member variables that
//     collide by name with the constituents already in the cohort —
//     PULSE_SPSS_NAME_COLLISION for the wide case, and a bogus extra
//     variable for the narrow one.
//
// Both are the same missing channel, so both ride the same struct.
//
// # What a recipient may assume
//
// ConvertJob.Run populates it ONLY when the row stream it is about to emit
// is faithful to Schema — one cell per schema field, in field order, with
// no projection (ConvertJob.Includes) and no label augmentation or
// replacement rewriting the cells. Where it is not, nothing is carried and
// the target takes the unchanged inference path. So a recipient may rely on
// Schema.Fields[i] describing emitted column i, and on
// Schema.Fields[i].CsvColumnIdx being i.
type ConvertSource = iocore.ConvertSource

// DedupJob converts an existing single-file cohort to a grouped cohort.
// Shard archives are not this job's input — the caller (pulse.Dedup)
// refuses them with a coded error before a byte is read.
type DedupJob = iio.DedupJob

// DedupReport describes a retro-dedup run.
type DedupReport = iio.DedupReport

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
type DiscardableWriter = iocore.DiscardableWriter

// ExportJob converts a .pulse file into tabular output.
type ExportJob = iio.ExportJob

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
type ExportReport = iio.ExportReport

// GroupCandidate is one detected parent group and its measured figures.
// The figures carry the same names and meaning as GroupReport's.
type GroupCandidate = iio.GroupCandidate

// GroupDecl declares one parent group by field NAME.
//
// Key names the fields that identify the parent (e.g. its ID); Members
// names the fields the key DETERMINES (the parent's attributes). The
// group stores Key ∪ Members. With a Key, every row that carries a key
// tuple already seen must agree with that earlier row on every Member —
// value and null state — or the import fails with
// PULSE_GROUP_MEMBER_NOT_CONSTANT naming the member and the row: a
// declaration that is not actually a parent group is refused, never
// silently turned into a larger dictionary. With no Key, Members is a
// plain tuple group: each distinct combination is one entry and there
// is nothing to violate.
type GroupDecl = iio.GroupDecl

// GroupDetection is import predict's candidate parent-group report
// (ImportJob.SuggestGroups). Candidates are SUGGESTIONS: declaring one
// is always the user's decision (--group / ImportJob.Groups).
type GroupDetection = iio.GroupDetection

// GroupReport describes one declared group: its verdict from the
// per-group viability gate (encoding.DedupGate) and the numbers behind
// it. A dropped group (verdict dropped_too_narrow) was not formed — its
// members stay row fields — and carries only its widths; an admitted
// or low_ratio group was written and carries the measured ratio, the
// dictionary it holds resident and the byte delta versus storing its
// members per row.
type GroupReport = iio.GroupReport

// ImportJob converts tabular source data into a .pulse file.
type ImportJob = iio.ImportJob

// ImportProjection is the measured pass's view of the file the import
// would write.
type ImportProjection = iio.ImportProjection

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
type ImportReport = iio.ImportReport

// InferOptions parameterises InferSchemaWithOptions. The zero value is
// safe — SampleRows / SetInferenceMinPct fall back to their package
// defaults and ColumnTypeOverrides is treated as empty.
type InferOptions = iio.InferOptions

// InferenceResult bundles the inference output. The Delimiters map is
// populated for columns classified as set_*; callers (importers) use
// it to pack per-cell masks during the row pass.
type InferenceResult = iio.InferenceResult

// InferenceWarning records a non-fatal observation during inference.
type InferenceWarning = iio.InferenceWarning

// LabelResolver is the io-package projection of the processing-layer
// label resolver. ExportJob / ConvertJob consume the interface so io
// stays free of the processing dependency.
//
// processing.LabelResolver satisfies this interface as-is; the
// pulse-package facade builds the resolver from the user-facing
// LabelBinding slice + LabelTable registry and hands the value to the
// job via ExportJob.LabelResolver.
type LabelResolver = iio.LabelResolver

// LabelWarning is the io-package projection of one resolver
// diagnostic record. ExportReport surfaces these for the caller; the
// CLI / MCP envelope wrapper promotes them to envelope warnings.
type LabelWarning = iio.LabelWarning

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
type NullAwareReader = iocore.NullAwareReader

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
type NullAwareWriter = iocore.NullAwareWriter

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
type OverlayAwareWriter = iocore.OverlayAwareWriter

// OverlayWarningEmitter is an optional extension a Writer can implement
// to surface per-format overlay warnings the dispatcher should lift onto
// the ExportReport / ConvertReport. The canonical user is the CSV / TSV
// adapter which emits PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED via this
// surface whenever the dispatcher hands it a non-empty overlay slate.
// Writers that embed overlays natively (Arrow / Parquet / Excel /
// NDJSON) do not implement this interface; their overlay output rides
// in the format-native sidecar instead.
type OverlayWarningEmitter = iocore.OverlayWarningEmitter

// PredictReport summarizes a validation-only run.
type PredictReport = iio.PredictReport

// Reader reads tabular data from a source format.
type Reader = iocore.Reader

// ResetReader is an optional interface for readers that support rewinding
// to the beginning. This is needed for schema inference followed by import.
type ResetReader = iocore.ResetReader

// RowError records a per-row error during import or export.
type RowError = iio.RowError

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
type SchemaAwareReader = iocore.SchemaAwareReader

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
type SchemaAwareWriter = iocore.SchemaAwareWriter

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
type SidecarEmitter = iocore.SidecarEmitter

// SourceAwareWriter is an optional extension of Writer for convert targets
// that rebuild a `.pulse` cohort from the row stream instead of writing the
// rows out directly.
//
// It is the convert-time peer of [SchemaAwareWriter], and deliberately NOT
// that interface: SetPulseSchema means "here is the cohort you are
// exporting from" and is answered by the typed-column adapters (Arrow,
// Parquet, Excel) by changing what they emit. This one means "here is what
// your source declared, for the cohort you are about to rebuild", is
// answered only by a target that rebuilds one, and changes nothing for a
// target that does not implement it — ConvertJob type-asserts it and does
// nothing at all when the assertion fails, so every other adapter's convert
// output is byte-identical to the pre-interface shape.
//
// ConvertJob.Run calls SetConvertSource BEFORE WriteHeader, matching the
// ordering every other push-shaped optional interface uses, so a recipient
// may consult it from the first buffered row onwards.
type SourceAwareWriter = iocore.SourceAwareWriter

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
type SourceWarningEmitter = iocore.SourceWarningEmitter

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
type TargetWarningEmitter = iocore.TargetWarningEmitter

// TransferExportJob compresses a cohort's exact bytes into a transfer
// artifact. Source must be an uncompressed `.pulse` (single-file or
// shard archive); Output is overwritten.
type TransferExportJob = iio.TransferExportJob

// TransferImportJob decompresses a transfer artifact back into a
// byte-identical `.pulse` at rest. The output is staged beside Output and
// renamed over it only once the whole stream decoded, its checksum held
// and the decoded bytes began with a Pulse magic — a refusal or a
// damaged artifact leaves nothing behind.
type TransferImportJob = iio.TransferImportJob

// TransferReport describes one transfer compress or decompress.
// SHA256 is always the digest of the UNCOMPRESSED cohort bytes, so the
// sender's and the receiver's reports compare directly.
type TransferReport = iio.TransferReport

// Writer writes tabular data to a target format.
type Writer = iocore.Writer

// CompareOverlayLayer returns nil when the two layers match across all
// renderer-facing slots (name, kind, scope, ref, payload, summary).
// Per-shape payload comparison dispatches through CompareOverlayPayload.
func CompareOverlayLayer(got *types.OverlayLayer, want *types.OverlayLayer) error {
	return iio.CompareOverlayLayer(got, want)
}

// CompareOverlayLayers returns nil when `got` matches `want` slot-for-
// slot. Mismatch returns an error describing the first divergence —
// length, slot index, name / kind / scope, shape, or per-shape payload.
// Designed for use inside table-driven test bodies; callers wrap the
// error in t.Errorf for the failure message.
//
// Exported so the per-format integration tests under io/exportoverlay/
// share one comparison surface across Arrow / Parquet / Excel / NDJSON
// round-trips.
func CompareOverlayLayers(got []*types.OverlayLayer, want []*types.OverlayLayer) error {
	return iio.CompareOverlayLayers(got, want)
}

// CompareOverlayPayload dispatches by shape and compares the populated
// arm. Absent arms (the non-matching shape's sub-payload nil) are
// untouched — the OverlayPayload union always populates exactly one arm
// per the Shape discriminator.
func CompareOverlayPayload(got types.OverlayPayload, want types.OverlayPayload) error {
	return iio.CompareOverlayPayload(got, want)
}

// CompareOverlayRef returns nil when the two refs share the same
// discriminator state. The structural comparison covers the Margin /
// Population / Stage / Slot arms — extending this helper when new ref
// arms land follows the additive-only rule (new arms compare nil-vs-
// nil before recursing into the new fields).
func CompareOverlayRef(got types.OverlayRef, want types.OverlayRef) error {
	return iio.CompareOverlayRef(got, want)
}

// CompareOverlaySummary returns nil when both summaries match across
// every populated field. nil-vs-nil counts as match; nil-vs-non-nil is
// a mismatch.
func CompareOverlaySummary(got *types.OverlaySummary, want *types.OverlaySummary) error {
	return iio.CompareOverlaySummary(got, want)
}

// DiscardWriter releases a writer's non-heap resources without emitting
// its target, for writers that implement DiscardableWriter. It is a
// no-op for every other writer — and specifically does NOT fall back to
// Close, which would write the file the caller is erroring out of.
func DiscardWriter(w Writer) error {
	return iio.DiscardWriter(w)
}

// ErrStopIteration returns the stop iteration sentinel for use by readers.
func ErrStopIteration() error {
	return iio.ErrStopIteration()
}

// InferSchema samples up to sampleRows rows from reader and proposes a Schema.
// If sampleRows <= 0, defaultSampleRows is used. The minimum is minSampleRows.
// Wrapper over InferSchemaWithOptions that drops the delimiter map; only
// CSV/TSV/Excel paths that ignore set-typed import packing should call
// this entry. Importers should call InferSchemaWithOptions directly.
func InferSchema(reader Reader, sampleRows int) (*encoding.Schema, []InferenceWarning, error) {
	return iio.InferSchema(reader, sampleRows)
}

// InferSchemaWithOptions is the parameterised inference entry. Returns
// the schema, accumulated warnings, AND the per-column delimiter map
// for columns classified as set_*. The delimiter map is used by
// ImportJob.Run during the row pass to split delimited cells into
// token lists for bit-packing.
func InferSchemaWithOptions(reader Reader, opts InferOptions) (*InferenceResult, error) {
	return iio.InferSchemaWithOptions(reader, opts)
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
	return iio.IsNullCell(v, explicit)
}

// MaxSetElements returns how many elements the widest rung addresses —
// the most a set column can ever hold.
func MaxSetElements() int {
	return iio.MaxSetElements()
}

// NewConvertJob creates a ConvertJob with default settings.
func NewConvertJob(source Reader, target Writer) *ConvertJob {
	return iio.NewConvertJob(source, target)
}

// NewExportJob creates an ExportJob.
func NewExportJob(source string, target Writer) *ExportJob {
	return iio.NewExportJob(source, target)
}

// NewImportJob creates an ImportJob with default settings.
func NewImportJob(source Reader, target string) *ImportJob {
	return iio.NewImportJob(source, target)
}

// ParseGroupDecl parses the CLI form of one group declaration:
//
//	KEY[,KEY...]:MEMBER[,MEMBER...]   a keyed group (members determined by the key)
//	MEMBER[,MEMBER...]                a plain tuple group (no key check)
//
// Names are trimmed of surrounding whitespace. An empty name, an empty
// side of the colon, or more than one colon is
// PULSE_GROUP_DECLARATION_INVALID. Field names containing ',' or ':'
// cannot be written in this form; use ImportJob.Groups directly.
func ParseGroupDecl(s string) (GroupDecl, error) {
	return iio.ParseGroupDecl(s)
}

// ResolveTransferLevel validates level and applies the default.
func ResolveTransferLevel(level int) (int, error) {
	return iio.ResolveTransferLevel(level)
}

// SetTypeFor returns the narrowest set_* type with a bit for each of
// `elements` elements, and reports false when no rung is wide enough
// (or when `elements` is not positive — a set with no elements has no
// type, and that is a caller error rather than a width verdict).
//
// This is the same selection inference makes, so a column of N distinct
// tokens and a declared response set of N constituents land on the same
// rung by construction rather than by agreement.
func SetTypeFor(elements int) (encoding.FieldType, bool) {
	return iio.SetTypeFor(elements)
}

// WidestSetType returns the top rung of the ladder — the widest set_*
// type Pulse has.
//
// Callers that must NAME the ceiling in a diagnostic use this rather
// than writing "set_u256" into a string, so the message cannot outlive
// the type it names.
func WidestSetType() encoding.FieldType {
	return iio.WidestSetType()
}

# Adding an I/O Format

**Audience:** internals contributors adding a new bidirectional
tabular format (a peer to the existing `csv/`, `tsv/`, `ndjson/`,
`jsonarray/`, `arrow/`, `parquet/`, `excel/` sub-packages).

> **From CLAUDE.md, Common Claude Code Workflows.**

## 1. Create the sub-package

Each format is a sub-package under `io/`. Create
`io/<format>/<format>.go` with both a reader and a writer.

The two interfaces to implement live in `io/`:

```go
// Reader
type Reader interface {
    ReadHeader() ([]string, error)
    ReadRows(ctx context.Context, fn func(row []string) error) error
    Close() error
}

// Writer
type Writer interface {
    WriteHeader(columns []string) error
    WriteRow(values []string) error
    Close() error
}
```

If the reader needs schema inference (header sample, then full
import), also implement `io.ResetReader.Reset()` so the import job
can rewind after sampling.

### Headerless formats: the first record is still a record

Some formats carry no header line at all — NDJSON and a top-level JSON
array both derive their column names from the keys of the **first
object**. That object is a RECORD as well as a column declaration, so
`ReadHeader` must buffer it and `ReadRows` must replay it as row one.

Getting this wrong is invisible: no error, no warning, every import
one row short and a single-record file importing nothing. `io/ndjson`
and `io/jsonarray` both keep the decoded first object in a `pending`
field and emit it before resuming the scan; copy that shape.

Only the first object's KEYS define the column set. Replaying the
record does not widen the columns, and a key that first appears in a
later object is still not a column.

## 2. Tests

Add `io/<format>/<format>_test.go` with the standard round-trip
checks: write rows, read them back, verify equality. Hermetic tests
should use `afero.NewMemMapFs()` — see [Testing
Conventions](../contributing/testing.md).

For a headerless format, assert the record COUNT on a one-record and a
many-record source, and assert the first record's VALUES — a count
alone passes on a reader that drops record one and duplicates another.

## 3. Register and wire it up

Registration is spread across **five** places, and a format wired into
only some of them is reachable by one verb and mysteriously absent from
another.

**`io/format/format.go`** — the shared dispatch. All four of:

- the format identifier constant;
- an `ext → id` case in `FromExt` (this is what makes `pulse convert`,
  `pulse import auto` and `pulse_import` detect the file at all);
- an entry in `SupportedImport`;
- a `NewReader` case.

`SupportedImport` is what documentation and help output enumerate, so a
reader with no entry there is reachable only by accident — and an entry
with no `NewReader` case advertises a reader the engine cannot build.
`TestSupportedImport_EveryEntryConstructs` closes that loop.

**`internal/cli/import.go`** — the import leaf's own switch, separate
from `io/format`'s:

- the `makeImportReader(format, ...)` case;
- an `importFormatCmd("yourformat")` line in the `Commands:` slice on
  `ImportCommand()`.

**`internal/cli/format.go`** — `newWriterForFormat`, plus the
`writerOptions` bag it takes. Writers are *not* in `io/format`; this
switch is their whole dispatch, which is why a per-format write knob has
no shared options struct to ride and gets a field on `writerOptions`
instead. If the format is import-only, do NOT leave it falling through
to the generic `unsupported format` default: the extension is
recognised, so that message says the wrong thing. Return a specific
coded error naming the format instead. (SPSS was the worked example
until its writer landed; `PULSE_SPSS_EXPORT_UNSUPPORTED` has since been
repurposed to mean "this cohort has no honest `.sav` form".)

**`internal/cli/export.go`** — an `exportFormatCmd("yourformat")` line
in `ExportCommand()`, when a writer exists.

**`descriptor/capabilities_export.go`** — the manifest capability
blocks. `importCapability()` gains an `ImportFormatCapability`
(name, extensions, `SchemaSource` — `inferred` or `authoritative` —
and whether the same format can be written); `exportCapability()`
gains an `ExportFormatCapability` only when a writer actually exists.
Both slices are alphabetised so the golden manifest stays stable, and
`TestManifestImportCapability_MatchesFormatRegistry` pins the
hand-declared table against `io/format`.

Two prose surfaces hardcode the format list and are easy to miss:
`mcp/contract.go` (the `ImportIn.Format` jsonschema description) and
`mcp/toolmeta/meta.go` (`DescImport`).

Regenerate the manifest golden afterwards — never hand-edit it:

```bash
go test ./descriptor/ -run 'Test.*Golden' -update
```

## 4. Schema mapping

If the new format has a native type system (Arrow / Parquet do, CSV
does not), share the type map with neighbouring formats via the
`io/arrow` package the way Parquet already does. CSV / TSV / NDJSON
/ JSON-array share `io/jsonshared` for value coercion.

### The external form of a `set_*` cell

A set column's *external* form is its selected **labels**, never the
bitmask. `ExportJob.Run` hands a Writer one string per set cell:
the selected dictionary labels joined with `pio.DefaultSetDelimiter`
(`"|"`), in ascending **bit** order — which is dictionary-index
order, not the order the tokens appeared in the source. This is the
exact inverse of the import obligation above, which is what makes a
set column survive `cohort → format → cohort`.

#### The three states, and the one marker that carries them

A set cell is not two-valued. It has **null** (no answer), an
**empty mask** (answered, selected nothing) and a selection, and all
three are distinct inside the cohort: null rides the per-record null
bitmap, an empty mask is a real zero-width selection. For survey
data the middle state is load-bearing — "ticked none of these" and
"skipped the question" give different denominators — so an adapter
that collapses them corrupts an analysis without failing anything.

The external forms, identical at every rung from `set_u8` to
`set_u256`:

| State | Flat text (`csv` / `tsv` / `excel`) | JSON (`ndjson` / `jsonarray`) | `arrow` / `parquet` |
|---|---|---|---|
| null | `""` (any `isNullToken` spelling) | `null` | validity bit clear |
| empty mask | `pio.EmptySetCell` — a bare `"|"`, no token | `"|"` on write, `[]` also accepted on read | zero-length `LIST<UTF8>` |
| selection | `A|B` | `"A|B"` | `["A","B"]` |

`pio.EmptySetCell` is one marker for every format, so the convention
cannot drift between adapters, and it survives a round trip through a
third-party tool because it is ordinary cell **text**: unlike CSV's
`,,` versus `,"",`, a spreadsheet or a generic CSV writer has nothing
to normalise away. It costs nothing at the other two states — a null
cell is still `""` and a **non-empty** cell is still its joined
tokens, byte for byte.

It works by the composition of two documented behaviours in the
shared import path, and **both are part of the contract**:
`isNullToken` recognises exactly `""`, `na`, `n/a` and `null`, so
`"|"` reaches value conversion instead of being read as null; and
`splitSetTokens` trims each part and drops the empty ones, so `"|"`
yields zero tokens — mask 0, with no dictionary mutation. Widening
the null-token set to cover a lone delimiter, or making
`splitSetTokens` retain empty tokens, re-collapses the two states
SILENTLY: both spellings keep importing and only the meaning changes.

**Implementer obligations.**

- A Writer that stringifies (`csv`, `tsv`, `excel`, and the JSON
  adapters via `jsonshared.CoerceValue`) needs no set-specific code:
  the marker is already in the string `ExportJob.Run` hands over.
- A Writer with a native list type MUST route set cells through
  `parrow.AppendSetList` (or reproduce it): `nil` and `""` are the
  **null** cell and take the validity bit; anything else opens a
  list, empty for the marker. Reading `""` as an empty list is the
  bug this replaced.
- A Reader with a native list type must render a present zero-length
  list as `pio.EmptySetCell`, not `""` — `""` is a null token and the
  null cell already has its own channel one level up. `io/arrow`'s
  `formatStringListSlice` is the reference.
- A Reader over JSON must map `[]` to the marker
  (`jsonshared.ValueToString`), so a producer that does not know the
  convention and simply emits `[]` still imports as an empty
  selection.

The matrix that pins all of this for every adapter at a narrow rung
and a wide rung is `io/settristate/`.

`categorical_*` has the same empty-string-vs-null collapse and it
does have a real out-of-band null channel now — see "The categorical
null cell" below. The two mechanisms do not overlap: a set cell's
present-but-empty state stays on the `pio.EmptySetCell` marker at
every adapter, including the four with a null channel.

**The form is width-agnostic.** Every rung from `set_u8` to
`set_u256` externalizes identically; a 206-label cell is simply a
longer token list. A Writer therefore never branches on the rung —
but it MUST branch on `Type.IsSet()` if its native type for a set is
a list (`io/arrow`'s `LIST<UTF8>`), because the joined string has to
be split back into elements before it reaches a list builder.
Handing the joined string to a list builder's generic
`AppendValueFromString` makes it parse as JSON, fail on every row,
and produce an export that reports success having written no rows.

On the decode side the wide rungs (`set_u128` / `set_u256`) do not
fit the `uint64` value API at all: `encoding.ReadFieldValue` refuses
them with `ENCODING_TYPE_MISMATCH`, so the export loop reads them
with `encoding.ReadSetMask` instead. A format that walks cohort
bytes itself (`io.CohortWriter`, below) has to make the same split —
branch on `FieldType.IsWideSet()`.

### The categorical null cell: `io.NullAwareWriter` / `io.NullAwareReader`

`categorical_*` has the same two-states-that-look-alike problem and it
cannot be solved the same way. A set's `"|"` marker works because no
legal selection can spell it; **any** text is a legal categorical
value, so every candidate sentinel makes some real value
unrepresentable — a worse bug than the one it would fix. The `.pulse`
format already keeps the pair apart (null rides the bitmap, `""` is an
ordinary dictionary entry), so the distinction has to ride **out of
band**, beside the cell rather than inside it.

| State | export row (`[]any`) | JSON | `arrow` / `parquet` | flat text |
|---|---|---|---|---|
| value | `"red"` | `"red"` | `"red"` | `red` |
| empty-string value | `""` | `""` | present `""` | *(collapses)* |
| null | `nil` | `null` | validity bit clear | *(collapses)* |

**Write side.** `ExportJob.Run` spells a bitmap-null cell as an
untyped **`nil`** and an empty-string value as `""`, then declares the
convention with `io.NullAwareWriter.SetExplicitNulls(true)`.
`ConvertJob` never makes that call, because its rows are source TEXT
in which `""` *is* the null token `isNullToken` recognises — so a
writer's null policy follows the caller's provenance, never the cell's
spelling. Use `pio.IsNullCell(v, explicit)` rather than testing the
flag by hand. A Writer that ignores the interface entirely keeps the
text convention and is unaffected; `io/csv` and `io/tsv` already
render `nil` as `""`, so their bytes did not move.

**Read side.** `Reader.ReadRows` yields `[]string`, which has no null
channel at all — a JSON null, an Arrow validity bit and an empty JSON
string all arrive as `""`. A source whose format HAS the channel
implements `io.NullAwareReader.RowNulls() []bool`, valid for the row
currently in the callback, and `ImportJob` believes it over the text:
a declared null is null whatever the text says, and a cell the source
declares PRESENT keeps `""` as a value **only** where the type can
hold it — a dictionary-bearing non-set type, and nothing else. `""`
in a `u32` or a `date` column is still a null, because reading it as a
value would turn a recoverable null into a per-row import error.
`set_*` is excluded on purpose: its present-but-empty state already
has the `pio.EmptySetCell` spelling and must behave identically on
every adapter.

**The adapter matrix, gaps included.**

| Adapter | Carries null vs `""` | How |
|---|---|---|
| `ndjson`, `jsonarray` | yes | JSON `null` vs `""`, via `jsonshared.CoerceValueExplicitNull` on write and key-presence on read |
| `arrow`, `parquet` | yes | `AppendNull` vs `Append("")`; `Array.IsNull` on read |
| `csv`, `tsv` | **no** | RFC 4180 has one spelling for an absent value, and Go's `encoding/csv` does not preserve unquoted-empty vs quoted-empty on read |
| `excel` | **no** (write side only) | the writer already emits a genuinely blank cell for `nil` and an empty-string cell for `""`, so the artefact carries it; excelize's row API (`GetRows` / `Rows.Columns`) drops it, and the per-cell `GetCellType` probe that recovers it is O(rows) per call |
| `spss` | n/a | does not ride the row path — `io.CohortWriter` reads the bitmap itself |

A gap is documented and asserted, never silent, and its **direction is
part of the contract**: an adapter that cannot carry the distinction
reads BOTH states back as **null**. Losing an empty string into null
is the pre-existing behaviour and is recoverable from the source;
inventing an empty-string value where the cohort held a null would be
new data. `io/nullcell/` pins every row of that table, including the
three gaps — flipping an entry fails the matrix.

### Authoritative schemas: `io.SchemaAwareReader`

By default `ImportJob.Run` samples up to `SampleRows` rows and votes
on each column's type in `io/infer.go` — a guess made from
stringified cells. A format that carries its own dictionary (SPSS
`.sav`; in principle Parquet and Arrow) should not be guessed at.
Such a reader implements the optional `io.SchemaAwareReader`:

```go
type SchemaAwareReader interface {
    Reader
    PulseSchema() (*encoding.Schema, error)
}
```

It is the read-side mirror of `io.SchemaAwareWriter`: `ExportJob.Run`
pushes the `.pulse` schema *into* a writer via `SetPulseSchema`;
`ImportJob` pulls the `.pulse` schema *out of* a reader via
`PulseSchema`. Both `ImportJob.Run` and `ImportJob.Predict`
type-assert it, and a reader that does not implement it takes the
unchanged sample-then-infer path byte-for-byte.

**Implementer obligations.** The returned schema is written verbatim,
so every field must be complete: `Name`, `Type`, `Nullable`,
`CsvColumnIdx` (the index into the `[]string` row `ReadRows` yields —
fields are *not* positionally matched to the header), `Precision` /
`Scale` for `decimal128`, and a **pre-populated** `Dictionary` for
every type where `Type.HasDictionary()` is true. That dictionary is
the load-bearing slot: entry order *is* the on-wire encoding —
position `i` is the `categorical_*` ID and the `set_*` mask bit — so
handing over the source's own ordering is what preserves the source
codes. Leave it nil and the importer fills it in first-seen order
from cell text, which is exactly the re-guessing the interface exists
to prevent. Cell text still flows through the normal conversion, so
the dictionary is pre-seeded rather than sealed: a value absent from
it is appended, subject to the type's width limit. `set_*` cells must
arrive as tokens joined with `pio.DefaultSetDelimiter` (`"|"`).

**Precedence.** `ImportJob` carries four slots that exist only to
steer inference. With an authoritative schema there is nothing to
steer, so all four are inert:

| Slot | With an authoritative schema |
|---|---|
| `SampleRows` | inert — nothing to sample |
| `SetInferenceMinPct` | inert — no delimited-cell heuristic runs |
| `SetDelimiters` | inert — `DefaultSetDelimiter` is the fixed contract |
| `ColumnTypeOverrides` | inert — see below |

`ColumnTypeOverrides` is the managed-import `force_type` escape
hatch, and it loses deliberately. It is already documented as ignored
whenever a schema is supplied, and a reader-supplied schema is a
supplied schema; more importantly, forcing a type onto a
dictionary-carrying column would discard the source's category IDs /
mask bit positions and silently rebuild them in first-seen order. A
caller who genuinely needs a different type sets `ImportJob.Schema`
explicitly — an explicit schema wins outright and `PulseSchema` is
not even called.

**No null promotion.** `ImportJob.InferredSchema`'s out-of-sample-null
tolerance does not apply. An authoritative schema is not
inference-originated, so a null in a field the source declares
non-nullable is a genuine data error: it stays a
`PULSE_IMPORT_ROW_ERROR` and the field is never widened.
`ImportReport.PromotedFields` is always empty for such an import, and
setting `InferredSchema` alongside a `SchemaAwareReader` does not
re-enable promotion.

**Failure modes.** A non-nil error fails the import — never a quiet
fallback to inference, because a source that has a dictionary and
could not read it must not produce a differently-typed cohort from
re-guessed text. A `(nil, nil)` return is the deliberate opt-out
("this source carries no authoritative schema") and falls back to
inference, which then requires a `ResetReader` as usual. A schema
with no fields, or a field with a negative `CsvColumnIdx`, is a
malformed contract and fails the import.

`ConvertJob` consults this interface through the same precedence, via
the shared `readerSchema` resolver: an explicit `ConvertJob.Schema`
wins outright, otherwise an authoritative source schema is adopted
before inference is considered, and the intermediate `.pulse` file
`KeepPulseAt` writes is built from it. That matters because
registering an extension on `FromExt` immediately makes
`pulse convert source.ext out.csv` reachable — and a convert that
re-inferred types from the text the reader rendered would throw the
source dictionary away through a command the registration itself
created.

### Non-fatal diagnostics: `io.SourceWarningEmitter`

A reader whose parse can raise warnings that do not stop an import —
an unrecognised metadata record, a column mapped to a wider type than
the ideal, a declared row count that disagrees with the rows present —
implements the read-side peer of `OverlayWarningEmitter`:

```go
type SourceWarningEmitter interface {
    Reader
    Warnings() []*errors.CodedError
}
```

`ImportJob.Run`, `ImportJob.Predict` and `ConvertJob.Run` type-assert
it **after** the row pass (so progressively-discovered warnings are all
knowable) and lift the result onto `ImportReport.SourceWarnings` /
`PredictReport.SourceWarnings` / `ConvertReport.SourceWarnings`. The
CLI then routes those onto the `--json` envelope's `warnings` array and
prints `Warning [CODE]: message` lines on the text path.

Implementations must be pure accessors: calling `Warnings()` must not
itself trigger a parse, and calling it twice must not double the set.
Readers that do not implement the interface contribute `nil`, keeping
every pre-existing report byte-identical.

### Total failure is fatal, partial failure is not

A per-row refusal — your reader cannot convert a cell, or your writer
refuses a value — lands on `ImportReport.RowErrors` /
`ExportReport.RowErrors` and the job carries on. That is the PARTIAL
contract and it is unchanged.

What is not partial is a pass that read rows and got **none** of them
through. `ImportJob.Run`, `ExportJob.Run` and `ConvertJob.Run` classify
that as total failure and return a coded error with **no report**:
`PULSE_IMPORT_ROW_ERROR` / `PULSE_EXPORT_ROW_ERROR` (or the first row
error's own code when it carries one), with `rows_read`, `rows_failed`,
`first_row` and `first_error` in `Details`. Import additionally writes
no `.pulse` file, and convert writes no `KeepPulseAt` intermediate. A
source or cohort with **no rows at all** is an empty cohort and stays a
success.

Convert falls back to the EXPORT code by provenance: its row loop hands
raw cell text through and never objects to it, so every `RowError` it
can record came out of your `WriteRow`.

Convert has one refusal of its own, and it is not row-shaped. A
categorical dictionary that fills its rung mid-pass raises
`PULSE_IMPORT_CATEGORICAL_OVERFLOW` — the IMPORT-half code, by the same
provenance rule, since the overflow happens reading the source — and
`ConvertJob.Run` stops there with no report and no `KeepPulseAt`
intermediate. It is fatal rather than partial because capacity is not a
property of the row: past the limit every further unseen category is
dropped for the rest of the file, which used to leave a `ConvertReport`
whose schema carried a silently short dictionary.

This matters when you write an adapter because a format-wide mistake —
a column type your writer has no mapping for, a cell shape your reader
never parses — fails identically on every row. Before the verdict
existed, three such bugs shipped looking like successful zero-row runs.
Your adapter needs no code for this; just do not swallow the per-row
error, because the verdict is computed from the row-error slice.

### Source metadata with no Pulse home: `io.SidecarEmitter`

A source format may declare things a `.pulse` file has nowhere to put.
An SPSS dictionary is the motivating case: measure levels, print
formats, arbitrary value codes, missing-value specifications, declared
string widths, multiple-response sets, document records and a source
charset are all load-bearing for a round trip, and none of them fits a
9-byte header, a schema block or a one-bit-per-field null bitmap.

Such a reader implements the third optional interface:

```go
type SidecarEmitter interface {
    Reader
    WriteSidecar(fs afero.Fs, cohortPath string) error
}
```

`ImportJob.Run` type-asserts it and calls it **after** the cohort has
been written. The ordering is load-bearing: an implementation is
expected to fingerprint the cohort it describes, which requires the
cohort's bytes to exist. `fs` is `ImportJob.FS` and `cohortPath` is
`ImportJob.Target`, so the sidecar lands on the same filesystem as the
cohort and `fs.NewMemMap()` stays hermetic — never reach for `os`.
`ConvertJob`'s `KeepPulseAt` path delegates to `ImportJob.Run`, so it
inherits the hook with no extra wiring.

Four conventions an implementation should follow:

1. **Name it by suffix**, per the `imports.Sidecar` convention —
   append to the cohort filename rather than replacing its extension,
   so cohort and sidecar stay adjacent and sort together. Pick a suffix
   that does not collide with `imports.SidecarSuffix` (`.meta.json`),
   which a managed import writes for the same cohort.
2. **Version the document** and carry a `kind`, so a reader can reject
   one written by something else before trusting its shape.
3. **Carry a fingerprint block** modelled on `encoding.Index`: a
   32-byte SHA-256 plus `SourceSize` (u64) and `SourceModTime` (i64
   Unix ns), over the **cohort** — never over the original source file,
   which may be long gone. The read path then does an O(1) size+mtime
   stat and reserves the full hash for a verify pass. The policy that
   pairs with it: an **absent** sidecar is a warning (a cohort that was
   never derived from that format correctly has none), a **stale** one
   is an error (a stale dictionary over changed data yields a file that
   looks authoritative and is wrong).
4. **Keep the metadata payload flat and self-contained** — no
   filesystem paths, no byte offsets into the source, no references to
   anything outside itself — and hold it in a slot separate from the
   fingerprint. A `.pulse` schema metadata block would delete the
   sidecar entirely but needs a `FormatVersion` bump; a payload shaped
   this way can be lifted into one verbatim if that ever lands.

Build the document with `encoding/json` — never `fmt.Sprintf`. A
returned error **fails the import**: the cohort write on the same
filesystem has just succeeded, so a sidecar write that then fails is a
genuine fault, and a cohort silently missing the only surviving record
of its source dictionary is exactly the quiet degradation the sidecar
exists to prevent. Readers that do not implement the interface write no
extra file and take no extra stat, keeping every other format's import
byte-identical.

### Encoding from raw storage: `io.CohortWriter`

`ExportJob.Run` decodes a cohort, renders each record into `[]any`, and
hands the row to `Writer.WriteRow`. For most targets that is exactly
right. For one class of target it is not, and the mismatch is not close
enough to fake.

`io/spss` is the case that forced the interface. A `.sav` variable's
on-wire value is derived from a categorical's dictionary **ID**, a
`set_*`'s mask **bits** and the **null bitmap** — and every one of those
is gone by the time a row has been rendered. `formatFieldValue` resolves
a categorical to its label text (and two SPSS codes may legitimately
share one label), and a null renders as `""`, which a string categorical
can hold as a real value. Rebuilding the storage from the text would be
a guess in exactly the places fidelity is decided.

```go
type CohortSource struct {
    FS       afero.Fs // ExportJob.FS — a hermetic MemMapFs export stays hermetic
    Path     string   // the `.pulse` cohort path; also where a format sidecar rides
    Includes []string // ExportJob.Includes verbatim
    Labelled bool     // a label resolver is rewriting or augmenting cells
}

type CohortWriter interface {
    Writer
    WriteCohort(ctx context.Context, src CohortSource) (int, error)
}
```

**Dispatch.** `ExportJob.Run` type-asserts the interface **after**
`SetPulseSchema` and **after** `WriteHeader` — so a cohort writer still
receives both, and the header is still the projection-aware column list —
and then calls `WriteCohort` *instead of* its own row loop. Nothing is
double-decoded: the row loop does not run, and `WriteRow` is never
called on that path. Writers that do not implement it take the unchanged
row path, byte for byte.

**`Includes` and `Labelled` are carried so you can REFUSE them,** not
because a cohort writer is expected to implement them. A writer that
silently ignored `Includes` would answer `pulse export spss --include age`
with a file carrying every column, which is the quiet wrong answer this
whole surface exists to avoid. Return a coded error naming the option
instead. The returned count becomes `ExportReport.RowsExported`.

**A writer can implement both paths.** `io/spss` does: `WriteCohort` for
an export whose source is a cohort, and a buffering `WriteRow` for
`pulse convert data.csv out.sav`, where no cohort exists — it collects
the rows, builds an intermediate cohort in memory through the ordinary
import path, and encodes that. Buffering rather than streaming is
inherent: an intermediate cohort cannot be written until the last row has
been seen.

### What the row path lost: `io.SourceAwareWriter`

A target that rebuilds a cohort from the row stream re-derives a schema
by **inferring** it from the text the source just rendered, and inference
cannot recover what the source *declared*. Two things went missing on
`pulse convert survey.sav out.sav` because of it, and neither was
recoverable downstream:

- the **declared schema**. A `set_*` column round-trips as `"Q1A|Q1C"`
  tokens, so a re-inferred rung is only as wide as the options somebody
  actually ticked; a value-labelled numeric came back as a string column
  of bare numerals.
- the **metadata sidecar**. It is the only home of the code / label /
  dictionary-ID triple *and* of the derived-column registry, so the
  rebuilt cohort could not tell a synthesised multiple-dichotomy `set_*`
  column from a real one. `io/spss` then expanded it into member
  variables that collided by name with the constituents already in the
  cohort — `PULSE_SPSS_NAME_COLLISION`, which refused every
  multiple-dichotomy convert outright.

```go
type ConvertSource struct {
    Schema  *encoding.Schema // the source's DECLARED schema, or nil
    Sidecar SidecarEmitter   // the source Reader itself, or nil
}

type SourceAwareWriter interface {
    Writer
    SetConvertSource(src ConvertSource)
}
```

`ConvertJob.Run` type-asserts it and calls `SetConvertSource` **before**
`WriteHeader`; a target that does not implement it is never consulted and
its convert output is byte-identical to the pre-interface shape.

**What you may assume, and what you may not.** The facts are offered only
when the row stream is *faithful* to the schema — one cell per field, in
field order, with no `Includes` projection and no label binding inserting
or rewriting cells — so `Schema.Fields[i]` describes emitted column `i`
and `CsvColumnIdx` is `i`. `Schema` is nil when convert **inferred** the
schema itself: there is nothing declared to preserve, and re-inferring is
the same guess over the same values. `Sidecar` is the source `Reader`, so
write it against the cohort **you** just built (`WriteSidecar(fs, path)`
re-fingerprints the document); the source's own copy is fingerprinted
over the source cohort and a read path would correctly refuse it as
stale.

This is deliberately *not* `SchemaAwareWriter`. `SetPulseSchema` means
"here is the cohort you are exporting from" and the typed-column adapters
answer it by changing what they emit; this one means "here is what your
source declared, for the cohort you are about to rebuild".

### Encode-side diagnostics: `io.TargetWarningEmitter`

The write-side mirror of `SourceWarningEmitter`, and distinct from
`OverlayWarningEmitter`, which answers only the narrower question "were
overlay layers dropped?":

```go
type TargetWarningEmitter interface {
    Writer
    Warnings() []*errors.CodedError
}
```

`ExportJob.Run` and `ConvertJob.Run` collect **after** the write pass and
lift the result onto `ExportReport.TargetWarnings` /
`ConvertReport.TargetWarnings`. Implementations must be pure accessors —
calling `Warnings()` must not itself trigger work, and calling it twice
must not double the set. Writers implementing neither contribute `nil`,
so every pre-existing report stays byte-identical.

The canonical user is again `io/spss`, whose encode raises diagnostics
that do not stop an export but change what the file MEANS: a metadata
sidecar that was absent or deliberately ignored (so the dictionary was
*synthesised* rather than reproduced), and every variable rename
`--sanitize-names` performed. A user who never sees those cannot tell a
faithful re-emission from a reconstruction.

> **Re-read after `Close`.** `internal/cli/export.go` deliberately calls
> `Warnings()` again after `writer.Close()` rather than trusting
> `report.TargetWarnings`. A writer that buffers and encodes at `Close` —
> which the `.sav` row path does — has raised none of its diagnostics by
> the time the job builds its report. Because the accessor is pure,
> asking twice cannot double the set.

### Releasing without emitting: `io.DiscardableWriter`

Most adapters need nothing here, and the absent implementation is the
correct answer.

```go
type DiscardableWriter interface {
    Writer
    Discard() error
}
```

The io CLI leaves deliberately do **not** call `writer.Close()` when the
job returns an error. Every adapter buffers its output and emits it
exactly once inside `Close` (`afero.WriteFile`, not an incrementally
written handle), so skipping `Close` writes *nothing* — the correct
outcome for a hard failure. Adding a `defer writer.Close()` would put a
zero-row target next to the error instead, which is precisely the
silent-success trap the total-failure verdict exists to close.
`TestExportTargets_EmitNothingBeforeClose` pins that property across all
eight adapters, header **and** rows.

That reasoning is about DATA. Resources are a separate question, and one
adapter answers it differently: `io/excel` drives an excelize
`StreamWriter` whose buffer spills to an `os.CreateTemp` file past
`excelize.StreamChunkSize` (16 MiB), and only `excelize.File.Close`
removes those files. `Discard` is the release-without-emitting half of
`Close` — drop the buffers and any temp files, write nothing, and leave
the writer inert so a later `Close` also writes nothing.

`internal/cli/export.go` and `internal/cli/convert.go` run
`pio.DiscardWriter(writer)` on every error return. It is a no-op for a
writer that does not implement the interface, and it never falls back to
`Close`. Implement `Discard` only if your writer holds something the Go
GC cannot reclaim; if you buffer on the heap, do nothing.

### Predicting a refusal: `io.CohortValidator`

`pulse export predict` was target-blind. `ExportJob.Predict` read the
source header and schema, estimated the row count, and answered "this
export is fine" regardless of the target — which was harmless only for
as long as every writer was infallible at the target boundary. The text
adapters stringify anything handed to them. `io/spss` is the first
writer that can REFUSE, so predict was saying yes to exports that then
failed.

```go
type CohortValidator interface {
    Writer
    ValidateCohort(ctx context.Context, src CohortSource) ([]*errors.CodedError, error)
}
```

The returned slice is the non-fatal diagnostics the real export **would**
raise; they land on `PredictReport.TargetWarnings`, the symmetric peer of
`ExportReport.TargetWarnings`. A non-nil error is the refusal the real
export **would** return, and it must carry the same code the export
itself carries — a predicted `PULSE_SPSS_NAME_INVALID` that exported as
something else would be worse than no prediction at all.

**Dispatch.** `ExportJob.Predict` type-asserts the interface. A Target
that is nil, or one that does not implement it, is predicted **exactly**
as it was before the interface existed: no extra read, no new failure
mode, the same `PredictReport`. That is the whole compatibility contract
and `TestExportJob_Predict_NonValidatingTargetUnchanged` pins it.

Unlike `CohortWriter`, a validator is reached **without** `SetPulseSchema`
and **without** `WriteHeader` — predict starts no write lifecycle on a
writer it will never `Close`. Everything a validator needs rides
`CohortSource`: `FS` + `Path` locate the cohort and any format sidecar
beside it, and `Includes` / `Labelled` carry the row-stream
transformations so they can be refused here on the same terms
`WriteCohort` refuses them.

> **A validator may never refuse something the real export would accept.**
> A false refusal blocks work that would have succeeded and the caller
> has no way to appeal it. Where a verdict needs a record — a value whose
> width overflows, a character the target charset cannot encode, a
> dictionary ID with no source code behind it — warn, or stay silent.
> Never guess. Predict is therefore a **sound but incomplete** filter.

The refusal set is mostly reachable without records precisely because a
`.pulse` cohort's records are fixed-width numerics: every string lives in
the schema block's dictionaries. Name legality, charset encodability of
the dictionary text, sidecar state, derived-column foldability and the
`Includes` / `Labels` refusals are all schema + sidecar facts.

**Implement it by re-running the write path's own checks, not by
re-stating them.** `io/spss` splits its encode at the last point before
the first record is read — `planCohort` returns the sidecar resolution,
the built dictionary and a bound encoder — so `WriteCohort` goes on to
the data pass and `ValidateCohort` closes the file and reports. One
implementation, two callers; a check that moves cannot move in only one
of them. Validation must also have no observable side effect: no output
file, no mutation of the writer's own encode state, and safe to call
before, after or instead of a write pass.

**CLI wiring.** `internal/cli/export.go`'s predict leaf builds the target
through `newWriterForFormat` against a **MemMapFs and a throwaway path**,
and never `Close`s it, so no adapter's bytes can reach any filesystem.
It mounts the target format's write flags too (`--sanitize-names` turns a
`.sav` name refusal into a warning, so a predict that could not be told
about it would refuse an export that would have succeeded), and with no
`--format` it keeps the old target-blind behaviour and says so in the
output.

## 5. Skill and doc update

Add or update a skill that points users at the new format. Cohort-
schema considerations (field-type round-trip, dictionary behaviour,
null markers) belong in `skills/cohort-schema-design.md`.

`skills/tool-import.md` carries the `format` enum the MCP tool accepts
and must list the new identifier.

Add a user-facing mdBook page under `docs/src/cli/` for the new leaf
(`docs/src/cli/import-spss.md` is the worked model — synopsis, type
mapping, flags and which of them the format ignores, the coded warning
and error tables, a real failure transcript) and register it in
`docs/src/SUMMARY.md`. Nothing gates this; it is missed unless written
deliberately.

`skills/session-bootstrap.md` only needs touching if the format adds a
**CLI flag** (as `--sheet` did for Excel, and as the four `.sav` write
knobs did for SPSS). Registering a new `pulse import <fmt>` subcommand
alone does not: that file is the MCP session-order guide and carries no
format list.

A new leaf **does** need a row in the command index in
`docs/src/cli/flags.md`. `TestSkillsCoverAllCliLeaves`
(`cmd/pulse/cli_leaves_test.go`) walks the real `buildApp()` command tree
and fails if any runnable command path is named nowhere under `skills/`
or `docs/src/`. It checks naming only — whether the prose is any good
stays the author's judgement.

## 6. Convert and orchestration plumbing

Make sure both directions flow through `pio.ImportJob` and
`pio.ExportJob`. The orchestration layer is format-agnostic; you
should not need to touch `service/` unless the new format requires
special metadata (e.g., Parquet's per-column statistics).

## 7. Run the gates

```bash
go test ./io/<format>/...
go test ./skills/ -run TestSkillsCoverAll
go test ./...
```

For format-specific perf, add benchmarks (`Benchmark<Format>...`) in
the sub-package. There's no required perf gate today, but neighbouring
formats have benchmarks you can mirror as a baseline.

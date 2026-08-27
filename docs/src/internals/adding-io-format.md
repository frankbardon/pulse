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

## 2. Tests

Add `io/<format>/<format>_test.go` with the standard round-trip
checks: write rows, read them back, verify equality. Hermetic tests
should use `afero.NewMemMapFs()` — see [Testing
Conventions](../contributing/testing.md).

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
**CLI flag** (as `--sheet` did for Excel). Registering a new
`pulse import <fmt>` subcommand alone does not: that file is the MCP
session-order guide and carries no CLI leaf or format list. There is no
automated CLI-leaf coverage gate today, so this judgement is the
author's — see the follow-up note in `.claude/reference/update-demand.md`.

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

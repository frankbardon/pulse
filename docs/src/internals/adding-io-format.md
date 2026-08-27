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

## 3. Wire it into the CLI

The CLI registers per-format leaves in `internal/cli/import.go` and
`internal/cli/export.go`. Add the format string to:

- The switch in `makeImportReader(format, ...)` in `import.go`.
- The corresponding `newWriterForFormat(format, ...)` switch in
  `export.go`.
- The `Commands:` slice on `ImportCommand()` and `ExportCommand()`
  in the same files (one `importFormatCmd("yourformat")` /
  `exportFormatCmd("yourformat")` line).

The `pulse convert` leaf auto-detects format from extension via
`formatFromExt`; add the extension mapping if the new format has a
canonical file extension.

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

`ConvertJob` does not consult this interface; its import half runs
off the schema `ConvertJob` itself resolved.

## 5. Skill update

Add or update a skill that points users at the new format. Cohort-
schema considerations (field-type round-trip, dictionary behaviour,
null markers) belong in `skills/cohort-schema-design.md`.

If the format adds a CLI flag (e.g. `--sheet` for Excel), update
`skills/session-bootstrap.md` so `TestSkillsCoverAllCliLeaves` keeps
passing.

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

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

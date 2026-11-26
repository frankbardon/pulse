# Readers, Writers and Jobs (`io`)

**Audience:** embedders moving tabular data in and out of Pulse from Go.
The package is [`github.com/frankbardon/pulse/io`](https://pkg.go.dev/github.com/frankbardon/pulse/io),
imported as `pio` so it does not shadow the standard `io`.

## When to use it

Use the factory whenever a program, not a user at a shell, picks the
format: a service receiving uploads, a pipeline converting a nightly
drop. It is what `pulse import`, `export` and `convert` call.

| Piece | What it does |
|---|---|
| `pio.Format` constants, `pio.Formats()`, `pio.FormatFromPath(path)` | Name a format (`FormatCSV`, `FormatParquet`, `FormatSPSS`, …) or detect one from an extension. |
| `pio.NewReader(f, fs, path, opts)` / `NewReaderFromBytes(f, data, opts)` | A source of rows, from a file on an `afero.Fs` or from memory. |
| `pio.NewWriter(f, fs, path, opts)` / `NewWriterToBuffer(f, opts)` | A sink, to a file or to a buffer (`Close` flushes it). |
| `pio.NewImportJob(reader, target)` → `Pulse.Import` | Tabular → `.pulse`; `job.Schema` pins the types, else they are inferred. |
| `pio.NewExportJob(source, writer)` → `Pulse.Export` | `.pulse` → tabular; `job.Includes` picks columns, `job.Labels` applies label tables. |
| `pio.ConvertJob` → `Pulse.Convert` | Tabular → tabular, with `.pulse` as the transparent middle. |

A job's `FS` names the filesystem its cohort path resolves on; set it to
the instance's (`p.Fs()`) unless you mean another.

## Upload bytes to a cohort

```go compile
package upload

import (
	"context"

	"github.com/frankbardon/pulse"
	pio "github.com/frankbardon/pulse/io"
)

// Store imports an uploaded file, format picked by its name, into
// target on the instance filesystem.
func Store(ctx context.Context, p *pulse.Pulse, name string, data []byte, target string) (int, error) {
	src, err := pio.NewReaderFromBytes(pio.FormatFromPath(name), data, pio.ReaderOptions{})
	if err != nil {
		return 0, err
	}
	job := pio.NewImportJob(src, target)
	job.FS = p.Fs()
	rep, err := p.Import(ctx, job)
	if err != nil {
		return 0, err
	}
	return rep.RowsImported, nil
}
```

## Where the detail lives

- Per-format options (CSV delimiter, sheets, time zones, SPSS labels)
  are `ReaderOptions` / `WriterOptions` fields; their semantics match
  the CLI flags on [CLI concepts & shared flags](../cli/flags.md) and
  [`import spss`](../cli/import-spss.md).
- Building a cohort row by row instead of from a file:
  [Reading & Building Cohorts](cohort-reader.md).
- A custom backend for the files themselves:
  [Custom Filesystems](custom-fs.md). A new format:
  [Adding an I/O Format](../internals/adding-io-format.md).
- GoDoc: [`io`](https://pkg.go.dev/github.com/frankbardon/pulse/io).

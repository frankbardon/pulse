# Go API Overview

**Audience:** Go developers embedding Pulse in a binary or a service.

Pulse is library-first. The CLI in `cmd/pulse/` is a thin adapter
around the package documented here. If you're reaching for `os/exec`
to shell out to the binary from Go, stop and use the library directly
— you'll skip a process boundary and gain typed responses.

> **LLM agents using MCP:** there is no LLM-facing skill that covers
> Go embedding directly. Agents speak MCP; this page is for the
> programs that host them.

## Module path

```go
import "github.com/frankbardon/pulse"
```

Sub-packages you'll commonly touch:

| Package | Purpose |
|---|---|
| `github.com/frankbardon/pulse`            | Public facade (`Pulse`, `Options`, `Request`, `Response`, `Ask`, ...) |
| `github.com/frankbardon/pulse/types`      | Request/response structs, component-type constants (`AGG_*`, ...) |
| `github.com/frankbardon/pulse/io`         | Tabular adapter interfaces (`Reader`, `Writer`, `ImportJob`, `ExportJob`, `ConvertJob`) |
| `github.com/frankbardon/pulse/io/<fmt>`   | Per-format readers/writers (`csv`, `tsv`, `ndjson`, `jsonarray`, `parquet`, `arrow`, `excel`) |
| `github.com/frankbardon/pulse/fs`         | `afero`-backed filesystem config (`fs.New`, `fs.Default`, `fs.NewMemMap`) |
| `github.com/frankbardon/pulse/errors`     | Typed `CodedError` system and code constants |
| `github.com/frankbardon/pulse/descriptor` | Manifest, predict, inspect (no-execute operations) |
| `github.com/frankbardon/pulse/synth`      | Synthetic data generator and profile types |
| `github.com/frankbardon/pulse/skills`     | Embedded skill pack — `skills.List()`, `skills.Get(name)` |

The `internal/` subtree (`internal/cli`, `internal/mcp`,
`internal/query`) is exactly that — internal. Don't import it.

## The facade

Construct a `Pulse` once per process (or per filesystem boundary) and
re-use it:

```go
p, err := pulse.New(pulse.Options{
    DataDir: "/var/data/pulse",
})
if err != nil {
    return err
}
```

The full `Options` shape (custom `afero.Fs`, smart-default toggling)
is documented at [pulse.New & Options](options.md).

## Public methods

From [`pulse.go`](https://github.com/frankbardon/pulse/blob/main/pulse.go):

| Method | Purpose |
|---|---|
| `Open(ctx, path) (*Cohort, error)` | Read header + schema, return a typed Cohort handle |
| `Process(ctx, req) (*Response, error)` | Execute one request |
| `ProcessStream(ctx, req) (RowIter, error)` | Same, pull-based iterator over result rows |
| `Compose(ctx, req) ([]*Response, error)` | Execute a batch sequentially |
| `ComposeParallel(ctx, req, opts) ([]*Response, error)` | Execute a batch in parallel with a worker pool |
| `Ask(ctx, askReq) (*AskResponse, error)` | Unified entry: predict + (optionally) process, with natural-language query support |
| `Import(ctx, job) (*ImportReport, error)` | Tabular → `.pulse` |
| `Export(ctx, job) (*ExportReport, error)` | `.pulse` → tabular |
| `Convert(ctx, job) (*ConvertReport, error)` | Tabular → tabular, with `.pulse` as the transparent middle |
| `Inspect(ctx, path) (*InspectResult, error)` | Read header + schema only (no record data) |
| `Predict(ctx, req) (*PredictResult, error)` | Validate a request without executing |
| `Sample(ctx, path, n) ([]Record, error)` | Up to n rows |
| `Facet(ctx, path, field) ([]string, error)` | Distinct values of a field |
| `Synth(ctx, spec, out, opts) (*SynthResult, error)` | Generate a synthetic cohort |
| `Profile(ctx, path, opts) (*Profile, error)` | Statistical summary suitable for `from-profile` synthesis |
| `Manifest(ctx) *Manifest` | Deterministic root self-description |
| `Fs() afero.Fs` | The underlying filesystem (used by `pulse mcp` and other embedders) |

Re-exported type aliases let you write `pulse.Request` instead of
`types.Request`:

```go
type (
    Request         = types.Request
    Response        = types.Response
    ComposedRequest = types.ComposedRequest
    SynthSpec       = synth.Spec
    Profile         = synth.Profile
    // … and so on
)
```

## Minimum viable embed

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/frankbardon/pulse"
    "github.com/frankbardon/pulse/types"
)

func main() {
    ctx := context.Background()

    p, err := pulse.New(pulse.Options{DataDir: "/var/data/pulse"})
    if err != nil {
        log.Fatal(err)
    }

    resp, err := p.Process(ctx, &pulse.Request{
        Cohort: &types.Cohort{Filename: "sales.pulse"},
        Aggregations: []*types.Aggregation{
            {Type: types.AGG_AVERAGE, Field: "revenue", Label: "avg_revenue"},
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(resp.Data)
}
```

## Where to go from here

- [pulse.New & Options](options.md) — full `Options` reference.
- [pulse.Ask Unified Entry Point](ask.md) — the one-shot facade
  the MCP server uses internally.
- [Custom Filesystems](custom-fs.md) — in-memory testing pattern,
  custom storage backends.
- [Streaming & ProcessStream](streaming.md) — pull-based iteration,
  what streams vs what buffers.
- [Parallel Compose](parallel-compose.md) — worker pool, fail-fast,
  per-request timeout.

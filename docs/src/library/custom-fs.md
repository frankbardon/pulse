# Custom Filesystems

**Audience:** Go embedders running Pulse in tests (hermetic, no
disk), in cloud-storage-backed environments (S3, GCS, Azure Blob via
afero), or behind a custom storage layer.

Pulse routes all file I/O through `afero.Fs`. Pass any
`afero.Fs`-conformant filesystem to `pulse.New(pulse.Options{FS:
...})` and Pulse never touches the OS filesystem directly.

> **LLM agents using MCP:** the MCP server's filesystem is fixed at
> startup via `PULSE_DATA_DIR` or `--data-dir`. Agents don't swap
> filesystems mid-session.

## In-memory testing pattern

The single most common reason to override the filesystem is hermetic
tests. Pass an in-memory afero filesystem directly:

```go
import (
    "github.com/frankbardon/pulse"
    "github.com/spf13/afero"
)

func TestSomething(t *testing.T) {
    p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
    if err != nil {
        t.Fatal(err)
    }

    // Write a .pulse file into the in-memory FS, then process it.
    // ...
}
```

The in-memory FS persists for the life of the FS reference. Create a
fresh one per test for isolation.

## Custom storage backends

Anything that implements `afero.Fs` works. Common patterns:

- **S3 / GCS / Azure Blob** — via community afero adapters
  (`afero/gcsfs`, `afero/s3`).
- **Encrypted overlays** — wrap a base FS with envelope encryption
  per file.
- **Read-only mounts** — `afero.NewReadOnlyFs(base)` for production
  cohort serving where mutation is by accident, not policy.

Example with a hypothetical S3 wrapper:

```go
import (
    "github.com/frankbardon/pulse"
    "example.com/myorg/aferos3"
)

func main() {
    s3fs := aferos3.New(aferos3.Config{
        Bucket: "my-pulse-cohorts",
        Region: "us-east-1",
    })
    p, _ := pulse.New(pulse.Options{FS: s3fs})
    // p reads and writes cohort files from S3 transparently.
}
```

## Filesystem configuration

`pulse.Options` is the only filesystem knob an embedder needs:
`FS` (any `afero.Fs`) and `DataDir` (the base directory; when both are
empty, `PULSE_DATA_DIR` is read from the environment). The lower-level
config constructors live in `internal/fs/` and are not importable from
outside the module. `(*pulse.Pulse).Fs()` returns the filesystem the
instance resolved, which is what to hand the `io` factory
(`io.NewReader(format, p.Fs(), path, opts)`).

## Path resolution

Pulse resolves a `Cohort` to a path with this rule (see
`resolveCohortPath` in `pulse.go`):

```
if cohort.DataDir != "" → "<DataDir>/<Filename>"
else                    → "<Filename>"
```

The custom FS is then asked to open that path. For an
`afero.MemMapFs`, an absolute-looking path like
`/var/data/sales.pulse` is just a key in the in-memory map — no need
to mirror the OS layout.

## What custom filesystems do NOT do

- Pulse never falls back to `os.Open` if the custom FS fails. The
  custom FS is the only filesystem; if it errors, that error
  propagates verbatim.
- The MCP server (`pulse mcp`) currently uses `afero.NewOsFs()` only.
  Custom filesystems are a library-side capability today.
- The Go race detector and `go test -race` work normally with
  in-memory filesystems; tests can run highly concurrent without
  fighting over a real directory.

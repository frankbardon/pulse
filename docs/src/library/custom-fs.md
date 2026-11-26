# Custom Filesystems

**Audience:** Go embedders running Pulse in tests (hermetic, no
disk), in cloud-storage-backed environments (S3, GCS, Azure Blob via
afero), or behind a custom storage layer.

Pulse routes all file I/O through `afero.Fs`. Pass any
`afero.Fs`-conformant filesystem to `pulse.New(pulse.Options{FS:
...})` and Pulse never touches the OS filesystem directly.

## In-memory testing pattern

The most common reason to override the filesystem is hermetic tests:

```go compile
package hermetic

import (
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

func TestSomething(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	_ = p // import or build a cohort into p.Fs(), then process it
}
```

An in-memory FS lives as long as the reference to it; create a fresh
one per test for isolation. Tests over it run concurrently without
sharing a real directory.

## Custom storage backends

Anything that implements `afero.Fs` works:

- **Cloud object stores** — afero's `gcsfs`, or a community S3 / Azure
  adapter.
- **Encrypted overlays** — wrap a base FS with per-file envelope
  encryption.
- **Read-only serving** — `afero.NewReadOnlyFs(base)` makes an accidental
  write fail instead of mutating production cohorts.
- **Jailing** — `afero.NewBasePathFs(base, root)` confines every path
  under `root`.

```go compile
package readonly

import (
	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// Serve opens cohorts under root on disk, refusing every write.
func Serve(root string) (*pulse.Pulse, error) {
	fs := afero.NewReadOnlyFs(afero.NewBasePathFs(afero.NewOsFs(), root))
	return pulse.New(pulse.Options{FS: fs})
}
```

## Filesystem configuration

`pulse.Options` holds the only two filesystem knobs: `FS` (any
`afero.Fs`) and `DataDir` (the base directory; when both are empty,
`PULSE_DATA_DIR` is read from the environment). `(*pulse.Pulse).Fs()`
returns the filesystem the instance resolved; hand it to the `io`
factory (`pio.NewReader(format, p.Fs(), path, opts)`) and to job `FS`
fields.

## Path resolution

A request's `cohort` resolves to `<data_dir>/<filename>` when the cohort
names a `data_dir`, else to `<filename>`. The FS is then asked to open
that path. On an `afero.MemMapFs` an absolute-looking path like
`/var/data/sales.pulse` is just a key; there is no need to mirror the
OS layout.

## What custom filesystems do NOT do

- Pulse never falls back to `os.Open`. If the custom FS errors, that
  error propagates.
- The stock `pulse mcp` binary serves the OS filesystem only. To serve
  a custom FS over MCP, build the instance with it and serve it with
  `mcpserve` or `mcp/gosdk` ([Serving MCP](mcp-serving.md)).

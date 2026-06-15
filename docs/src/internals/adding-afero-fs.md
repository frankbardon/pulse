# Adding an `afero.Fs` Implementation

**Audience:** Pulse internals contributors and embedders wiring a new
filesystem backend (a copy-on-read overlay caching remote objects, a
base-path wrapper, any prefix-translating shim) under `pulse.Options.FS`.

The Pulse iterator engages an `mmap` fast path when the cohort path
ultimately resolves to a real on-disk file. The eligibility probe
lives in `service/fs_probe.go` and is documented inline; the gist:
implement the `service.RealPather` capability interface so the probe
can ask your fs "where is this file on disk?" without opening it.

## The `RealPather` contract

```go
// service.RealPather — defined in service/fs_probe.go.
type RealPather interface {
    RealPath(name string) (string, error)
}
```

The returned path MUST be `os.Open`-able at the moment of the call,
and the bytes it returns MUST be identical to what
`fs.Open(name).Read` would yield. The signature deliberately matches
`*afero.BasePathFs.RealPath` so that wrapper is detected
automatically.

## Why this matters

`service.resolveRealPath` is the eligibility probe that decides
whether the streaming iterator engages the `mmap` fast path. Without
`RealPather`, the probe falls through to the `*afero.OsFs` check and
then to the `afero.ReadFile` slow path.

**Failure to implement this interface silently disables the mmap
optimisation** — no error, no warning, just a regression in scan
throughput on cold-cache wide cohorts. The mmap policy and probe
order are documented in the [Cohort schema design
skill](https://github.com/frankbardon/pulse/blob/main/skills/cohort-schema-design.md)
("Iterator mmap policy"); the rationale for omitting an open-and-
inspect fallback is inline at `service/fs_probe.go`.

## The regression gate

The `countingFs` test family in `service/` (e.g. `TestCountingFs_*`)
is the regression gate. It wraps an fs and fails the test if `Process`
calls `afero.ReadFile` on a single-file cohort path when the fs
advertises a real path. If you add a new wrapper and the gate flips
red, the wrapper is almost certainly missing `RealPather`.

## Lazy-materialising backends

For caches that materialise the file lazily (copy-on-read overlays),
advertise `RealPath` only after the local copy is on disk. Return a
non-nil error during the in-flight download window so the probe
declines and the iterator falls back to `afero.ReadFile` for that
call.

Hermetic tests that need to exercise the non-mmap path keep using
`fs.NewMemMap()` — `MemMapFs` does not satisfy `RealPather` and the
probe correctly declines.

## Run the gate

```bash
go test ./service/ -run TestCountingFs
```

# Watch & WatchDir

**Audience:** Go embedders building reactive systems on top of Pulse —
caches that invalidate on source change, UI layers that re-fetch when
the data updates, batch jobs that fire on file appearance.

`Pulse.Watch` and `Pulse.WatchDir` observe `.pulse` files for changes
and emit `ChangeEvent` records on a channel. The polling watcher
coalesces atomic-write patterns (write-temp + rename) into single
events so consumers don't see spurious intermediate states.

## Shape

```go
type ChangeEvent struct {
    Path      string
    Kind      ChangeKind  // Created | Modified | Removed | Renamed
    Hash      string      // SHA-256 hex of leading bytes; empty for Removed
    Timestamp time.Time
}

type ChangeKind int
const (
    ChangeCreated ChangeKind = iota
    ChangeModified
    ChangeRemoved
    ChangeRenamed
)
```

## Default usage

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// Single-file watch.
ch := p.Watch(ctx, "cohort.pulse")

// Directory watch (recursive, .pulse files only).
ch := p.WatchDir(ctx, "cohorts/", true)

for ev := range ch {
    switch ev.Kind {
    case pulse.ChangeCreated:
        log.Printf("new cohort %s (hash=%s)", ev.Path, ev.Hash)
    case pulse.ChangeModified, pulse.ChangeRenamed:
        invalidateCache(ev.Path)
    case pulse.ChangeRemoved:
        cache.Delete(ev.Path)
    }
}
```

Cancelling the context closes the channel and releases the watcher.

## WatchOptions

```go
type WatchOptions struct {
    PollInterval    time.Duration  // Default 250ms
    CoalesceWindow  time.Duration  // Default 100ms (0 disables coalescing)
    HashPrefixBytes int            // Default 64 KiB (< 0 hashes entire file)
    Recursive       bool           // WatchDir-only
    Suffix          string         // Default ".pulse" for WatchDir, "" for Watch
}

ch := p.WatchDirWithOptions(ctx, "cohorts/", pulse.WatchOptions{
    PollInterval:    1 * time.Second,
    HashPrefixBytes: -1,  // hash full file
    Recursive:       true,
    Suffix:          ".pulse",
})
```

### Tuning notes

- **`PollInterval`** — stat-poll cadence. The default 250ms is fine
  for local filesystems. Network filesystems should raise this to
  ~30s; on remote storage the stat call dominates over any latency
  fsnotify-style edge events would save.
- **`CoalesceWindow`** — events inside this window collapse to one.
  Critical for atomic-write patterns (see below).
- **`HashPrefixBytes`** — how much of the file's content participates
  in the hash. Default 64 KiB covers a `.pulse` file's header +
  schema + first dictionary block, enough to detect any structural
  change. Use `-1` when you need full-file hashing for small files
  where prefix collisions matter.
- **`Suffix`** — filter directory contents to files ending in this
  suffix. `WatchDir` defaults to `.pulse`. Set to `""` to watch every
  file in the directory.

## Atomic-write coalescing

Most tools that write `.pulse` files do so atomically — write to a
temp path, then rename into place. Without coalescing, a naive
watcher would see two events: a `Removed(temp)` and a
`Created(target)`. `WatchDir` folds these into a single
`ChangeRenamed(target)` event when:

- Both paths share a directory.
- The temp basename matches a canonical scratch pattern (`<target>.tmp`,
  `<target>.partial`, `<target>.swp`, `~<target>`, hidden dotfile
  variants, or `<target>.NNNN`).
- The two raw events fall inside `CoalesceWindow` of each other.

`Pulse.FilterToFileWithRequest` uses this pattern internally; a
consumer watching the output directory sees a clean `ChangeRenamed`
or `ChangeCreated` event without intermediate states.

## Hash semantics

`ChangeEvent.Hash` is the SHA-256 hex of the file's first
`HashPrefixBytes` bytes. The hash is informational — two events with
the same `Hash` value describe identical leading-byte content. The
watcher itself uses the hash internally to detect content changes the
mtime/size signals miss (e.g. an in-place write that preserves both).

For `ChangeRemoved` events the `Hash` field is empty.

## First-tick behaviour

The watcher does **not** pre-seed its state map. Files that already
exist when the watcher starts surface as `ChangeCreated` on the first
tick. If you only want subsequent mutations, drain the first batch:

```go
ch := p.WatchDir(ctx, dir, true)
// Drain initial Created events.
first := time.After(500 * time.Millisecond)
draining := true
for draining {
    select {
    case <-ch:
    case <-first:
        draining = false
    }
}
// Now treat subsequent events as actionable.
```

## Limitations

- Polling-based — no fsnotify dependency. The default 250ms cadence
  is the floor for change visibility; tune `PollInterval` for your
  latency budget.
- Symlinks are followed implicitly (the underlying `afero.Fs.Stat`
  resolves them) but the watcher does not re-resolve a link target
  that changes after watch-start.
- The afero abstraction means the watcher works against any custom
  filesystem (in-memory, encrypted, remote) supplied via
  `pulse.Options.FS`.

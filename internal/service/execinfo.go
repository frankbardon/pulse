package service

import (
	"context"
	stderrors "errors"
	"os"
	"sync"
	"sync/atomic"

	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ExecInfo is the per-request execution-facts carrier the facade's
// observe helper hands the service through ctx (WithExecInfo) when
// observability is on. The service stamps it at the existing decision
// points — the execution arm (fused crosstab, streaming vs buffered,
// shard-parallel, parallel decode, join), the projection, worker and
// shard counts — and adds the run's row and byte counters.
//
// It is independent of Response.Components: the row counters come from
// Response.Metadata (always built by the engine, before any `return`
// shaping), so they are set with DisableComponents and with a `return`
// block that drops components.run.
//
// Off (no carrier in ctx) every stamp is a nil-receiver no-op: the only
// off-path cost is a ctx.Value lookup, which never allocates.
//
// Concurrency: Compose slots (ComposeParallel) share one carrier, and
// shard / decode workers read through one counting file system, so the
// counters are atomics and the plan fields sit behind a mutex. Several
// slots stamping one carrier sum their counters; the plan fields keep
// the last slot's decision.
type ExecInfo struct {
	mu        sync.Mutex
	arm       observe.Arm
	workers   int
	shards    int
	projected int

	rowsScanned atomic.Int64
	rowsMatched atomic.Int64
	rowsOut     atomic.Int64
	bytesRead   atomic.Int64
}

// ExecSnapshot is a point-in-time copy of an ExecInfo.
type ExecSnapshot struct {
	Arm observe.Arm
	// Workers is the resolved worker count of the arm (1 for a serial
	// arm); Shards the shard count of an archive-backed cohort (0 for a
	// single file).
	Workers int
	Shards  int
	// ProjectedFields is the retained-field count when a decode
	// projection was installed; 0 means full decode.
	ProjectedFields int

	RowsScanned int64
	RowsMatched int64
	RowsOut     int64
	BytesRead   int64
}

type execInfoKey struct{}

// WithExecInfo returns ctx carrying ei. The facade calls it only when
// observability is on.
func WithExecInfo(ctx context.Context, ei *ExecInfo) context.Context {
	return context.WithValue(ctx, execInfoKey{}, ei)
}

// execInfoFrom returns the carrier in ctx, or nil when observability is
// off. Every ExecInfo method is nil-safe.
func execInfoFrom(ctx context.Context) *ExecInfo {
	if ctx == nil {
		return nil
	}
	ei, _ := ctx.Value(execInfoKey{}).(*ExecInfo)
	return ei
}

// Snapshot copies the carrier's current state.
func (e *ExecInfo) Snapshot() ExecSnapshot {
	if e == nil {
		return ExecSnapshot{}
	}
	e.mu.Lock()
	snap := ExecSnapshot{
		Arm:             e.arm,
		Workers:         e.workers,
		Shards:          e.shards,
		ProjectedFields: e.projected,
	}
	e.mu.Unlock()
	snap.RowsScanned = e.rowsScanned.Load()
	snap.RowsMatched = e.rowsMatched.Load()
	snap.RowsOut = e.rowsOut.Load()
	snap.BytesRead = e.bytesRead.Load()
	return snap
}

// setPlan records the arm that ran with its resolved worker count, the
// shard count of the cohort it read (0: single file) and the retained
// field count of its decode projection (0: full decode). One call per
// decision, so a later decision (a crosstab decode that fans out, a
// later Compose slot) replaces the whole plan.
func (e *ExecInfo) setPlan(arm observe.Arm, workers, shards, projected int) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.arm, e.workers, e.shards, e.projected = arm, workers, shards, projected
	e.mu.Unlock()
}

// addBytes counts n payload bytes read.
func (e *ExecInfo) addBytes(n int64) {
	if e == nil || n <= 0 {
		return
	}
	e.bytesRead.Add(n)
}

// addRows adds one finished response's row counters: scanned is the
// records read (Metadata.TotalRows), matched the filter survivors
// (Metadata.FilteredRows), out the result rows. Metadata.TotalRows ==
// Components.Run.TotalRecords by construction, on every arm.
func (e *ExecInfo) addRows(resp *types.Response) {
	if e == nil || resp == nil {
		return
	}
	if m := resp.Metadata; m != nil {
		e.rowsScanned.Add(m.TotalRows)
		e.rowsMatched.Add(m.FilteredRows)
	}
	e.rowsOut.Add(int64(len(resp.Data)))
}

// pathArm maps the processor's serial strategy to its arm.
func pathArm(streaming bool) observe.Arm {
	if streaming {
		return observe.ArmStreaming
	}
	return observe.ArmBuffered
}

// countReads routes the request-local cohort's reads through a counting
// file system when ctx carries a carrier: every iterator built from the
// cohort (newScanIter) then reports its payload bytes. Off, the cohort
// is untouched.
func countReads(ctx context.Context, cohort *Cohort) {
	ei := execInfoFrom(ctx)
	if ei == nil || cohort == nil || cohort.fs == nil {
		return
	}
	if _, ok := cohort.fs.(*execCountingFs); ok {
		return
	}
	cohort.fs = &execCountingFs{Fs: cohort.fs, ei: ei}
}

// execCountingFs wraps an afero.Fs so every file it opens adds the bytes it
// Reads to the carrier — one atomic add per Read. It forwards the mmap
// capability (RealPath) of the wrapped fs, so wrapping never changes
// which decode path runs; a mapped region is counted by noteMapped.
// Installed only when observability is on (countReads).
type execCountingFs struct {
	afero.Fs
	ei *ExecInfo
}

// RealPath forwards the wrapped fs's mmap capability; it declines (an
// error) exactly when resolveRealPath on the wrapped fs would.
func (c *execCountingFs) RealPath(name string) (string, error) {
	if p, ok := resolveRealPath(c.Fs, name); ok {
		return p, nil
	}
	return "", errNoRealPath
}

func (c *execCountingFs) Open(name string) (afero.File, error) {
	f, err := c.Fs.Open(name)
	if err != nil || f == nil {
		return f, err
	}
	return &execCountingFile{File: f, ei: c.ei}, nil
}

func (c *execCountingFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := c.Fs.OpenFile(name, flag, perm)
	if err != nil || f == nil {
		return f, err
	}
	return &execCountingFile{File: f, ei: c.ei}, nil
}

// errNoRealPath declines the mmap fast path for a wrapped fs that has none.
var errNoRealPath = stderrors.New("no real path")

// execCountingFile counts the bytes each Read / ReadAt returns.
type execCountingFile struct {
	afero.File
	ei *ExecInfo
}

func (f *execCountingFile) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	f.ei.addBytes(int64(n))
	return n, err
}

func (f *execCountingFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := f.File.ReadAt(p, off)
	f.ei.addBytes(int64(n))
	return n, err
}

// noteMapped counts a memory-mapped region read through fs when fs is a
// counting wrapper (the mmap fast path bypasses Read).
func noteMapped(fs afero.Fs, n int) {
	if c, ok := fs.(*execCountingFs); ok {
		c.ei.addBytes(int64(n))
	}
}

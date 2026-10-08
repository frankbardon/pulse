package service

import (
	"context"
	stderrors "errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

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
// Phases: the carrier also keeps the operation's phase clock. Phases
// are contiguous laps — Lap(ph) attributes the time since the previous
// boundary (the carrier's creation, a Lap or a Mark) to ph — so a
// boundary costs one clock read and nothing runs per row. A phase that
// runs in several pieces (plan before and after the cohort open) sums.
//
// Child operations: a Compose slot or a ProcessChain stage runs under
// its OWN carrier (startChild), so each child reports its own arm and
// counters; the facade folds every finished child into its parent with
// Absorb.
//
// Concurrency: shard / decode workers read through one counting file
// system and ComposeParallel children absorb into the parent from
// their goroutines, so the counters are atomics and the plan fields
// and the phase clock sit behind a mutex.
type ExecInfo struct {
	mu        sync.Mutex
	arm       observe.Arm
	workers   int
	shards    int
	projected int

	// children counts absorbed child operations; armMixed is set when
	// two of them ran different arms.
	children int
	armMixed bool

	// mark is the last phase boundary; phases the summed lap time per
	// phase (phaseIndex order) and ran the phases that lapped.
	mark   time.Time
	phases [phaseCount]time.Duration
	ran    uint8

	child ChildStart

	rowsScanned atomic.Int64
	rowsMatched atomic.Int64
	rowsOut     atomic.Int64
	bytesRead   atomic.Int64
}

// ChildStart starts a child operation — slot index of a Compose, stage
// index of a ProcessChain — running req under the parent carrier's
// context. It returns the context the child runs under (carrying the
// child's own carrier) and the function that ends it with the child's
// error. The facade supplies it (SetChildStart) for operations that
// fan out; the service calls it through startChild.
type ChildStart func(ctx context.Context, index int, req *types.Request) (context.Context, func(error))

// NewExecInfo returns a carrier whose phase clock starts at start (the
// operation's start).
func NewExecInfo(start time.Time) *ExecInfo {
	return &ExecInfo{mark: start}
}

// SetChildStart installs the child-operation starter.
func (e *ExecInfo) SetChildStart(f ChildStart) {
	if e == nil {
		return
	}
	e.child = f
}

// endNoChild is the end function of a child that was not started (no
// carrier, or no starter installed). A package-level value, so the off
// path allocates nothing.
var endNoChild = func(error) {}

// startChild starts child operation index for req when ctx carries a
// carrier with a starter, else returns ctx and a no-op end.
func startChild(ctx context.Context, index int, req *types.Request) (context.Context, func(error)) {
	ei := execInfoFrom(ctx)
	if ei == nil || ei.child == nil {
		return ctx, endNoChild
	}
	return ei.child(ctx, index, req)
}

// phaseCount is the number of observe phases.
const phaseCount = 8

// phaseOrder lists the phases in execution order — the order a
// snapshot reports them in (observe.AllPhases, without its allocation).
var phaseOrder = [phaseCount]observe.Phase{
	observe.PhasePlan, observe.PhaseOpen, observe.PhaseDecode, observe.PhaseScan,
	observe.PhaseReduce, observe.PhasePost, observe.PhaseOverlay, observe.PhaseShape,
}

func phaseIndex(ph observe.Phase) int {
	for i, p := range phaseOrder {
		if p == ph {
			return i
		}
	}
	return -1
}

// Lap closes the current phase segment: the time since the previous
// boundary is added to ph, and the boundary moves to now. Nil-safe — a
// no-op (and no clock read) when observability is off.
func (e *ExecInfo) Lap(ph observe.Phase) {
	if e == nil {
		return
	}
	i := phaseIndex(ph)
	now := time.Now()
	e.mu.Lock()
	if i >= 0 && !e.mark.IsZero() {
		e.phases[i] += now.Sub(e.mark)
		e.ran |= 1 << i
	}
	e.mark = now
	e.mu.Unlock()
}

// Mark moves the phase boundary to now without attributing the elapsed
// time to any phase — used after a stretch that belongs to child
// operations (their own carriers time it) or to no phase.
func (e *ExecInfo) Mark() {
	if e == nil {
		return
	}
	now := time.Now()
	e.mu.Lock()
	e.mark = now
	e.mu.Unlock()
}

// Absorb folds a finished child operation's snapshot into this (the
// parent's) carrier: row and byte counters sum, Workers / Shards /
// ProjectedFields keep the maximum, and the arm is the children's arm
// when every absorbed child ran the same one, else empty. Phases are
// not absorbed — each child reports its own.
func (e *ExecInfo) Absorb(c ExecSnapshot) {
	if e == nil {
		return
	}
	e.rowsScanned.Add(c.RowsScanned)
	e.rowsMatched.Add(c.RowsMatched)
	e.rowsOut.Add(c.RowsOut)
	e.bytesRead.Add(c.BytesRead)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.workers = max(e.workers, c.Workers)
	e.shards = max(e.shards, c.Shards)
	e.projected = max(e.projected, c.ProjectedFields)
	e.children++
	switch {
	case e.armMixed:
	case e.children == 1:
		e.arm = c.Arm
	case e.arm != c.Arm:
		e.arm, e.armMixed = "", true
	}
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

	// Phases are the phases that ran, in execution order, each with its
	// summed lap time.
	Phases []observe.PhaseTiming
}

type execInfoKey struct{}

// WithExecInfo returns ctx carrying ei. The facade calls it only when
// observability is on.
func WithExecInfo(ctx context.Context, ei *ExecInfo) context.Context {
	return context.WithValue(ctx, execInfoKey{}, ei)
}

// ExecInfoFrom returns the carrier in ctx, or nil when observability is
// off. The facade reads it to time its own shape phase.
func ExecInfoFrom(ctx context.Context) *ExecInfo { return execInfoFrom(ctx) }

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
	if e.ran != 0 {
		snap.Phases = make([]observe.PhaseTiming, 0, phaseCount)
		for i, ph := range phaseOrder {
			if e.ran&(1<<i) != 0 {
				snap.Phases = append(snap.Phases, observe.PhaseTiming{Phase: ph, Duration: e.phases[i]})
			}
		}
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

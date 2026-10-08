package pulse

import (
	"context"
	stderrors "errors"
	"sync"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/service"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// opSpec names one facade operation for the observe helper. Everything
// derived from it — the cohort path, the request hash — is computed
// only when observability is on, so building one on the off path is a
// handful of word copies.
type opSpec struct {
	kind observe.OperationKind
	// path is the cohort path when the method receives one directly.
	path string
	// cohort is resolved to a path lazily (resolveCohortPath
	// concatenates, which would allocate on the off path).
	cohort *types.Cohort
	// req hashes the operation's request lazily; nil when there is none.
	req interface{ Hash() string }
}

func (op opSpec) cohortPath() string {
	if op.cohort != nil {
		return resolveCohortPath(op.cohort)
	}
	return op.path
}

func (op opSpec) requestHash() string {
	if op.req == nil {
		return ""
	}
	return op.req.Hash()
}

// observing reports whether any observability surface is set. It is
// fixed at New, so the off path is one predictable branch.
func (p *Pulse) observing() bool {
	return p.hooks != nil || p.metrics != nil || p.logger != nil
}

// observe is the single instrumentation point for a facade operation:
// every instrumented *Pulse method runs its work as fn through it. Off
// (no Logger, Hooks or Metrics) it calls fn(ctx) and nothing else — no
// clock read, no hash, no allocation. On, it fires OnOperationStart
// before the work, runs fn under the context that hook returned, and
// fires OnOperationEnd with the duration and the error's code.
//
// It is deliberately NOT hung on Service.BoundRequest, which nests per
// Compose slot.
func (p *Pulse) observe(ctx context.Context, op opSpec, fn func(context.Context) error) error {
	if !p.observing() {
		return fn(ctx)
	}
	return p.observeOn(ctx, op, fn, nil)
}

// observeOn is the on path of observe. result, when non-nil, returns the
// operation's result value after fn ran, so the logger can report the
// warning codes it carries.
func (p *Pulse) observeOn(ctx context.Context, op opSpec, fn func(context.Context) error, result func() any) error {
	run := p.beginOp(ctx, p.topInfo(op))
	err := fn(run.work)
	run.end(err, result)
	return err
}

// topInfo builds a top-level operation's OperationInfo. On path only:
// it issues an ID, resolves the cohort path and hashes the request.
func (p *Pulse) topInfo(op opSpec) observe.OperationInfo {
	return observe.OperationInfo{
		Kind:        op.kind,
		Scope:       observe.ScopeTop,
		ID:          p.opSeq.Add(1),
		Cohort:      op.cohortPath(),
		RequestHash: op.requestHash(),
	}
}

// opRun is one observed operation in flight: built by beginOp, ended by
// end (or, for a streaming operation, endOnce).
type opRun struct {
	p     *Pulse
	info  observe.OperationInfo
	start time.Time
	// hookCtx is the context OnOperationStart returned (the hooks see
	// it); work is hookCtx carrying the execution-facts carrier (the
	// operation's work runs under it).
	hookCtx, work context.Context
	exec          *service.ExecInfo
	// parent is the enclosing operation of a child, nil for a top-level
	// one; a finished child folds its counters into it.
	parent *opRun
	once   sync.Once
}

// fansOut reports the kinds whose slots or stages run as child
// operations.
func fansOut(kind observe.OperationKind) bool {
	return kind == observe.OpCompose || kind == observe.OpComposeParallel || kind == observe.OpProcessChain
}

// beginOp starts an operation: OnOperationStart, then the
// execution-facts carrier (arm, counters, phase clock) the service
// stamps through ctx. Installed only here, on the on path.
func (p *Pulse) beginOp(ctx context.Context, info observe.OperationInfo) *opRun {
	if p.om != nil {
		p.om.begin(info)
	}
	run := &opRun{p: p, info: info, start: time.Now()}
	run.hookCtx = p.hookStart(ctx, info)
	run.exec = service.NewExecInfo(run.start)
	if fansOut(info.Kind) {
		run.exec.SetChildStart(run.startChild)
	}
	run.work = service.WithExecInfo(run.hookCtx, run.exec)
	return run
}

// startChild is the service.ChildStart of a fanning-out operation: slot
// (Compose) or stage (ProcessChain) index runs as a child operation of
// the same kind, scope child, linked to its parent by ID. Each child has
// its own carrier, so its arm and counters are its own.
func (run *opRun) startChild(ctx context.Context, index int, req *types.Request) (context.Context, func(error)) {
	p := run.p
	info := observe.OperationInfo{
		Kind:   run.info.Kind,
		Scope:  observe.ScopeChild,
		ID:     p.opSeq.Add(1),
		Parent: run.info.ID,
		Index:  index,
	}
	if req != nil {
		if req.Cohort != nil {
			info.Cohort = resolveCohortPath(req.Cohort)
		}
		info.RequestHash = req.Hash()
	}
	child := p.beginOp(ctx, info)
	child.parent = run
	return child.work, func(err error) { child.end(err, nil) }
}

// end finishes the operation exactly once (later calls are no-ops, so
// a stream's drain, Close and ctx cancel can all race to end it): it
// reports the plan at Debug, logs a top-level outcome, fires OnPhase per
// phase that ran (execution order) and then OnOperationEnd. A child
// folds its counters into its parent first and leaves the outcome
// records to the parent, so a failed slot is logged once.
//
// The parent's OperationResult aggregates its children: row and byte
// counters are their sum, Workers / Shards the maximum, Arm the
// children's arm when they all ran the same one (else empty) and
// Projected whether any child projected.
func (run *opRun) end(err error, result func() any) {
	run.once.Do(func() { run.finish(err, result) })
}

func (run *opRun) finish(err error, result func() any) {
	p := run.p
	snap := run.exec.Snapshot()
	res := observe.OperationResult{
		Duration:    time.Since(run.start),
		Code:        operationCode(err),
		RowsScanned: snap.RowsScanned,
		RowsMatched: snap.RowsMatched,
		RowsOut:     snap.RowsOut,
		BytesRead:   snap.BytesRead,
		Shards:      snap.Shards,
		Workers:     snap.Workers,
		Arm:         snap.Arm,
		Projected:   snap.ProjectedFields > 0,
	}
	if run.parent != nil {
		run.parent.exec.Absorb(snap)
	}
	if p.logger != nil {
		p.logPlan(run.hookCtx, run.info, snap)
		if run.parent == nil {
			var out any
			if err == nil && result != nil {
				out = result()
			}
			p.logOperation(run.hookCtx, run.info, res, err, out)
		}
	}
	if p.om != nil {
		p.om.end(run.info, res, snap.Phases, err)
	}
	for _, ph := range snap.Phases {
		p.hookPhase(run.hookCtx, run.info, ph)
	}
	p.hookEnd(run.hookCtx, run.info, res)
}

// observed adapts observe to a method returning (T, error). Off, it is
// fn(ctx) and nothing else.
func observed[T any](p *Pulse, ctx context.Context, op opSpec, fn func(context.Context) (T, error)) (T, error) {
	if !p.observing() {
		return fn(ctx)
	}
	var out T
	err := p.observeOn(ctx, op, func(ctx context.Context) error {
		var err error
		out, err = fn(ctx)
		return err
	}, func() any { return out })
	return out, err
}

// observedStream is observed for a streaming operation, whose work
// outlives the call: fn receives the end function and arranges for it
// to run when the stream finishes — drained, closed or cancelled. end
// runs at most once however many of those race; a call that fails ends
// the operation at once. Off, fn gets a nil end and nothing else
// happens. A stream the caller never drains, closes or cancels never
// ends (a leaked iterator is a caller bug).
func observedStream[T any](p *Pulse, ctx context.Context, op opSpec, fn func(ctx context.Context, end func(error)) (T, error)) (T, error) {
	if !p.observing() {
		return fn(ctx, nil)
	}
	run := p.beginOp(ctx, p.topInfo(op))
	out, err := fn(run.work, func(err error) { run.end(err, nil) })
	if err != nil {
		run.end(err, nil)
	}
	return out, err
}

// operationCode maps an operation's error to OperationResult.Code: ok
// for nil, the Pulse error code for a coded error (found through the
// wrap chain), else the fixed uncoded placeholder. Never the message.
func operationCode(err error) string {
	if err == nil {
		return observe.CodeOK
	}
	var coded *errors.CodedError
	if stderrors.As(err, &coded) && coded != nil && coded.Code != "" {
		return string(coded.Code)
	}
	return observe.CodeUncoded
}

// Hook names, as reported when a hook panics.
const (
	hookStart = "start"
	hookEnd   = "end"
	hookPh    = "phase"
)

// hookStart fires OnOperationStart, returning the context it handed back
// (the input context when the hook is unset, returns nil or panics).
func (p *Pulse) hookStart(ctx context.Context, info observe.OperationInfo) (out context.Context) {
	out = ctx
	if p.hooks == nil || p.hooks.OnOperationStart == nil {
		return out
	}
	defer p.recoverHook(ctx, hookStart, info.Kind)
	if derived := p.hooks.OnOperationStart(ctx, info); derived != nil {
		out = derived
	}
	return out
}

func (p *Pulse) hookEnd(ctx context.Context, info observe.OperationInfo, res observe.OperationResult) {
	if p.hooks == nil || p.hooks.OnOperationEnd == nil {
		return
	}
	defer p.recoverHook(ctx, hookEnd, info.Kind)
	p.hooks.OnOperationEnd(ctx, info, res)
}

func (p *Pulse) hookPhase(ctx context.Context, info observe.OperationInfo, ph observe.PhaseTiming) {
	if p.hooks == nil || p.hooks.OnPhase == nil {
		return
	}
	defer p.recoverHook(ctx, hookPh, info.Kind)
	p.hooks.OnPhase(ctx, info, ph)
}

// recoverHook swallows a hook panic so the operation continues, logging
// the hook and operation kind — never the panic value, which could carry
// row data — and counting it into pulse_hook_panics_total{hook} when
// Metrics is set. Must be called directly by defer.
func (p *Pulse) recoverHook(ctx context.Context, hook string, kind observe.OperationKind) {
	if r := recover(); r != nil {
		if p.om != nil {
			p.om.hookPanic(hook)
		}
		if p.logger != nil {
			p.logger.WarnContext(ctx, "pulse: observability hook panicked",
				"hook", hook, "op", string(kind))
		}
	}
}

// The constructors below build an opSpec from a possibly-nil request or
// job without dereferencing it, so a nil argument still reaches the
// method's own refusal. A nil request contributes no hash.

func requestOp(kind observe.OperationKind, req *Request) opSpec {
	if req == nil {
		return opSpec{kind: kind}
	}
	return opSpec{kind: kind, cohort: req.Cohort, req: req}
}

func chainOp(kind observe.OperationKind, req *ChainRequest) opSpec {
	if req == nil {
		return opSpec{kind: kind}
	}
	return opSpec{kind: kind, cohort: req.Cohort, req: req}
}

func facetOp(kind observe.OperationKind, req *FacetRequest) opSpec {
	if req == nil {
		return opSpec{kind: kind}
	}
	return opSpec{kind: kind, cohort: req.Cohort, req: req}
}

func lookupOp(kind observe.OperationKind, req *LookupRequest) opSpec {
	if req == nil {
		return opSpec{kind: kind}
	}
	return opSpec{kind: kind, cohort: req.Cohort, req: req}
}

func requestHasher(req *Request) interface{ Hash() string } {
	if req == nil {
		return nil
	}
	return req
}

func composedHasher(req *ComposedRequest) interface{ Hash() string } {
	if req == nil {
		return nil
	}
	return req
}

func synthHasher(spec *SynthSpec) interface{ Hash() string } {
	if spec == nil {
		return nil
	}
	return spec
}

func sampleCohort(req *SampleRequest) *types.Cohort {
	if req == nil {
		return nil
	}
	return req.Cohort
}

func filterToFileSource(req *FilterToFileRequest) string {
	if req == nil {
		return ""
	}
	return req.SourcePath
}

func importTarget(job *pio.ImportJob) string {
	if job == nil {
		return ""
	}
	return job.Target
}

func exportSource(job *pio.ExportJob) string {
	if job == nil {
		return ""
	}
	return job.Source
}

func transferExportSource(job *pio.TransferExportJob) string {
	if job == nil {
		return ""
	}
	return job.Source
}

func transferImportOutput(job *pio.TransferImportJob) string {
	if job == nil {
		return ""
	}
	return job.Output
}

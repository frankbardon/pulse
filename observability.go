package pulse

import (
	"context"
	stderrors "errors"
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
	info := observe.OperationInfo{
		Kind:        op.kind,
		Scope:       observe.ScopeTop,
		ID:          p.opSeq.Add(1),
		Cohort:      op.cohortPath(),
		RequestHash: op.requestHash(),
	}
	start := time.Now()
	ctx = p.hookStart(ctx, info)
	// The execution-facts carrier the service stamps at its decision
	// points (arm, projection, workers, shards) and counts rows and
	// bytes into. Installed only here, on the on path.
	exec := &service.ExecInfo{}
	err := fn(service.WithExecInfo(ctx, exec))
	// TODO(observability/E2-S2): streaming operations (ProcessStream,
	// ProcessStreamResult, SynthStream) end here, at call return; E2-S2
	// moves their end to drain / Close / ctx cancel.
	snap := exec.Snapshot()
	res := observe.OperationResult{
		Duration:    time.Since(start),
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
	if p.logger != nil {
		p.logPlan(ctx, info, snap)
		var out any
		if err == nil && result != nil {
			out = result()
		}
		p.logOperation(ctx, info, res, err, out)
	}
	p.hookEnd(ctx, info, res)
	return err
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

// Hook names, as reported when a hook panics. (OnPhase joins with
// phase emission in E2-S2.)
const (
	hookStart = "start"
	hookEnd   = "end"
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

// recoverHook swallows a hook panic so the operation continues, logging
// the hook and operation kind — never the panic value, which could carry
// row data. Must be called directly by defer.
func (p *Pulse) recoverHook(ctx context.Context, hook string, kind observe.OperationKind) {
	if r := recover(); r != nil {
		// TODO(observability/E3-S1): count into pulse_hook_panics_total.
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

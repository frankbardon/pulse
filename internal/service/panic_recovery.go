package service

import (
	"context"
	"sync"

	"github.com/frankbardon/pulse/internal/oppanic"
)

// recoverInto is the engine's worker-goroutine panic recover. recover()
// only catches a panic on its own goroutine, so every pool goroutine
// that runs operator code defers it directly at its top (recover works
// only in the deferred function itself):
//
//	defer recoverInto("parallel_decode", rethrow, &err)
//
// On a panic it sets *err to the shared PROCESSING_PANIC error
// (internal/oppanic: details op, panic_type, stack — never the panic
// value), which the pool returns through its ordinary error path so the
// siblings cancel like any other failure. op names the pool that
// recovered it. With rethrow (pulse.Options.DisablePanicRecovery) it
// re-panics with the original value instead. No panic: one recover()
// call, nothing else.
func recoverInto(op string, rethrow bool, err *error) {
	r := recover()
	if r == nil {
		return
	}
	if rethrow {
		panic(r)
	}
	*err = oppanic.Error(op, r)
}

// Pool names recoverInto stamps into details.op: where the panic was
// recovered, beside the facade's operation kinds.
const (
	panicOpParallelDecode  = "parallel_decode"
	panicOpShardParallel   = "shard_parallel"
	panicOpComposeParallel = "compose_parallel"
	panicOpComposeFits     = "compose_fits"
)

// joinOnReturn runs f, then wg.Done — the Done NEVER deferred. A pool
// worker that re-panics (DisablePanicRecovery) still runs every outer
// defer while it unwinds; a deferred Done would release the caller's
// wg.Wait before the crash, with no error recorded for the slot, and
// the caller would return a partial "success" (nil slot, nil error)
// in the window before the process dies. Joining only on a normal
// return keeps the caller blocked in Wait until the crash, so a
// re-panic is never observable as a return. With recovery on, the
// worker's recoverInto turns the panic into its error and f returns
// normally, so the join always happens.
func joinOnReturn(wg *sync.WaitGroup, f func()) {
	f()
	wg.Done()
}

// workerGroup is errgroup.WithContext with joinOnReturn's rule:
// errgroup.Group.Go defers its own join, so a re-panicking worker
// would release Wait (nil error) before the crash. Go records the
// first worker error and cancels the group context; Wait returns it
// once every worker returned normally, then cancels the context.
type workerGroup struct {
	wg     sync.WaitGroup
	once   sync.Once
	err    error
	cancel context.CancelFunc
}

func newWorkerGroup(ctx context.Context) (*workerGroup, context.Context) {
	gctx, cancel := context.WithCancel(ctx)
	return &workerGroup{cancel: cancel}, gctx
}

func (g *workerGroup) Go(f func() error) {
	g.wg.Add(1)
	go joinOnReturn(&g.wg, func() {
		if err := f(); err != nil {
			g.once.Do(func() {
				g.err = err
				g.cancel()
			})
		}
	})
}

func (g *workerGroup) Wait() error {
	g.wg.Wait()
	g.cancel()
	return g.err
}

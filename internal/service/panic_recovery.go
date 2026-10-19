package service

import "github.com/frankbardon/pulse/internal/oppanic"

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

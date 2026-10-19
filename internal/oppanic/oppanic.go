// Package oppanic converts a recovered operation panic into the one
// coded error every recover site returns: PROCESSING_PANIC with details
// op, panic_type and stack. The facade's recover (observability.go) and
// the engine's worker-pool recovers (internal/service) both build their
// error here, so there is one code and one detail shape everywhere.
//
// The panic VALUE is never kept: it can carry row data, so neither the
// message nor details nor a wrapped cause holds it. Only its Go type
// name and the stack (function plus file:line per frame) survive.
package oppanic

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// MaxFrames caps details.stack.
const MaxFrames = 32

// Error builds the PROCESSING_PANIC error for a panic recovered while
// running operation op (an observe.OperationKind spelling, e.g.
// "process"). recovered is the value recover() returned; only its type
// name is kept.
//
// Call it from the deferred function that called recover(), so the
// stack still holds the panicking frames: the stack starts at the frame
// that panicked (runtime panic helpers skipped). Called outside a panic
// it starts at its caller.
func Error(op string, recovered any) *errors.CodedError {
	panicType := fmt.Sprintf("%T", recovered)
	return &errors.CodedError{
		Code:    errors.PROCESSING_PANIC,
		Message: "operation " + op + " panicked (" + panicType + ") and was recovered; details.stack names the frames",
		Details: map[string]any{
			"op":         op,
			"panic_type": panicType,
			"stack":      stack(),
		},
	}
}

// stack returns at most MaxFrames "function file:line" entries starting
// at the frame that panicked.
func stack() []string {
	var pcs [128]uintptr
	// Skip runtime.Callers, stack and Error.
	n := runtime.Callers(3, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	var all []runtime.Frame
	start := 0
	for {
		f, more := frames.Next()
		all = append(all, f)
		if f.Function == "runtime.gopanic" {
			start = len(all)
		}
		if !more {
			break
		}
	}
	// After gopanic, skip the runtime helpers that raised a runtime
	// error (sigpanic, panicIndex, ...) so frame 0 is the user's.
	for start < len(all) && start > 0 && strings.HasPrefix(all[start].Function, "runtime.") {
		start++
	}
	out := make([]string, 0, min(MaxFrames, len(all)-start))
	for _, f := range all[start:] {
		if len(out) == MaxFrames {
			break
		}
		out = append(out, f.Function+" "+filepath.Base(f.File)+":"+strconv.Itoa(f.Line))
	}
	return out
}

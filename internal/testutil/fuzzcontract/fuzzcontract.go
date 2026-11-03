// Package fuzzcontract is the shared contract every Pulse fuzz target
// holds its untrusted-input surface to, in one call:
//
//	func FuzzX(f *testing.F) {
//		f.Add([]byte(`seed`))
//		f.Fuzz(func(t *testing.T, in []byte) {
//			fuzzcontract.Run(t, in, func(in []byte) error {
//				_, err := surface(in)
//				return err
//			})
//		})
//	}
//
// The contract, per input:
//
//  1. No panic. A panic inside the target is recovered and reported
//     with the input and the panicking goroutine's stack.
//  2. A bounded run. The input is capped at Contract.MaxInput bytes
//     BEFORE the target sees it, and one call must return within
//     Contract.Timeout (a hang is reported, with the input, rather than
//     stalling the fuzzer). A target that builds a Pulse instance caps
//     the engine with [Limits].
//  3. Coded errors only. Every non-nil error satisfies
//     errors.As(err, **errors.CodedError) — Pulse's public errors
//     package — and its code is a registered one (errors.ParseCode). A
//     raw library fault (encoding/json, a third-party parser) leaking
//     to a caller fails the contract.
//
// Coded entries a surface reports beside its error (a descriptor
// envelope's errors / warnings) are held to the registry with
// [RequireCode].
//
// Test-only: no non-test file imports it (TestFuzzContract_TestOnlyImporters).
package fuzzcontract

import (
	stderrors "errors"
	"fmt"
	"runtime/debug"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
)

// DefaultMaxInput is the input cap a zero Contract applies: 64 KiB,
// far beyond any seed and small enough that one call stays cheap.
const DefaultMaxInput = 64 << 10

// DefaultTimeout is the per-call wall-clock ceiling a zero Contract
// applies. Generous — a loaded fuzz worker must never trip it on an
// honest input — so a report means a hang or a pathological blow-up.
const DefaultTimeout = 10 * time.Second

// Contract tunes [Contract.Run]; the zero value applies the defaults.
type Contract struct {
	// MaxInput caps the input in bytes before the target is called (a
	// longer input is truncated). 0 is DefaultMaxInput.
	MaxInput int
	// Timeout bounds one target call. 0 is DefaultTimeout.
	Timeout time.Duration
}

// Run holds target(input) to the contract under the default Contract.
func Run(tb testing.TB, input []byte, target func([]byte) error) {
	tb.Helper()
	Contract{}.Run(tb, input, target)
}

// outcome is one target call's result, carried off its goroutine.
type outcome struct {
	err       error
	panicked  bool
	recovered any
	stack     []byte
}

// Run holds target(input) to the contract: it caps input, calls the
// target on its own goroutine (so a hang is bounded and a panic is
// recovered with its stack) and checks the returned error. Every
// failure is a tb.Fatalf naming the input.
func (c Contract) Run(tb testing.TB, input []byte, target func([]byte) error) {
	tb.Helper()
	maxInput := c.MaxInput
	if maxInput <= 0 {
		maxInput = DefaultMaxInput
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if len(input) > maxInput {
		input = input[:maxInput]
	}

	done := make(chan outcome, 1)
	go func() {
		var out outcome
		defer func() {
			if r := recover(); r != nil {
				out = outcome{panicked: true, recovered: r, stack: debug.Stack()}
			}
			done <- out
		}()
		out.err = target(input)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var out outcome
	select {
	case out = <-done:
	case <-timer.C:
		tb.Fatalf("fuzzcontract: target did not return within %s on input %q", timeout, input)
		return
	}
	if out.panicked {
		tb.Fatalf("fuzzcontract: target panicked on input %q: %v\n%s", input, out.recovered, out.stack)
		return
	}
	if msg := checkError(out.err); msg != "" {
		tb.Fatalf("fuzzcontract: %s on input %q", msg, input)
	}
}

// checkError returns why err breaks the coded-error rule ("" when it
// holds: nil, or a CodedError in the chain carrying a registered code).
func checkError(err error) string {
	if err == nil {
		return ""
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		return fmt.Sprintf("uncoded error %T: %v", err, err)
	}
	if _, ok := perr.ParseCode(string(ce.Code)); !ok {
		return fmt.Sprintf("coded error carries unregistered code %q: %v", ce.Code, err)
	}
	return ""
}

// RequireCode fails tb unless code is a registered error code — for the
// coded entries a surface reports beside its error (a descriptor
// envelope's errors and warnings).
func RequireCode(tb testing.TB, input []byte, code string) {
	tb.Helper()
	if _, ok := perr.ParseCode(code); !ok {
		tb.Fatalf("fuzzcontract: entry carries unregistered code %q on input %q", code, input)
	}
}

// Limits is the tight engine cap a fuzz target that builds a Pulse
// instance passes as Options.Limits, so no fuzzed request can claim
// unbounded groups, cells, matrix width, slots, stages, join rows,
// derived columns, memory or wall time.
func Limits() limits.Limits {
	return limits.Limits{
		RequestTimeout:     2 * time.Second,
		MaxGroups:          10_000,
		MaxCrosstabCells:   10_000,
		MaxEstimatedMemory: 64 << 20,
		MaxMatrixDim:       64,
		MaxComposeSlots:    16,
		MaxChainStages:     16,
		MaxJoinBuildRows:   10_000,
		MaxDerivedColumns:  64,
	}
}

package oppanic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

const secret = "row-value-4711"

// recoverFrom runs f and converts its panic the way every recover site
// does: recover() called directly by the deferred func, then Error.
func recoverFrom(op string, f func()) (err *errors.CodedError) {
	defer func() {
		if r := recover(); r != nil {
			err = Error(op, r)
		}
	}()
	f()
	return nil
}

func panickingLeaf() { panic(secret) }

func deep(n int) {
	if n == 0 {
		panic(fmt.Errorf("wrapped %s", secret))
	}
	deep(n - 1)
}

// TestError_Shape pins the one detail shape every recover site emits:
// PROCESSING_PANIC with details op, panic_type and stack, and nothing
// else. Falsified by dropping the panic_type entry (fails "keys").
func TestError_Shape(t *testing.T) {
	ce := recoverFrom("process", panickingLeaf)
	if ce == nil {
		t.Fatal("no error recovered")
	}
	if ce.Code != errors.PROCESSING_PANIC {
		t.Fatalf("code = %s, want PROCESSING_PANIC", ce.Code)
	}
	if ce.Cause != nil {
		t.Fatalf("Cause = %v, want nil (a cause could carry the panic value)", ce.Cause)
	}
	if len(ce.Details) != 3 {
		t.Fatalf("keys: details = %v, want exactly op, panic_type, stack", ce.Details)
	}
	if got := ce.Details["op"]; got != "process" {
		t.Errorf("op = %v, want process", got)
	}
	if got := ce.Details["panic_type"]; got != "string" {
		t.Errorf("panic_type = %v, want string", got)
	}
	stack, ok := ce.Details["stack"].([]string)
	if !ok || len(stack) == 0 {
		t.Fatalf("stack = %#v, want a non-empty []string", ce.Details["stack"])
	}
	// The first frame is the function that panicked, not the runtime
	// or the recover machinery.
	if !strings.Contains(stack[0], "oppanic.panickingLeaf ") || !strings.Contains(stack[0], "oppanic_test.go:") {
		t.Errorf("stack[0] = %q, want the panicking function with file:line", stack[0])
	}
	if !strings.Contains(ce.Message, "process") {
		t.Errorf("message %q does not name the operation", ce.Message)
	}
}

// TestError_NeverCarriesValue: the panic value (which can hold row
// data) appears nowhere in Error(), Message or details. Falsified by
// formatting the value into the message.
func TestError_NeverCarriesValue(t *testing.T) {
	for name, f := range map[string]func(){
		"string": panickingLeaf,
		"error":  func() { deep(0) },
	} {
		t.Run(name, func(t *testing.T) {
			ce := recoverFrom("compose", f)
			if strings.Contains(ce.Error(), secret) {
				t.Errorf("Error() leaks the panic value: %q", ce.Error())
			}
			if leaked := fmt.Sprintf("%v", ce.Details); strings.Contains(leaked, secret) {
				t.Errorf("details leak the panic value: %s", leaked)
			}
		})
	}
}

// TestError_PanicTypeIsTypeName: panic_type names the Go type.
func TestError_PanicTypeIsTypeName(t *testing.T) {
	ce := recoverFrom("facet", func() { deep(0) })
	if got := ce.Details["panic_type"]; got != "*errors.errorString" {
		t.Errorf("panic_type = %v, want *errors.errorString", got)
	}
	ce = recoverFrom("facet", func() {
		var s []int
		_ = s[3] // deliberate out-of-range: a runtime panic
	})
	if got := ce.Details["panic_type"]; got != "runtime.boundsError" {
		t.Errorf("panic_type = %v, want runtime.boundsError", got)
	}
	stack := ce.Details["stack"].([]string)
	if strings.HasPrefix(stack[0], "runtime.") {
		t.Errorf("stack[0] = %q, want the user frame, not a runtime panic helper", stack[0])
	}
}

// TestError_StackCapped: at most MaxFrames frames however deep the
// panic. Falsified by removing the cap.
func TestError_StackCapped(t *testing.T) {
	ce := recoverFrom("process", func() { deep(100) })
	stack := ce.Details["stack"].([]string)
	if len(stack) != MaxFrames {
		t.Fatalf("len(stack) = %d, want %d", len(stack), MaxFrames)
	}
	if MaxFrames != 32 {
		t.Fatalf("MaxFrames = %d, want 32", MaxFrames)
	}
	for _, f := range stack {
		if strings.Count(f, " ") != 1 || !strings.Contains(f, ".go:") {
			t.Errorf("frame %q is not \"function file:line\"", f)
		}
	}
}

// TestError_OutsidePanic: called without a panic in flight (a
// defensive use) it still builds the shape, starting at the caller.
func TestError_OutsidePanic(t *testing.T) {
	ce := Error("process", 42)
	stack := ce.Details["stack"].([]string)
	if len(stack) == 0 || !strings.Contains(stack[0], "TestError_OutsidePanic") {
		t.Fatalf("stack = %v, want it to start at the caller", stack)
	}
	if ce.Details["panic_type"] != "int" {
		t.Errorf("panic_type = %v, want int", ce.Details["panic_type"])
	}
}

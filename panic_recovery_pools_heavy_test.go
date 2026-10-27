//go:build heavy

package pulse

import (
	stderrors "errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestPanicRecovery_PoolRepanics (FR-10, FR-20): with
// DisablePanicRecovery every pool and stream producer re-panics with
// the original value on its own goroutine — which crashes the process,
// so each arm runs in a child copy of this test binary and the parent
// asserts the child died of the panic on that goroutine.
//
// Falsified per arm by dropping the rethrow (recoverInto's
// `if rethrow { panic(r) }`, the stream goroutines'
// `if p.disablePanicRecovery { panic(r) }`) or by passing false for it
// at a site: the child then exits cleanly and reports the recovered
// error.
func TestPanicRecovery_PoolRepanics(t *testing.T) {
	if arm := os.Getenv(panicChildEnv); arm != "" {
		runRepanicChild(t, arm, os.Getenv(panicChildDirEnv))
		return
	}
	for _, arm := range poolArms() {
		t.Run(arm.name, func(t *testing.T) {
			dir := t.TempDir()
			if arm.prepare != nil {
				arm.prepare(t, dir)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestPanicRecovery_PoolRepanics$", "-test.count=1")
			cmd.Env = append(os.Environ(), panicChildEnv+"="+arm.name, panicChildDirEnv+"="+dir)
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !stderrors.As(err, &exitErr) {
				t.Fatalf("child exited cleanly (err %v); want a crash\n%s", err, out)
			}
			text := string(out)
			if !strings.Contains(text, "panic: "+panicSecret) {
				t.Fatalf("child did not die of the original panic value\n%s", text)
			}
			if !strings.Contains(text, arm.frame) {
				t.Errorf("crash trace names no %s frame\n%s", arm.frame, text)
			}
			if strings.Contains(text, "CHILD_RETURNED") {
				t.Errorf("child returned before the crash\n%s", text)
			}
		})
	}
}

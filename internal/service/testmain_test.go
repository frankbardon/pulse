package service

import (
	"os"
	"testing"
)

// TestMain forces worker-pool panic recovery OFF for every Service this
// test binary builds through New: an engine panic on a pool goroutine
// must crash the ordinary test that hit it, never surface as a
// recovered error a test could silently tolerate. The panic suite
// (panic_recovery*_test.go) re-enables recovery per instance with
// SetDisablePanicRecovery(false).
func TestMain(m *testing.M) {
	testForcePanicRecoveryOff = true
	os.Exit(m.Run())
}

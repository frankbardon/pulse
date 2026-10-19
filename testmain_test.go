package pulse

import (
	"os"
	"testing"
)

// TestMain forces operation panic recovery OFF for every *Pulse this
// test binary builds through New (package pulse and pulse_test alike):
// an engine panic must crash the ordinary test that hit it, never
// surface as a recovered error a test could silently tolerate. The
// panic suite (panic_recovery*_test.go) re-enables recovery per
// instance with enablePanicRecovery.
func TestMain(m *testing.M) {
	testForcePanicRecoveryOff = true
	os.Exit(m.Run())
}

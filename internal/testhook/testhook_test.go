package testhook_test

import (
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testhook"
	"github.com/spf13/afero"
)

func TestServiceHookInstalled(t *testing.T) {
	t.Parallel()

	if testhook.Service == nil {
		t.Fatal("root package did not install the Service hook")
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := testhook.Service(p)
	if svc == nil {
		t.Fatal("Service(*Pulse) = nil, want the instance's engine handle")
	}
	if testhook.Service(p) != svc {
		t.Fatal("Service(*Pulse) is not stable across calls")
	}
	if testhook.Service("not a pulse") != nil {
		t.Fatal("Service(non-Pulse) should return nil")
	}
	if testhook.Service((*pulse.Pulse)(nil)) != nil {
		t.Fatal("Service(nil *Pulse) should return nil")
	}
}

// TestForcePanicRecoveryOffHook proves an external test package can turn
// panic recovery off for every instance New builds afterwards, and back
// on, through the hook alone.
func TestForcePanicRecoveryOffHook(t *testing.T) {
	// serial: flips the root package's process-wide panic-recovery override
	if testhook.ForcePanicRecoveryOff == nil {
		t.Fatal("root package did not install the ForcePanicRecoveryOff hook")
	}
	newDisabled := func() bool {
		t.Helper()
		p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return testhook.Service(p).PanicRecoveryDisabled()
	}
	orig := testhook.ForcePanicRecoveryOff(false)
	defer testhook.ForcePanicRecoveryOff(orig)

	if newDisabled() {
		t.Fatal("override off: New built an instance with panic recovery disabled")
	}
	if prev := testhook.ForcePanicRecoveryOff(true); prev {
		t.Fatalf("ForcePanicRecoveryOff(true) returned prev=%v, want false", prev)
	}
	if !newDisabled() {
		t.Fatal("override on: New built an instance with panic recovery enabled")
	}
	if prev := testhook.ForcePanicRecoveryOff(false); !prev {
		t.Fatalf("ForcePanicRecoveryOff(false) returned prev=%v, want true", prev)
	}
	if newDisabled() {
		t.Fatal("override reset: New still disables panic recovery")
	}
}

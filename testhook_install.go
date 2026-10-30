package pulse

import (
	"github.com/frankbardon/pulse/internal/service"
	"github.com/frankbardon/pulse/internal/testhook"
)

// init installs the test-only hooks black-box tests read through
// internal/testhook, so the facade exports no service accessor and no
// panic-recovery switch. Production code never calls either hook.
func init() {
	testhook.Service = func(v any) *service.Service {
		p, ok := v.(*Pulse)
		if !ok || p == nil {
			return nil
		}
		return p.svc
	}
	testhook.ForcePanicRecoveryOff = func(off bool) bool {
		prev := testForcePanicRecoveryOff
		testForcePanicRecoveryOff = off
		return prev
	}
}

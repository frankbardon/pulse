package pulse

import "github.com/frankbardon/pulse/internal/service"

// ServiceForTest exposes the engine handle to the external pulse_test
// package so its extension-registry assertions can inspect wiring. It
// is compiled only under `go test`; the public facade has no service
// accessor.
func ServiceForTest(p *Pulse) *service.Service { return p.svc }

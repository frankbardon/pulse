package pulse

import (
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
)

// init installs the in-module hooks the MCP go-sdk adapter reads
// through internal/facadebridge, so the facade needs no exported
// service accessor.
func init() {
	facadebridge.ExtensionsSnapshot = func(v any) *descx.ExtensionsSnapshot {
		p, ok := v.(*Pulse)
		if !ok || p == nil {
			return nil
		}
		return p.svc.ExtensionsSnapshot()
	}
	facadebridge.InstanceSnapshot = func(v any) *descx.InstanceSnapshot {
		p, ok := v.(*Pulse)
		if !ok || p == nil {
			return nil
		}
		return p.svc.InstanceSnapshot()
	}
	facadebridge.CohortScanDisabled = func(v any) bool {
		p, ok := v.(*Pulse)
		if !ok || p == nil || p.featureProfile == nil || p.featureProfile.Behaviour == nil {
			return false
		}
		return p.featureProfile.Behaviour.DisableCohortScan
	}
}

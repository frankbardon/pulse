package pulse

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
)

// init installs the in-module hooks the MCP go-sdk adapter reads
// through internal/facadebridge, so the facade needs no exported
// service accessor.
func init() {
	facadebridge.ExtensionsSnapshot = func(v any) *descriptor.ExtensionsSnapshot {
		p, ok := v.(*Pulse)
		if !ok || p == nil {
			return nil
		}
		return p.svc.ExtensionsSnapshot()
	}
}

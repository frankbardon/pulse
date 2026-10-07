package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
)

// LimitsFor lists the instance's EFFECTIVE resource limits as the
// manifest's always-present `limits` block, in declaration order. A
// nil snapshot (or one pulse.New never stamped) reports the built-in
// defaults — InstanceSnapshot.Limits' own fallback.
func LimitsFor(inst *InstanceSnapshot) []descriptor.LimitMeta {
	l := inst.Limits()
	names := limits.Names()
	out := make([]descriptor.LimitMeta, 0, len(names))
	for _, n := range names {
		out = append(out, descriptor.LimitMeta{
			Name:    string(n),
			Value:   limits.Value(l, n),
			Default: limits.Default(n),
			Unit:    limits.Unit(n),
		})
	}
	return out
}

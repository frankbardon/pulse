package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// Zone-capability values surfaced on the manifest `zone` key of an
// operator entry (descriptor.Operator.Zone) or an overlay kind entry
// (descriptor.OverlayCapability.Zone). An operator absent from the
// table is not zone-capable: the key is omitted and an explicit slot
// `tz` on it is refused.
const (
	// ZoneCapable marks an operator whose slot accepts a per-slot `tz`
	// and resolves a zone through slot → request → options → UTC.
	ZoneCapable = "capable"

	// ZoneFollowing marks an overlay kind with no zone slot of its own
	// that inherits its host grouper's resolved zone.
	ZoneFollowing = "following"
)

// zoneCapabilities is the single declaration of time-zone participation
// for built-in operators and overlay kinds. Operator names are unique
// across categories (each carries its family prefix), so one name-keyed
// table serves groupers, filterers, attributes, features and overlays.
// Extension operators never appear here — they are not zone-capable.
//
// It lives in the no-execute layer so predict, the manifest and the
// request-time resolver (internal/service imports this package) read the
// same table without importing internal/processing.
var zoneCapabilities = map[string]string{
	string(types.GROUP_DATE):         ZoneCapable,
	string(types.GROUP_DATE_RANGES):  ZoneCapable,
	string(types.FILTER_DATE_RANGES): ZoneCapable,
	string(types.ATTR_DATE_PART):     ZoneCapable,
	string(types.FEAT_DATE_FEATURES): ZoneCapable,
	string(types.OverlayKindYoY):     ZoneFollowing,
}

// ZoneCapabilityOf returns the declared zone participation for a
// built-in operator or overlay kind name: ZoneCapable, ZoneFollowing,
// or "" when the name is not zone-capable (including every extension
// operator and every unknown name).
func ZoneCapabilityOf(name string) string {
	return zoneCapabilities[name]
}

// IsZoneCapable reports whether the named operator accepts a per-slot
// `tz`. A zone-following overlay kind is NOT zone-capable — it has no
// `tz` slot.
func IsZoneCapable(name string) bool {
	return zoneCapabilities[name] == ZoneCapable
}

// withZone stamps each operator's Zone from the declaration table.
// Applied by the manifest builder so the capability files stay free of
// a per-entry duplicate of the table.
func withZone(ops []descriptor.Operator) []descriptor.Operator {
	for i := range ops {
		ops[i].Zone = ZoneCapabilityOf(ops[i].Name)
	}
	return ops
}

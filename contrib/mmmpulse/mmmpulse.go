// Package mmmpulse is Pulse's retired media-mix modelling pack. Both of
// its models — REG_MMM_HILL and REG_MMM_BAYES — and their skills are
// built into Pulse: every instance runs them with no registration, and
// registering either name is refused (PULSE_EXTENSION_NAME_COLLISION).
//
// The module remains only so existing imports compile; Extensions
// returns an empty registration set.
package mmmpulse

import "github.com/frankbardon/pulse"

// Extensions returns an empty registration set: the pack's models and
// skills are built into Pulse. Merging it into pulse.Options.Extensions
// changes nothing.
func Extensions() pulse.Extensions {
	return pulse.Extensions{}
}

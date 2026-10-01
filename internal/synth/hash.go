package synth

import "github.com/frankbardon/pulse/types"

// Hash returns the canonical content hash of the synth Spec. Same
// logical spec produces the same hash across processes and Pulse
// versions where the spec's semantic meaning is unchanged.
func (s *Spec) Hash() string {
	if s == nil {
		return types.CanonicalHash("synth", (*Spec)(nil))
	}
	return types.CanonicalHash("synth", s)
}

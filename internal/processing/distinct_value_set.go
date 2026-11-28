package processing

import (
	"math"
	"slices"
)

// distinctInlineMax is the most values a distinctValueSet keeps in its
// inline slice before it promotes to a map. A linear scan over 16
// words is cheaper than a map probe, and the slice tops out at one
// 128-byte allocation — below a small Go map's header and first group.
const distinctInlineMax = 16

// distinctValueSet is AGG_DISTINCT_COUNT's exact value set (#723). It
// keeps the same identity as the map[float64]struct{} it replaced:
//
//   - -0 and +0 are one value (they compare equal, so the map stored
//     one key): both are kept as the bits of +0.
//   - NaN never equals itself, so the map stored every NaN it was
//     handed as its own key, and a merge re-inserted each of them: every
//     NaN observation is one more distinct value. The set counts them in
//     nans instead of storing them.
//
// Every other value is kept as its IEEE-754 bits — equal floats have
// equal bits once ±0 is folded — first in small (unordered, deduped by
// a linear scan), then, past distinctInlineMax values, in big (small is
// dropped). The cardinality is a pure set size, so it is the same
// whatever the insertion or merge order: worker-invariant by
// construction.
type distinctValueSet struct {
	small []uint64
	big   map[uint64]struct{}
	nans  int
}

// distinctBits is v's set key: its bits, with -0 folded onto +0. v
// must not be NaN.
func distinctBits(v float64) uint64 {
	if v == 0 {
		return 0
	}
	return math.Float64bits(v)
}

// add inserts one observed value.
func (s *distinctValueSet) add(v float64) {
	if math.IsNaN(v) {
		s.nans++
		return
	}
	s.addBits(distinctBits(v))
}

// addBits inserts one non-NaN key.
func (s *distinctValueSet) addBits(b uint64) {
	if s.big != nil {
		s.big[b] = struct{}{}
		return
	}
	if slices.Contains(s.small, b) {
		return
	}
	if len(s.small) < distinctInlineMax {
		s.small = append(s.small, b)
		return
	}
	s.promote(len(s.small) + 1)
	s.big[b] = struct{}{}
}

// promote moves the inline values into a map sized for hint entries.
func (s *distinctValueSet) promote(hint int) {
	s.big = make(map[uint64]struct{}, hint)
	for _, b := range s.small {
		s.big[b] = struct{}{}
	}
	s.small = nil
}

// merge folds o into s (o is not modified).
func (s *distinctValueSet) merge(o *distinctValueSet) {
	s.nans += o.nans
	if o.big != nil {
		if s.big == nil {
			s.promote(len(s.small) + len(o.big))
		}
		for b := range o.big {
			s.big[b] = struct{}{}
		}
		return
	}
	if s.big == nil && len(s.small) == 0 {
		// Nothing to dedupe against: an exact-capacity copy.
		s.small = slices.Clone(o.small)
		return
	}
	for _, b := range o.small {
		s.addBits(b)
	}
}

// len is the cardinality: the distinct non-NaN values plus one per
// NaN observed.
func (s *distinctValueSet) len() int {
	if s.big != nil {
		return len(s.big) + s.nans
	}
	return len(s.small) + s.nans
}

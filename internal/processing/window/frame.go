package window

import "github.com/frankbardon/pulse/types"

// resolveFrame returns inclusive [lo, hi] indices into partition for the frame
// centered at rowIdx. rowIdx is the position of the current row within
// partition (0-based). Both nil Preceding and nil Following mean unbounded.
//
// Semantics:
//   - Preceding = nil  → lo = 0 (UNBOUNDED PRECEDING)
//   - Preceding = N    → lo = max(0, rowIdx - N)
//   - Following = nil  → hi = len(partition) - 1 (UNBOUNDED FOLLOWING)
//   - Following = N    → hi = min(len-1, rowIdx + N)
func resolveFrame(rowIdx, partitionLen int, frame *types.FrameSpec) (lo, hi int) {
	if frame == nil {
		// Defensive — Apply should never pass nil here for frame ops.
		return 0, partitionLen - 1
	}
	if frame.Preceding == nil {
		lo = 0
	} else {
		lo = rowIdx - *frame.Preceding
		if lo < 0 {
			lo = 0
		}
	}
	if frame.Following == nil {
		hi = partitionLen - 1
	} else {
		hi = rowIdx + *frame.Following
		if hi > partitionLen-1 {
			hi = partitionLen - 1
		}
	}
	return lo, hi
}

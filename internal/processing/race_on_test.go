//go:build race

package processing

// raceEnabled reports a -race build: the race runtime allocates on its
// own, so pinned allocation ceilings do not apply.
const raceEnabled = true

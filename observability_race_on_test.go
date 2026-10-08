//go:build race

package pulse

// raceEnabled reports a -race build: the race runtime allocates on its
// own, so TestObservabilityOffNoAllocs' pinned counts do not apply.
const raceEnabled = true

//go:build !race

package temporal

// raceEnabled reports a -race build. The race runtime allocates on its
// own, so this package's testing.AllocsPerRun ceilings hold in plain
// builds only: under -race each one still runs, and only its ceiling
// assertion is relaxed (see #587).
const raceEnabled = false

package template

import "time"

// This file exposes the store's rescan internals to the package's external
// tests (package template_test) and to nothing else — an export_test.go is
// compiled only into the test binary, so none of it is public API.
//
// It exists because the two properties E3-S1 has to prove are invisible
// from the outside. "The interval gated the walk" and "an unchanged file
// was not re-parsed" are both statements about work NOT done, and the only
// honest way to assert work not done is to count it. Inferring either from
// wall-clock timing would produce exactly the slow, flaky test the Reload
// escape hatch exists to avoid.

// RescanInterval is the lookup-path rescan gate, exposed so a test can
// advance a fake clock by a real multiple of it rather than by a magic
// number that silently stops matching if the constant moves.
const RescanInterval = templateRescanInterval

// SetClock replaces the store's clock and re-anchors the snapshot
// timestamp to it, so a fake clock starting at an arbitrary instant leaves
// the store behaving as though it had just scanned. Without the
// re-anchoring, a fake clock reading earlier than the real construction
// time would make the snapshot look permanently fresh.
//
// Call it before driving lookups, not concurrently with them.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = now
	s.lastScan = now()
}

// ScanCount reports how many walks of the configured roots have completed,
// construction included. It is the instrument behind "two lookups inside
// the interval trigger at most one walk".
func (s *Store) ScanCount() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scans
}

// ParseCount reports how many template files those walks have read and
// parsed, construction included. It is the instrument behind "a file whose
// size and mtime are unchanged is not re-parsed": the count stays flat
// across a rescan that finds nothing changed.
func (s *Store) ParseCount() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.parses
}

// BrokenCount reports how many candidate files the most recent scan could
// not turn into a template.
//
// It is the instrument behind the per-file degradation contract: "the store
// is aware of exactly one broken file" is not observable from List alone,
// because a broken file that is shadowed by a healthy one in a
// higher-precedence root deliberately does not surface in the listing. It
// also proves the state CLEARS on repair rather than merely stopping being
// reported.
//
// It lives here rather than on Store for the same reason ScanCount does: it
// is a claim about the store's internal bookkeeping, and a public counter
// would be a permanent API surface bought to serve a test.
func (s *Store) BrokenCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, faults := range s.faults {
		n += len(faults)
	}
	return n
}

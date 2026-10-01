// Package settristate holds the cross-adapter integration tests for the
// THREE states of a `set_*` column — null, empty mask ("answered,
// selected nothing") and non-empty mask.
//
// The states are distinct inside a `.pulse` cohort by construction: null
// rides the per-record bitmap and an empty mask is a real zero-width
// selection. Keeping them distinct through an EXPORT is a per-adapter
// obligation, so the assertions have to run against the real Writer and
// Reader of every format. That cannot live in the root io/ package —
// every format subpackage imports io/ for the Writer interface, so the
// dependency only runs one way — and it should not be copied into each
// adapter either, because the whole point is that the seven formats
// agree on one convention (io.EmptySetCell).
//
// The package is test-only: every .go file but this one ends in
// _test.go. It follows the precedent set by io/exportoverlay.
package settristate

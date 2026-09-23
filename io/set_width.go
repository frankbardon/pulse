package io

import "github.com/frankbardon/pulse/encoding"

// Set-width selection, exported so there is exactly ONE ladder.
//
// The rung table itself is encoding.SetLadder, derived off
// FieldType.MaxSetEntries so a rung's capacity cannot drift from the
// bitmask width it names. These are thin aliases retained so existing
// io callers keep their import, and so the two packages cannot disagree
// about which rung holds N elements.
//
// It exists because io/spss/mrset.go carried a second, independent copy
// of the ladder for the derived multiple-dichotomy column. A duplicated
// ladder does not fail loudly when it falls behind: the SPSS importer
// went on refusing every set over 64 constituents — the exact case the
// wide rungs were added for — while inference beside it was already
// typing a 206-token column as set_u256. Nothing errored; a convenience
// column simply was not there, with a warning explaining that no wider
// type existed.

// SetTypeFor returns the narrowest set_* type with a bit for each of
// `elements` elements, and reports false when no rung is wide enough
// (or when `elements` is not positive — a set with no elements has no
// type, and that is a caller error rather than a width verdict).
//
// This is the same selection inference makes, so a column of N distinct
// tokens and a declared response set of N constituents land on the same
// rung by construction rather than by agreement.
func SetTypeFor(elements int) (encoding.FieldType, bool) {
	return encoding.SetTypeFor(elements)
}

// WidestSetType returns the top rung of the ladder — the widest set_*
// type Pulse has.
//
// Callers that must NAME the ceiling in a diagnostic use this rather
// than writing "set_u256" into a string, so the message cannot outlive
// the type it names.
func WidestSetType() encoding.FieldType {
	return encoding.WidestSetType()
}

// MaxSetElements returns how many elements the widest rung addresses —
// the most a set column can ever hold.
func MaxSetElements() int {
	return int(WidestSetType().MaxSetEntries())
}

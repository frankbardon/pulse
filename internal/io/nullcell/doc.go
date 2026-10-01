// Package nullcell holds the cross-adapter integration tests for the
// TWO absent-looking states of a `categorical_*` column — a null cell
// and a cell whose value IS the empty string.
//
// The states are distinct inside a `.pulse` cohort by construction:
// null rides the per-record bitmap and "" is an ordinary dictionary
// entry. Keeping them distinct through an export and a re-import is a
// per-adapter obligation, and unlike `set_*` it cannot be met with an
// in-band marker: every text a sentinel could use is a legal
// categorical value, so the distinction rides io.NullAwareWriter /
// io.NullAwareReader instead.
//
// Four adapters carry it (ndjson, jsonarray, arrow, parquet) and three
// do not (csv, tsv, excel). The gap is DOCUMENTED rather than silent,
// and the assertions here pin both halves — including the direction of
// the collapse, which must always be toward NULL. See io.NullAwareReader
// for the matrix and the reasons.
//
// The package is test-only: every .go file but this one ends in
// _test.go. It follows the precedent set by io/settristate.
package nullcell

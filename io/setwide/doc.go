// Package setwide holds the per-adapter wide-set round-trip tests: one
// test per tabular format, proving that a 206-member `set_u256` column
// survives an export and a re-import with every bit in the place it
// started.
//
// # Why nine tests and not one table
//
// Every adapter reaches a set column through the same shared surface —
// io/export.go renders the mask to a delimiter-joined label list and
// io/import.go parses one back — so eight of the nine LOOK like they
// inherit wide sets for free. That resemblance is exactly what lets one
// adapter quietly not work: io/arrow and io/parquet each declared the
// column LIST<UTF8> and then handed the joined string to a builder that
// parses JSON, so a set export reported success having written ZERO
// rows, at every width including the four narrow rungs that predate the
// wide ones. Nothing in the shared surface was wrong.
//
// A table-driven matrix would have expressed that as one row, and a row
// is the thing that goes missing. So each format gets a named test that
// wires its own Writer and Reader and spells out its own expectations.
// The plumbing they share (building the fixture cohort, reading the
// stored masks back out of the `.pulse` bytes) is format-independent by
// construction: a fault there fails all nine, loudly.
//
// # What each test asserts
//
//   - a 206-entry dictionary, which is past every rung but the widest;
//   - one record selecting members on BOTH sides of bit 64, bit 127 and
//     bit 200 — the places a low-word-only implementation truncates in
//     silence — and neighbouring bits that must stay clear, so an
//     off-by-one shift cannot pass as a match;
//   - null and empty-mask surviving as DISTINCT states, because an empty
//     selection is a real answer ("shown the question, ticked nothing")
//     and not a missing one;
//   - a non-zero exported row count, which is what the arrow/parquet
//     hole would have failed.
//
// The package is test-only — every .go file but this one ends in
// _test.go — and follows the precedent of io/settristate, which runs the
// null / empty / selected tri-state across the same adapters at two
// rungs. This one goes to the top of the ladder and adds the two formats
// that matrix cannot reach through a []string Reader: `.sav`, whose set
// column expands to one dichotomy variable per member, and jsonshared,
// which is a coercion pair rather than a Reader/Writer.
package setwide

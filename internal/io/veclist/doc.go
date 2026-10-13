// Package veclist holds the per-adapter tests for importing a NATIVE
// list column as a vector field (`--vector NAME` with no column list):
// Arrow FixedSizeList and LIST, Parquet LIST, an NDJSON array and a
// JSON-array file.
//
// Each list source implements iocore.ListAwareReader, which hands the
// import a cell's elements typed — off the Arrow child array or the
// decoded JSON numbers — before the row text the source pipe-joins for
// the set_* ladder is consulted. The tests pin, per format: the vector
// a list column becomes (dim = list length, a null list a null vector,
// a null element NaN, a list of nulls only a null vector), the f32 and
// f64 targets, the coded refusals (variable length, non-numeric
// elements, a scalar cell, an out-of-range or unknowable dim), and that
// WITHOUT the declaration the import is byte-identical to one through a
// source with no list channel at all.
//
// The package is test-only — every .go file but this one ends in
// _test.go — following internal/io/settristate and internal/io/setwide.
package veclist

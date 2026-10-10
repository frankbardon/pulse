package encoding

import "github.com/frankbardon/pulse/encoding"

// CellWidth is the bytes one field of type ft occupies in a LOGICAL
// (ungrouped) record payload: its ByteSize, except that a bit-packed
// type (u4, packed_bool — ByteSize()==0) still consumes one whole byte
// on the wire, matching encoding.Schema.RecordByteSize. It is the one
// per-type width every schema-producing path lays a row out with.
func CellWidth(ft encoding.FieldType) int {
	if ft.IsBitPacked() {
		return 1
	}
	return ft.ByteSize()
}

// LayoutFields is the canonical record layout: it assigns every field's
// ByteOffset (the running sum of CellWidth over the fields before it)
// and BitPosition (always 0 — each bit-packed field owns its own byte,
// never sharing it with a neighbour) in place, and returns the payload
// width (the stride minus any trailing null bitmap).
//
// The stored offset is a cache of this derivation — readers are
// cursor-driven — but it is written into the schema block and compared
// by shard cohesion and the shard dictionary rewrite, so every path that
// builds a schema (inference, explicit schema files, the cohort builder,
// adapters that read a typed source schema, synth, width promotion)
// lays it out HERE. A second hand-rolled sum that summed ByteSize()
// gave every field after a bit-packed one an offset one byte short.
func LayoutFields(fields []encoding.Field) int {
	off := 0
	for i := range fields {
		fields[i].ByteOffset = off
		fields[i].BitPosition = 0
		off += CellWidth(fields[i].Type)
	}
	return off
}

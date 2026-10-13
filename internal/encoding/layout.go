package encoding

import "github.com/frankbardon/pulse/encoding"

// LayoutFields is the canonical record layout: it assigns every field's
// ByteOffset (the running sum of Schema.FieldWidth over the fields
// before it) and BitPosition (always 0 — each bit-packed field owns its
// own byte, never sharing it with a neighbour) in place, and returns the
// payload width (the stride minus any trailing null bitmap).
//
// The stored offset is a cache of this derivation — readers are
// cursor-driven — but it is written into the schema block and compared
// by shard cohesion and the shard dictionary rewrite, so every path that
// builds a schema (inference, explicit schema files, the cohort builder,
// adapters that read a typed source schema, synth, width promotion)
// lays it out HERE. A second hand-rolled sum that summed ByteSize()
// gave every field after a bit-packed one an offset one byte short.
//
// It takes the whole schema, not a field slice: a field's width is the
// field-aware Schema.FieldWidth, never a function of its type byte alone
// (TestNoTypeOnlyWidthInStridePaths).
func LayoutFields(s *encoding.Schema) int {
	off := 0
	for i := range s.Fields {
		s.Fields[i].ByteOffset = off
		s.Fields[i].BitPosition = 0
		off += s.FieldWidth(i)
	}
	return off
}

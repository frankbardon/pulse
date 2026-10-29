package encoding_test

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// strideSchema is one point of the space TestValidateStructuralCohesion_FixesRecordStride
// walks: nine fields (a two-byte null bitmap when any is nullable), the
// nullable flags of fields 0, 4 and 8 from nullMask, a vec_f32 at field 3
// of the given dim, and one of three group layouts — none, an indexed group
// over fields 0 and 4 (field 0 its key), or a constant group over field 6.
func strideSchema(nullMask, dim, layout int) *encoding.Schema {
	fields := []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU8},
		{Name: "b", Type: encoding.FieldTypePackedBool},
		{Name: "c", Type: encoding.FieldTypeU4},
		{Name: "v", Type: encoding.FieldTypeVecF32},
		{Name: "d", Type: encoding.FieldTypeF64},
		{Name: "e", Type: encoding.FieldTypeU16},
		{Name: "f", Type: encoding.FieldTypeU16},
		{Name: "g", Type: encoding.FieldTypeU32},
		{Name: "h", Type: encoding.FieldTypeU16},
	}
	for bit, fi := range []int{0, 4, 8} {
		fields[fi].Nullable = nullMask&(1<<bit) != 0
	}
	s := &encoding.Schema{Fields: fields, Vectors: []encoding.VectorField{{FieldIndex: 3, Dim: dim}}}
	switch layout {
	case 1:
		s.Groups = []encoding.Group{{Kind: encoding.GroupKindIndexed,
			Members: []encoding.GroupMember{{Field: 0, Key: true}, {Field: 4}}}}
	case 2:
		s.Groups = []encoding.Group{{Kind: encoding.GroupKindConstant,
			Members: []encoding.GroupMember{{Field: 6, Key: true}}}}
	}
	return s
}

// TestValidateStructuralCohesion_FixesRecordStride pins the invariant that
// let ShardDecodeCompatible drop its own record-stride check (cohort-reshape
// E6-S3): two schemas ValidateStructuralCohesion admits have the SAME
// RecordByteSize. Every shard is decoded at the canonical stride, so if
// cohesion ever admitted a pair whose strides differ — say the nullability
// rule stopped comparing bitmap widths — every row after the first would
// decode misaligned, as plausible values. The walk covers every ordered pair
// over nullability, vector dim and group layout, and must see both admitted
// pairs that differ (the nullable-canonical, non-nullable-shard admission)
// and refusals, so it cannot pass vacuously.
func TestValidateStructuralCohesion_FixesRecordStride(t *testing.T) {
	t.Parallel()

	var schemas []*encoding.Schema
	for nullMask := 0; nullMask < 8; nullMask++ {
		for _, dim := range []int{2, 3} {
			for layout := 0; layout < 3; layout++ {
				schemas = append(schemas, strideSchema(nullMask, dim, layout))
			}
		}
	}
	admittedDiffering, refused := 0, 0
	for ci, canonical := range schemas {
		for ii, incoming := range schemas {
			if _, err := encx.ValidateStructuralCohesion(canonical, incoming); err != nil {
				refused++
				continue
			}
			if ci != ii {
				admittedDiffering++
			}
			if c, n := canonical.RecordByteSize(), incoming.RecordByteSize(); c != n {
				t.Errorf("cohesion admitted schemas %d and %d with strides %d and %d", ci, ii, c, n)
			}
		}
	}
	if admittedDiffering == 0 || refused == 0 {
		t.Fatalf("admitted %d differing pairs and refused %d; the walk must exercise both", admittedDiffering, refused)
	}
}

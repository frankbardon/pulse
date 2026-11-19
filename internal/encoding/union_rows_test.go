package encoding_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

func unionDict(t *testing.T, vals ...string) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range vals {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// unionRowSchemas: a member (region {b, a}, tags set_u8 {z, x}, units
// u16, all but units nullable) and the merged union it moves into
// (region {a, b, c}, tags widened to set_u128 {x, y, z}, units, and the
// source field {m0, m1}).
func unionRowSchemas(t *testing.T) (member, merged *encoding.Schema) {
	t.Helper()
	member = &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: unionDict(t, "b", "a")},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Nullable: true, Dictionary: unionDict(t, "z", "x")},
		{Name: "units", Type: encoding.FieldTypeU16},
	}}
	merged = &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: unionDict(t, "a", "b", "c")},
		{Name: "tags", Type: encoding.FieldTypeSetU128, Nullable: true, Dictionary: unionDict(t, "x", "y", "z")},
		{Name: "units", Type: encoding.FieldTypeU16},
		{Name: "_source", Type: encoding.FieldTypeCategoricalU8, Dictionary: unionDict(t, "m0", "m1")},
	}}
	return member, merged
}

// A member record moves into the merged layout: the categorical index
// and every set bit move to their values' merged entries, the set is
// re-encoded at the wider rung, other bytes copy, null bits keep their
// field and a null categorical keeps its placeholder, and the source
// field holds the member index. Whole merged records skip as whole
// member records.
func TestUnionRowPlan_TranscodesIntoMergedLayout(t *testing.T) {
	t.Parallel()
	member, merged := unionRowSchemas(t)
	plan, err := encx.NewUnionRowPlan(member, merged, 1)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceStride() != 1+1+2+1 || plan.Stride() != 1+16+2+1+1 {
		t.Fatalf("strides %d -> %d", plan.SourceStride(), plan.Stride())
	}
	// Row 0: region "b" (0), tags {z, x} (bits 0, 1), units 513.
	// Row 1: region null (placeholder 1), tags {x} (bit 1), units 7.
	src := []byte{0, 0b11, 0x01, 0x02, 0, 1, 0b10, 7, 0, 0b001}
	r := encx.NewUnionRowStream(bytes.NewReader(src), plan)
	row := make([]byte, plan.Stride())
	if _, err := io.ReadFull(r, row); err != nil {
		t.Fatal(err)
	}
	if row[0] != 1 {
		t.Errorf("region index %d, want 1 (b)", row[0])
	}
	mask, err := encoding.SetMaskFromBytes(encoding.FieldTypeSetU128, row[1:17])
	if err != nil {
		t.Fatal(err)
	}
	if want := (encoding.SetMask{}).WithBit(0).WithBit(2); mask != want {
		t.Errorf("tags mask %v, want {x, z} = bits 0, 2", mask)
	}
	if units := binary.LittleEndian.Uint16(row[17:19]); units != 513 || row[19] != 1 || row[20] != 0 {
		t.Errorf("units %d source %d bitmap %08b, want 513 / 1 / none null", units, row[19], row[20])
	}
	if _, err := io.ReadFull(r, row); err != nil {
		t.Fatal(err)
	}
	if row[0] != 1 || row[20] != 0b001 || !encoding.BitmapIsNull(row[20:], 0) {
		t.Errorf("null region: placeholder %d bitmap %08b, want the placeholder kept and bit 0 set", row[0], row[20])
	}
	if _, err := io.ReadFull(r, row); err != io.EOF {
		t.Errorf("past the last record: %v, want io.EOF", err)
	}

	// Seek: the first record skipped whole, then part of the second.
	sk := encx.NewUnionRowStream(bytes.NewReader(src), plan).(io.ReadSeeker)
	if _, err := sk.Seek(int64(plan.Stride())+17, io.SeekCurrent); err != nil {
		t.Fatal(err)
	}
	tail := make([]byte, plan.Stride()-17)
	if _, err := io.ReadFull(sk, tail); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(tail[0:2]) != 7 || tail[2] != 1 {
		t.Errorf("after seek: units %d source %d, want 7 / 1", binary.LittleEndian.Uint16(tail[0:2]), tail[2])
	}
}

// A plan over schemas the union merge did not produce is refused as an
// internal fault, never transcoded.
func TestUnionRowPlan_RefusesForeignSchemas(t *testing.T) {
	t.Parallel()
	member, merged := unionRowSchemas(t)
	noSource := &encoding.Schema{Fields: merged.Fields[:3]}
	renamed := &encoding.Schema{Fields: append([]encoding.Field{{Name: "zone", Type: encoding.FieldTypeCategoricalU8}}, merged.Fields[1:]...)}
	for name, m := range map[string]*encoding.Schema{"no source field": noSource, "renamed field": renamed} {
		if _, err := encx.NewUnionRowPlan(member, m, 0); !errors.HasCode(err, errors.ENCODING_INTERNAL) {
			t.Errorf("%s: %v, want ENCODING_INTERNAL", name, err)
		}
	}
	if _, err := encx.NewUnionRowPlan(member, merged, 256); !errors.HasCode(err, errors.ENCODING_INTERNAL) {
		t.Errorf("member index past the source width: %v, want ENCODING_INTERNAL", err)
	}
}

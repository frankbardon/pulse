package encoding

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// truncationSchema declares every 0x02 feature at once — an indexed
// parent group over a nullable member, a vector with kind, scale and
// labels, a categorical dictionary — so its schema block exercises every
// reader the extension block has.
func truncationSchema() *Schema {
	d := NewDictionary()
	d.Add("red")
	d.Add("blue")
	return &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8},
			{Name: "b", Type: FieldTypeU16, Nullable: true},
			{Name: "v", Type: FieldTypeVecF64, Description: "a two-element vector"},
			{Name: "c", Type: FieldTypeCategoricalU8, Dictionary: d},
		},
		Groups: []Group{{
			Kind:    GroupKindIndexed,
			Members: []GroupMember{{Field: 0, Key: true}, {Field: 1}},
			// Entry = a (1 byte) + b (2 bytes) + member null bitmap (1 byte).
			Entries: []byte{1, 10, 0, 0, 2, 0, 0, 0x02},
		}},
		Vectors: []VectorField{{FieldIndex: 2, Dim: 2, Kind: VectorKindScale, HasScale: true, ScaleMin: 0, ScaleMax: 10, Labels: []string{"x", "y"}}},
	}
}

// TestReadSchema_V2EveryTruncationRefused: a 0x02 schema block cut at ANY
// byte is refused with a coded encoding error by both the full reader
// and the layout-only reader — never accepted as a shorter schema, never
// a panic. A truncated cohort header must not decode into a plausible
// smaller schema.
func TestReadSchema_V2EveryTruncationRefused(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := writeSchemaVersion(&b, truncationSchema(), FormatVersionV2); err != nil {
		t.Fatalf("write: %v", err)
	}
	full := b.Bytes()
	for _, layout := range []bool{false, true} {
		got, err := readSchema(bytes.NewReader(full), FormatVersionV2, layout)
		if err != nil {
			t.Fatalf("layout=%v: full block refused: %v", layout, err)
		}
		if len(got.Groups) != 1 || (!layout && got.GroupEntryCount(0) != 2) || len(got.Vectors) != 1 || got.Vectors[0].Labels[1] != "y" {
			t.Fatalf("layout=%v: round trip lost the extension: %+v", layout, got)
		}
		for cut := 0; cut < len(full); cut++ {
			_, err := readSchema(bytes.NewReader(full[:cut]), FormatVersionV2, layout)
			if err == nil {
				t.Fatalf("layout=%v: block cut at %d/%d accepted", layout, cut, len(full))
			}
			if !errors.HasCode(err, errors.ENCODING_INVALID) && !errors.HasCode(err, errors.ENCODING_IO) {
				t.Fatalf("layout=%v: cut at %d: err = %v, want a coded encoding error", layout, cut, err)
			}
		}
	}
}

// TestWriteSchema_V2IOErrorsSurface: a writer that fails at ANY byte of
// the 0x02 schema block surfaces an error — the writer never reports
// success over a short block.
func TestWriteSchema_V2IOErrorsSurface(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	if err := writeSchemaVersion(&b, truncationSchema(), FormatVersionV2); err != nil {
		t.Fatal(err)
	}
	for budget := 0; budget < b.Len(); budget++ {
		if err := writeSchemaVersion(&limitWriter{n: budget}, truncationSchema(), FormatVersionV2); err == nil {
			t.Fatalf("write failing after %d/%d bytes reported success", budget, b.Len())
		}
	}
}

package encoding

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// TestConstantDetector_Edges: the detector refuses a grouped schema and
// a row of the wrong width (without counting it), reports nothing
// before its first row, keeps its own copy of that row, and calls a
// field null-constant only when it is nullable AND null on that row.
func TestConstantDetector_Edges(t *testing.T) {
	t.Parallel()

	fx := groupFixtures(t)[0]
	_, gs := groupedTwin(t, flatCohort(t, fx.schema, fx.rows), fx.specs)
	if _, err := NewConstantDetector(gs); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("grouped schema: err = %v, want ENCODING_INVALID", err)
	}
	if _, err := PlanConstantElisionFor(gs, nil, MinElisionRows, nil); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("PlanConstantElisionFor(grouped): err = %v, want ENCODING_INVALID", err)
	}

	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU8},
		{Name: "b", Type: encoding.FieldTypeU8, Nullable: true},
	}}
	d, err := NewConstantDetector(s)
	if err != nil {
		t.Fatal(err)
	}
	if d.FirstRow() != nil || d.ConstantFields() != nil || d.IsNullConstant(1) {
		t.Errorf("before any row: first %v, constant %v, null-constant %v", d.FirstRow(), d.ConstantFields(), d.IsNullConstant(1))
	}
	if err := d.Observe([]byte{1, 2}); !errors.HasCode(err, errors.ENCODING_INVALID) || d.Rows() != 0 {
		t.Errorf("short row: (err %v, rows %d), want ENCODING_INVALID and no row counted", err, d.Rows())
	}
	row := []byte{7, 0, 0x02} // a = 7, b = null (bit 1)
	if err := d.Observe(row); err != nil {
		t.Fatal(err)
	}
	row[0] = 99 // the caller reuses its buffer
	if err := d.Observe([]byte{7, 5, 0x02}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.FirstRow(), []byte{7, 0, 0x02}) {
		t.Errorf("FirstRow = %v, want the detector's own copy [7 0 2]", d.FirstRow())
	}
	// b's placeholder byte changed under the null bit, so it is not
	// byte-constant; a is.
	if got := d.ConstantFields(); len(got) != 1 || got[0] != 0 {
		t.Errorf("ConstantFields = %v, want [0]", got)
	}
	if d.IsNullConstant(0) {
		t.Error("non-nullable a reported null-constant")
	}

	nb := &encoding.Schema{Fields: []encoding.Field{{Name: "a", Type: encoding.FieldTypeU8}}}
	dn, _ := NewConstantDetector(nb)
	_ = dn.Observe([]byte{3})
	if dn.IsNullConstant(0) {
		t.Error("bitmap-free schema reported null-constant")
	}

	dc, _ := NewConstantDetector(s)
	for i := 0; i < 3; i++ {
		_ = dc.Observe([]byte{1, 0, 0x02})
	}
	if !dc.IsNullConstant(1) {
		t.Error("b null on every row is not null-constant")
	}
}

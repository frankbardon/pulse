package spss

import (
	"bytes"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// TestCorruptCategorical_SPSSSetMemberRefused (U51 E5-S8): a set cell
// selecting a bit past its dictionary has no member variable to land in,
// so writing the case would drop the selection silently; readCohortCase
// refuses it. A NULL set over the same stored mask is not data and
// reads.
//
// Falsified by deleting the encx.SetMemberFault check at the end of
// readCohortCase (the corrupt case then reads).
func TestCorruptCategorical_SPSSSetMemberRefused(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	_, _ = dict.Add("A")
	_, _ = dict.Add("B")
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "picks", Type: encoding.FieldTypeSetU8, Nullable: true, Dictionary: dict},
	}}
	read := func(mask uint64, null bool) error {
		var rec bytes.Buffer
		if err := encoding.WriteFieldValue(&rec, encoding.FieldTypeSetU8, mask); err != nil {
			t.Fatal(err)
		}
		bm := make([]byte, s.BitmapByteSize())
		if null {
			encoding.BitmapSetNull(bm, 0)
		}
		if err := encoding.WriteBitmap(&rec, bm); err != nil {
			t.Fatal(err)
		}
		return readCohortCase(bytes.NewReader(rec.Bytes()), s, NewCase(s))
	}
	if err := read(0b11, false); err != nil {
		t.Fatalf("in-range mask: %v", err)
	}
	if err := read(0b100, true); err != nil {
		t.Fatalf("null cell over an out-of-range placeholder: %v", err)
	}
	err := read(0b101, false)
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.ENCODING_INVALID || ce.Details["reason"] != encx.ReasonSetMemberOutOfRange {
		t.Fatalf("err = %v, want ENCODING_INVALID (%s)", err, encx.ReasonSetMemberOutOfRange)
	}
}

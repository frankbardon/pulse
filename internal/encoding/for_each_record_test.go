package encoding

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestForEachRecord: whole records are visited in order; a clean end
// between records ends the walk; a tail shorter than one record is
// ENCODING_INVALID naming the trailing bytes, never silently dropped;
// a non-positive stride is refused.
func TestForEachRecord(t *testing.T) {
	var seen []byte
	n, err := ForEachRecord(bytes.NewReader([]byte{1, 2, 3, 4, 5, 6}), 2, func(rec []byte) error {
		seen = append(seen, rec[0])
		return nil
	})
	if err != nil || n != 3 || !bytes.Equal(seen, []byte{1, 3, 5}) {
		t.Fatalf("n=%d seen=%v err=%v", n, seen, err)
	}
	n, err = ForEachRecord(bytes.NewReader([]byte{1, 2, 3, 4, 5}), 2, func([]byte) error { return nil })
	var ce *errors.CodedError
	if !errors.HasCode(err, errors.ENCODING_INVALID) || n != 2 {
		t.Fatalf("truncated: n=%d err=%v", n, err)
	}
	if ce, _ = err.(*errors.CodedError); ce == nil || ce.Details["trailing_bytes"] != 1 {
		t.Fatalf("details = %v", err)
	}
	if _, err := ForEachRecord(bytes.NewReader(nil), 0, func([]byte) error { return nil }); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("zero stride: err = %v", err)
	}
}

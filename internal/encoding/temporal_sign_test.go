package encoding

import (
	"encoding/binary"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestDecoders_TemporalWordsAreSigned pins BOTH float decoders — the
// reference rawToFloat64 and the fast decodeFixed — to the signed reading
// of the date (int32 days) and datetime (int64 seconds) words. The
// equivalence suite only proves they agree; this proves they agree on the
// right answer.
func TestDecoders_TemporalWordsAreSigned(t *testing.T) {
	for _, days := range []int32{-25567, -1, 0, 19782} {
		raw := uint64(uint32(days))
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, uint32(days))
		if got := rawToFloat64(encoding.FieldTypeDate, raw); got != float64(days) {
			t.Errorf("rawToFloat64(date %d) = %v", days, got)
		}
		if got := decodeFixed(encoding.FieldTypeDate, buf); got != float64(days) {
			t.Errorf("decodeFixed(date %d) = %v", days, got)
		}
	}
	for _, sec := range []int64{-2208967200, -1, 0, 1709209800} {
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, uint64(sec))
		if got := rawToFloat64(encoding.FieldTypeDateTime, uint64(sec)); got != float64(sec) {
			t.Errorf("rawToFloat64(datetime %d) = %v", sec, got)
		}
		if got := decodeFixed(encoding.FieldTypeDateTime, buf); got != float64(sec) {
			t.Errorf("decodeFixed(datetime %d) = %v", sec, got)
		}
	}
}

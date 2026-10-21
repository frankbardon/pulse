package processing

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestRenderRecordCells_U64Exact: a u64 cell renders as the record's
// EXACT stored word (Go uint64), never its float64 echo, so 2^53+1 and
// MaxUint64 print their own digits; a value below 2^53 prints the same
// digits the float64 echo always did. Other numeric cells stay float64.
func TestRenderRecordCells_U64Exact(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU64},
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 8},
	}}
	const two53 = uint64(1) << 53
	for _, v := range []uint64{two53 - 1, two53, two53 + 1, 1<<64 - 1, 1<<64 - 2, 7, 0} {
		rec := NewRecord(schema, map[string]float64{"k": float64(v), "a": 1.5})
		rec.SetExactU64("k", v)
		cells := RenderRecordCells(rec)
		got, ok := cells["k"].(uint64)
		if !ok || got != v {
			t.Errorf("k cell = %T %v, want uint64 %d", cells["k"], cells["k"], v)
		}
		if _, ok := cells["a"].(float64); !ok {
			t.Errorf("a cell = %T, want float64", cells["a"])
		}
		raw, err := json.Marshal(cells["k"])
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != strconv.FormatUint(v, 10) {
			t.Errorf("json(k) = %s, want %d", raw, v)
		}
		if v < two53 {
			echo, _ := json.Marshal(float64(v))
			if string(raw) != string(echo) {
				t.Errorf("below 2^53 wire changed: %s vs float echo %s", raw, echo)
			}
		}
	}
}

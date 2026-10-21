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

// TestFinalizeRowCells_U64Words: record rows carry the float64 echo
// until finalizeRowCells, which swaps in each record's exact word — by
// row identity, so a reorder between capture and render is followed —
// and leaves a cell no stage left as that word's echo (null / absent, or
// rewritten) untouched. No u64 field means no side table at all.
func TestFinalizeRowCells_U64Words(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU64},
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 8},
	}}
	const big = uint64(1)<<53 + 1
	r0 := NewRecord(schema, map[string]float64{"k": float64(big), "a": 1})
	r0.SetExactU64("k", big)
	r1 := NewRecord(schema, map[string]float64{"k": 7, "a": 2})
	r1.SetExactU64("k", 7)
	r2 := NewRecordWithNulls(schema, map[string]float64{"a": 3}, map[string]bool{"k": true})
	rows, words := recordsToRows([]*Record{r0, r1, r2}, schema)
	if _, ok := rows[0]["k"].(float64); !ok {
		t.Fatalf("pre-render k = %T, want the float64 echo", rows[0]["k"])
	}
	rows[0], rows[1] = rows[1], rows[0] // a Request.Sort reorder
	rows[2]["a"] = 9.0
	finalizeRowCells(rows, schema, nil, words)
	if got, ok := rows[1]["k"].(uint64); !ok || got != big {
		t.Errorf("reordered row k = %T %v, want uint64 %d", rows[1]["k"], rows[1]["k"], big)
	}
	if got, ok := rows[0]["k"].(uint64); !ok || got != 7 {
		t.Errorf("row k = %T %v, want uint64 7", rows[0]["k"], rows[0]["k"])
	}
	if _, present := rows[2]["k"]; present {
		t.Errorf("null k rendered as %v, want absent", rows[2]["k"])
	}
	if _, ok := rows[0]["a"].(float64); !ok {
		t.Errorf("f64 a = %T, want float64", rows[0]["a"])
	}
	// A stage that rewrote the cell keeps its value.
	rows2, words2 := recordsToRows([]*Record{r0}, schema)
	rows2[0]["k"] = 1.5
	finalizeRowCells(rows2, schema, nil, words2)
	if rows2[0]["k"] != 1.5 {
		t.Errorf("rewritten k = %v, want 1.5 kept", rows2[0]["k"])
	}
	if w := newU64RowWords(&encoding.Schema{Fields: []encoding.Field{{Name: "a", Type: encoding.FieldTypeF64}}}, 1); w != nil {
		t.Errorf("no-u64 schema side table = %+v, want nil", w)
	}
}

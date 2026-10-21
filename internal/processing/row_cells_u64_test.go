package processing

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/types"
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
		cells, err := RenderRecordCells(rec)
		if err != nil {
			t.Fatal(err)
		}
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
	rows, words, err := recordsToRows([]*Record{r0, r1, r2}, schema)
	if err != nil {
		t.Fatal(err)
	}
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
	rows2, words2, err := recordsToRows([]*Record{r0}, schema)
	if err != nil {
		t.Fatal(err)
	}
	rows2[0]["k"] = 1.5
	finalizeRowCells(rows2, schema, nil, words2)
	if rows2[0]["k"] != 1.5 {
		t.Errorf("rewritten k = %v, want 1.5 kept", rows2[0]["k"])
	}
	if w := newU64RowWords(&encoding.Schema{Fields: []encoding.Field{{Name: "a", Type: encoding.FieldTypeF64}}}, 1); w != nil {
		t.Errorf("no-u64 schema side table = %+v, want nil", w)
	}
}

// TestFinalizeRowCells_U64CopyWindows (E4-S7): a WIN_LAG / WIN_LEAD
// output copying a u64 cell renders the SOURCE row's exact word — across
// offsets, partitions, a lag of a lag and a Request.Sort reorder — while
// every window and the sort read the float64 echo until the render. An
// out-of-partition default / null and a copy of a non-u64 column are
// untouched, as is a non-copy window over the id.
func TestFinalizeRowCells_U64CopyWindows(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU64},
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 8},
		{Name: "g", Type: encoding.FieldTypeF64, ByteOffset: 16},
	}}
	const two53 = uint64(1) << 53
	ids := []uint64{two53 - 1, two53 + 1, 1<<64 - 1, two53, 1<<64 - 2, 7}
	var recs []*Record
	for i, id := range ids {
		r := NewRecord(schema, map[string]float64{"k": float64(id), "a": float64(i), "g": float64(i % 2)})
		r.SetExactU64("k", id)
		recs = append(recs, r)
	}
	// A null id in partition 0 at a = 6.
	recs = append(recs, NewRecordWithNulls(schema, map[string]float64{"a": 6, "g": 0}, map[string]bool{"k": true}))
	rows, words, err := recordsToRows(recs, schema)
	if err != nil {
		t.Fatal(err)
	}
	ord := []types.OrderKey{{Field: "a"}}
	part := []string{"g"}
	wins := []*types.Window{
		{Type: types.WIN_LAG, Field: "k", Label: "lag1", OrderBy: ord},
		{Type: types.WIN_LEAD, Field: "k", Label: "lead2", OrderBy: ord, Params: json.RawMessage(`{"offset":2}`)},
		{Type: types.WIN_LAG, Field: "k", Label: "plag", PartitionBy: part, OrderBy: ord},
		{Type: types.WIN_LEAD, Field: "k", Label: "plead", PartitionBy: part, OrderBy: ord, Params: json.RawMessage(`{"default":5}`)},
		{Type: types.WIN_LAG, Field: "lag1", Label: "lag_lag", OrderBy: ord},
		{Type: types.WIN_LAG, Field: "a", Label: "alag", OrderBy: ord},
		{Type: types.WIN_DELTA, Field: "k", Label: "dk", OrderBy: ord},
	}
	if err := window.ApplyObserved(context.Background(), rows, wins, nil, nil, words.copyObserver()); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if v := row["lag1"]; v != nil {
			if _, ok := v.(float64); !ok {
				t.Fatalf("row %d lag1 before render = %T, want the float64 echo", i, v)
			}
		}
	}
	window.Sort(rows, []types.OrderKey{{Field: "k", Desc: true}})
	finalizeRowCells(rows, schema, wins, words)

	byA := map[float64]map[string]any{}
	for _, row := range rows {
		byA[row["a"].(float64)] = row
	}
	n := len(recs)
	idAt := func(i int) any { // the exact id of input row i, nil when null / out of range
		if i < 0 || i >= len(ids) {
			return nil
		}
		return ids[i]
	}
	inPart := func(i, step int) any { // same-g neighbour step rows away
		j := i + 2*step
		if j < 0 || j >= n {
			return nil
		}
		return idAt(j)
	}
	for i := 0; i < n; i++ {
		row := byA[float64(i)]
		check := func(col string, want any) {
			t.Helper()
			if want == nil {
				if row[col] != nil {
					t.Errorf("a=%d %s = %T %v, want null", i, col, row[col], row[col])
				}
				return
			}
			got, ok := row[col].(uint64)
			if !ok || got != want.(uint64) {
				t.Errorf("a=%d %s = %T %v, want uint64 %d", i, col, row[col], row[col], want)
			}
		}
		check("lag1", idAt(i-1))
		check("lead2", idAt(i+2))
		check("plag", inPart(i, -1))
		check("lag_lag", idAt(i-2))
		if j := i + 2; j < n {
			check("plead", inPart(i, 1))
		} else if row["plead"] != 5.0 {
			t.Errorf("a=%d plead = %T %v, want the default 5.0 untouched", i, row["plead"], row["plead"])
		}
		if i > 0 {
			if _, ok := row["alag"].(float64); !ok {
				t.Errorf("a=%d alag = %T, want float64 (non-u64 copy)", i, row["alag"])
			}
		}
		if _, ok := row["dk"].(float64); !ok && i > 0 && i < len(ids) {
			t.Errorf("a=%d dk = %T, want float64 (non-copy window)", i, row["dk"])
		}
	}
	// Without the observer a copy stays the echo (E4-S6 behaviour).
	rows2, words2, err := recordsToRows(recs[:2], schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := window.ApplyKeyed(context.Background(), rows2, wins[:1], nil, nil); err != nil {
		t.Fatal(err)
	}
	finalizeRowCells(rows2, schema, wins[:1], words2)
	if _, ok := rows2[1]["lag1"].(float64); !ok {
		t.Errorf("unobserved lag1 = %T, want float64", rows2[1]["lag1"])
	}
}

//go:build heavy

package processing

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestGroupMemory_ChainRowIteratorHoldsNoRecords (U51 E5-S12, #499): a
// later chain stage's input costs a few bytes a row beside the previous
// stage's rows — the categorical codes resolved up front — never a
// record a row: the iterator builds each record when Next reaches it
// and keeps none past the current one. Materializing every record up
// front (the eager form) held ~300 B a row of a two-field schema for
// the whole stage. Keys are low-cardinality so the dictionary is noise.
func TestGroupMemory_ChainRowIteratorHoldsNoRecords(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	const n = 200_000
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"k": fmt.Sprintf("key%d", i%10), "v": float64(i) + 0.5}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeCategoricalU32, Dictionary: encoding.NewDictionary()},
		{Name: "v", Type: encoding.FieldTypeF64},
	}}
	live := func() uint64 {
		runtime.GC()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}
	before := live()
	it, err := NewChainRowIterator(rows, schema)
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for it.Next() {
		v, _ := it.Record().NumericValue("v")
		sum += v
	}
	after := live()
	runtime.KeepAlive(it)
	runtime.KeepAlive(rows)
	perRow := (float64(after) - float64(before)) / n
	t.Logf("iterator holds %.1f B a row after a full pass (sum %g)", perRow, sum)
	if perRow > 32 {
		t.Errorf("the chain-row iterator holds %.1f B a row after a pass, want <= 32 (the categorical codes alone): it keeps records", perRow)
	}
}

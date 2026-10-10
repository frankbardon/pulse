package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestMatrixMemory_CountsRowBuffer: the memory estimate's matrix term
// adds a buffered spec's row store (a rank method keeps every admitted
// row: 8·(p + 1) bytes per record) on top of its merge-block state; a
// streamable spec adds none.
func TestMatrixMemory_CountsRowBuffer(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 8},
		{Name: "c", Type: encoding.FieldTypeF64, ByteOffset: 16},
	}}
	in := LimitInputs{Records: 10_000}
	mem := func(params string) int64 {
		req := &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Fields: []string{"a", "b", "c"}, Params: json.RawMessage(params)}}}
		return matrixMemory(req, schema, nil, in)
	}
	pearson := mem(`{"method": "pearson"}`)
	if pearson <= 0 {
		t.Fatalf("pearson matrix memory = %d, want > 0", pearson)
	}
	for _, method := range []string{"spearman", "kendall"} {
		got := mem(`{"method": "` + method + `"}`)
		if want := pearson + in.Records*8*4; got != want {
			t.Errorf("%s matrix memory = %d, want %d (block state + 10000 rows × 32 bytes)", method, got, want)
		}
	}
}

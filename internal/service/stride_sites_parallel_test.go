package service

import (
	"bytes"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// Fixed-stride regression suite, parallel-decode arm. The buffered
// Process path splits a single-file cohort into segments at multiples of
// Schema.RecordByteSize — the PHYSICAL stride — and each worker's
// RecordReader expands its own segment. On a deduped (0x02) cohort a
// split by the logical stride, or a count by it, would land segment
// boundaries mid-row and every worker after the first would decode
// garbage (or silently drop rows). Like TestWideSetInvariance_Heavy
// the fixture sits above parallelDecodeRecordThreshold on the real OS
// filesystem, and the gate is asserted open before anything is
// compared, because the eligibility predicate bails silently.

const strideParallelRows = parallelDecodeRecordThreshold + 3001 // not a multiple of any worker count

// strideParallelSchema: a child row (id, amount) under a parent block
// (parent, region, weight — weight nullable, null for every fifth
// parent) plus a global constant (src).
func strideParallelSchema() *encoding.Schema {
	region := encoding.NewDictionary()
	for _, v := range []string{"north", "south", "east", "west"} {
		_, _ = region.Add(v)
	}
	src := encoding.NewDictionary()
	_, _ = src.Add("batch-a")
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "parent", Type: encoding.FieldTypeU32, ByteOffset: 4, CsvColumnIdx: 1},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 8, CsvColumnIdx: 2, Dictionary: region},
		{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 9, CsvColumnIdx: 3, Nullable: true},
		{Name: "amount", Type: encoding.FieldTypeF64, ByteOffset: 17, CsvColumnIdx: 4},
		{Name: "src", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 25, CsvColumnIdx: 5, Dictionary: src},
	}}
}

func strideParallelCohorts(t *testing.T) map[string][]byte {
	t.Helper()
	schema := strideParallelSchema()
	recs := make([][]uint64, strideParallelRows)
	for r := range recs {
		p := r / 9
		recs[r] = []uint64{
			uint64(r + 1),
			uint64(5000 + p),
			uint64(p % 4),
			math.Float64bits(float64(10 + p%7)),
			math.Float64bits(float64(r%101) + 0.25),
			0,
		}
	}
	flat := writeNullablePulse(t, schema, recs, func(r, f int) bool { return f == 3 && (r/9)%5 == 0 })
	dedup := func(specs []encx.GroupSpec) []byte {
		var out bytes.Buffer
		if _, n, err := encx.DedupCohort(&out, bytes.NewReader(flat), specs); err != nil || n != strideParallelRows {
			t.Fatalf("DedupCohort: %d rows, %v", n, err)
		}
		return out.Bytes()
	}
	return map[string][]byte{
		"flat": flat,
		"grouped": dedup([]encx.GroupSpec{
			{Kind: encoding.GroupKindIndexed, Members: []string{"parent", "region", "weight"}, Key: []string{"parent"}},
			{Kind: encoding.GroupKindConstant, Members: []string{"src"}},
		}),
		"elided": dedup([]encx.GroupSpec{{Kind: encoding.GroupKindConstant, Members: []string{"src"}}}),
	}
}

func toF(t *testing.T, v any) float64 {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	}
	t.Fatalf("unexpected aggregate type %T (%v)", v, v)
	return 0
}

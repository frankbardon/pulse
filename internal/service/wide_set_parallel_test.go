package service

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The wide-set cohort and request of the wide-set worker-invariance
// family (wide_set_invariance_test.go): a set_u128 / set_u256 column
// end to end through the decode-worker and shard-worker arms.

const wideSetParityDictSize = 206

func wideSetParityLabel(i int) string { return fmt.Sprintf("P%03d", i) }

// wideSetParityMask is the deterministic per-row selection. Bits are
// placed one per 64-bit word plus a tail bit so every word of the mask
// carries signal: a decode that drops a word, or a reducer that merges
// only the low word, changes the answer.
func wideSetParityMask(r int) encoding.SetMask {
	var m encoding.SetMask
	m = m.WithBit(r % 8)
	m = m.WithBit(64 + r%8)
	m = m.WithBit(128 + r%8)
	m = m.WithBit(192 + r%13)
	return m
}

// wideSetParityCohort builds a two-field cohort — a set_u256 column with
// a 206-entry dictionary plus an f64 weight — and returns the schema
// with the raw record payload.
func wideSetParityCohort(t testing.TB, rowCount int) (*encoding.Schema, []byte) {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < wideSetParityDictSize; i++ {
		if _, err := dict.Add(wideSetParityLabel(i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU256, Dictionary: dict, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 32, CsvColumnIdx: 1},
		},
	}
	stride := schema.RecordByteSize()
	out := bytes.NewBuffer(make([]byte, 0, rowCount*stride))
	for r := 0; r < rowCount; r++ {
		if err := encoding.WriteSetMask(out, encoding.FieldTypeSetU256, wideSetParityMask(r)); err != nil {
			t.Fatalf("write tags(%d): %v", r, err)
		}
		w := float64((r*37)%509) + 0.125
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeF64, math.Float64bits(w)); err != nil {
			t.Fatalf("write weight(%d): %v", r, err)
		}
	}
	return schema, out.Bytes()
}

// writeWideSetCohort lays a header + schema + payload down at path on
// the supplied fs.
func writeWideSetCohort(t testing.TB, fsys afero.Fs, path string, schema *encoding.Schema, payload []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(fsys, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// wideSetParityRequest is the mergeable set-aggregation request both
// arms run. Every slot is a set aggregator with a MergeOnline, which is
// what makes the request eligible for either parallel path.
func wideSetParityRequest(path string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "tags", Label: "n"},
			{Type: types.AGG_SET_UNION, Field: "tags", Label: "union"},
			{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "freq"},
			{Type: types.AGG_SET_CARDINALITY_SUM, Field: "tags", Label: "card_sum"},
			{Type: types.AGG_SET_CARDINALITY_AVG, Field: "tags", Label: "card_avg"},
			{Type: types.AGG_SET_DISTINCT_VALUES, Field: "tags", Label: "distinct"},
		},
	}
}

// assertWideSetRowNonDegenerate fails when the aggregation row could
// have been produced by a low-64-bit-only fold. Without this the parity
// assertion would happily pass on two identically truncated answers.
func assertWideSetRowNonDegenerate(t *testing.T, row map[string]any) {
	t.Helper()
	labels, ok := row["union"].([]string)
	if !ok {
		t.Fatalf("union rich value is %T, want []string", row["union"])
	}
	if len(labels) <= 8 {
		t.Fatalf("union resolved %d labels; a 256-bit fold must resolve more than the low word's 8",
			len(labels))
	}
	// At least one selected member must live at or above bit 64.
	sawHigh := false
	for i := 64; i < wideSetParityDictSize; i++ {
		for _, l := range labels {
			if l == wideSetParityLabel(i) {
				sawHigh = true
				break
			}
		}
		if sawHigh {
			break
		}
	}
	if !sawHigh {
		t.Fatal("union resolved no member at or above bit 64 — the fold truncated to the low word")
	}
}

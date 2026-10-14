package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestRecordsFromChainRows_NaNIsNull: an f64 cell carrying NaN — the
// undefined figure the wire writes as null (an expanded vector element
// no row answered) — reaches the next chain stage as a null, like a
// nil or missing cell, never as a NaN value.
func TestRecordsFromChainRows_NaNIsNull(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "m[b1]", Type: encoding.FieldTypeF64}}}
	recs, err := RecordsFromChainRows([]map[string]any{
		{"m[b1]": 2.5},
		{"m[b1]": math.NaN()},
		{"m[b1]": nil},
	}, schema)
	if err != nil {
		t.Fatal(err)
	}
	if recs[0].IsNull("m[b1]") {
		t.Errorf("row 0 is null, want 2.5")
	}
	for i := 1; i < 3; i++ {
		if !recs[i].IsNull("m[b1]") {
			t.Errorf("row %d is not null", i)
		}
	}
}

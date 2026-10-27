//go:build heavy

package synth

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestSuggestBlocks_WideCohortIsAbandonedNotTruncated pins the field cap
// from both sides, the way TestSuggestRules_HighCardinalityFieldIsNotAGate
// pins maxGateLevels: a truncated field set would yield blocks that are
// exact among the fields it kept and silently missing the rest.
//
// The fixture is expressed RELATIVE to the constant, so it keeps
// exercising the cap when the constant moves; the cost is that the
// numeric VALUE is not pinned here, which is correct — it is a tuning
// documented at its declaration.
func TestSuggestBlocks_WideCohortIsAbandonedNotTruncated(t *testing.T) {
	t.Parallel()

	build := func(nullable int) *Profile {
		t.Helper()
		var fields []encoding.Field
		for i := 0; i < nullable; i++ {
			fields = append(fields, encoding.Field{
				Name: fmt.Sprintf("f%04d", i), Type: encoding.FieldTypeU4, Nullable: true})
		}
		const n = 40
		rows := make([]map[string]any, 0, n)
		nulls := make([]map[string]bool, 0, n)
		for i := 0; i < n; i++ {
			row := map[string]any{}
			rowNull := map[string]bool{}
			for j := 0; j < nullable; j++ {
				name := fmt.Sprintf("f%04d", j)
				row[name] = float64((i + j) % 8)
				if i%4 == 0 {
					rowNull[name] = true
				}
			}
			rows = append(rows, row)
			nulls = append(nulls, rowNull)
		}
		schema := &encoding.Schema{Fields: fields}
		data := encodeModelRows(t, schema, rows, nulls)
		prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
		if err != nil {
			t.Fatalf("profileRecords: %v", err)
		}
		return prof
	}

	over := build(maxBlockFields + 1)
	if got := len(blockCandidates(over.RuleCandidates)); got != 0 {
		t.Errorf("a %d-field cohort produced %d blocks; over the cap the detector must abandon",
			maxBlockFields+1, got)
	}
	var reported bool
	for _, w := range over.Warnings {
		if strings.Contains(w, "co-missing block detection was not run") {
			reported = true
			kind, attention := classifyWarning(w)
			if kind != "rule candidate not considered" || !attention {
				t.Errorf("abandonment classified as %q / attention=%v", kind, attention)
			}
		}
	}
	if !reported {
		t.Errorf("the abandonment was not reported; warnings = %v", over.Warnings)
	}

	atCap := build(maxBlockFields)
	if got := len(blockCandidates(atCap.RuleCandidates)); got != 1 {
		t.Errorf("a cohort with exactly %d nullable fields produced %d blocks, want 1 — "+
			"the cap must admit its own boundary", maxBlockFields, got)
	}
}

package mmmfixture

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestMMMFixture_RowsShape pins the weekly cohort's shape: one row per
// market-week in schema field order.
func TestMMMFixture_RowsShape(t *testing.T) {
	t.Parallel()

	rows, fields := Rows(), len(Schema().Fields)
	if len(rows) != len(Markets)*Weeks {
		t.Fatalf("rows = %d, want %d", len(rows), len(Markets)*Weeks)
	}
	for i, r := range rows {
		if len(r) != fields {
			t.Fatalf("row %d has %d values, want %d", i, len(r), fields)
		}
	}
}

// TestMMMFixture_RowsMatchExamplesCSV: Rows() is bit-identical to the
// checked-in examples cohort mmm_weekly.csv (written from it by
// internal/examples/fixtures/gen.go at full precision, which
// round-trips every float64). The generator is FMA-free and
// detmath-only, so this holds on every architecture — on amd64 CI it
// proves the arm64-written CSV is what Rows() computes there too.
func TestMMMFixture_RowsMatchExamplesCSV(t *testing.T) {
	t.Parallel()

	f, err := os.Open(filepath.Join("..", "examples", "fixtures", "mmm_weekly.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	rows := Rows()
	if len(recs) != len(rows)+1 {
		t.Fatalf("csv has %d data rows, Rows() %d", len(recs)-1, len(rows))
	}
	for i, r := range rows {
		rec := recs[i+1]
		if rec[0] != r[0].(string) || rec[1] != strconv.FormatUint(r[1].(uint64), 10) {
			t.Fatalf("row %d keys %v, csv %v", i, r[:2], rec[:2])
		}
		for j, v := range r[2:] {
			if got := strconv.FormatFloat(v.(float64), 'g', -1, 64); got != rec[j+2] {
				t.Errorf("row %d %s = %s, csv %s: regenerate with go run internal/examples/fixtures/gen.go",
					i, recs[0][j+2], got, rec[j+2])
			}
		}
	}
}

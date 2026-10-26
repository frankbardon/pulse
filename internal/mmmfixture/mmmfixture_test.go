package mmmfixture

import "testing"

// TestMMMFixture_RowsShape pins the weekly cohort's shape: one row per
// market-week in schema field order.
func TestMMMFixture_RowsShape(t *testing.T) {
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

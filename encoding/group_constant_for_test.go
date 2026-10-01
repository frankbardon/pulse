package encoding

import (
	"reflect"
	"testing"
)

// TestPlanConstantElisionFor_MatchesDetectorPlan: a plan from a caller's
// own constant-field list decides exactly what the detector-fed plan
// decides — same fields, retained field, saving and skip reason — over
// the same rows, across the planner's rules.
func TestPlanConstantElisionFor_MatchesDetectorPlan(t *testing.T) {
	s, rows := constantRows(t, 400, "u16_a", "u8_c", "cat_u16")
	same := make([][]byte, 300)
	for i := range same {
		same[i] = rows[0]
	}
	cases := []struct {
		name     string
		rows     [][]byte
		reserved []string
	}{
		{"elides", rows, nil},
		{"reserved", rows, []string{"u8_c"}},
		{"too few rows", rows[:1], nil},
		{"not smaller", rows[:2], nil},
		{"all constant", same, nil},
	}
	for _, c := range cases {
		d := detect(t, s, c.rows)
		want, err := PlanConstantElision(d, c.reserved)
		if err != nil {
			t.Fatal(err)
		}
		got, err := PlanConstantElisionFor(s, d.ConstantFields(), d.Rows(), c.reserved)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: PlanConstantElisionFor %+v, PlanConstantElision %+v", c.name, got, want)
		}
	}
	if _, err := PlanConstantElisionFor(&Schema{Fields: s.Fields, Groups: []Group{{}}}, nil, 10, nil); err == nil {
		t.Error("a grouped schema was accepted")
	}
}

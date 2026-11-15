package regression

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/slotfail"
	"github.com/frankbardon/pulse/types"
)

// order_test.go: the engine half of RegressionSpec.Order — Arrange's
// sort, drop and gap policy, SortOrdered's keep, and the buffered
// driver's use of it (the slot fails alone; an unordered slot's rows
// are untouched).

func orderRec(week float64, mkt string, y, x float64) Record {
	r := labeledRecord{mockRecord: mockRecord{values: map[string]float64{"week": week, "y": y, "x": x}}, labels: map[string]string{}}
	if mkt != "" {
		r.labels["mkt"] = mkt
	}
	return r
}

func weekOf(t *testing.T, r Record) float64 {
	t.Helper()
	v, _ := r.NumericValue("week")
	return v
}

func mktOf(r Record) string {
	s, _, _ := r.(LabeledRecord).StringValueChecked("mkt")
	return s
}

// TestArrange_SortsByPartitionThenField: rows arrive sorted by market
// (labels A–Z) then week; a null week or market drops the row.
func TestArrange_SortsByPartitionThenField(t *testing.T) {
	t.Parallel()

	nullWeek := labeledRecord{mockRecord: mockRecord{values: map[string]float64{"y": 1, "x": 1}, nulls: map[string]bool{"week": true}}, labels: map[string]string{"mkt": "a"}}
	records := []Record{
		orderRec(2, "b", 1, 1), orderRec(3, "a", 1, 1), nullWeek, orderRec(1, "b", 1, 1),
		orderRec(1, "a", 1, 1), orderRec(2, "a", 1, 1), orderRec(9, "", 1, 1),
	}
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"},
		Order: &types.RegressionOrder{Field: "week", Partition: "mkt"}}
	got, err := Arrange(spec, records)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		mkt  string
		week float64
	}{{"a", 1}, {"a", 2}, {"a", 3}, {"b", 1}, {"b", 2}}
	if len(got) != len(want) {
		t.Fatalf("Arrange kept %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		if mktOf(got[i]) != w.mkt || weekOf(t, got[i]) != w.week {
			t.Errorf("row %d = (%s, %v), want (%s, %v)", i, mktOf(got[i]), weekOf(t, got[i]), w.mkt, w.week)
		}
	}
}

// TestArrange_GapPolicyReadsAdmittedRows: a gap left by a null-target
// row is refused; a row the fit would not admit anyway does not repeat a
// period; a spec without an order returns the records untouched.
func TestArrange_GapPolicyReadsAdmittedRows(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"},
		Order: &types.RegressionOrder{Field: "week"}}
	nullY := labeledRecord{mockRecord: mockRecord{values: map[string]float64{"week": 2, "x": 1}, nulls: map[string]bool{"y": true}}}

	_, err := Arrange(spec, []Record{orderRec(1, "", 1, 1), nullY, orderRec(3, "", 1, 1), orderRec(4, "", 1, 1)})
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PROCESSING_REGRESSION_ORDER || ce.Details["reason"] != "gap" || ce.Details["from"] != int64(1) || ce.Details["to"] != int64(3) {
		t.Fatalf("null-target gap: %v", err)
	}
	if _, alone := slotfail.From(slotfail.KindRegressions, err); !alone {
		t.Errorf("the order refusal does not fail the slot alone: %v", err)
	}

	// The unadmitted week-2 duplicate is not a repeat.
	dupNull := labeledRecord{mockRecord: mockRecord{values: map[string]float64{"week": 2, "x": 1}, nulls: map[string]bool{"y": true}}}
	got, err := Arrange(spec, []Record{orderRec(2, "", 1, 1), dupNull, orderRec(1, "", 1, 1), orderRec(3, "", 1, 1)})
	if err != nil {
		t.Fatalf("unadmitted repeat refused: %v", err)
	}
	if len(got) != 4 || weekOf(t, got[0]) != 1 || weekOf(t, got[3]) != 3 {
		t.Errorf("unadmitted rows are kept (the engine drops them) and sorted: %d rows", len(got))
	}

	_, err = Arrange(spec, []Record{orderRec(1, "", 1, 1), orderRec(1, "", 2, 2)})
	if ce, ok := err.(*errors.CodedError); !ok || ce.Details["reason"] != "duplicate" {
		t.Errorf("admitted repeat: %v", err)
	}

	plain := []Record{orderRec(3, "", 1, 1), orderRec(1, "", 1, 1)}
	unordered := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}
	if got, err := Arrange(unordered, plain); err != nil || len(got) != 2 || weekOf(t, got[0]) != 3 {
		t.Errorf("an unordered spec's records moved: %v", err)
	}
}

// TestSortOrdered_KeepsUnplacedRows: the holdout order — sorted, never
// refused, every row kept (an unplaceable one last).
func TestSortOrdered_KeepsUnplacedRows(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Order: &types.RegressionOrder{Field: "week"}}
	nullWeek := labeledRecord{mockRecord: mockRecord{values: map[string]float64{"y": 1, "x": 1}, nulls: map[string]bool{"week": true}}}
	got := SortOrdered(spec, []Record{orderRec(5, "", 1, 1), nullWeek, orderRec(2, "", 1, 1), orderRec(2, "", 3, 3)})
	if _, placed := got[len(got)-1].NumericValue("week"); len(got) != 4 || weekOf(t, got[0]) != 2 || weekOf(t, got[2]) != 5 || placed {
		t.Errorf("SortOrdered = %v", got)
	}
}

// TestFitEachBuffered_OrderedSlotFailsAlone: through the buffered
// driver an ordered slot with a gap fails alone while its sibling fits,
// and a contiguous ordered slot fits the same coefficients as unordered
// (OLS reads no order) up to the order of its sums.
func TestFitEachBuffered_OrderedSlotFailsAlone(t *testing.T) {
	t.Parallel()

	var records []Record
	for _, w := range []float64{4, 1, 3, 2, 6} { // week 5 is missing
		records = append(records, orderRec(w, "", 2*w+1, w))
	}
	ordered := &types.RegressionSpec{Type: types.REG_OLS, Name: "o", Target: "y", Predictors: []string{"x"}, Order: &types.RegressionOrder{Field: "week"}}
	plain := &types.RegressionSpec{Type: types.REG_OLS, Name: "p", Target: "y", Predictors: []string{"x"}}
	res, errs, err := FitEachBufferedWith([]*types.RegressionSpec{ordered, plain}, nil, records, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ce, ok := errs[0].(*errors.CodedError); !ok || ce.Code != errors.PROCESSING_REGRESSION_ORDER {
		t.Fatalf("ordered slot: %v, want PROCESSING_REGRESSION_ORDER", errs[0])
	}
	if errs[1] != nil || res[1] == nil {
		t.Fatalf("sibling slot: %v", errs[1])
	}

	contiguous := records[:4]
	res, errs, err = FitEachBufferedWith([]*types.RegressionSpec{ordered, plain}, nil, contiguous, nil)
	if err != nil || errs[0] != nil || errs[1] != nil {
		t.Fatalf("contiguous: %v %v %v", err, errs[0], errs[1])
	}
	if d := res[0].Coefficients["x"] - res[1].Coefficients["x"]; d > 1e-12 || d < -1e-12 {
		t.Errorf("ordered slope %v != unordered %v", res[0].Coefficients["x"], res[1].Coefficients["x"])
	}
}

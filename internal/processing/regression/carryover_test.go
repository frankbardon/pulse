package regression

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// carryover_test.go pins the engine half of adstock carry-over by hand:
// the state a fit records (each series' last max_lag periods of raw
// spend, ordered, keyed) and the carried adstock a scored row reads —
// a geometric kernel written out term by term.

// carrySpec is an in-model geometric REG_MMM_HILL over raw tv, ordered
// by week, max_lag 3.
func carrySpec() *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_MMM_HILL, Name: "m", Target: "y", Predictors: []string{"tv", "z"},
		Params: json.RawMessage(`{"channels":["tv"],"adstock":"geometric","max_lag":3}`),
		Order:  &types.RegressionOrder{Field: "week"}}
}

func weekRow(week, tv float64) Record {
	return mockRecord{values: map[string]float64{"week": week, "tv": tv, "z": 1, "y": 2}}
}

// TestAdstockStateOf_KeepsTheLastPeriods: rows in any order record the
// last max_lag periods ascending, step the smallest gap, kernel and
// window from params; a spec that does not adstock in-model records
// nothing.
func TestAdstockStateOf_KeepsTheLastPeriods(t *testing.T) {
	t.Parallel()

	var recs []Record
	for _, w := range []float64{5, 1, 3, 4, 2} {
		recs = append(recs, weekRow(w, 10*w))
	}
	st := AdstockStateOf(carrySpec(), recs)
	if st == nil {
		t.Fatal("no state")
	}
	if st.Field != "week" || st.Partition != "" || st.Kernel != "geometric" || st.MaxLag != 3 || st.Step != 1 || len(st.Series) != 1 {
		t.Fatalf("state %+v", st)
	}
	s := st.Series[0]
	if s.Key != "" || len(s.Order) != 3 || s.Order[0] != 3 || s.Order[2] != 5 {
		t.Fatalf("series %+v, want weeks 3..5", s)
	}
	if got := s.Spend["tv"]; len(got) != 3 || got[0] != 30 || got[2] != 50 {
		t.Fatalf("spend %v, want 30 40 50", got)
	}
	plain := carrySpec()
	plain.Params = json.RawMessage(`{"channels":["tv"]}`)
	if AdstockStateOf(plain, recs) != nil {
		t.Error("plain REG_MMM_HILL recorded a state")
	}
}

// TestAdstockStateOf_SeriesByLabel: a REG_MMM_BAYES geo panel keys each
// series by its label, ascending; the step is the panel's smallest gap.
func TestAdstockStateOf_SeriesByLabel(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_MMM_BAYES, Target: "y", Predictors: []string{"tv"},
		Order: &types.RegressionOrder{Field: "day", Partition: "mkt"}, Params: json.RawMessage(`{"channels":["tv"],"geo":"mkt","max_lag":2}`)}
	var recs []Record
	for i, mkt := range []string{"west", "east", "west", "east"} {
		recs = append(recs, labeledRecord{mockRecord: mockRecord{values: map[string]float64{"day": float64(7 * (i / 2)), "tv": float64(i), "y": 1}},
			labels: map[string]string{"mkt": mkt}})
	}
	st := AdstockStateOf(spec, recs)
	if st == nil || st.Partition != "mkt" || st.Step != 7 || len(st.Series) != 2 || st.Series[0].Key != "east" || st.Series[1].Key != "west" {
		t.Fatalf("state %+v", st)
	}
}

// TestCarrier_GeometricByHand: decay 0.5 over lags 0..3, the fit's tail
// weeks 8..10 spending 4, 2 and 6; the scored rows weeks 11..13 spending
// 10, 0 and 8. Each carried adstock is Σ 0.5^l·spend(week − l), the
// tail read where no scored row holds the week:
//
//	week 11: 10 + 0.5·6 + 0.25·2 + 0.125·4 = 14
//	week 12:  0 + 0.5·10 + 0.25·6 + 0.125·2 = 6.75
//	week 13:  8 + 0.5·0 + 0.25·10 + 0.125·6 = 11.25
//
// The same rows restarted at zero (no tail) would read 10, 5 and 10.5;
// every other column passes through.
func TestCarrier_GeometricByHand(t *testing.T) {
	t.Parallel()

	var fit []Record
	for w, tv := range map[float64]float64{7: 100, 8: 4, 9: 2, 10: 6} {
		fit = append(fit, weekRow(w, tv))
	}
	st := AdstockStateOf(carrySpec(), fit)
	c, err := NewCarrier(st, map[string]float64{"decay:tv": 0.5}, "week", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	scored := []Record{weekRow(12, 0), weekRow(11, 10), weekRow(13, 8)}
	for _, r := range scored {
		c.Add(r)
	}
	if err := c.Finalize(); err != nil {
		t.Fatal(err)
	}
	for i, want := range []float64{6.75, 14, 11.25} {
		row := c.Row(scored[i])
		if got, ok := row.NumericValue("tv"); !ok || got != want {
			t.Errorf("week %v: carried tv %v %v, want %v", scored[i].(mockRecord).values["week"], got, ok, want)
		}
		if z, ok := row.NumericValue("z"); !ok || z != 1 {
			t.Errorf("z passes through as %v %v", z, ok)
		}
	}

	// A row with a null own spend, or no week, reads tv null.
	nullTV := mockRecord{values: map[string]float64{"week": 14}, nulls: map[string]bool{"tv": true}}
	noWeek := mockRecord{values: map[string]float64{"tv": 3}}
	for _, r := range []Record{nullTV, noWeek} {
		if v, ok := c.Row(r).NumericValue("tv"); ok {
			t.Errorf("unplaceable or null row reads tv %v", v)
		}
	}
}

// TestCarrier_SeriesRefusals: a repeated or non-contiguous scored period
// is PROCESSING_REGRESSION_ORDER naming the reason, the field and the
// slot's details.
func TestCarrier_SeriesRefusals(t *testing.T) {
	t.Parallel()

	st := AdstockStateOf(carrySpec(), []Record{weekRow(1, 1), weekRow(2, 1)})
	for name, tc := range map[string]struct {
		weeks  []float64
		reason string
	}{
		"duplicate": {[]float64{3, 3}, "duplicate"},
		"gap":       {[]float64{3, 5}, "gap"},
	} {
		c, err := NewCarrier(st, map[string]float64{"decay:tv": 0.5}, "week", "", map[string]any{"attribute": "ATTR_REG_PREDICT"})
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range tc.weeks {
			c.Add(weekRow(w, 1))
		}
		err = c.Finalize()
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PROCESSING_REGRESSION_ORDER || ce.Details["reason"] != tc.reason || ce.Details["field"] != "week" || ce.Details["attribute"] != "ATTR_REG_PREDICT" {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestAttachAdstockState: an in-model adstocked fit's outputs gain the
// state in its JSON form, beside the model's own; a nil result is left
// alone.
func TestAttachAdstockState(t *testing.T) {
	t.Parallel()

	res := &types.RegressionResult{Outputs: map[string]any{"k": 1}}
	if err := AttachAdstockState(carrySpec(), res, []Record{weekRow(1, 2)}); err != nil {
		t.Fatal(err)
	}
	st, ok, err := model.ReadAdstockState(res.Outputs)
	if err != nil || !ok || st.Series[0].Spend["tv"][0] != 2 || res.Outputs["k"] != 1 {
		t.Fatalf("outputs %v (%v %v)", res.Outputs, ok, err)
	}
	if err := AttachAdstockState(carrySpec(), nil, nil); err != nil {
		t.Fatal(err)
	}
}

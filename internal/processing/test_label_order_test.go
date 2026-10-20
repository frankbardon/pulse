package processing

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// U48 E1-S2: TEST_CHISQ (independence) and TEST_FISHER_EXACT report
// their row / col labels SORTED byte-wise — as every other test and the
// goodness-of-fit form do — never in first-seen order, and
// details.contingency is indexed by the sorted labels.

// labelOrderRow is one (arm, o) record with its weight.
type labelOrderRow struct {
	arm, o string
	w      float64
}

func runContingency(t *testing.T, typ types.TestType, rows []labelOrderRow, kind types.WeightKind) *types.TestResult {
	t.Helper()
	schema := armSchema()
	spec := types.Test{Type: typ, Rows: "arm", Cols: "o"}
	if kind != "" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
	}
	rt, err := rowTestRegistry[spec.Type](&spec, schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		rec := NewRecord(schema, map[string]float64{
			"arm": float64(dictIDOrAdd(schema, "arm", r.arm)),
			"o":   float64(dictIDOrAdd(schema, "o", r.o)),
			"w":   r.w,
		})
		if err := rt.UpdateRow(rec); err != nil {
			t.Fatal(err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("%s Finalize: %v", typ, err)
	}
	return res
}

// expand turns (arm, o, count, weight) cells into rows, in the given
// (first-seen) order.
func expandCells(cells []struct {
	arm, o string
	n      int
	w      float64
}) []labelOrderRow {
	var out []labelOrderRow
	for _, c := range cells {
		for range c.n {
			out = append(out, labelOrderRow{c.arm, c.o, c.w})
		}
	}
	return out
}

// The fixture is fed reverse-alphabetically on both axes so first-seen
// order differs from sorted order.
var labelOrderCells = []struct {
	arm, o string
	n      int
	w      float64
}{
	{"treatment", "yes", 7, 2},
	{"treatment", "no", 3, 2},
	{"control", "yes", 4, 1},
	{"control", "no", 6, 1},
}

func TestChiSq_LabelsSorted(t *testing.T) {
	rows := expandCells(labelOrderCells)
	for _, kind := range []types.WeightKind{"", types.WeightKindFrequency} {
		res := runContingency(t, types.TEST_CHISQ, rows, kind)
		d := res.Details
		if got := d["row_labels"]; !reflect.DeepEqual(got, []string{"control", "treatment"}) {
			t.Errorf("weight %q row_labels = %v, want sorted", kind, got)
		}
		if got := d["col_labels"]; !reflect.DeepEqual(got, []string{"no", "yes"}) {
			t.Errorf("weight %q col_labels = %v, want sorted", kind, got)
		}
		if kind == "" {
			if got := d["contingency"]; !reflect.DeepEqual(got, [][]int64{{6, 4}, {3, 7}}) {
				t.Errorf("contingency = %v, want [[6 4] [3 7]]", got)
			}
			if got := d["row_totals"]; !reflect.DeepEqual(got, []int64{10, 10}) {
				t.Errorf("row_totals = %v", got)
			}
			if got := d["col_totals"]; !reflect.DeepEqual(got, []int64{9, 11}) {
				t.Errorf("col_totals = %v", got)
			}
			continue
		}
		if got := d["contingency"]; !reflect.DeepEqual(got, [][]float64{{6, 4}, {6, 14}}) {
			t.Errorf("weighted contingency = %v, want [[6 4] [6 14]]", got)
		}
		if got := d["row_totals"]; !reflect.DeepEqual(got, []float64{10, 20}) {
			t.Errorf("weighted row_totals = %v", got)
		}
		if got := d["col_totals"]; !reflect.DeepEqual(got, []float64{12, 18}) {
			t.Errorf("weighted col_totals = %v", got)
		}
	}
}

func TestFisherExact_LabelsSorted(t *testing.T) {
	res := runContingency(t, types.TEST_FISHER_EXACT, expandCells(labelOrderCells), "")
	d := res.Details
	if got := d["row_labels"]; !reflect.DeepEqual(got, []string{"control", "treatment"}) {
		t.Errorf("row_labels = %v, want sorted", got)
	}
	if got := d["col_labels"]; !reflect.DeepEqual(got, []string{"no", "yes"}) {
		t.Errorf("col_labels = %v, want sorted", got)
	}
	if got := d["contingency"]; !reflect.DeepEqual(got, [][]int{{6, 4}, {3, 7}}) {
		t.Errorf("contingency = %v, want [[6 4] [3 7]]", got)
	}
	// The conditional MLE of the sorted table [[6 4] [3 7]] is above 1
	// (R fisher.test, refined: 3.2742486015053243, pinned by
	// TestReferenceOracle_FisherExact); first-seen order would invert it.
	if got := d["odds_ratio"].(float64); got <= 1 || got != fisherConditionalOddsRatio(6, 4, 3, 7) {
		t.Errorf("odds_ratio = %v, want the sorted table's conditional MLE (≈ 3.2742)", got)
	}
}

// TestTiesDominateWarning_SaysSharesOfTies: the shared warning text
// every rank test emits names what the test then mostly measures.
func TestTiesDominateWarning_SaysSharesOfTies(t *testing.T) {
	schema := twoSampleFixtureSchema()
	rt, err := newMannWhitneyRow(&types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "revenue", SplitBy: "treatment"}, schema)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		v := 0.0
		if i%5 == 0 {
			v = float64(i)
		}
		_ = rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "a"))
		_ = rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": v + 1}, "treatment", "b"))
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, w := range res.Warnings {
		if strings.HasPrefix(w, string(errors.PULSE_TEST_TIES_DOMINATE)) {
			found = w
		}
	}
	if found == "" {
		t.Fatalf("no ties warning in %v", res.Warnings)
	}
	if !strings.Contains(found, "share of tied values") {
		t.Errorf("ties warning %q does not say the test then mostly compares the share of tied values", found)
	}
	for _, scope := range []string{"values", "|diff| values", "values in at least one column"} {
		if w := tiesDominateWarning(scope); !strings.Contains(w, "share of tied values") || !strings.Contains(w, scope) {
			t.Errorf("tiesDominateWarning(%q) = %q", scope, w)
		}
	}
}

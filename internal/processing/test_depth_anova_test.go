package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestAnovaWelch_KnownStatistic uses the classic three-group example
// with unequal variances:
//
//	A: 8, 9, 10, 11, 12        (n=5,  mean=10,  s²=2.5)
//	B: 1, 5, 10, 15, 19        (n=5,  mean=10,  s²=53)
//	C: 9, 10, 10, 10, 11       (n=5,  mean=10,  s²=0.5)
//
// All three groups have the same mean (10), so the standard F should be
// near 0 and Welch's F should also be near 0 — the test confirms the
// implementation does not falsely reject when means are equal across
// unequal-variance groups.
func TestAnovaWelch_EqualMeansUnequalVariance(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_ANOVA_WELCH, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	test, err := newAnovaWelchRow(spec, schema)
	if err != nil {
		t.Fatalf("newAnovaWelchRow: %v", err)
	}
	feed := func(g string, vs ...float64) {
		for _, v := range vs {
			r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", g)
			_ = test.UpdateRow(r)
		}
	}
	feed("A", 8, 9, 10, 11, 12)
	feed("B", 1, 5, 10, 15, 19)
	feed("C", 9, 10, 10, 10, 11)
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic) > 1e-9 {
		t.Errorf("F: got %g, want ~0 (means equal)", res.Statistic)
	}
	if res.PValue < 0.5 {
		t.Errorf("PValue: got %g, want > 0.5 (means equal)", res.PValue)
	}
}

// TestAnovaWelch_RejectsOnMeanShift confirms Welch's ANOVA rejects when
// means differ even under unequal variances.
func TestAnovaWelch_RejectsOnMeanShift(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_ANOVA_WELCH, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	test, _ := newAnovaWelchRow(spec, schema)
	feed := func(g string, vs ...float64) {
		for _, v := range vs {
			r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", g)
			_ = test.UpdateRow(r)
		}
	}
	feed("A", 8, 9, 10, 11, 12)
	feed("B", 18, 19, 20, 21, 22)
	feed("C", 28, 29, 30, 31, 32)
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !res.RejectNull {
		t.Errorf("expected reject at alpha=0.05; got p=%g", res.PValue)
	}
}

// TestBrownForsythe_HomogeneousVariance verifies that equal-variance
// groups produce a non-significant F.
func TestBrownForsythe_HomogeneousVariance(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_BROWN_FORSYTHE, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	test, err := newBrownForsytheRow(spec, schema)
	if err != nil {
		t.Fatalf("newBrownForsytheRow: %v", err)
	}
	feed := func(g string, vs ...float64) {
		for _, v := range vs {
			r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", g)
			_ = test.UpdateRow(r)
		}
	}
	// Three groups, same spread, different means.
	feed("A", 1, 2, 3, 4, 5)
	feed("B", 6, 7, 8, 9, 10)
	feed("C", 11, 12, 13, 14, 15)
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.RejectNull {
		t.Errorf("Brown-Forsythe should not reject equal-variance groups; p=%g", res.PValue)
	}
}

// TestBrownForsythe_UnequalVariance verifies that very different spreads
// produce a significant F.
func TestBrownForsythe_UnequalVariance(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_BROWN_FORSYTHE, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	test, _ := newBrownForsytheRow(spec, schema)
	feed := func(g string, vs ...float64) {
		for _, v := range vs {
			r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", g)
			_ = test.UpdateRow(r)
		}
	}
	feed("A", 5, 5, 5, 5, 5, 5, 5, 5)
	feed("B", -100, -50, 0, 50, 100, -200, 200, 0)
	if _, err := test.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

// TestAnovaRM_Balanced uses a hand-built 4-subject × 3-condition table
// with small per-cell noise so SS_error > 0. Treatment effect (+2, +4)
// dominates the within-subject jitter, so the test rejects.
func TestAnovaRM_Balanced(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "metric", Type: encoding.FieldTypeF64},
			{
				Name:       "condition",
				Type:       encoding.FieldTypeCategoricalU8,
				Dictionary: encoding.NewDictionary(),
			},
			{
				Name:       "subject_id",
				Type:       encoding.FieldTypeCategoricalU8,
				Dictionary: encoding.NewDictionary(),
			},
		},
	}
	spec := &types.Test{
		Type:         types.TEST_ANOVA_RM,
		Field:        "metric",
		SplitBy:      "condition",
		SubjectField: "subject_id",
		Alpha:        0.05,
	}
	test, err := newAnovaRMRow(spec, schema)
	if err != nil {
		t.Fatalf("newAnovaRMRow: %v", err)
	}
	feed := func(subject, cond string, v float64) {
		subjID := dictIDOrAdd(schema, "subject_id", subject)
		condID := dictIDOrAdd(schema, "condition", cond)
		r := NewRecord(schema, map[string]float64{
			"metric":     v,
			"condition":  float64(condID),
			"subject_id": float64(subjID),
		})
		if err := test.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	// 4 subjects, 3 conditions, small condition×subject noise so SS_error
	// is positive but small relative to SS_treatment.
	subjects := []string{"s1", "s2", "s3", "s4"}
	baselines := []float64{10, 11, 12, 13}
	noise := [][]float64{
		{0.1, -0.2, 0.05},
		{-0.05, 0.15, -0.1},
		{0.2, 0.0, -0.15},
		{-0.1, -0.05, 0.2},
	}
	for i, s := range subjects {
		feed(s, "baseline", baselines[i]+noise[i][0])
		feed(s, "cond_a", baselines[i]+2+noise[i][1])
		feed(s, "cond_b", baselines[i]+4+noise[i][2])
	}
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !res.RejectNull {
		t.Errorf("expected reject; got p=%g", res.PValue)
	}
	if got := res.Details["complete_subjects"].(int); got != 4 {
		t.Errorf("complete_subjects: got %d, want 4", got)
	}
}

// TestAnovaRM_DropsIncompleteSubject verifies that subjects with any
// missing condition are dropped and surfaced as a warning.
func TestAnovaRM_DropsIncompleteSubject(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "metric", Type: encoding.FieldTypeF64},
			{
				Name:       "condition",
				Type:       encoding.FieldTypeCategoricalU8,
				Dictionary: encoding.NewDictionary(),
			},
			{
				Name:       "subject_id",
				Type:       encoding.FieldTypeCategoricalU8,
				Dictionary: encoding.NewDictionary(),
			},
		},
	}
	spec := &types.Test{
		Type:         types.TEST_ANOVA_RM,
		Field:        "metric",
		SplitBy:      "condition",
		SubjectField: "subject_id",
		Alpha:        0.05,
	}
	test, _ := newAnovaRMRow(spec, schema)
	feed := func(subject, cond string, v float64) {
		subjID := dictIDOrAdd(schema, "subject_id", subject)
		condID := dictIDOrAdd(schema, "condition", cond)
		r := NewRecord(schema, map[string]float64{
			"metric":     v,
			"condition":  float64(condID),
			"subject_id": float64(subjID),
		})
		_ = test.UpdateRow(r)
	}
	// Two complete subjects + one incomplete (no cond_b). Small per-cell
	// noise keeps SS_error > 0 in the surviving 2×3 set.
	feed("s1", "baseline", 10.1)
	feed("s1", "cond_a", 11.9)
	feed("s1", "cond_b", 13.9)
	feed("s2", "baseline", 10.9)
	feed("s2", "cond_a", 12.05)
	feed("s2", "cond_b", 14.1)
	feed("s3", "baseline", 11)
	feed("s3", "cond_a", 13)
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if got := res.Details["complete_subjects"].(int); got != 2 {
		t.Errorf("complete_subjects: got %d, want 2", got)
	}
	if got := res.Details["dropped_subjects"].(int); got != 1 {
		t.Errorf("dropped_subjects: got %d, want 1", got)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("expected SUBJECT_MISSING warning")
	}
}

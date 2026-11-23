package processing

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// rmCell is one (subject, condition, value) observation.
type rmCell struct {
	subj, cond string
	v          float64
}

// runAnovaRM feeds cells to a fresh TEST_ANOVA_RM built with params and
// returns Finalize's outcome.
func runAnovaRM(t *testing.T, params string, cells []rmCell) (*types.TestResult, error) {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "metric", Type: encoding.FieldTypeF64},
		{Name: "condition", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "subject_id", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	spec := &types.Test{Type: types.TEST_ANOVA_RM, Field: "metric", SplitBy: "condition", SubjectField: "subject_id"}
	if params != "" {
		spec.Params = json.RawMessage(params)
	}
	rt, err := newAnovaRMRow(spec, schema)
	if err != nil {
		t.Fatalf("newAnovaRMRow: %v", err)
	}
	for _, c := range cells {
		r := NewRecord(schema, map[string]float64{
			"metric":     c.v,
			"condition":  float64(dictIDOrAdd(schema, "condition", c.cond)),
			"subject_id": float64(dictIDOrAdd(schema, "subject_id", c.subj)),
		})
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	return rt.Finalize()
}

// rmBalanced is 5 subjects × 3 conditions with a clear subject effect
// (an interior REML optimum).
func rmBalanced() []rmCell {
	table := [][]float64{{45.13, 50.71, 55.37}, {42.29, 42.83, 45.11}, {36.47, 41.03, 43.59}, {39.61, 35.17, 40.93}, {51.07, 55.41, 59.23}}
	var out []rmCell
	for i, row := range table {
		for j, v := range row {
			out = append(out, rmCell{fmt.Sprintf("s%d", i+1), []string{"a", "b", "c"}[j], v})
		}
	}
	return out
}

// TestAnovaRM_DuplicateCellRefused (#678): a second value for a
// (subject, condition) cell is PULSE_TEST_BALANCED_DESIGN_REQUIRED
// naming the subject and condition, under either params.incomplete —
// never a silent last-wins overwrite. The code is a slot-failure code,
// so the slot fails alone.
func TestAnovaRM_DuplicateCellRefused(t *testing.T) {
	t.Parallel()

	cells := append(rmBalanced(), rmCell{"s2", "b", 99})
	for _, params := range []string{"", `{"incomplete":"mixed"}`} {
		_, err := runAnovaRM(t, params, cells)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_BALANCED_DESIGN_REQUIRED {
			t.Fatalf("params %q: err = %v, want PULSE_TEST_BALANCED_DESIGN_REQUIRED", params, err)
		}
		if ce.Details["subject"] != "s2" || ce.Details["condition"] != "b" {
			t.Errorf("params %q: details = %v, want subject s2 / condition b", params, ce.Details)
		}
	}
	if !slotFailCode(errors.PULSE_TEST_BALANCED_DESIGN_REQUIRED) {
		t.Error("PULSE_TEST_BALANCED_DESIGN_REQUIRED is not a per-slot (data-dependent) test refusal")
	}
	if _, err := runAnovaRM(t, "", rmBalanced()); err != nil {
		t.Fatalf("no duplicate: %v", err)
	}
}

func slotFailCode(c errors.Code) bool { return sparseTestCodes[c] }

// TestAnovaRM_MixedBalancedMatchesClassic (#678): on balanced data with
// an interior REML optimum, params.incomplete "mixed" reproduces the
// classic RM ANOVA — F, the error df (n − 1)(k − 1) and p — to 1e-9.
func TestAnovaRM_MixedBalancedMatchesClassic(t *testing.T) {
	t.Parallel()

	classic, err := runAnovaRM(t, "", rmBalanced())
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := runAnovaRM(t, `{"incomplete":"mixed"}`, rmBalanced())
	if err != nil {
		t.Fatal(err)
	}
	if mixed.Variant != "mixed" || classic.Variant != "balanced" {
		t.Fatalf("variants %q / %q", classic.Variant, mixed.Variant)
	}
	rel := func(name string, got, want float64) {
		if math.Abs(got-want) > 1e-9*math.Abs(want) {
			t.Errorf("%s: mixed %.17g, classic %.17g", name, got, want)
		}
	}
	rel("F", mixed.Statistic, classic.Statistic)
	rel("df", mixed.DF, classic.DF)
	rel("df_error", mixed.Details["df_error"].(float64), classic.Details["df_error"].(float64))
	rel("p", mixed.PValue, classic.PValue)
	ssErr := classic.Details["ss_error"].(float64) / classic.Details["df_error"].(float64)
	rel("var_residual = MS_error", mixed.Details["var_residual"].(float64), ssErr)
	if mixed.Details["incomplete_subjects"].(int) != 0 || len(mixed.Warnings) != 0 {
		t.Errorf("balanced mixed: details %v, warnings %v", mixed.Details, mixed.Warnings)
	}
}

// TestAnovaRM_MixedKeepsIncompleteSubjects: under "mixed" a subject
// missing a condition still enters (no drop, no SUBJECT_MISSING
// warning); the result does not depend on the row order (subjects and
// conditions are summed in sorted order).
func TestAnovaRM_MixedKeepsIncompleteSubjects(t *testing.T) {
	t.Parallel()

	cells := append(rmBalanced(), rmCell{"s6", "a", 47.31}, rmCell{"s6", "c", 52.77})
	res, err := runAnovaRM(t, `{"incomplete":"mixed"}`, cells)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Details["incomplete_subjects"].(int); got != 1 {
		t.Errorf("incomplete_subjects = %d, want 1", got)
	}
	if got := res.Details["n_obs"].(int); got != 17 {
		t.Errorf("n_obs = %d, want 17", got)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %v, want none", res.Warnings)
	}
	rev := make([]rmCell, len(cells))
	for i, c := range cells {
		rev[len(cells)-1-i] = c
	}
	res2, err := runAnovaRM(t, `{"incomplete":"mixed"}`, rev)
	if err != nil {
		t.Fatal(err)
	}
	if math.Float64bits(res.Statistic) != math.Float64bits(res2.Statistic) ||
		math.Float64bits(res.Details["df_error"].(float64)) != math.Float64bits(res2.Details["df_error"].(float64)) {
		t.Errorf("row order moved the fit: %v / %v", res.Details, res2.Details)
	}
	// The complete-case default drops s6.
	cc, err := runAnovaRM(t, `{"incomplete":"complete_case"}`, cells)
	if err != nil {
		t.Fatal(err)
	}
	if cc.Details["dropped_subjects"].(int) != 1 || len(cc.Warnings) != 1 {
		t.Errorf("complete_case: %v %v", cc.Details, cc.Warnings)
	}
}

// TestAnovaRM_IncompleteParamRefused: a value outside the enum is the
// constructor's PROCESSING_CONFIG (testopts, the refusal predict raises).
func TestAnovaRM_IncompleteParamRefused(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "metric", Type: encoding.FieldTypeF64},
		{Name: "condition", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "subject_id", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	_, err := newAnovaRMRow(&types.Test{Type: types.TEST_ANOVA_RM, Field: "metric", SplitBy: "condition",
		SubjectField: "subject_id", Params: json.RawMessage(`{"incomplete":"drop"}`)}, schema)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Details["param"] != "incomplete" {
		t.Fatalf("err = %v, want PROCESSING_CONFIG on incomplete", err)
	}
}

// TestReferenceOracle_AnovaRMMixed: params.incomplete "mixed" against
// the closed-form REML / Satterthwaite figures lmerTest approximates
// (1e-8 relative: the REML root is bisected to the last bit, but the
// Satterthwaite ddf rides an eigen-decomposition), lmerTest's own
// anova within 1e-4, and the balanced case's classic aov F.
func TestReferenceOracle_AnovaRMMixed(t *testing.T) {
	t.Parallel()

	doc := loadReference[struct {
		Name               string    `json:"name"`
		Subject            []string  `json:"subject"`
		Condition          []string  `json:"condition"`
		Value              []float64 `json:"value"`
		Conditions         []string  `json:"conditions"`
		Subjects           int       `json:"subjects"`
		CompleteSubjects   int       `json:"complete_subjects"`
		IncompleteSubjects int       `json:"incomplete_subjects"`
		NObs               int       `json:"n_obs"`
		Statistic          float64   `json:"statistic"`
		DFTreatment        float64   `json:"df_treatment"`
		DFError            float64   `json:"df_error"`
		PValue             float64   `json:"p_value"`
		VarSubject         float64   `json:"var_subject"`
		VarResidual        float64   `json:"var_residual"`
		ConditionMeans     []float64 `json:"condition_means"`
		RStatistic         float64   `json:"r_lmertest_statistic"`
		RDFError           float64   `json:"r_lmertest_df_error"`
		RPValue            float64   `json:"r_lmertest_p_value"`
		ClassicStatistic   *float64  `json:"classic_statistic"`
		ClassicDFError     *float64  `json:"classic_df_error"`
	}](t, "test_anova_rm_mixed")
	c := newOracleCheck(t, "TEST_ANOVA_RM mixed", 1e-8, oracleFigureAbsFloor)
	r := newOracleCheck(t, "TEST_ANOVA_RM mixed vs lmerTest", 1e-4, oracleFigureAbsFloor)
	for _, k := range doc.Cases {
		cells := make([]rmCell, len(k.Value))
		for i := range k.Value {
			cells[i] = rmCell{k.Subject[i], k.Condition[i], k.Value[i]}
		}
		res, err := runAnovaRM(t, `{"incomplete":"mixed"}`, cells)
		if err != nil {
			t.Fatalf("%s: %v", k.Name, err)
		}
		d := res.Details
		for key, want := range map[string]int{"subjects": k.Subjects, "complete_subjects": k.CompleteSubjects,
			"incomplete_subjects": k.IncompleteSubjects, "n_obs": k.NObs} {
			if got := d[key].(int); got != want {
				t.Errorf("%s: %s = %d, want %d", k.Name, key, got, want)
			}
		}
		if got := fmt.Sprint(d["conditions"]); got != fmt.Sprint(k.Conditions) {
			t.Errorf("%s: conditions = %s, want %v", k.Name, got, k.Conditions)
		}
		c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(res.DF, k.DFTreatment, "%s df", k.Name)
		c.check(detailFloat(t, d, "df_error"), k.DFError, "%s df_error", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
		c.check(detailFloat(t, d, "var_subject"), k.VarSubject, "%s var_subject", k.Name)
		c.check(detailFloat(t, d, "var_residual"), k.VarResidual, "%s var_residual", k.Name)
		means := d["condition_means"].([]float64)
		for j, want := range k.ConditionMeans {
			c.check(means[j], want, "%s condition_means[%d]", k.Name, j)
		}
		r.check(res.Statistic, k.RStatistic, "%s statistic", k.Name)
		r.check(detailFloat(t, d, "df_error"), k.RDFError, "%s df_error", k.Name)
		r.check(res.PValue, k.RPValue, "%s p_value", k.Name)
		if k.ClassicStatistic != nil {
			c.check(res.Statistic, *k.ClassicStatistic, "%s classic F", k.Name)
			c.check(detailFloat(t, d, "df_error"), *k.ClassicDFError, "%s classic df_error", k.Name)
		}
	}
	c.done(len(doc.Cases))
	r.done(len(doc.Cases))
}

// TestAnovaRM_MixedBoundary: when the subjects differ less than the
// noise predicts, the REML subject variance sits at its 0 boundary;
// only the residual variance is free, so the Satterthwaite df is the
// fixed-effects N − p.
func TestAnovaRM_MixedBoundary(t *testing.T) {
	t.Parallel()

	// Every subject's mean is 10; the within-subject noise is large.
	table := [][]float64{{8, 13, 9}, {12, 7, 11}, {9, 12, 9}, {11, 8, 11}}
	var cells []rmCell
	for i, row := range table {
		for j, v := range row {
			if i == 3 && j == 1 {
				continue // one missing cell
			}
			cells = append(cells, rmCell{fmt.Sprintf("s%d", i+1), []string{"a", "b", "c"}[j], v})
		}
	}
	res, err := runAnovaRM(t, `{"incomplete":"mixed"}`, cells)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Details["var_subject"].(float64); got != 0 {
		t.Errorf("var_subject = %g, want the 0 boundary", got)
	}
	if got, want := res.Details["df_error"].(float64), float64(len(cells)-3); math.Abs(got-want) > 1e-9*want {
		t.Errorf("df_error = %.17g, want N − p = %g", got, want)
	}
}

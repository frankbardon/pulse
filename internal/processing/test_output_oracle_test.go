package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Assembled TEST_* outputs against R (U36 E1-S2). The goldens are
// written by scripts/reference/gen_test_outputs.R (`make reference`);
// each case carries its inputs at %.17g, so the operator replays the
// doubles R read. Tolerance: the shared relative 1e-10 (oracleRelTol)
// with an absolute floor only at the representable edge
// (oracleProbAbsFloor). The one looser comparison — TEST_TREND against
// Kendall::MannKendall — is a single-precision cross-check beside the
// double-precision oracle, and says so where it is made.

// oracleFigureAbsFloor is the floor for non-probability figures: they are O(1)
// sums and differences, never at the underflow edge, so the floor is
// the probability one and never masks a miss.
const oracleFigureAbsFloor = oracleProbAbsFloor

func detailFloat(t *testing.T, d map[string]any, key string) float64 {
	t.Helper()
	switch v := d[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	t.Fatalf("details[%q] = %#v (%T), want a number", key, d[key], d[key])
	return 0
}

// TestReferenceOracle_AnovaRM: TEST_ANOVA_RM against summary(aov(y ~
// cond + Error(subj / cond))) on the complete subjects.
func TestReferenceOracle_AnovaRM(t *testing.T) {
	doc := loadReference[struct {
		Name              string    `json:"name"`
		Subject           []string  `json:"subject"`
		Condition         []string  `json:"condition"`
		Value             []float64 `json:"value"`
		Conditions        []string  `json:"conditions"`
		CompleteSubjects  int       `json:"complete_subjects"`
		DroppedSubjects   int       `json:"dropped_subjects"`
		Statistic         float64   `json:"statistic"`
		DFTreatment       float64   `json:"df_treatment"`
		DFError           float64   `json:"df_error"`
		PValue            float64   `json:"p_value"`
		SSTreatment       float64   `json:"ss_treatment"`
		SSError           float64   `json:"ss_error"`
		SSBetweenSubjects float64   `json:"ss_between_subjects"`
		GrandMean         float64   `json:"grand_mean"`
		ConditionMeans    []float64 `json:"condition_means"`
		PartialEtaSquared float64   `json:"partial_eta_squared"`
	}](t, "test_anova_rm")
	c := newOracleCheck(t, "TEST_ANOVA_RM", oracleRelTol, oracleFigureAbsFloor)
	for _, k := range doc.Cases {
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "metric", Type: encoding.FieldTypeF64},
			{Name: "condition", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "subject_id", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		}}
		rt, err := newAnovaRMRow(&types.Test{
			Type: types.TEST_ANOVA_RM, Field: "metric", SplitBy: "condition", SubjectField: "subject_id",
		}, schema)
		if err != nil {
			t.Fatalf("%s: newAnovaRMRow: %v", k.Name, err)
		}
		for i, v := range k.Value {
			r := NewRecord(schema, map[string]float64{
				"metric":     v,
				"condition":  float64(dictIDOrAdd(schema, "condition", k.Condition[i])),
				"subject_id": float64(dictIDOrAdd(schema, "subject_id", k.Subject[i])),
			})
			if err := rt.UpdateRow(r); err != nil {
				t.Fatalf("%s: UpdateRow: %v", k.Name, err)
			}
		}
		res, err := rt.Finalize()
		if err != nil {
			t.Fatalf("%s: Finalize: %v", k.Name, err)
		}
		d := res.Details
		if got := d["complete_subjects"].(int); got != k.CompleteSubjects {
			t.Errorf("%s: complete_subjects = %d, want %d", k.Name, got, k.CompleteSubjects)
		}
		if got := d["dropped_subjects"].(int); got != k.DroppedSubjects {
			t.Errorf("%s: dropped_subjects = %d, want %d", k.Name, got, k.DroppedSubjects)
		}
		if got := fmt.Sprint(d["conditions"]); got != fmt.Sprint(k.Conditions) {
			t.Errorf("%s: conditions = %s, want %s", k.Name, got, fmt.Sprint(k.Conditions))
		}
		c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(res.DF, k.DFTreatment, "%s df", k.Name)
		c.check(detailFloat(t, d, "df_error"), k.DFError, "%s df_error", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
		c.check(detailFloat(t, d, "ss_treatment"), k.SSTreatment, "%s ss_treatment", k.Name)
		c.check(detailFloat(t, d, "ss_error"), k.SSError, "%s ss_error", k.Name)
		c.check(detailFloat(t, d, "ss_between_subjects"), k.SSBetweenSubjects, "%s ss_between_subjects", k.Name)
		c.check(detailFloat(t, d, "grand_mean"), k.GrandMean, "%s grand_mean", k.Name)
		means := d["condition_means"].([]float64)
		for j, want := range k.ConditionMeans {
			c.check(means[j], want, "%s condition_means[%d]", k.Name, j)
		}
		es := d["effect_size"].(map[string]any)
		c.check(es["partial_eta_squared"].(float64), k.PartialEtaSquared, "%s partial_eta_squared", k.Name)
	}
	c.done(len(doc.Cases))
}

// TestReferenceOracle_TukeyHSD: TEST_TUKEY_HSD against TukeyHSD(aov(y ~
// g)), p_adj and q_critical refined beyond stock ptukey / qtukey (the
// U08 pattern; R's own values ride beside as r_*).
func TestReferenceOracle_TukeyHSD(t *testing.T) {
	type pair struct {
		A      string  `json:"a"`
		B      string  `json:"b"`
		Diff   float64 `json:"diff"`
		SE     float64 `json:"se"`
		Q      float64 `json:"q"`
		PAdj   float64 `json:"p_adj"`
		CILow  float64 `json:"ci_low"`
		CIHigh float64 `json:"ci_high"`
		RDiff  float64 `json:"r_diff"`
		RPAdj  float64 `json:"r_p_adj"`
	}
	doc := loadReference[struct {
		Name        string    `json:"name"`
		Alpha       float64   `json:"alpha"`
		Groups      []string  `json:"groups"`
		Means       []float64 `json:"means"`
		N           []float64 `json:"n"`
		MSWithin    float64   `json:"ms_within"`
		DFWithin    float64   `json:"df_within"`
		QCritical   float64   `json:"q_critical"`
		Comparisons []pair    `json:"comparisons"`
	}](t, "test_tukey_hsd")
	c := newOracleCheck(t, "TEST_TUKEY_HSD", oracleRelTol, oracleFigureAbsFloor)
	for _, k := range doc.Cases {
		params, _ := json.Marshal(map[string]any{"ms_within": k.MSWithin, "df_within": k.DFWithin})
		pt, err := newTukeyHSDPost(&types.Test{
			Type: types.TEST_TUKEY_HSD, Field: "mean", SplitBy: "group", Alpha: k.Alpha, Params: params,
		}, nil)
		if err != nil {
			t.Fatalf("%s: newTukeyHSDPost: %v", k.Name, err)
		}
		// Feed the groups in reverse: the operator sorts by label.
		rows := make([]map[string]any, 0, len(k.Groups))
		for i := len(k.Groups) - 1; i >= 0; i-- {
			rows = append(rows, map[string]any{"group": k.Groups[i], "mean": k.Means[i], "n": k.N[i]})
		}
		res, err := pt.Run(rows)
		if err != nil {
			t.Fatalf("%s: Run: %v", k.Name, err)
		}
		c.check(detailFloat(t, res.Details, "q_critical"), k.QCritical, "%s q_critical", k.Name)
		got := res.Details["comparisons"].([]map[string]any)
		if len(got) != len(k.Comparisons) {
			t.Fatalf("%s: %d comparisons, want %d", k.Name, len(got), len(k.Comparisons))
		}
		minP := 1.0
		for i, want := range k.Comparisons {
			g := got[i]
			if g["a"] != want.A || g["b"] != want.B {
				t.Fatalf("%s: comparison %d is %v-%v, want %s-%s", k.Name, i, g["a"], g["b"], want.A, want.B)
			}
			where := k.Name + " " + want.A + "-" + want.B
			c.check(g["diff"].(float64), want.Diff, "%s diff", where)
			// TukeyHSD's own diff (b − a, from its model tables) is the
			// same figure negated, a few ulps apart.
			c.check(g["diff"].(float64), -want.RDiff, "%s diff vs TukeyHSD", where)
			c.check(g["se"].(float64), want.SE, "%s se", where)
			c.check(g["q"].(float64), want.Q, "%s q", where)
			c.check(g["p_adj"].(float64), want.PAdj, "%s p_adj", where)
			c.check(g["ci_low"].(float64), want.CILow, "%s ci_low", where)
			c.check(g["ci_high"].(float64), want.CIHigh, "%s ci_high", where)
			if rej := g["reject_null"].(bool); rej != (want.PAdj < k.Alpha) {
				t.Errorf("%s: reject_null = %v at p_adj %g, alpha %g", where, rej, want.PAdj, k.Alpha)
			}
			minP = math.Min(minP, want.PAdj)
		}
		c.check(res.PValue, minP, "%s p_value (min p_adj)", k.Name)
	}
	c.done(len(doc.Cases))
}

// TestReferenceOracle_Trend: TEST_TREND (Mann-Kendall) against base R in
// double precision — cor.test(kendall, exact = FALSE, continuity =
// TRUE) for Z and p, cor(kendall) for tau-b — and against
// Kendall::MannKendall. The package computes in single precision
// (float32 carries ~6e-8 relative), so S, Var(S) and tau are held to it
// at a relative 1e-6 and its p-value (single precision, and AS 71's
// own tail evaluation at small n: 1.3e-8 from the asymptotic p at n =
// 10) to an absolute 1e-6: a cross-check that the oracle is the
// package's statistic, not an oracle of its own.
func TestReferenceOracle_Trend(t *testing.T) {
	doc := loadReference[struct {
		Name      string    `json:"name"`
		Value     []float64 `json:"value"`
		Statistic float64   `json:"statistic"`
		PValue    float64   `json:"p_value"`
		Tau       float64   `json:"tau"`
		S         float64   `json:"s"`
		VarS      float64   `json:"var_s"`
		N         int       `json:"n"`
		KTau      float64   `json:"r_kendall_tau"`
		KSL       float64   `json:"r_kendall_sl"`
		KS        float64   `json:"r_kendall_s"`
		KVarS     float64   `json:"r_kendall_var_s"`
	}](t, "test_trend")
	if doc.Packages["Kendall"] == "" {
		t.Fatal("test_trend golden does not record the Kendall package version")
	}
	c := newOracleCheck(t, "TEST_TREND", oracleRelTol, oracleFigureAbsFloor)
	single := newOracleCheck(t, "TEST_TREND vs Kendall::MannKendall (single precision)", 1e-6, 0)
	singleP := newOracleCheck(t, "TEST_TREND p vs Kendall::MannKendall (single precision)", 0, 1e-6)
	for _, k := range doc.Cases {
		pt, err := newTrendPost(&types.Test{
			Type: types.TEST_TREND, Field: "value", OrderBy: []types.OrderKey{{Field: "period"}},
		}, nil)
		if err != nil {
			t.Fatalf("%s: newTrendPost: %v", k.Name, err)
		}
		// Rows arrive reversed: the operator re-sorts by period.
		rows := make([]map[string]any, 0, len(k.Value))
		for i := len(k.Value) - 1; i >= 0; i-- {
			rows = append(rows, map[string]any{"period": float64(i + 1), "value": k.Value[i]})
		}
		res, err := pt.Run(rows)
		if err != nil {
			t.Fatalf("%s: Run: %v", k.Name, err)
		}
		d := res.Details
		if got := d["n"].(int); got != k.N {
			t.Errorf("%s: n = %d, want %d", k.Name, got, k.N)
		}
		c.check(detailFloat(t, d, "s"), k.S, "%s s", k.Name)
		c.check(detailFloat(t, d, "var_s"), k.VarS, "%s var_s", k.Name)
		c.check(detailFloat(t, d, "tau"), k.Tau, "%s tau", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
		if k.Statistic == 0 {
			if res.Statistic != 0 {
				t.Errorf("%s: statistic = %v, want 0", k.Name, res.Statistic)
			}
		} else {
			c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
		}
		single.check(detailFloat(t, d, "s"), k.KS, "%s s", k.Name)
		single.check(detailFloat(t, d, "var_s"), k.KVarS, "%s var_s", k.Name)
		single.check(detailFloat(t, d, "tau"), k.KTau, "%s tau", k.Name)
		singleP.check(res.PValue, k.KSL, "%s p_value", k.Name)
	}
	c.done(len(doc.Cases))
	single.done(len(doc.Cases))
	singleP.done(len(doc.Cases))
}

// TestReferenceOracle_KS: TEST_KS D against ks.test and p against base
// R's limiting Kolmogorov survival at Stephens' corrected lambda — the
// published approximation Pulse documents (ks.test's own exact and
// asymptotic p ride beside in the golden as r_p_*).
func TestReferenceOracle_KS(t *testing.T) {
	doc := loadReference[struct {
		Name      string    `json:"name"`
		A         []float64 `json:"a"`
		B         []float64 `json:"b"`
		Statistic float64   `json:"statistic"`
		PValue    float64   `json:"p_value"`
	}](t, "test_ks")
	c := newOracleCheck(t, "TEST_KS", oracleRelTol, oracleFigureAbsFloor)
	for _, k := range doc.Cases {
		schema := twoSampleFixtureSchema()
		rt, err := newKSRow(&types.Test{Type: types.TEST_KS, Field: "revenue", SplitBy: "treatment"}, schema)
		if err != nil {
			t.Fatalf("%s: newKSRow: %v", k.Name, err)
		}
		for _, side := range []struct {
			label string
			vals  []float64
		}{{"a", k.A}, {"b", k.B}} {
			for _, v := range side.vals {
				if err := rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", side.label)); err != nil {
					t.Fatalf("%s: UpdateRow: %v", k.Name, err)
				}
			}
		}
		res, err := rt.Finalize()
		if err != nil {
			t.Fatalf("%s: Finalize: %v", k.Name, err)
		}
		c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
	}
	c.done(len(doc.Cases))
}

// pairedFixtureSchema is two numeric columns, x and y.
func pairedFixtureSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}}
}

func runPairedRowTest(t *testing.T, name string, rt RowTest, x, y []float64) *types.TestResult {
	t.Helper()
	schema := pairedFixtureSchema()
	for i := range x {
		if err := rt.UpdateRow(NewRecord(schema, map[string]float64{"x": x[i], "y": y[i]})); err != nil {
			t.Fatalf("%s: UpdateRow: %v", name, err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("%s: Finalize: %v", name, err)
	}
	return res
}

// TestReferenceOracle_KendallTau: TEST_KENDALL_TAU (tau-b) against
// cor.test(method = "kendall") at its defaults, mostly with ties.
func TestReferenceOracle_KendallTau(t *testing.T) {
	doc := loadReference[struct {
		Name      string    `json:"name"`
		X         []float64 `json:"x"`
		Y         []float64 `json:"y"`
		Ties      bool      `json:"ties"`
		Statistic float64   `json:"statistic"`
		PValue    float64   `json:"p_value"`
		Z         *float64  `json:"z"`
	}](t, "test_kendall_tau")
	c := newOracleCheck(t, "TEST_KENDALL_TAU", oracleRelTol, oracleFigureAbsFloor)
	tied := 0
	for _, k := range doc.Cases {
		rt, err := newKendallTauRow(&types.Test{Type: types.TEST_KENDALL_TAU, Field: "x", Field2: "y"}, pairedFixtureSchema())
		if err != nil {
			t.Fatalf("%s: newKendallTauRow: %v", k.Name, err)
		}
		res := runPairedRowTest(t, k.Name, rt, k.X, k.Y)
		c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
		if k.Z != nil {
			c.check(detailFloat(t, res.Details, "z"), *k.Z, "%s z", k.Name)
		}
		if k.Ties {
			tied++
		}
	}
	if tied < 3 {
		t.Fatalf("golden has %d tied cases; the oracle exists for tau-b with ties", tied)
	}
	c.done(len(doc.Cases))
}

// TestReferenceOracle_PearsonR: TEST_PEARSON_R against cor.test(x, y,
// conf.level = 1 - alpha) — r, t, df, p and the Fisher-z interval.
func TestReferenceOracle_PearsonR(t *testing.T) {
	doc := loadReference[struct {
		Name      string    `json:"name"`
		Alpha     float64   `json:"alpha"`
		X         []float64 `json:"x"`
		Y         []float64 `json:"y"`
		Statistic float64   `json:"statistic"`
		T         float64   `json:"t"`
		DF        float64   `json:"df"`
		PValue    float64   `json:"p_value"`
		CILow     float64   `json:"ci_low"`
		CIHigh    float64   `json:"ci_high"`
	}](t, "test_pearson_r")
	c := newOracleCheck(t, "TEST_PEARSON_R", oracleRelTol, oracleFigureAbsFloor)
	for _, k := range doc.Cases {
		rt, err := newPearsonRRow(&types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Alpha: k.Alpha}, pairedFixtureSchema())
		if err != nil {
			t.Fatalf("%s: newPearsonRRow: %v", k.Name, err)
		}
		res := runPairedRowTest(t, k.Name, rt, k.X, k.Y)
		c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(res.DF, k.DF, "%s df", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
		c.check(detailFloat(t, res.Details, "t"), k.T, "%s t", k.Name)
		c.check(detailFloat(t, res.Details, "ci_low"), k.CILow, "%s ci_low", k.Name)
		c.check(detailFloat(t, res.Details, "ci_high"), k.CIHigh, "%s ci_high", k.Name)
	}
	c.done(len(doc.Cases))
}

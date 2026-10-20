package processing

import (
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// U36 E1-S3: TEST_SHAPIRO_WILK against nortest::sf.test and
// TEST_BROWN_FORSYTHE against anova(lm(|x − median| ~ g)), including the
// zero-within-spread cases whose F is +Inf or undefined. Goldens from
// scripts/reference/gen_test_outputs.R (`make reference`); relative
// 1e-10 (oracleRelTol) throughout.

// TestReferenceOracle_ShapiroWilk: W' and p on the row and post arms
// match nortest::sf.test — the same Shapiro-Francia formula with
// Royston's (1993) transform, so the shared 1e-10 holds even though the
// transform is itself a published approximation.
func TestReferenceOracle_ShapiroWilk(t *testing.T) {
	doc := loadReference[struct {
		Name      string    `json:"name"`
		N         int       `json:"n"`
		Value     []float64 `json:"value"`
		Statistic float64   `json:"statistic"`
		PValue    float64   `json:"p_value"`
	}](t, "test_shapiro_wilk")
	c := newOracleCheck(t, "TEST_SHAPIRO_WILK", oracleRelTol, oracleProbAbsFloor)
	for _, k := range doc.Cases {
		schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
		rt, err := newShapiroWilkRow(&types.Test{Type: types.TEST_SHAPIRO_WILK, Field: "x"}, schema)
		if err != nil {
			t.Fatalf("%s: newShapiroWilkRow: %v", k.Name, err)
		}
		rows := make([]map[string]any, len(k.Value))
		for i, v := range k.Value {
			if err := rt.UpdateRow(NewRecord(schema, map[string]float64{"x": v})); err != nil {
				t.Fatal(err)
			}
			rows[i] = map[string]any{"x": v}
		}
		row, err := rt.Finalize()
		if err != nil {
			t.Fatalf("%s: Finalize: %v", k.Name, err)
		}
		post := runRegisteredPost(t, &types.Test{Type: types.TEST_SHAPIRO_WILK, Field: "x"}, rows)
		for arm, res := range map[string]*types.TestResult{"row": row, "post": post} {
			c.check(res.Statistic, k.Statistic, "%s %s statistic", k.Name, arm)
			c.check(res.PValue, k.PValue, "%s %s p_value", k.Name, arm)
			g := res.Details["per_group"].([]map[string]any)[0]
			if g["n"] != k.N || len(res.Warnings) != 0 {
				t.Errorf("%s %s: per_group n %v warnings %v, want n %d and no warning", k.Name, arm, g["n"], res.Warnings, k.N)
			}
		}
	}
	c.done(len(doc.Cases))
}

// TestShapiroFrancia_RefusesBelowFive: Royston's transform is
// calibrated for n ≥ 5 (sf.test refuses below it too), so n = 3 / 4 is
// PULSE_TEST_INSUFFICIENT_N on the row and post arms — a data-dependent
// refusal the engine turns into a per-slot failure — and n = 5 runs
// without a warning. Replaces the U08 advisory-warning behaviour.
func TestShapiroFrancia_RefusesBelowFive(t *testing.T) {
	for _, sample := range [][]float64{{1.0, 2.5, 4.0}, {1.0, 2.5, 3.1, 4.0}, {1.0, 2.5, 3.1, 4.0, 6.2}} {
		schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
		rt, err := newShapiroWilkRow(&types.Test{Type: types.TEST_SHAPIRO_WILK, Field: "x"}, schema)
		if err != nil {
			t.Fatal(err)
		}
		rows := make([]map[string]any, len(sample))
		for i, v := range sample {
			_ = rt.UpdateRow(NewRecord(schema, map[string]float64{"x": v}))
			rows[i] = map[string]any{"x": v}
		}
		_, rowErr := rt.Finalize()
		pt, _ := newShapiroWilkPost(&types.Test{Type: types.TEST_SHAPIRO_WILK, Field: "x"}, nil)
		postRes, postErr := pt.Run(rows)
		n := len(sample)
		for arm, err := range map[string]error{"row": rowErr, "post": postErr} {
			if n >= 5 {
				if err != nil {
					t.Errorf("n=%d %s: %v, want a result", n, arm, err)
				}
				continue
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_INSUFFICIENT_N ||
				ce.Details["n"] != n || ce.Details["min_required"] != 5 {
				t.Errorf("n=%d %s: err %v, want PULSE_TEST_INSUFFICIENT_N {n: %d, min_required: 5}", n, arm, err, n)
			}
		}
		if n >= 5 && len(postRes.Warnings) != 0 {
			t.Errorf("n=%d post: warnings %v, want none", n, postRes.Warnings)
		}
	}
}

// TestShapiroFrancia_ZeroVarianceRefuses (U36 E1-S7): a constant group
// has no W' (cor(x, m) is 0 / 0; nortest::sf.test answers NA), so the
// row and post arms refuse it with PULSE_TEST_VARIANCE_ZERO — a
// data-dependent refusal the engine turns into a per-slot failure —
// in place of the old W' = 1, p = 1 and its warning. The check is exact
// (min = max), so a constant whose float mean is inexact (0.1 × 6)
// refuses too, and one constant group refuses a split slot.
func TestShapiroFrancia_ZeroVarianceRefuses(t *testing.T) {
	for _, c := range []struct {
		name   string
		values []float64
		groups []string
		group  string
	}{
		{"constant", []float64{0.1, 0.1, 0.1, 0.1, 0.1, 0.1}, nil, ""},
		{"one_constant_group", []float64{1, 2.5, 3.1, 4, 6.2, 7, 7, 7, 7, 7}, []string{"a", "a", "a", "a", "a", "b", "b", "b", "b", "b"}, "b"},
	} {
		spec := &types.Test{Type: types.TEST_SHAPIRO_WILK, Field: "x"}
		schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
		if c.groups != nil {
			spec.SplitBy = "g"
			schema.Fields = append(schema.Fields, encoding.Field{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()})
		}
		rt, err := newShapiroWilkRow(spec, schema)
		if err != nil {
			t.Fatal(err)
		}
		rows := make([]map[string]any, len(c.values))
		for i, v := range c.values {
			vals := map[string]float64{"x": v}
			rows[i] = map[string]any{"x": v}
			if c.groups != nil {
				vals["g"] = float64(dictIDOrAdd(schema, "g", c.groups[i]))
				rows[i]["g"] = c.groups[i]
			}
			_ = rt.UpdateRow(NewRecord(schema, vals))
		}
		_, rowErr := rt.Finalize()
		pt, _ := newShapiroWilkPost(spec, nil)
		_, postErr := pt.Run(rows)
		for arm, err := range map[string]error{"row": rowErr, "post": postErr} {
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_VARIANCE_ZERO || ce.Details["group"] != c.group || ce.Details["n"] != 5+boolInt(c.groups == nil) {
				t.Errorf("%s %s: err %v, want PULSE_TEST_VARIANCE_ZERO {group: %q}", c.name, arm, err, c.group)
			}
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// bfOracleCase is one test_brown_forsythe.json case. A nil Statistic /
// PValue carries its undefined form in the *_class key ("inf", "nan").
type bfOracleCase struct {
	Name           string    `json:"name"`
	Value          []float64 `json:"value"`
	Group          []string  `json:"group"`
	DF             float64   `json:"df"`
	DFWithin       float64   `json:"df_within"`
	GroupMedians   []float64 `json:"group_medians"`
	Statistic      *float64  `json:"statistic"`
	StatisticClass string    `json:"statistic_class"`
	PValue         *float64  `json:"p_value"`
	PValueClass    string    `json:"p_value_class"`
	SSBetween      float64   `json:"ss_between"`
	SSWithin       float64   `json:"ss_within"`
}

// checkFigure compares got to the golden figure or its undefined class.
func (k bfOracleCase) checkFigure(t *testing.T, c *oracleCheck, got float64, want *float64, class, what string) {
	t.Helper()
	switch class {
	case "":
		c.check(got, *want, "%s %s", k.Name, what)
	case "inf":
		if !math.IsInf(got, 1) {
			t.Errorf("%s %s = %v, want +Inf (R: exact limit at zero within-group spread)", k.Name, what, got)
		}
	case "nan":
		if !math.IsNaN(got) {
			t.Errorf("%s %s = %v, want NaN (R: 0 / 0)", k.Name, what, got)
		}
	default:
		t.Fatalf("%s: unknown class %q", k.Name, class)
	}
}

// TestReferenceOracle_BrownForsythe: TEST_BROWN_FORSYTHE on the row,
// post and frequency-weighted arms against anova(lm(|x − median| ~ g)).
// At zero within-group spread the golden carries R's exact limit: F =
// +Inf with p = 0 when the groups' deviations differ, F and p undefined
// (NaN; reject_null false) when every deviation is equal — never the
// pre-U36 F = 0 / p = 0 "significant" result. The frequency arm reads
// each case with its duplicate rows collapsed into integer weights (unit
// weights where nothing repeats), which R's expanded-row value pins
// (frequency = expansion).
func TestReferenceOracle_BrownForsythe(t *testing.T) {
	doc := loadReference[bfOracleCase](t, "test_brown_forsythe")
	c := newOracleCheck(t, "TEST_BROWN_FORSYTHE", oracleRelTol, oracleProbAbsFloor)
	undefined := 0
	spec := types.Test{Type: types.TEST_BROWN_FORSYTHE, Field: "x", SplitBy: "g"}
	for _, k := range doc.Cases {
		var plain, collapsed []wRow
		rows := make([]map[string]any, len(k.Value))
		at := map[[2]any]int{}
		for i, v := range k.Value {
			plain = append(plain, wRow{x: v, g: k.Group[i]})
			rows[i] = map[string]any{"x": v, "g": k.Group[i]}
			key := [2]any{v, k.Group[i]}
			if j, ok := at[key]; ok {
				collapsed[j].w++
				continue
			}
			at[key] = len(collapsed)
			collapsed = append(collapsed, wRow{x: v, g: k.Group[i], w: 1})
		}
		// The weighted arm needs two raw rows per group (its n ≥ 2 check
		// counts rows, not Σw): a group collapsed to one row of weight
		// w ≥ 2 is split back into weights w − 1 and 1.
		perGroup := map[string]int{}
		for _, r := range collapsed {
			perGroup[r.g]++
		}
		for i, r := range collapsed {
			if perGroup[r.g] == 1 && r.w >= 2 {
				collapsed[i].w--
				collapsed = append(collapsed, wRow{x: r.x, g: r.g, w: 1})
			}
		}
		arms := map[string]*types.TestResult{
			"row":       runWeightedRowTest(t, spec, plain, ""),
			"post":      runRegisteredPost(t, &spec, rows),
			"frequency": runWeightedRowTest(t, spec, collapsed, types.WeightKindFrequency),
		}
		for arm, res := range arms {
			what := arm + " "
			k.checkFigure(t, c, res.Statistic, k.Statistic, k.StatisticClass, what+"statistic")
			k.checkFigure(t, c, res.PValue, k.PValue, k.PValueClass, what+"p_value")
			c.check(res.DF, k.DF, "%s %sdf", k.Name, what)
			c.check(detailFloat(t, res.Details, "df_within"), k.DFWithin, "%s %sdf_within", k.Name, what)
			c.check(detailFloat(t, res.Details, "ss_between"), k.SSBetween, "%s %sss_between", k.Name, what)
			if k.SSWithin == 0 {
				if got := detailFloat(t, res.Details, "ss_within"); got != 0 {
					t.Errorf("%s %sss_within = %v, want exactly 0", k.Name, what, got)
				}
			} else {
				c.check(detailFloat(t, res.Details, "ss_within"), k.SSWithin, "%s %sss_within", k.Name, what)
			}
			meds := res.Details["group_medians"].([]float64)
			for j, want := range k.GroupMedians {
				c.check(meds[j], want, "%s %sgroup_medians[%d]", k.Name, what, j)
			}
			if wantReject := !math.IsNaN(res.PValue) && res.PValue < res.Alpha; res.RejectNull != wantReject {
				t.Errorf("%s %s: reject_null %v with p %v", k.Name, arm, res.RejectNull, res.PValue)
			}
			if k.StatisticClass == "nan" && res.RejectNull {
				t.Errorf("%s %s: an undefined F must not reject", k.Name, arm)
			}
		}
		if k.StatisticClass != "" {
			undefined++
		}
	}
	if undefined < 3 {
		t.Fatalf("golden has %d zero-spread cases, want ≥ 3 (inf ×2, nan ×1)", undefined)
	}
	c.done(len(doc.Cases))
}

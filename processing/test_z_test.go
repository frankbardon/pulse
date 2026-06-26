package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestZTwoSample_KnownStatistic verifies the two-sample z statistic against
// the same fixture used by TestTTest_TwoSample_Welch:
//
//	control: [10, 12, 14, 16, 18]   n=5, mean=14, s²=10
//	variant: [11, 13, 19, 22, 25]   n=5, mean=18, s²=35
//	se = √(10/5 + 35/5) = 3
//	z  = (14 - 18) / 3 = -1.3333
//	p  = 2·(1 − Φ(|z|)) ≈ 0.1824
//
// Same statistic as TEST_WELCH; the p-value differs because TEST_WELCH
// uses T_df (df ≈ 6.113 → p ≈ 0.230) while TEST_Z_TWO_SAMPLE uses Φ.
func TestZTwoSample_KnownStatistic(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{
		Type:    types.TEST_Z_TWO_SAMPLE,
		Field:   "revenue",
		SplitBy: "treatment",
		Alpha:   0.05,
	}
	rt, err := newZTestRow(spec, schema)
	if err != nil {
		t.Fatalf("newZTestRow: %v", err)
	}
	for _, v := range []float64{10, 12, 14, 16, 18} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "control")
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	for _, v := range []float64{11, 13, 19, 22, 25} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "variant")
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-(-1.3333)) > 1e-3 {
		t.Errorf("statistic = %g, want ~-1.3333", res.Statistic)
	}
	if math.Abs(res.PValue-0.1824) > 5e-3 {
		t.Errorf("p_value = %g, want ~0.1824 (normal-CDF)", res.PValue)
	}
	if res.RejectNull {
		t.Errorf("reject_null = true, want false at alpha 0.05 (p=%g)", res.PValue)
	}
	if res.Type != types.TEST_Z_TWO_SAMPLE {
		t.Errorf("type = %s, want TEST_Z_TWO_SAMPLE", res.Type)
	}
	if res.Variant != "two_sample_normal" {
		t.Errorf("variant = %s, want two_sample_normal", res.Variant)
	}
	if res.DF != 0 {
		t.Errorf("df = %g, want 0 (z-test has no df)", res.DF)
	}
}

// TestZTwoSample_LargeNConvergesToWelch verifies that for large n the z and
// Welch t p-values agree to ~3 decimals. Same data fed to both, p-values
// must differ by < 1e-3 with n=500 per group.
func TestZTwoSample_LargeNConvergesToWelch(t *testing.T) {
	schema := twoSampleFixtureSchema()
	zSpec := &types.Test{Type: types.TEST_Z_TWO_SAMPLE, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	tSpec := &types.Test{Type: types.TEST_WELCH, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	z, _ := newZTestRow(zSpec, schema)
	tw, _ := newTTestRow(tSpec, schema)
	// Deterministic two-group sequence with modest treatment shift; n=500
	// each side keeps the t/z divergence below ~1e-3.
	for i := range 500 {
		ctrl := float64(i%10) + 50.0
		var_ := float64(i%10) + 51.5
		rc := newTestRecord(t, schema, map[string]float64{"revenue": ctrl}, "treatment", "control")
		rv := newTestRecord(t, schema, map[string]float64{"revenue": var_}, "treatment", "variant")
		z.UpdateRow(rc)
		z.UpdateRow(rv)
		tw.UpdateRow(rc)
		tw.UpdateRow(rv)
	}
	zRes, err := z.Finalize()
	if err != nil {
		t.Fatalf("z Finalize: %v", err)
	}
	tRes, err := tw.Finalize()
	if err != nil {
		t.Fatalf("welch Finalize: %v", err)
	}
	if math.Abs(zRes.Statistic-tRes.Statistic) > 1e-12 {
		t.Errorf("statistic mismatch: z=%g welch=%g (SE math must be identical)", zRes.Statistic, tRes.Statistic)
	}
	if math.Abs(zRes.PValue-tRes.PValue) > 1e-3 {
		t.Errorf("p-value divergence at n=500: z=%g welch=%g (expected < 1e-3)", zRes.PValue, tRes.PValue)
	}
}

// TestZTwoSample_RequiresSplitBy rejects when split_by is absent.
func TestZTwoSample_RequiresSplitBy(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_Z_TWO_SAMPLE, Field: "revenue", Alpha: 0.05}
	_, err := newZTestRow(spec, schema)
	if err == nil {
		t.Fatal("expected error for missing split_by, got nil")
	}
}

// TestZTwoSample_RejectsKGreaterThan2 errors out on a three-level split.
func TestZTwoSample_RejectsKGreaterThan2(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_Z_TWO_SAMPLE, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	rt, _ := newZTestRow(spec, schema)
	for _, arm := range []string{"a", "b", "c"} {
		for i := range 3 {
			r := newTestRecord(t, schema, map[string]float64{"revenue": float64(10 + i)}, "treatment", arm)
			rt.UpdateRow(r)
		}
	}
	_, err := rt.Finalize()
	if err == nil {
		t.Fatal("expected PULSE_TEST_SPLIT_GROUPS_LT_2 with k=3, got nil")
	}
	if !containsCode(err, "PULSE_TEST_SPLIT_GROUPS_LT_2") {
		t.Errorf("error = %v, want PULSE_TEST_SPLIT_GROUPS_LT_2", err)
	}
}

// TestZTwoSample_FieldTypeMustBeNumeric rejects categorical fields up front.
func TestZTwoSample_FieldTypeMustBeNumeric(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "treatment", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	spec := &types.Test{Type: types.TEST_Z_TWO_SAMPLE, Field: "region", SplitBy: "treatment", Alpha: 0.05}
	_, err := newZTestRow(spec, schema)
	if err == nil {
		t.Fatal("expected PULSE_TEST_FIELD_NOT_NUMERIC, got nil")
	}
	if !containsCode(err, "PULSE_TEST_FIELD_NOT_NUMERIC") {
		t.Errorf("error = %v, want PULSE_TEST_FIELD_NOT_NUMERIC", err)
	}
}

package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// ---------- TEST_PAIRED_T ----------

// TestPairedT_KnownStatistic verifies a hand-computed reference. Paired
// samples with constant +1 delta over 5 pairs: mean_diff=1, var=0, so
// constant delta produces zero-variance error — instead use a small
// non-zero variance series so the math is exercised.
//
// Pairs (a, b): (10,8), (12,11), (15,12), (18,16), (21,17)
//	diffs = [2, 1, 3, 2, 4], mean=2.4, sample_var=1.3, sd≈1.140
//	SE   = 1.140 / √5 = 0.5099
//	t    = 2.4 / 0.5099 ≈ 4.707
//	df   = 4, p ≈ 0.0093
func TestPairedT_KnownStatistic(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64},
		{Name: "b", Type: encoding.FieldTypeF64},
	}}
	spec := &types.Test{
		Type:   types.TEST_PAIRED_T,
		Field:  "a",
		Field2: "b",
		Alpha:  0.05,
	}
	rt, err := newPairedTRow(spec, schema)
	if err != nil {
		t.Fatalf("newPairedTRow: %v", err)
	}
	pairs := [][2]float64{{10, 8}, {12, 11}, {15, 12}, {18, 16}, {21, 17}}
	for _, p := range pairs {
		r := NewRecord(schema, map[string]float64{"a": p[0], "b": p[1]})
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-4.707) > 0.01 {
		t.Errorf("statistic = %g, want ~4.707", res.Statistic)
	}
	if res.DF != 4 {
		t.Errorf("df = %g, want 4", res.DF)
	}
	if math.Abs(res.PValue-0.0093) > 0.005 {
		t.Errorf("p_value = %g, want ~0.0093", res.PValue)
	}
	if !res.RejectNull {
		t.Errorf("reject_null = false at alpha 0.05 (p=%g); expected true", res.PValue)
	}
	if math.Abs(res.Details["mean_diff"].(float64)-2.4) > 1e-9 {
		t.Errorf("mean_diff = %v, want 2.4", res.Details["mean_diff"])
	}
}

// TestPairedT_NullPairDropped verifies rows with either field null are skipped.
func TestPairedT_NullPairDropped(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64},
		{Name: "b", Type: encoding.FieldTypeF64},
	}}
	spec := &types.Test{Type: types.TEST_PAIRED_T, Field: "a", Field2: "b", Alpha: 0.05}
	rt, _ := newPairedTRow(spec, schema)
	// Non-constant diffs so the test isn't accidentally rejected for
	// PULSE_TEST_VARIANCE_ZERO; the assertion under check is the
	// null-pair skip, not the statistic value.
	for _, p := range [][2]float64{{10, 7}, {12, 11}, {15, 12}, {18, 14}} {
		r := NewRecord(schema, map[string]float64{"a": p[0], "b": p[1]})
		rt.UpdateRow(r)
	}
	nullA := NewRecordWithNulls(schema,
		map[string]float64{"a": 0, "b": 5},
		map[string]bool{"a": true})
	for range 3 {
		rt.UpdateRow(nullA)
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.Details["n"].(int64) != 4 {
		t.Errorf("n = %v, want 4 (null-paired rows skipped)", res.Details["n"])
	}
}

// ---------- TEST_PROP_Z ----------

// TestPropZ_KnownStatistic verifies a hand-computed reference. Standard
// textbook two-proportion z example:
//	control:  150/200 = 0.75
//	variant:  170/200 = 0.85
//	pooled:   320/400 = 0.80
//	SE_pool = √(0.80·0.20·(1/200+1/200)) ≈ 0.040
//	z       = (0.75 − 0.85) / 0.040 = -2.5
//	p       = 2(1 − Φ(2.5)) ≈ 0.01242
func TestPropZ_KnownStatistic(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "treatment", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "converted", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	spec := &types.Test{
		Type:    types.TEST_PROP_Z,
		Field:   "converted",
		SplitBy: "treatment",
		Alpha:   0.05,
		Params:  json.RawMessage(`{"success": "yes"}`),
	}
	rt, err := newPropZRow(spec, schema)
	if err != nil {
		t.Fatalf("newPropZRow: %v", err)
	}
	feed := func(treatment, converted string, count int) {
		treatmentID := dictIDOrAdd(schema, "treatment", treatment)
		convertedID := dictIDOrAdd(schema, "converted", converted)
		for range count {
			r := NewRecord(schema, map[string]float64{
				"treatment": float64(treatmentID),
				"converted": float64(convertedID),
			})
			rt.UpdateRow(r)
		}
	}
	feed("control", "yes", 150)
	feed("control", "no", 50)
	feed("variant", "yes", 170)
	feed("variant", "no", 30)
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-(-2.5)) > 0.01 {
		t.Errorf("statistic = %g, want ~-2.5", res.Statistic)
	}
	if math.Abs(res.PValue-0.01242) > 0.001 {
		t.Errorf("p_value = %g, want ~0.01242", res.PValue)
	}
	if !res.RejectNull {
		t.Errorf("reject_null = false at alpha 0.05 (p=%g); expected true", res.PValue)
	}
	props := res.Details["proportion"].([]float64)
	if math.Abs(props[0]-0.75) > 1e-9 || math.Abs(props[1]-0.85) > 1e-9 {
		t.Errorf("proportions = %v, want [0.75 0.85]", props)
	}
}

// TestPropZ_RequiresSuccessParam rejects when params.success is missing.
func TestPropZ_RequiresSuccessParam(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "treatment", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "converted", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	spec := &types.Test{Type: types.TEST_PROP_Z, Field: "converted", SplitBy: "treatment", Alpha: 0.05}
	_, err := newPropZRow(spec, schema)
	if err == nil {
		t.Fatal("expected PULSE_TEST_SUCCESS_VALUE_MISSING, got nil")
	}
	if !containsCode(err, "PULSE_TEST_SUCCESS_VALUE_MISSING") {
		t.Errorf("error = %v, want PULSE_TEST_SUCCESS_VALUE_MISSING", err)
	}
}

// ---------- TEST_PEARSON_R ----------

// TestPearsonR_PerfectPositive: r = 1 on y = 2x + 3.
func TestPearsonR_PerfectPositive(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}}
	spec := &types.Test{
		Type:   types.TEST_PEARSON_R,
		Field:  "x",
		Field2: "y",
		Alpha:  0.05,
	}
	rt, err := newPearsonRRow(spec, schema)
	if err != nil {
		t.Fatalf("newPearsonRRow: %v", err)
	}
	for _, x := range []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} {
		y := 2*x + 3
		r := NewRecord(schema, map[string]float64{"x": x, "y": y})
		rt.UpdateRow(r)
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-1.0) > 1e-9 {
		t.Errorf("r = %g, want 1", res.Statistic)
	}
	if res.PValue > 1e-9 {
		t.Errorf("p_value = %g, want ~0", res.PValue)
	}
	if !res.RejectNull {
		t.Error("reject_null = false; want true on perfect correlation")
	}
}

// TestPearsonR_PerfectNegative: r = -1 on y = -x + 10.
func TestPearsonR_PerfectNegative(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}}
	spec := &types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Alpha: 0.05}
	rt, _ := newPearsonRRow(spec, schema)
	for _, x := range []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} {
		y := -x + 10
		r := NewRecord(schema, map[string]float64{"x": x, "y": y})
		rt.UpdateRow(r)
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-(-1.0)) > 1e-9 {
		t.Errorf("r = %g, want -1", res.Statistic)
	}
}

// TestPearsonR_NoCorrelation: x and y independent → r near 0.
// Use a deterministic pair so the test isn't flaky.
func TestPearsonR_NoCorrelation(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}}
	spec := &types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Alpha: 0.05}
	rt, _ := newPearsonRRow(spec, schema)
	// y oscillates in a cycle uncorrelated with x.
	pairs := [][2]float64{
		{1, 5}, {2, 1}, {3, 4}, {4, 2}, {5, 3},
		{6, 5}, {7, 1}, {8, 4}, {9, 2}, {10, 3},
	}
	for _, p := range pairs {
		r := NewRecord(schema, map[string]float64{"x": p[0], "y": p[1]})
		rt.UpdateRow(r)
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic) > 0.2 {
		t.Errorf("r = %g, want |r| < 0.2", res.Statistic)
	}
}

// TestPearsonR_ZeroVarianceRejects: y constant → CORRELATION_UNDEFINED.
func TestPearsonR_ZeroVarianceRejects(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}}
	spec := &types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Alpha: 0.05}
	rt, _ := newPearsonRRow(spec, schema)
	for _, x := range []float64{1, 2, 3, 4, 5} {
		r := NewRecord(schema, map[string]float64{"x": x, "y": 7})
		rt.UpdateRow(r)
	}
	_, err := rt.Finalize()
	if err == nil {
		t.Fatal("expected PULSE_TEST_CORRELATION_UNDEFINED, got nil")
	}
	if !containsCode(err, "PULSE_TEST_CORRELATION_UNDEFINED") {
		t.Errorf("error = %v, want PULSE_TEST_CORRELATION_UNDEFINED", err)
	}
}

// TestPearsonR_InsufficientN rejects when n < 3.
func TestPearsonR_InsufficientN(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}}
	spec := &types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Alpha: 0.05}
	rt, _ := newPearsonRRow(spec, schema)
	for _, p := range [][2]float64{{1, 2}, {2, 3}} {
		r := NewRecord(schema, map[string]float64{"x": p[0], "y": p[1]})
		rt.UpdateRow(r)
	}
	_, err := rt.Finalize()
	if err == nil {
		t.Fatal("expected PULSE_TEST_INSUFFICIENT_N, got nil")
	}
	if !containsCode(err, "PULSE_TEST_INSUFFICIENT_N") {
		t.Errorf("error = %v, want PULSE_TEST_INSUFFICIENT_N", err)
	}
}

package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// newTestRecord builds a Record with numeric + categorical-string values.
// The categorical split value is stored as the dictionary ID under the
// categorical field name so record.StringValue resolves it through the
// schema's dictionary.
func newTestRecord(t *testing.T, schema *encoding.Schema, values map[string]float64, splitField, splitValue string) *Record {
	t.Helper()
	if splitField != "" {
		id := dictIDOrAdd(schema, splitField, splitValue)
		values[splitField] = float64(id)
	}
	return NewRecord(schema, values)
}

// dictIDOrAdd looks up the dictionary ID for value, adding it if missing.
// Mutates the schema in place — fine for tests.
func dictIDOrAdd(schema *encoding.Schema, fieldName, value string) uint32 {
	f := schema.Field(fieldName)
	if f == nil || f.Dictionary == nil {
		return 0
	}
	if id, ok := f.Dictionary.IDFor(value); ok {
		return id
	}
	id, _ := f.Dictionary.Add(value)
	return id
}

// twoSampleFixtureSchema returns a schema with a numeric `revenue` field
// and a categorical `treatment` field with an empty dictionary.
func twoSampleFixtureSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "revenue", Type: encoding.FieldTypeF64},
			{
				Name:       "treatment",
				Type:       encoding.FieldTypeCategoricalU8,
				Dictionary: encoding.NewDictionary(),
			},
		},
	}
}

// numericFixtureSchema returns a schema with a single numeric field.
func numericFixtureSchema(name string) *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: name, Type: encoding.FieldTypeF64},
		},
	}
}

// categoricalFixtureSchema returns a schema with a single categorical
// field whose dictionary contains the supplied values.
func categoricalFixtureSchema(name string, values ...string) *encoding.Schema {
	d := encoding.NewDictionary()
	for _, v := range values {
		d.Add(v)
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: name, Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
		},
	}
}

// TestTTest_OneSample_KnownStatistic verifies the one-sample t statistic
// against a hand-computed reference. Sample [1,2,3,4,5] tested against
// μ₀=3 yields t=0, p=1 (mean equals hypothesis).
func TestTTest_OneSample_KnownStatistic(t *testing.T) {
	schema := numericFixtureSchema("x")
	spec := &types.Test{
		Type:   types.TEST_T,
		Field:  "x",
		Alpha:  0.05,
		Params: json.RawMessage(`{"mu": 3.0}`),
	}
	rt, err := newTTestRow(spec, schema)
	if err != nil {
		t.Fatalf("newTTestRow: %v", err)
	}
	for _, v := range []float64{1, 2, 3, 4, 5} {
		r := NewRecord(schema, map[string]float64{"x": v})
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic) > 1e-12 {
		t.Errorf("statistic = %g, want ~0", res.Statistic)
	}
	if math.Abs(res.PValue-1) > 1e-9 {
		t.Errorf("p_value = %g, want ~1", res.PValue)
	}
	if res.RejectNull {
		t.Error("reject_null = true, want false")
	}
	if res.DF != 4 {
		t.Errorf("df = %g, want 4", res.DF)
	}
}

// TestTTest_OneSample_SignificantDifference verifies a clearly non-zero
// effect rejects the null. Sample [10..14] vs μ₀=0 → very large t.
func TestTTest_OneSample_SignificantDifference(t *testing.T) {
	schema := numericFixtureSchema("x")
	spec := &types.Test{
		Type:   types.TEST_T,
		Field:  "x",
		Alpha:  0.05,
		Params: json.RawMessage(`{"mu": 0.0}`),
	}
	rt, err := newTTestRow(spec, schema)
	if err != nil {
		t.Fatalf("newTTestRow: %v", err)
	}
	for _, v := range []float64{10, 11, 12, 13, 14} {
		r := NewRecord(schema, map[string]float64{"x": v})
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.Statistic < 10 {
		t.Errorf("statistic = %g, want large positive", res.Statistic)
	}
	if res.PValue > 1e-4 {
		t.Errorf("p_value = %g, want ~0", res.PValue)
	}
	if !res.RejectNull {
		t.Error("reject_null = false, want true")
	}
}

// TestTTest_TwoSample_Welch verifies the Welch two-sample statistic on a
// fixed dataset. Reference values (hand-computed, matches
// scipy.stats.ttest_ind equal_var=False to 4 decimal places):
//
//	control: [10, 12, 14, 16, 18]   n=5, mean=14, s²=10
//	variant: [11, 13, 19, 22, 25]   n=5, mean=18, s²=35
//	se = √(10/5 + 35/5) = 3
//	t  = (14 - 18) / 3 = -1.3333
//	df = 9² / ((100/100) + (1225/100)) = 81 / 13.25 ≈ 6.113
//	p ≈ 0.230 (two-sided)
func TestTTest_TwoSample_Welch(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{
		Type:    types.TEST_WELCH,
		Field:   "revenue",
		SplitBy: "treatment",
		Alpha:   0.05,
	}
	rt, err := newTTestRow(spec, schema)
	if err != nil {
		t.Fatalf("newTTestRow: %v", err)
	}
	// control: [10, 12, 14, 16, 18] mean=14 var=10
	for _, v := range []float64{10, 12, 14, 16, 18} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "control")
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	// variant: [11, 13, 19, 22, 25] mean=18 var=34
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
	if math.Abs(res.DF-6.113) > 1e-2 {
		t.Errorf("df = %g, want ~6.113", res.DF)
	}
	if math.Abs(res.PValue-0.230) > 0.01 {
		t.Errorf("p_value = %g, want ~0.230", res.PValue)
	}
	if res.RejectNull {
		t.Errorf("reject_null = true, want false at alpha 0.05 (p=%g)", res.PValue)
	}
	if res.Type != types.TEST_WELCH {
		t.Errorf("type = %s, want TEST_WELCH", res.Type)
	}
	if res.Variant != "welch_two_sample" {
		t.Errorf("variant = %s, want welch_two_sample", res.Variant)
	}
}

// TestTTest_TwoSample_LargeEffectRejects verifies a clear treatment
// effect rejects the null.
func TestTTest_TwoSample_LargeEffectRejects(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{
		Type:    types.TEST_T,
		Field:   "revenue",
		SplitBy: "treatment",
		Alpha:   0.05,
	}
	rt, err := newTTestRow(spec, schema)
	if err != nil {
		t.Fatalf("newTTestRow: %v", err)
	}
	for _, v := range []float64{1, 2, 3, 4, 5, 1, 2, 3, 4, 5} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "control")
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	for _, v := range []float64{100, 101, 102, 103, 104, 100, 101, 102, 103, 104} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "variant")
		if err := rt.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.PValue > 1e-12 {
		t.Errorf("p_value = %g, want ~0", res.PValue)
	}
	if !res.RejectNull {
		t.Error("reject_null = false, want true")
	}
	details := res.Details
	groups := details["groups"].([]string)
	if groups[0] != "control" || groups[1] != "variant" {
		t.Errorf("groups = %v, want [control variant]", groups)
	}
}

// TestTTest_InsufficientN_OneSample rejects with PULSE_TEST_INSUFFICIENT_N
// when n < 2.
func TestTTest_InsufficientN_OneSample(t *testing.T) {
	schema := numericFixtureSchema("x")
	spec := &types.Test{Type: types.TEST_T, Field: "x", Alpha: 0.05}
	rt, _ := newTTestRow(spec, schema)
	rt.UpdateRow(NewRecord(schema, map[string]float64{"x": 5}))
	_, err := rt.Finalize()
	if err == nil {
		t.Fatal("Finalize: expected PULSE_TEST_INSUFFICIENT_N, got nil")
	}
	if !containsCode(err, "PULSE_TEST_INSUFFICIENT_N") {
		t.Errorf("error = %v, want PULSE_TEST_INSUFFICIENT_N", err)
	}
}

// TestTTest_VarianceZero rejects when the sample is constant.
func TestTTest_VarianceZero(t *testing.T) {
	schema := numericFixtureSchema("x")
	spec := &types.Test{Type: types.TEST_T, Field: "x", Alpha: 0.05}
	rt, _ := newTTestRow(spec, schema)
	for range 4 {
		rt.UpdateRow(NewRecord(schema, map[string]float64{"x": 7}))
	}
	_, err := rt.Finalize()
	if err == nil {
		t.Fatal("Finalize: expected PULSE_TEST_VARIANCE_ZERO, got nil")
	}
	if !containsCode(err, "PULSE_TEST_VARIANCE_ZERO") {
		t.Errorf("error = %v, want PULSE_TEST_VARIANCE_ZERO", err)
	}
}

// TestTTest_SplitGroupsLT2 rejects when only one split group is observed.
func TestTTest_SplitGroupsLT2(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{
		Type:    types.TEST_T,
		Field:   "revenue",
		SplitBy: "treatment",
		Alpha:   0.05,
	}
	rt, _ := newTTestRow(spec, schema)
	for _, v := range []float64{1, 2, 3, 4, 5} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "control")
		rt.UpdateRow(r)
	}
	_, err := rt.Finalize()
	if err == nil {
		t.Fatal("Finalize: expected PULSE_TEST_SPLIT_GROUPS_LT_2, got nil")
	}
	if !containsCode(err, "PULSE_TEST_SPLIT_GROUPS_LT_2") {
		t.Errorf("error = %v, want PULSE_TEST_SPLIT_GROUPS_LT_2", err)
	}
}

// TestTTest_InvalidAlpha rejects alphas outside (0, 1).
func TestTTest_InvalidAlpha(t *testing.T) {
	schema := numericFixtureSchema("x")
	for _, alpha := range []float64{-0.1, 1.0, 1.5} {
		spec := &types.Test{Type: types.TEST_T, Field: "x", Alpha: alpha}
		_, err := newTTestRow(spec, schema)
		if err == nil {
			t.Errorf("alpha %g: expected PULSE_TEST_INVALID_ALPHA, got nil", alpha)
			continue
		}
		if !containsCode(err, "PULSE_TEST_INVALID_ALPHA") {
			t.Errorf("alpha %g: error = %v, want PULSE_TEST_INVALID_ALPHA", alpha, err)
		}
	}
}

// TestTTest_FieldNotNumeric rejects when Field references a categorical.
func TestTTest_FieldNotNumeric(t *testing.T) {
	schema := categoricalFixtureSchema("category", "a", "b")
	spec := &types.Test{Type: types.TEST_T, Field: "category", Alpha: 0.05}
	_, err := newTTestRow(spec, schema)
	if err == nil {
		t.Fatal("expected PULSE_TEST_FIELD_NOT_NUMERIC, got nil")
	}
	if !containsCode(err, "PULSE_TEST_FIELD_NOT_NUMERIC") {
		t.Errorf("error = %v, want PULSE_TEST_FIELD_NOT_NUMERIC", err)
	}
}

// TestTTest_NullValuesSkipped verifies null values do not contribute.
func TestTTest_NullValuesSkipped(t *testing.T) {
	schema := numericFixtureSchema("x")
	spec := &types.Test{Type: types.TEST_T, Field: "x", Alpha: 0.05, Params: json.RawMessage(`{"mu": 3.0}`)}
	rt, _ := newTTestRow(spec, schema)
	for _, v := range []float64{1, 2, 3, 4, 5} {
		rt.UpdateRow(NewRecord(schema, map[string]float64{"x": v}))
	}
	nullRec := NewRecordWithNulls(schema, map[string]float64{"x": 0}, map[string]bool{"x": true})
	for range 3 {
		rt.UpdateRow(nullRec)
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	n := res.Details["n"].(int64)
	if n != 5 {
		t.Errorf("n = %d, want 5 (nulls should be skipped)", n)
	}
}

// containsCode checks if a CodedError carries a specific code by
// inspecting the error's string form. Avoids importing errors here just
// for the type assertion; the wire format is "<CODE>: <message>".
func containsCode(err error, code string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for i := 0; i+len(code) <= len(msg); i++ {
		if msg[i:i+len(code)] == code {
			return true
		}
	}
	return false
}

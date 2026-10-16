package fieldaccept

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

func TestTypeNames_ValueWindow(t *testing.T) {
	want := []string{"date", "f32", "f64", "u16", "u32", "u4", "u64", "u8"}
	if got := TypeNames("WIN_LAG", SlotField); !reflect.DeepEqual(got, want) {
		t.Fatalf("WIN_LAG field: %v, want %v", got, want)
	}
	if got := TypeNames("FEAT_POLY", SlotField); !reflect.DeepEqual(got, append([]string{"date", "decimal128"}, want[1:]...)) {
		t.Fatalf("FEAT_POLY field: %v", got)
	}
	if got := TypeNames("WIN_LAG", SlotPredictor); got != nil {
		t.Fatalf("untabled slot: %v, want nil", got)
	}
}

func TestTypeNames_NeverListsVectorOrRefused(t *testing.T) {
	for _, op := range Operators() {
		for _, slot := range []Slot{SlotField, SlotField2, SlotTarget, SlotPredictor} {
			for _, n := range TypeNames(op, slot) {
				ft, ok := encoding.ParseFieldType(n)
				if !ok || ft.IsVector() || !Accepts(op, slot, ft) {
					t.Errorf("%s %s lists %s", op, slot, n)
				}
			}
		}
	}
}

// TestTypeNames_ValueReaders pins the classes of the runtime
// constructors' own refusals: no set rung on a value reader, no decimal
// on the value matchers (the decimal rule), no categorical on a
// quantity reader, the Welford six.
func TestTypeNames_ValueReaders(t *testing.T) {
	for _, op := range []string{"AGG_DISTINCT_COUNT", "AGG_FREQUENCY", "AGG_MODE", "AGG_MODE_COUNT", "AGG_WELFORD",
		"ATTR_NORMALIZED", "ATTR_PERCENTILE", "ATTR_TSCORE", "ATTR_ZSCORE", "GROUP_CATEGORY", "GROUP_QUANTILE",
		"GROUP_RANGE", "GROUP_ROUNDED", "FILTER_INCLUDE", "FILTER_EXCLUDE", "FILTER_RANGE"} {
		r, ok := Lookup(op, SlotField)
		if !ok || r.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED {
			t.Fatalf("%s: tabled=%v code=%v", op, ok, r.Code)
		}
		for _, n := range TypeNames(op, SlotField) {
			if strings.HasPrefix(n, "set_") {
				t.Errorf("%s lists %s", op, n)
			}
		}
		if !Accepts(op, SlotField, encoding.FieldTypeU8) {
			t.Errorf("%s refuses u8", op)
		}
	}
	for _, op := range []string{"AGG_FREQUENCY", "AGG_MODE", "AGG_MODE_COUNT", "AGG_WELFORD"} {
		if Accepts(op, SlotField, encoding.FieldTypeDecimal128) {
			t.Errorf("%s accepts decimal128", op)
		}
	}
	if !Accepts("AGG_DISTINCT_COUNT", SlotField, encoding.FieldTypeDecimal128) || !Accepts("GROUP_CATEGORY", SlotField, encoding.FieldTypeCategoricalU8) {
		t.Error("nonSetValue narrowed")
	}
	for _, op := range []string{"ATTR_NORMALIZED", "ATTR_PERCENTILE", "ATTR_TSCORE", "ATTR_ZSCORE",
		"GROUP_QUANTILE", "GROUP_RANGE", "GROUP_ROUNDED", "FILTER_RANGE"} {
		if Accepts(op, SlotField, encoding.FieldTypeCategoricalU16) {
			t.Errorf("%s accepts a categorical (a dictionary index is no quantity)", op)
		}
		for _, ft := range []encoding.FieldType{encoding.FieldTypeDecimal128, encoding.FieldTypeDateTime, encoding.FieldTypePackedBool, encoding.FieldTypeDate, encoding.FieldTypeU4} {
			if !Accepts(op, SlotField, ft) {
				t.Errorf("%s refuses %s, which reads as the number it is", op, ft)
			}
		}
	}
	if got, want := TypeNames("AGG_WELFORD", SlotField), []string{"f32", "f64", "u16", "u32", "u64", "u8"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AGG_WELFORD: %v, want %v", got, want)
	}
	for _, slot := range []Slot{SlotTarget, SlotPredictor} {
		if Accepts("ATTR_REG_FITTED", slot, encoding.FieldTypeCategoricalU8) || Accepts("ATTR_REG_FITTED", slot, encoding.FieldTypeSetU8) ||
			!Accepts("ATTR_REG_FITTED", slot, encoding.FieldTypePackedBool) {
			t.Errorf("ATTR_REG_FITTED %s: not the analytics set", slot)
		}
	}
}

func TestRefusal(t *testing.T) {
	if r := Refusal("WIN_LAG", SlotField, "f", encoding.FieldTypeF64); r != nil {
		t.Fatalf("accepted type refused: %v", r)
	}
	if r := Refusal("WIN_LAG", SlotField, "v", encoding.FieldTypeVecF64); r != nil {
		t.Fatalf("vector refused here (the field-reference walk owns it): %v", r)
	}
	if r := Refusal("WIN_EXT_NS_LAG", SlotField, "c", encoding.FieldTypeCategoricalU8); r != nil {
		t.Fatalf("untabled (extension) operator refused: %v", r)
	}
	r := Refusal("WIN_LAG", SlotField, "c", encoding.FieldTypeCategoricalU8)
	if r == nil || r.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED {
		t.Fatalf("categorical window: %v", r)
	}
	if r.Details["field_type"] != "categorical_u8" || r.Details["slot"] != "field" || r.Details["type"] != "WIN_LAG" {
		t.Fatalf("details: %v", r.Details)
	}
	if !strings.Contains(r.Message, "categorical_u8") || !strings.Contains(r.Message, "u64") {
		t.Fatalf("message: %q", r.Message)
	}
	if r := Refusal("TEST_PEARSON_R", SlotField2, "s", encoding.FieldTypeSetU8); r == nil || r.Code != errors.PULSE_TEST_FIELD2_NOT_NUMERIC {
		t.Fatalf("set field2: %v", r)
	}
	if r := Refusal("REG_OLS", SlotTarget, "s", encoding.FieldTypeSetU256); r == nil || r.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED {
		t.Fatalf("set target: %v", r)
	}
}

func TestCheck(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "c", Type: encoding.FieldTypeCategoricalU8},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
	if r := Check("FEAT_POLY", SlotField, "c", nil); r != nil {
		t.Fatalf("nil schema: %v", r)
	}
	if r := Check("FEAT_POLY", SlotField, "", schema); r != nil {
		t.Fatalf("empty name: %v", r)
	}
	if r := Check("FEAT_POLY", SlotField, "derived", schema); r != nil {
		t.Fatalf("non-schema name: %v", r)
	}
	if r := Check("FEAT_POLY", SlotField, "x", schema); r != nil {
		t.Fatalf("f64: %v", r)
	}
	if r := Check("FEAT_POLY", SlotField, "c", schema); r == nil {
		t.Fatal("categorical FEAT_POLY accepted")
	}
}

func TestOperatorsRaising(t *testing.T) {
	got := OperatorsRaising(errors.PULSE_FIELD_TYPE_UNSUPPORTED)
	for _, want := range []string{"FEAT_POLY", "REG_OLS", "WIN_DELTA"} {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("%s missing from %v", want, got)
		}
	}
	// A tier-1 numeric test's field / field2 refusal keeps its specific
	// code; its split_by refusal (a categorical) is the generic one.
	for _, op := range []string{"TEST_T", "TEST_PEARSON_R"} {
		if r, _ := Lookup(op, SlotField); r.Code != errors.PULSE_TEST_FIELD_NOT_NUMERIC {
			t.Errorf("%s field: %s", op, r.Code)
		}
	}
	if r, _ := Lookup("TEST_T", SlotSplitBy); r.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED {
		t.Errorf("TEST_T split_by: %s", r.Code)
	}
	if r, _ := Lookup("TEST_CHISQ", SlotRows); r.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED {
		t.Errorf("TEST_CHISQ rows (must be categorical) raises %s", r.Code)
	}
	// No row raises a code that does not name a type refusal.
	for _, code := range []errors.Code{errors.PROCESSING_CONFIG, errors.SERVICE_VALIDATION} {
		if got := OperatorsRaising(code); len(got) != 0 {
			t.Errorf("%s raised by %v", code, got)
		}
	}
}

func TestTypeNames_SetOperators(t *testing.T) {
	want := []string{"set_u128", "set_u16", "set_u256", "set_u32", "set_u64", "set_u8"}
	for _, op := range []string{"AGG_SET_UNION", "ATTR_SET_HAS", "FILTER_SET_EQUALS", "GROUP_SET_VALUE"} {
		if got := TypeNames(op, SlotField); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", op, got, want)
		}
		want := errors.PULSE_FIELD_TYPE_UNSUPPORTED
		if strings.HasPrefix(op, "AGG_") {
			want = errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL
		}
		if r := Refusal(op, SlotField, "d", encoding.FieldTypeDecimal128); r == nil || r.Code != want {
			t.Errorf("%s on decimal128: %v, want %s", op, r, want)
		}
	}
}

func TestTypeNames_DateRanges(t *testing.T) {
	for _, op := range []string{"GROUP_DATE_RANGES", "FILTER_DATE_RANGES"} {
		if got, want := TypeNames(op, SlotField), []string{"date", "datetime"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", op, got, want)
		}
	}
}

// TestDecimalRule: a decimal128 primary field on a built-in aggregator
// with no decimal arm is refused PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL,
// tabled row or not; a decimal-arm aggregator, a non-field slot and an
// extension (unknown) aggregator are not judged by it.
func TestDecimalRule(t *testing.T) {
	d := encoding.FieldTypeDecimal128
	for _, op := range []string{"AGG_MEDIAN", "AGG_PERCENTILE", "AGG_MODE", "AGG_WELFORD", "AGG_SET_UNION"} {
		r := Refusal(op, SlotField, "amt", d)
		if r == nil || r.Code != errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL {
			t.Errorf("%s: %v", op, r)
			continue
		}
		if r.Details["field_type"] != "decimal128" || r.Details["type"] != op {
			t.Errorf("%s details: %v", op, r.Details)
		}
		if Accepts(op, SlotField, d) {
			t.Errorf("%s accepts decimal128", op)
		}
	}
	for _, op := range DecimalAggregators() {
		if r := Refusal(op, SlotField, "amt", d); r != nil {
			t.Errorf("decimal-arm %s refused: %v", op, r)
		}
	}
	// AGG_NULL_COUNT reads the null bitmap only, so it has a decimal arm.
	if !HasDecimalArm("AGG_NULL_COUNT") || Refusal("AGG_NULL_COUNT", SlotField, "amt", d) != nil {
		t.Error("AGG_NULL_COUNT refuses decimal128")
	}
	if r := Refusal("AGG_EXT_NS_MEDIAN", SlotField, "amt", d); r != nil {
		t.Errorf("extension aggregator refused: %v", r)
	}
	if r := Refusal("AGG_MEDIAN", SlotField, "amt", encoding.FieldTypeF64); r != nil {
		t.Errorf("f64 refused: %v", r)
	}
	for _, n := range TypeNames("AGG_MODE", SlotField) {
		if n == "decimal128" {
			t.Error("AGG_MODE lists decimal128")
		}
	}
}

// TestTableCodes enumerates the codes the table raises: every one names
// a type refusal (#313) — the generic PULSE_FIELD_TYPE_UNSUPPORTED, the
// tier-1 numeric tests' PULSE_TEST_FIELD_NOT_NUMERIC /
// PULSE_TEST_FIELD2_NOT_NUMERIC and the decimal rule's
// PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL.
func TestTableCodes(t *testing.T) {
	allowed := map[errors.Code]bool{
		errors.PULSE_FIELD_TYPE_UNSUPPORTED:         true,
		errors.PULSE_TEST_FIELD_NOT_NUMERIC:         true,
		errors.PULSE_TEST_FIELD2_NOT_NUMERIC:        true,
		errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL: true,
	}
	for _, op := range Operators() {
		for _, slot := range []Slot{SlotField, SlotField2, SlotTarget, SlotPredictor, SlotSplitBy, SlotSubject, SlotRows, SlotCols, SlotStratify, SlotPrediction} {
			for b := 0; b < 256; b++ {
				ft := encoding.FieldType(b)
				if !ft.IsKnown() {
					continue
				}
				r := Refusal(op, slot, "f", ft)
				if r == nil {
					continue
				}
				if !allowed[r.Code] {
					t.Errorf("%s %s %s: %s", op, slot, ft, r.Code)
				}
				if strings.Contains(r.Message, " field field ") || strings.Contains(r.Message, " "+string(slot)+" "+string(slot)+" ") {
					t.Errorf("%s %s: message repeats a word: %q", op, slot, r.Message)
				}
			}
		}
	}
}

// TestTypeNames_NumericReaders pins E2-S4's rows: the arithmetic
// aggregators, AGG_RATIO's two params columns and FEAT_LOG / SQRT /
// BUCKETIZE read the analytics-numeric types (the decimal rule still
// trims decimal128 from an aggregator with no decimal arm) and refuse
// a categorical or a set; the counting aggregators are not tabled for
// a categorical refusal.
func TestTypeNames_NumericReaders(t *testing.T) {
	all := []string{"date", "datetime", "decimal128", "f32", "f64", "packed_bool", "u16", "u32", "u4", "u64", "u8"}
	noDecimal := []string{"date", "datetime", "f32", "f64", "packed_bool", "u16", "u32", "u4", "u64", "u8"}
	for op, want := range map[string][]string{
		"AGG_SUM": all, "AGG_VARIANCE": all, "AGG_MEDIAN": noDecimal, "AGG_CI_UPPER": noDecimal,
		"FEAT_LOG": all, "FEAT_SQRT": all, "FEAT_BUCKETIZE": all,
	} {
		if got := TypeNames(op, SlotField); !reflect.DeepEqual(got, want) {
			t.Errorf("%s field: %v, want %v", op, got, want)
		}
	}
	for _, slot := range []Slot{SlotNumerator, SlotDenominator} {
		if got := TypeNames("AGG_RATIO", slot); !reflect.DeepEqual(got, all) {
			t.Errorf("AGG_RATIO %s: %v, want %v", slot, got, all)
		}
	}
	if _, tabled := Lookup("AGG_RATIO", SlotField); tabled {
		t.Error("AGG_RATIO's ignored field is tabled")
	}
	for _, op := range []string{"AGG_COUNT", "AGG_MODE", "AGG_MODE_COUNT", "AGG_FREQUENCY", "AGG_DISTINCT_COUNT", "AGG_NULL_COUNT"} {
		if !Accepts(op, SlotField, encoding.FieldTypeCategoricalU16) {
			t.Errorf("%s refuses a categorical", op)
		}
	}
}

// TestCategoricalValue_PackedBoolLevel pins the level class: every slot
// that reads a level accepts a packed_bool beside the categoricals —
// the categorical encoders and FEAT_TRAIN_TEST_SPLIT's stratify
// included — while TEST_ANOVA_RM's subject (a two-level subject id
// identifies no subject) stays categorical-only.
func TestCategoricalValue_PackedBoolLevel(t *testing.T) {
	level := []string{"categorical_u16", "categorical_u32", "categorical_u8", "packed_bool"}
	categorical := level[:3]
	for _, tc := range []struct {
		op   string
		slot Slot
		want []string
	}{
		{"TEST_PROP_Z", SlotField, level},
		{"TEST_PROP_Z", SlotSplitBy, level},
		{"TEST_CHISQ", SlotRows, level},
		{"TEST_CHISQ", SlotCols, level},
		{"TEST_FISHER_EXACT", SlotRows, level},
		{"TEST_FISHER_EXACT", SlotCols, level},
		{"TEST_T", SlotSplitBy, level},
		{"TEST_BOX_M", SlotSplitBy, level},
		{"TEST_ANOVA_RM", SlotSplitBy, level},
		{"TEST_ANOVA_RM", SlotSubject, categorical},
		{"FEAT_ONE_HOT", SlotField, level},
		{"FEAT_FREQUENCY_ENCODE", SlotField, level},
		{"FEAT_TARGET_ENCODE", SlotField, level},
		{"FEAT_TRAIN_TEST_SPLIT", SlotStratify, level},
	} {
		if got := TypeNames(tc.op, tc.slot); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %s: %v, want %v", tc.op, tc.slot, got, tc.want)
		}
	}
}

// TestLevels pins the level list FEAT_ONE_HOT's columns come from on
// both sides: a categorical's dictionary labels in dictionary order, a
// packed_bool's "0" / "1", nil for anything else.
func TestLevels(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("b")
	dict.Add("a")
	for _, tc := range []struct {
		name string
		f    *encoding.Field
		want []string
	}{
		{"nil", nil, nil},
		{"categorical", &encoding.Field{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict}, []string{"b", "a"}},
		{"categorical without dictionary", &encoding.Field{Name: "c", Type: encoding.FieldTypeCategoricalU8}, nil},
		{"packed_bool", &encoding.Field{Name: "p", Type: encoding.FieldTypePackedBool}, []string{"0", "1"}},
		{"f64", &encoding.Field{Name: "x", Type: encoding.FieldTypeF64}, nil},
	} {
		if got := Levels(tc.f); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSuccessLevelRefusal pins TEST_PROP_Z's strict packed_bool success:
// "0" / "1" only, refused with the valid levels in details; a
// categorical (or unknown) outcome is never judged here.
func TestSuccessLevelRefusal(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	for _, tc := range []struct {
		field, success string
		refused        bool
	}{
		{"flag", "0", false}, {"flag", "1", false},
		{"flag", "true", true}, {"flag", "false", true}, {"flag", " 1", true}, {"flag", "1.0", true},
		{"cat", "anything", false}, {"missing", "x", false},
	} {
		cerr := SuccessLevelRefusal("TEST_PROP_Z", tc.field, tc.success, schema)
		if (cerr != nil) != tc.refused {
			t.Errorf("%s=%q: refusal %v, want refused=%v", tc.field, tc.success, cerr, tc.refused)
			continue
		}
		if cerr == nil {
			continue
		}
		if cerr.Code != errors.PULSE_TEST_SUCCESS_VALUE_MISSING {
			t.Errorf("%s=%q: code %s", tc.field, tc.success, cerr.Code)
		}
		if got := cerr.Details["valid_levels"]; !reflect.DeepEqual(got, []string{"0", "1"}) {
			t.Errorf("%s=%q: valid_levels %v", tc.field, tc.success, got)
		}
	}
}

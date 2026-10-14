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
				if !ok || ft.IsVector() || ft.IsSet() || ft.IsCategorical() {
					t.Errorf("%s %s lists %s", op, slot, n)
				}
			}
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
	for _, g := range got {
		if strings.HasPrefix(g, "TEST_") {
			t.Errorf("%s raises the test codes, not PULSE_FIELD_TYPE_UNSUPPORTED", g)
		}
	}
	if got := OperatorsRaising(errors.PROCESSING_CONFIG); got != nil {
		t.Errorf("PROCESSING_CONFIG owners: %v", got)
	}
}

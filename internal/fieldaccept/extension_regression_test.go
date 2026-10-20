package fieldaccept

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// TestExtensionRegression_Rule: an extension REG_* model with no Accepts
// follows REG_OLS's row (a categorical refused, a u32 accepted); a
// declared Accepts is exactly the set; the refusal names the model and
// lists what it accepts.
func TestExtensionRegression_Rule(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "n", Type: encoding.FieldTypeU32},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8},
	}}
	if got, want := ExtensionRegressionTypeNames(nil), TypeNames("REG_OLS", SlotPredictor); !slices.Equal(got, want) {
		t.Errorf("default type names = %v, want REG_OLS's %v", got, want)
	}
	if cerr := CheckExtensionRegression("REG_ACME_X", nil, SlotPredictor, "n", schema); cerr != nil {
		t.Errorf("u32 under the default rule refused: %v", cerr)
	}
	cerr := CheckExtensionRegression("REG_ACME_X", nil, SlotTarget, "c", schema)
	if cerr == nil || cerr.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED || cerr.Details["type"] != "REG_ACME_X" || cerr.Details["slot"] != "target" {
		t.Fatalf("categorical target: %v", cerr)
	}
	accepts := []encoding.FieldType{encoding.FieldTypeF64, encoding.FieldTypeF64}
	if got := ExtensionRegressionTypeNames(accepts); !slices.Equal(got, []string{"f64"}) {
		t.Errorf("declared type names = %v, want [f64]", got)
	}
	cerr = CheckExtensionRegression("REG_ACME_X", accepts, SlotPredictor, "n", schema)
	if cerr == nil || !slices.Equal(cerr.Details["accepts_types"].([]string), []string{"f64"}) {
		t.Fatalf("u32 outside the declared set: %v", cerr)
	}
	for _, field := range []string{"y", "missing", ""} {
		if cerr := CheckExtensionRegression("REG_ACME_X", accepts, SlotTarget, field, schema); cerr != nil {
			t.Errorf("field %q refused: %v", field, cerr)
		}
	}
	if !ExtensionRegressionAccepts(accepts, encoding.FieldTypeVecF32) {
		t.Error("a vector type is the field-reference walk's refusal, not this rule's")
	}
}

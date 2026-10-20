package fieldaccept

import (
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// extensionRegressionRule is the row a registered extension REG_* model's
// target and predictor follow when its registration declares no Accepts:
// the built-in regressions' (REG_OLS's) — the analytics-numeric set.
const extensionRegressionRule = "REG_OLS"

// ExtensionRegressionAccepts reports whether a registered extension
// REG_* model's target / predictor slot accepts ft: the built-in
// regressions' rule when accepts is empty, else exactly the listed
// types. A vector type answers true (the field-reference walk refuses
// it), as Accepts does.
func ExtensionRegressionAccepts(accepts []encoding.FieldType, ft encoding.FieldType) bool {
	if ft.IsVector() {
		return true
	}
	if len(accepts) == 0 {
		return Accepts(extensionRegressionRule, SlotPredictor, ft)
	}
	return slices.Contains(accepts, ft)
}

// ExtensionRegressionTypeNames lists the field types a registered
// extension REG_* model's target and predictors accept
// (ExtensionRegressionAccepts), sorted byte-wise like TypeNames; vector
// types are never listed.
func ExtensionRegressionTypeNames(accepts []encoding.FieldType) []string {
	if len(accepts) == 0 {
		return slices.Clone(TypeNames(extensionRegressionRule, SlotPredictor))
	}
	var out []string
	for _, ft := range accepts {
		if ft.IsKnown() && !ft.IsVector() && !slices.Contains(out, ft.String()) {
			out = append(out, ft.String())
		}
	}
	sort.Strings(out)
	return out
}

// CheckExtensionRegression is Check for a registered extension REG_*
// model op's target or predictor slot (ExtensionRegressionAccepts):
// nil when the schema lacks the field or the slot accepts it, else
// PULSE_FIELD_TYPE_UNSUPPORTED in the table's shape, under op's name.
// The runtime adapter and predict raise the same error.
func CheckExtensionRegression(op string, accepts []encoding.FieldType, slot Slot, field string, schema *encoding.Schema) *errors.CodedError {
	if schema == nil || field == "" {
		return nil
	}
	f := schema.Field(field)
	if f == nil || ExtensionRegressionAccepts(accepts, f.Type) {
		return nil
	}
	names := ExtensionRegressionTypeNames(accepts)
	return errors.NewCodedErrorWithDetails(errors.PULSE_FIELD_TYPE_UNSUPPORTED,
		op+" "+string(slot)+" "+field+" has type "+f.Type.String()+"; accepted: "+strings.Join(names, ", "),
		map[string]any{
			"type":          op,
			"slot":          string(slot),
			"field":         field,
			"field_type":    f.Type.String(),
			"accepts_types": names,
		})
}

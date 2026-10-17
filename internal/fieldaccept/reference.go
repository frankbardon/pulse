package fieldaccept

import (
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The Details["reason"] values of PULSE_TEST_REFERENCE_INVALID.
const (
	ReferenceReasonWithoutSplitBy = "without_split_by"
	ReferenceReasonNotALevel      = "not_a_level"
)

// referenceTests are the two-group tests that read params.reference:
// the split_by level every signed figure (diff, its interval, the
// statistic, the signed effect size) is read against.
var referenceTests = map[types.TestType]bool{
	types.TEST_MANN_WHITNEY_U: true,
	types.TEST_PROP_Z:         true,
	types.TEST_T:              true,
	types.TEST_WELCH:          true,
	types.TEST_Z_TWO_SAMPLE:   true,
}

// TakesReference reports whether test type op reads params.reference.
func TakesReference(op types.TestType) bool { return referenceTests[op] }

// ReferenceRefusal is the two-group tests' params.reference rule: an
// empty reference is absent (alphabetical order, nothing to judge); a
// set one needs split_by (the one-sample / one-proportion forms have no
// second group to read against), and on a schema field must be one of
// its levels — "0" / "1" on a packed_bool, a dictionary entry on a
// categorical. A split_by the schema does not hold (a derived column)
// is not judged here: the runtime's Finalize refuses an unobserved
// reference (PULSE_TEST_REFERENCE_MISSING). Predict and the runtime
// constructors both raise it (PULSE_TEST_REFERENCE_INVALID), after the
// field and split_by type refusals. Nil when usable.
func ReferenceRefusal(op, splitBy, reference string, schema *encoding.Schema) *errors.CodedError {
	if reference == "" {
		return nil
	}
	if splitBy == "" {
		return errors.NewCodedErrorWithDetails(errors.PULSE_TEST_REFERENCE_INVALID,
			op+" params.reference names the split_by group signed figures are read against; it needs split_by (drop reference for the one-sample form)",
			map[string]any{"type": op, "reason": ReferenceReasonWithoutSplitBy, "reference": reference})
	}
	if schema == nil {
		return nil
	}
	f := schema.Field(splitBy)
	if f == nil {
		return nil
	}
	notALevel := func(hint string, extra map[string]any) *errors.CodedError {
		details := map[string]any{"type": op, "reason": ReferenceReasonNotALevel, "reference": reference, "split_by": splitBy, "field_type": f.Type.String()}
		for k, v := range extra {
			details[k] = v
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_TEST_REFERENCE_INVALID,
			op+" params.reference "+strconv.Quote(reference)+" is not a level of "+f.Type.String()+" field "+splitBy+"; "+hint, details)
	}
	switch {
	case f.Type == encoding.FieldTypePackedBool:
		if reference != PackedBoolFalse && reference != PackedBoolTrue {
			return notALevel("use \"1\" (true) or \"0\" (false)", map[string]any{"valid_levels": PackedBoolLevels()})
		}
	case f.Type.IsCategorical() && f.Dictionary != nil:
		if _, ok := f.Dictionary.IDFor(reference); !ok {
			return notALevel("name a dictionary entry exactly (pulse inspect lists them)", nil)
		}
	}
	return nil
}

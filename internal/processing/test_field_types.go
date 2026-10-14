package processing

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// testFieldTypeRefusal is the accepted-types table's refusal
// (internal/fieldaccept) for a tier-1 test's field and, for a paired /
// bivariate test, its field2: a categorical's dictionary index and a
// set's bitmask are not values, so the test refuses them
// (PULSE_TEST_FIELD_NOT_NUMERIC / PULSE_TEST_FIELD2_NOT_NUMERIC) rather
// than reading every row as null and reporting n = 0. Predict reports
// the same refusal. Nil when both are accepted or not schema fields.
func testFieldTypeRefusal(spec *types.Test, schema *encoding.Schema) *errors.CodedError {
	op := string(spec.Type)
	if cerr := fieldaccept.Check(op, fieldaccept.SlotField, spec.Field, schema); cerr != nil {
		return cerr
	}
	return fieldaccept.Check(op, fieldaccept.SlotField2, spec.Field2, schema)
}

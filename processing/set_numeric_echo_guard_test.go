package processing

import (
	stderrors "errors"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The numeric-echo guard family.
//
// Record.NumericValue refuses set fields: the decoder's float64 echo is
// the LOW 64 BITS of the membership bitmask for set_u128 / set_u256 and
// is already lossy above 2^53 for set_u64. Every operator that reads a
// field through NumericValue therefore has exactly two possible
// behaviours on a set column and both are SILENT — it matches the echo
// (a plausible wrong answer) or, after the refusal, drops every row (a
// plausible empty answer).
//
// These tests pin the third behaviour: refuse at construction, loudly,
// with PROCESSING_CONFIG naming the field, its type and the set-aware
// alternative. The descriptor half (AcceptsTypes no longer listing any
// set rung for these operators) is asserted in
// descriptor/wide_set_surface_test.go.

// echoGuardSchema builds a one-set-field schema at the given rung.
func echoGuardSchema(t *testing.T, ft encoding.FieldType, members int) *encoding.Schema {
	t.Helper()
	d := encoding.NewDictionary()
	for i := range members {
		if _, err := d.Add("m" + strconv.Itoa(i)); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: ft, Nullable: true, Dictionary: d, Description: "Multi-select survey response tags"},
		{Name: "age", Type: encoding.FieldTypeU8, Description: "Respondent age in years"},
	}}
}

// assertSetRefusal checks err is a PROCESSING_CONFIG coded error whose
// message names the field and its type.
func assertSetRefusal(t *testing.T, err error, who string, ft encoding.FieldType) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s on a %s column built without error — it would answer silently", who, ft)
	}
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("%s refusal is not a *errors.CodedError: %T %v", who, err, err)
	}
	if coded.Code != errors.PROCESSING_CONFIG {
		t.Errorf("%s refusal code = %s, want PROCESSING_CONFIG", who, coded.Code)
	}
	msg := coded.Error()
	if !strings.Contains(msg, "tags") {
		t.Errorf("%s refusal does not name the field: %q", who, msg)
	}
	if !strings.Contains(msg, ft.String()) {
		t.Errorf("%s refusal does not name the type %s: %q", who, ft, msg)
	}
}

// setRungs is every registered rung, narrow and wide. The guard is not
// a wide-set guard — set_u64's echo is already lossy above 2^53.
var setRungs = []struct {
	ft      encoding.FieldType
	members int
}{
	{encoding.FieldTypeSetU8, 8},
	{encoding.FieldTypeSetU64, 64},
	{encoding.FieldTypeSetU128, 120},
	{encoding.FieldTypeSetU256, 206},
}

func TestFilterInclude_RefusesSetField(t *testing.T) {
	for _, rung := range setRungs {
		t.Run(rung.ft.String(), func(t *testing.T) {
			schema := echoGuardSchema(t, rung.ft, rung.members)
			for _, ftype := range []types.FiltererType{types.FILTER_INCLUDE, types.FILTER_EXCLUDE} {
				b, ok := filtererRegistry[ftype]
				if !ok {
					t.Fatalf("no registry entry for %s", ftype)
				}
				_, err := b().Build(&types.Filterer{Type: ftype, Field: "tags", Values: []string{"1"}}, schema)
				assertSetRefusal(t, err, string(ftype), rung.ft)
			}
		})
	}
}

// A non-set field must keep working — the guard is a set guard, not a
// general narrowing of these filterers.
func TestFilterInclude_StillAcceptsNumericField(t *testing.T) {
	schema := echoGuardSchema(t, encoding.FieldTypeSetU256, 206)
	for _, ftype := range []types.FiltererType{types.FILTER_INCLUDE, types.FILTER_EXCLUDE} {
		if _, err := filtererRegistry[ftype]().Build(
			&types.Filterer{Type: ftype, Field: "age", Values: []string{"30"}}, schema); err != nil {
			t.Errorf("%s on a u8 field: %v", ftype, err)
		}
	}
}

func TestGroupCategory_RefusesSetField(t *testing.T) {
	for _, rung := range setRungs {
		t.Run(rung.ft.String(), func(t *testing.T) {
			schema := echoGuardSchema(t, rung.ft, rung.members)
			_, err := newCategoryGrouper(&types.Group{Type: types.GROUP_CATEGORY, Field: "tags"}, schema)
			assertSetRefusal(t, err, string(types.GROUP_CATEGORY), rung.ft)
			if !strings.Contains(err.Error(), string(types.GROUP_SET_VALUE)) {
				t.Errorf("GROUP_CATEGORY refusal does not point at GROUP_SET_VALUE: %q", err)
			}
		})
	}
}

func TestGroupCategory_StillAcceptsNumericField(t *testing.T) {
	schema := echoGuardSchema(t, encoding.FieldTypeSetU256, 206)
	if _, err := newCategoryGrouper(&types.Group{Type: types.GROUP_CATEGORY, Field: "age"}, schema); err != nil {
		t.Errorf("GROUP_CATEGORY on a u8 field: %v", err)
	}
}

func TestNumericAggregators_RefuseSetField(t *testing.T) {
	aggs := []types.AggregationType{types.AGG_FREQUENCY, types.AGG_MODE, types.AGG_DISTINCT_COUNT}
	for _, rung := range setRungs {
		t.Run(rung.ft.String(), func(t *testing.T) {
			schema := echoGuardSchema(t, rung.ft, rung.members)
			for _, at := range aggs {
				factory, ok := aggregatorRegistry[at]
				if !ok {
					t.Fatalf("no registry entry for %s", at)
				}
				_, err := factory(&types.Aggregation{Type: at, Field: "tags"}, schema)
				assertSetRefusal(t, err, string(at), rung.ft)
			}
		})
	}
}

func TestNumericAggregators_StillAcceptNumericField(t *testing.T) {
	schema := echoGuardSchema(t, encoding.FieldTypeSetU256, 206)
	for _, at := range []types.AggregationType{types.AGG_FREQUENCY, types.AGG_MODE, types.AGG_DISTINCT_COUNT} {
		if _, err := aggregatorRegistry[at](&types.Aggregation{Type: at, Field: "age"}, schema); err != nil {
			t.Errorf("%s on a u8 field: %v", at, err)
		}
	}
}

// AGG_COUNT and AGG_NULL_COUNT genuinely handle set columns — they ask
// presence, not value (processing/record_presence.go). The guard must
// not catch them.
func TestPresenceAggregators_StillAcceptSetField(t *testing.T) {
	schema := echoGuardSchema(t, encoding.FieldTypeSetU256, 206)
	for _, at := range []types.AggregationType{types.AGG_COUNT, types.AGG_NULL_COUNT} {
		if _, err := aggregatorRegistry[at](&types.Aggregation{Type: at, Field: "tags"}, schema); err != nil {
			t.Errorf("%s on a set_u256 field: %v — presence aggregators must keep counting set columns", at, err)
		}
	}
}

func TestNumericAttributes_RefuseSetField(t *testing.T) {
	attrs := []types.AttributeType{
		types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED, types.ATTR_PERCENTILE,
	}
	for _, rung := range setRungs {
		t.Run(rung.ft.String(), func(t *testing.T) {
			schema := echoGuardSchema(t, rung.ft, rung.members)
			for _, at := range attrs {
				factory, ok := attributeRegistry[at]
				if !ok {
					t.Fatalf("no registry entry for %s", at)
				}
				_, err := factory(&types.Attribute{Type: at, Field: "tags", Label: "out"}, schema)
				assertSetRefusal(t, err, string(at), rung.ft)
			}
		})
	}
}

func TestNumericAttributes_StillAcceptNumericField(t *testing.T) {
	schema := echoGuardSchema(t, encoding.FieldTypeSetU256, 206)
	for _, at := range []types.AttributeType{
		types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED, types.ATTR_PERCENTILE,
	} {
		if _, err := attributeRegistry[at](&types.Attribute{Type: at, Field: "age", Label: "out"}, schema); err != nil {
			t.Errorf("%s on a u8 field: %v", at, err)
		}
	}
}

// A nil schema is the registry probe-construction path (and the
// extension probe). Every guard must wave it through — it has nothing
// to check and refusing there would break registry construction.
//
// Asserted against the guard helpers directly rather than through the
// factories: includeFilterer.Build dereferences the schema immediately
// after the guard, so a nil schema panics there today for reasons that
// have nothing to do with set types and that this story does not change.
func TestNumericEchoGuards_TolerateNilSchema(t *testing.T) {
	if err := rejectSetFieldForNumericFilter(&types.Filterer{Type: types.FILTER_INCLUDE, Field: "tags"}, nil); err != nil {
		t.Errorf("rejectSetFieldForNumericFilter(nil schema) = %v, want nil", err)
	}
	if err := rejectSetFieldForNumericGrouper(&types.Group{Type: types.GROUP_CATEGORY, Field: "tags"}, nil); err != nil {
		t.Errorf("rejectSetFieldForNumericGrouper(nil schema) = %v, want nil", err)
	}
	if err := rejectSetFieldForNumericAggregator(&types.Aggregation{Type: types.AGG_MODE, Field: "tags"}, nil); err != nil {
		t.Errorf("rejectSetFieldForNumericAggregator(nil schema) = %v, want nil", err)
	}
	if err := rejectSetFieldForNumericAttribute(&types.Attribute{Type: types.ATTR_ZSCORE, Field: "tags"}, nil); err != nil {
		t.Errorf("rejectSetFieldForNumericAttribute(nil schema) = %v, want nil", err)
	}

	// An unknown field is likewise not the guards' business — the
	// operators have their own handling for it.
	schema := echoGuardSchema(t, encoding.FieldTypeSetU256, 206)
	if err := rejectSetFieldForNumericAggregator(&types.Aggregation{Type: types.AGG_MODE, Field: "nope"}, schema); err != nil {
		t.Errorf("rejectSetFieldForNumericAggregator(unknown field) = %v, want nil", err)
	}
	if err := rejectSetFieldForNumericAttribute(&types.Attribute{Type: types.ATTR_ZSCORE, Field: "nope"}, schema); err != nil {
		t.Errorf("rejectSetFieldForNumericAttribute(unknown field) = %v, want nil", err)
	}
}

// TestWideSets_RejectedAsIndexKeys pins the point-lookup half: the new
// rungs inherit the existing IsSet()-keyed rejection and its written
// rationale, unchanged.
func TestWideSets_RejectedAsIndexKeys(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU16, encoding.FieldTypeSetU32,
		encoding.FieldTypeSetU64, encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	} {
		if IsIndexKeyableFieldType(ft) {
			t.Errorf("%s reported index-keyable; a multi-select bitmask has no unambiguous equality value", ft)
		}
		msg := IndexKeyRejectionMessage(ft)
		if !strings.Contains(msg, "set_*") {
			t.Errorf("%s rejection message fell back to the generic text: %q", ft, msg)
		}
		if !strings.Contains(msg, "FILTER_SET") {
			t.Errorf("%s rejection message does not name the alternative: %q", ft, msg)
		}
	}
}

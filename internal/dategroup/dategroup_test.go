package dategroup

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

func TestParse_Accepts(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		want  Spec
		dated bool
	}{
		{``, Spec{Component: "month", WeekStart: time.Monday}, false},
		{`{}`, Spec{Component: "month", WeekStart: time.Monday}, false},
		{`{"component":"hour"}`, Spec{Component: "hour", WeekStart: time.Monday}, false},
		{`{"component":"week"}`, Spec{Component: "week", WeekStart: time.Monday}, false},
		{`{"component":"week","week_start":"monday"}`, Spec{Component: "week", WeekStart: time.Monday}, false},
		{`{"component":"week","week_start":"sunday"}`, Spec{Component: "week", WeekStart: time.Sunday}, true},
		{`{"component":"week","week_start":"saturday"}`, Spec{Component: "week", WeekStart: time.Saturday}, true},
		{`{"component":"quarter","fiscal_offset":-3}`, Spec{Component: "quarter", FiscalOffset: -3, WeekStart: time.Monday}, false},
		{`{"component":"day","week_start":null}`, Spec{Component: "day", WeekStart: time.Monday}, false},
	} {
		got, err := Parse(json.RawMessage(tc.raw))
		if err != nil {
			t.Fatalf("Parse(%s): %v", tc.raw, err)
		}
		if got != tc.want || got.DatedWeeks() != tc.dated {
			t.Fatalf("Parse(%s) = %+v (dated %v), want %+v (dated %v)", tc.raw, got, got.DatedWeeks(), tc.want, tc.dated)
		}
	}
}

func TestParse_Refusals(t *testing.T) {
	for _, tc := range []struct{ raw, msg string }{
		{`{`, "invalid GROUP_DATE params: unexpected end of JSON input"},
		{`{"component":"minute"}`, `invalid date group component "minute": must be one of year, quarter, month, week, day, hour, day_of_week`},
		{`{"component":"year","fiscal_offset":12}`, "invalid GROUP_DATE fiscal_offset 12: must be in range [-11, 11]"},
		{`{"component":"hour","fiscal_offset":3}`, `GROUP_DATE fiscal_offset only applies to component=year or component=quarter, got "hour"`},
		{`{"component":"week","week_start":"Sunday"}`, `invalid GROUP_DATE week_start "Sunday": must be one of monday, tuesday, wednesday, thursday, friday, saturday, sunday`},
		{`{"component":"week","week_start":"sun"}`, `invalid GROUP_DATE week_start "sun": must be one of monday, tuesday, wednesday, thursday, friday, saturday, sunday`},
		{`{"component":"day","week_start":"sunday"}`, `GROUP_DATE week_start only applies to component=week, got "day"`},
		{`{"week_start":"monday"}`, `GROUP_DATE week_start only applies to component=week, got "month"`},
	} {
		_, err := Parse(json.RawMessage(tc.raw))
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PROCESSING_CONFIG || ce.Message != tc.msg {
			t.Fatalf("Parse(%s) = %v, want PROCESSING_CONFIG %q", tc.raw, err, tc.msg)
		}
	}
}

func TestCheckField(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "ts", Type: encoding.FieldTypeDateTime},
		{Name: "d", Type: encoding.FieldTypeDate},
		{Name: "n", Type: encoding.FieldTypeU32},
	}}
	hour := Spec{Component: "hour", WeekStart: time.Monday}
	day := Spec{Component: "day", WeekStart: time.Monday}
	for _, tc := range []struct {
		spec   Spec
		field  string
		schema *encoding.Schema
		msg    string
	}{
		{hour, "ts", schema, ""},
		{hour, "d", nil, ""},
		{day, "d", schema, ""},
		{day, "n", schema, ""},
		{hour, "d", schema, `GROUP_DATE component=hour requires a datetime field; field "d" is of type date`},
		{hour, "n", schema, `GROUP_DATE component=hour requires a datetime field; field "n" is of type u32`},
		{hour, "derived", schema, `GROUP_DATE component=hour requires a datetime field; field "derived" is absent from the schema (a derived field is read as epoch days)`},
	} {
		err := CheckField(tc.spec, tc.field, tc.schema)
		if tc.msg == "" {
			if err != nil {
				t.Fatalf("CheckField(%s, %s): %v", tc.spec.Component, tc.field, err)
			}
			continue
		}
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PROCESSING_CONFIG || ce.Message != tc.msg {
			t.Fatalf("CheckField(%s, %s) = %v, want %q", tc.spec.Component, tc.field, err, tc.msg)
		}
	}
}

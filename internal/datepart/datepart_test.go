package datepart

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

func TestParse(t *testing.T) {
	for _, part := range []string{"year", "month", "day", "year_month", "year_month_day", "month_day", "hour"} {
		got, err := Parse(json.RawMessage(`{"part":"` + part + `"}`))
		if err != nil || got != part {
			t.Errorf("%s: got %q, %v", part, got, err)
		}
	}
	for raw, want := range map[string]string{
		``:                  `date_part attribute requires params with a "part" field`,
		`{}`:                `date_part attribute requires a "part" field in params`,
		`{"part":"minute"}`: `invalid date part "minute": must be one of year, month, day, year_month, year_month_day, month_day, hour`,
	} {
		_, err := Parse(json.RawMessage(raw))
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PROCESSING_CONFIG || ce.Message != want {
			t.Errorf("%q: err = %v, want %q", raw, err, want)
		}
	}
	if _, err := Parse(json.RawMessage(`{`)); err == nil {
		t.Error("malformed JSON accepted")
	}
}

func TestCheckField(t *testing.T) {
	date := &encoding.Field{Name: "d", Type: encoding.FieldTypeDate}
	dt := &encoding.Field{Name: "ts", Type: encoding.FieldTypeDateTime}
	num := &encoding.Field{Name: "x", Type: encoding.FieldTypeF64}
	for _, tc := range []struct {
		part string
		f    *encoding.Field
		ok   bool
	}{
		{"year", date, true}, {"year", dt, true}, {"hour", dt, true},
		{"hour", date, false}, {"year", num, false}, {"year", nil, false},
	} {
		err := CheckField(tc.part, "f", tc.f)
		if (err == nil) != tc.ok {
			t.Errorf("%s over %v: err = %v, want ok=%v", tc.part, tc.f, err, tc.ok)
		}
	}
}

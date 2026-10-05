package io

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestFormatFieldValue_PreEpochRoundTrip pins the text writer's signed
// read of the date and datetime words: a literal converted by
// convertValue must format back to itself on both sides of the epoch.
func TestFormatFieldValue_PreEpochRoundTrip(t *testing.T) {
	cases := []struct {
		ft  encoding.FieldType
		lit string
	}{
		{encoding.FieldTypeDate, "1900-01-01"},
		{encoding.FieldTypeDate, "1969-12-31"},
		{encoding.FieldTypeDate, "1970-01-01"},
		{encoding.FieldTypeDate, "2024-02-29"},
		{encoding.FieldTypeDateTime, "1900-01-01T06:00:00Z"},
		{encoding.FieldTypeDateTime, "1969-12-31T23:59:59Z"},
		{encoding.FieldTypeDateTime, "2024-02-29T12:30:00Z"},
	}
	for _, c := range cases {
		raw, err := convertValue(c.lit, c.ft, nil, "")
		if err != nil {
			t.Fatalf("convertValue(%s, %q): %v", c.ft, c.lit, err)
		}
		if got := formatFieldValue(c.ft, raw, nil, nil); got != c.lit {
			t.Errorf("%s %q formatted back as %q", c.ft, c.lit, got)
		}
	}
}

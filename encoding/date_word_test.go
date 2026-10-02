package encoding

import (
	"bytes"
	"testing"
)

func TestDateDays_SignedRoundTrip(t *testing.T) {
	cases := []struct {
		lit  string
		want int32
	}{
		{"1900-01-01", -25567},
		{"1969-12-31", -1},
		{"1970-01-01", 0},
		{"2024-02-29", 19782},
	}
	for _, c := range cases {
		raw, err := ParseDate(c.lit)
		if err != nil {
			t.Fatalf("ParseDate(%q): %v", c.lit, err)
		}
		var buf bytes.Buffer
		if err := WriteFieldValue(&buf, FieldTypeDate, uint64(raw)); err != nil {
			t.Fatal(err)
		}
		got, err := ReadFieldValue(&buf, FieldTypeDate)
		if err != nil {
			t.Fatal(err)
		}
		if d := DateDays(got); d != c.want {
			t.Errorf("%s: DateDays = %d, want %d", c.lit, d, c.want)
		}
	}
}

func TestDateTimeSeconds_Signed(t *testing.T) {
	raw, err := ParseDateTime("1969-12-31T23:59:59Z")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteFieldValue(&buf, FieldTypeDateTime, raw); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFieldValue(&buf, FieldTypeDateTime)
	if err != nil {
		t.Fatal(err)
	}
	if s := DateTimeSeconds(got); s != -1 {
		t.Fatalf("DateTimeSeconds = %d, want -1", s)
	}
	if day := DateTimeToDay(DateTimeSeconds(got)); day != -1 {
		t.Fatalf("DateTimeToDay = %d, want -1", day)
	}
}

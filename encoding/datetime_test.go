package encoding

import (
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
)

// TestParseDateTime_AcceptedLayouts pins the exact literal surface
// ParseDateTime accepts. Every entry of DateTimeFormats must have at
// least one row here — the accepted layout list is a contract, not an
// implementation detail.
func TestParseDateTime_AcceptedLayouts(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int64 // epoch seconds
	}{
		{"rfc3339_utc_z", "2024-03-04T10:11:12Z", 1709547072},
		{"rfc3339_positive_offset", "2024-03-04T12:11:12+02:00", 1709547072},
		{"rfc3339_negative_offset", "2024-03-04T05:11:12-05:00", 1709547072},
		{"naive_seconds", "2024-03-04T10:11:12", 1709547072},
		{"minute_precision_offset", "2024-03-04T10:11Z", 1709547060},
		{"naive_minute_precision", "2024-03-04T10:11", 1709547060},
		{"space_separated_seconds", "2024-03-04 10:11:12", 1709547072},
		{"space_separated_minutes", "2024-03-04 10:11", 1709547060},
		{"fractional_seconds_truncated", "2024-03-04T10:11:12.750Z", 1709547072},
		{"midnight", "2024-03-04T00:00:00Z", 1709510400},
		{"epoch", "1970-01-01T00:00:00Z", 0},
		{"leap_day", "2024-02-29T23:59:59Z", 1709251199},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDateTime(tc.raw)
			if err != nil {
				t.Fatalf("ParseDateTime(%q) error = %v, want nil", tc.raw, err)
			}
			if int64(got) != tc.want {
				t.Errorf("ParseDateTime(%q) = %d, want %d", tc.raw, int64(got), tc.want)
			}
		})
	}
}

// TestParseDateTime_EveryDeclaredLayoutParses guards against a layout
// being added to DateTimeFormats that Go's time package cannot round a
// reference value through.
func TestParseDateTime_EveryDeclaredLayoutParses(t *testing.T) {
	ref := time.Date(2024, 3, 4, 10, 11, 12, 0, time.UTC)
	for _, layout := range DateTimeFormats {
		lit := ref.Format(layout)
		if _, err := ParseDateTime(lit); err != nil {
			t.Errorf("layout %q formats to %q which ParseDateTime rejects: %v", layout, lit, err)
		}
	}
}

// TestParseDateTime_RejectsAmbiguousAndNonDateTime is the strictness
// half of the contract. The slash forms are ambiguous day/month and the
// date-only forms are `date` values, not datetimes — both must fail
// loudly rather than resolve to a guessed instant.
func TestParseDateTime_Rejects(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		why  string
	}{
		{"ambiguous_us_slash", "03/04/2024", "day/month order is ambiguous"},
		{"ambiguous_slash_with_time", "03/04/2024 10:11:12", "day/month order is ambiguous"},
		{"ambiguous_slash_iso_order", "2024/03/04 10:11:12", "slash forms are not accepted"},
		{"date_only_iso", "2024-03-04", "date-only literal is a date, not a datetime"},
		{"date_only_slashed", "2024/03/04", "date-only literal is a date, not a datetime"},
		{"date_only_dmon", "04-Mar-2024", "date-only literal is a date, not a datetime"},
		{"time_only", "10:11:12", "no calendar date"},
		{"empty", "", "not a literal"},
		{"garbage", "not-a-datetime", "not a literal"},
		{"epoch_seconds_integer", "1709547072", "a bare integer is not a datetime literal"},
		{"trailing_garbage", "2024-03-04T10:11:12Zextra", "trailing content"},
		{"out_of_range_month", "2024-13-04T10:11:12Z", "month 13 does not exist"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDateTime(tc.raw)
			if err == nil {
				t.Fatalf("ParseDateTime(%q) = nil error, want rejection (%s)", tc.raw, tc.why)
			}
			var coded *errors.CodedError
			if !errorsAs(err, &coded) {
				t.Fatalf("ParseDateTime(%q) error type = %T, want *errors.CodedError", tc.raw, err)
			}
			if coded.Code != errors.ENCODING_INVALID {
				t.Errorf("ParseDateTime(%q) code = %s, want %s", tc.raw, coded.Code, errors.ENCODING_INVALID)
			}
		})
	}
}

// TestParseDateTime_RejectsEveryDateOnlyLayout pins the boundary
// against the sibling date type: no layout DateFormats accepts as a
// date-only literal may be swallowed by ParseDateTime. This is what
// keeps io/infer.go's date-vs-datetime classification stable.
func TestParseDateTime_RejectsEveryDateOnlyLayout(t *testing.T) {
	ref := time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)
	for _, layout := range DateFormats {
		if hasTimeComponent(layout) {
			continue
		}
		lit := ref.Format(layout)
		if _, err := ParseDateTime(lit); err == nil {
			t.Errorf("date-only layout %q formats to %q which ParseDateTime accepted; "+
				"date columns would be reclassified as datetime", layout, lit)
		}
	}
}

// hasTimeComponent reports whether a reference layout carries a
// clock-time element. Test-local: the production code never needs it.
func hasTimeComponent(layout string) bool {
	for _, marker := range []string{"15", "03", "04", "05"} {
		if strings.Contains(layout, marker) {
			return true
		}
	}
	return false
}

// TestFormatDateTime_CanonicalForm pins the emitted text form.
func TestFormatDateTime_CanonicalForm(t *testing.T) {
	tests := []struct {
		name string
		raw  int64
		want string
	}{
		{"epoch", 0, "1970-01-01T00:00:00Z"},
		{"midday", 1709547072, "2024-03-04T10:11:12Z"},
		{"midnight", 1709510400, "2024-03-04T00:00:00Z"},
		{"one_second_before_epoch", -1, "1969-12-31T23:59:59Z"},
		{"pre_epoch", -2208988800, "1900-01-01T00:00:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDateTime(uint64(tc.raw)); got != tc.want {
				t.Errorf("FormatDateTime(%d) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestDateTime_RoundTripThroughCanonicalForm is the load-bearing
// invariant for the text adapters: the canonical string a `datetime`
// exports as must re-import to the identical on-wire uint64, including
// the time-of-day.
func TestDateTime_RoundTripThroughCanonicalForm(t *testing.T) {
	literals := []string{
		"1970-01-01T00:00:00Z",
		"2024-03-04T10:11:12Z",
		"2024-03-04T12:11:12+02:00",
		"2024-03-04 10:11:12",
		"2024-03-04T10:11",
		"2024-02-29T23:59:59Z",
		"1900-01-01T00:00:00Z",
		"2999-12-31T23:59:59Z",
	}
	for _, lit := range literals {
		t.Run(lit, func(t *testing.T) {
			first, err := ParseDateTime(lit)
			if err != nil {
				t.Fatalf("ParseDateTime(%q): %v", lit, err)
			}
			canonical := FormatDateTime(first)
			second, err := ParseDateTime(canonical)
			if err != nil {
				t.Fatalf("ParseDateTime(%q) (canonical form of %q): %v", canonical, lit, err)
			}
			if second != first {
				t.Errorf("round trip drifted: %q -> %d -> %q -> %d", lit, first, canonical, second)
			}
			if again := FormatDateTime(second); again != canonical {
				t.Errorf("canonical form unstable: %q vs %q", again, canonical)
			}
		})
	}
}

// TestDateTime_NotConflatedWithDate guards the seconds-vs-days
// distinction. The same wall-clock day must produce wildly different
// on-wire numbers for the two types; a swap would be silent corruption.
func TestDateTime_NotConflatedWithDate(t *testing.T) {
	days, err := ParseDate("2024-03-04")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	secs, err := ParseDateTime("2024-03-04T00:00:00Z")
	if err != nil {
		t.Fatalf("ParseDateTime: %v", err)
	}
	if uint64(days)*86400 != secs {
		t.Errorf("midnight datetime = %d seconds, want %d (= %d days * 86400)",
			secs, uint64(days)*86400, days)
	}
	if uint64(days) == secs {
		t.Error("date epoch-days and datetime epoch-seconds must not be the same number")
	}
}

// errorsAs is a tiny local shim so the test file does not shadow the
// package's own `errors` import name with the stdlib one.
func errorsAs(err error, target **errors.CodedError) bool {
	if ce, ok := err.(*errors.CodedError); ok {
		*target = ce
		return true
	}
	return false
}

// TestDateTimeToDay_Truncates pins the day-truncation semantic the
// date-family operators apply to a `datetime` column: the time of day
// is discarded, never rounded, and the floor runs toward the past so a
// pre-1970 instant lands on the calendar day that actually contains it.
func TestDateTimeToDay_Truncates(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int64 // epoch days
	}{
		{"midnight", "2024-03-04T00:00:00Z", 19786},
		{"midday", "2024-03-04T12:00:00Z", 19786},
		{"last second of the day", "2024-03-04T23:59:59Z", 19786},
		{"first second of the next day", "2024-03-05T00:00:00Z", 19787},
		{"epoch midnight", "1970-01-01T00:00:00Z", 0},
		{"epoch last second", "1970-01-01T23:59:59Z", 0},
		{"offset normalises before truncation", "2024-03-05T00:30:00+02:00", 19786},
		{"pre-epoch one second before midnight", "1969-12-31T23:59:59Z", -1},
		{"pre-epoch start of day", "1969-12-31T00:00:00Z", -1},
		{"pre-epoch two days back", "1969-12-30T12:00:00Z", -2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			secs, err := ParseDateTime(tc.raw)
			if err != nil {
				t.Fatalf("ParseDateTime(%q): %v", tc.raw, err)
			}
			if got := DateTimeToDay(int64(secs)); got != tc.want {
				t.Errorf("DateTimeToDay(%q) = %d days; want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// TestDateTimeToDay_AgreesWithParseDate is the cross-authority check:
// truncating a midnight datetime must land on the exact epoch-day
// integer ParseDate produces for the same calendar day. If these two
// ever diverge, a GROUP_DATE_RANGES set compiled from date literals
// would silently mis-bucket every datetime row.
func TestDateTimeToDay_AgreesWithParseDate(t *testing.T) {
	days := []string{"1970-01-01", "2000-02-29", "2024-03-04", "2038-01-19"}
	for _, d := range days {
		t.Run(d, func(t *testing.T) {
			want, err := ParseDate(d)
			if err != nil {
				t.Fatalf("ParseDate(%q): %v", d, err)
			}
			for _, clock := range []string{"T00:00:00Z", "T09:15:00Z", "T23:59:59Z"} {
				secs, err := ParseDateTime(d + clock)
				if err != nil {
					t.Fatalf("ParseDateTime(%q): %v", d+clock, err)
				}
				if got := DateTimeToDay(int64(secs)); got != int64(want) {
					t.Errorf("DateTimeToDay(%q) = %d; ParseDate(%q) = %d", d+clock, got, d, want)
				}
			}
		})
	}
}

// TestSecondsPerDay_MatchesParseDate guards the shared constant against
// drift from the divisor ParseDate itself uses.
func TestSecondsPerDay_MatchesParseDate(t *testing.T) {
	if SecondsPerDay != 86400 {
		t.Fatalf("SecondsPerDay = %d; want 86400", SecondsPerDay)
	}
	oneDay, err := ParseDateTime("1970-01-02T00:00:00Z")
	if err != nil {
		t.Fatalf("ParseDateTime: %v", err)
	}
	if oneDay != SecondsPerDay {
		t.Errorf("one day after the epoch = %d seconds; want SecondsPerDay = %d", oneDay, SecondsPerDay)
	}
}

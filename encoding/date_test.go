package encoding

import (
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
)

func TestParseDate_KnownLayouts(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"iso date", "2024-01-01"},
		{"us slash", "01/02/2024"},
		{"iso datetime Z", "2024-01-01T00:00:00Z"},
		{"iso datetime no zone", "2024-01-01T00:00:00"},
		{"slash date", "2024/01/01"},
		{"dd-Mon-yyyy", "01-Jan-2024"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDate(tt.input)
			if err != nil {
				t.Fatalf("ParseDate(%q): %v", tt.input, err)
			}
			if got == 0 {
				t.Errorf("ParseDate(%q) = 0, want a non-zero epoch-day value", tt.input)
			}
		})
	}
}

// TestParseDate_MatchesManualEpochDayMath proves ParseDate's on-wire
// value is exactly days-since-Unix-epoch, computed independently of
// ParseDate itself.
func TestParseDate_MatchesManualEpochDayMath(t *testing.T) {
	got, err := ParseDate("2024-01-01")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	want := uint32(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix() / 86400)
	if got != want {
		t.Errorf("ParseDate(\"2024-01-01\") = %d, want %d", got, want)
	}
}

func TestParseDate_UnparseableLiteral(t *testing.T) {
	_, err := ParseDate("not-a-date")
	if err == nil {
		t.Fatal("expected error for an unparseable date literal")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("expected ENCODING_INVALID, got: %v", err)
	}
}

func TestParseDate_EmptyLiteral(t *testing.T) {
	_, err := ParseDate("")
	if err == nil {
		t.Fatal("expected error for an empty date literal")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("expected ENCODING_INVALID, got: %v", err)
	}
}

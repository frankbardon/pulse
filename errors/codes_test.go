package errors_test

import (
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// TestErrorCodeFormat verifies all codes follow expected naming patterns.
func TestErrorCodeFormat(t *testing.T) {
	for _, code := range perr.AllCodes() {
		s := string(code)
		if s == "" {
			t.Error("empty error code found")
		}
		// Must be UPPER_SNAKE_CASE
		if s != strings.ToUpper(s) {
			t.Errorf("code %q is not uppercase", s)
		}
		if strings.Contains(s, " ") {
			t.Errorf("code %q contains spaces", s)
		}
	}
}

// TestErrorCodeUniqueness ensures no duplicate code values.
func TestErrorCodeUniqueness(t *testing.T) {
	seen := make(map[perr.Code]bool)
	for _, code := range perr.AllCodes() {
		if seen[code] {
			t.Errorf("duplicate error code: %s", code)
		}
		seen[code] = true
	}
}

// TestErrorCodeStringRepresentation verifies codes produce correct string output.
func TestErrorCodeStringRepresentation(t *testing.T) {
	tests := []struct {
		code perr.Code
		want string
	}{
		{perr.ENCODING_INVALID, "ENCODING_INVALID"},
		{perr.ENCODING_IO, "ENCODING_IO"},
		{perr.PROCESSING_CONFIG, "PROCESSING_CONFIG"},
		{perr.SERVICE_VALIDATION, "SERVICE_VALIDATION"},
		{perr.DATA_FILE, "DATA_FILE"},
		{perr.CLI_INPUT, "CLI_INPUT"},
		{perr.PULSE_IMPORT_SCHEMA_AMBIGUOUS, "PULSE_IMPORT_SCHEMA_AMBIGUOUS"},
		{perr.PULSE_IMPORT_ROW_ERROR, "PULSE_IMPORT_ROW_ERROR"},
		{perr.PULSE_EXPORT_ROW_ERROR, "PULSE_EXPORT_ROW_ERROR"},
		{perr.PULSE_IMPORT_CATEGORICAL_OVERFLOW, "PULSE_IMPORT_CATEGORICAL_OVERFLOW"},
		{perr.PULSE_IMPORT_CATEGORICAL_UNBOUNDED, "PULSE_IMPORT_CATEGORICAL_UNBOUNDED"},
		{perr.PULSE_IMPORT_DESCRIPTION_TOO_LONG, "PULSE_IMPORT_DESCRIPTION_TOO_LONG"},
		{perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL, "PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL"},
		{perr.PULSE_FIELD_DESCRIPTION_LOW_QUALITY, "PULSE_FIELD_DESCRIPTION_LOW_QUALITY"},
	}

	for _, tt := range tests {
		if got := string(tt.code); got != tt.want {
			t.Errorf("Code string = %q, want %q", got, tt.want)
		}
	}
}

// TestNoFrontendCodes ensures no FRONTEND_* codes exist.
func TestNoFrontendCodes(t *testing.T) {
	for _, code := range perr.AllCodes() {
		if strings.HasPrefix(string(code), "FRONTEND_") {
			t.Errorf("found frontend code: %s", code)
		}
	}
}

// TestNoTwirpCodes ensures no Twirp-related codes exist.
func TestNoTwirpCodes(t *testing.T) {
	for _, code := range perr.AllCodes() {
		s := strings.ToLower(string(code))
		if strings.Contains(s, "twirp") {
			t.Errorf("found twirp code: %s", code)
		}
	}
}

// TestNoOrbitPrefixes ensures no ORBIT_* identifiers exist.
func TestNoOrbitPrefixes(t *testing.T) {
	for _, code := range perr.AllCodes() {
		if strings.HasPrefix(string(code), "ORBIT_") {
			t.Errorf("found orbit code: %s", code)
		}
	}
}

// TestParseCode verifies string-to-code parsing.
func TestParseCode(t *testing.T) {
	t.Run("valid code round-trips", func(t *testing.T) {
		for _, code := range perr.AllCodes() {
			parsed, ok := perr.ParseCode(string(code))
			if !ok {
				t.Errorf("ParseCode(%q) failed", code)
			}
			if parsed != code {
				t.Errorf("ParseCode(%q) = %q, want %q", code, parsed, code)
			}
		}
	})

	t.Run("unknown string returns false", func(t *testing.T) {
		_, ok := perr.ParseCode("NOT_A_REAL_CODE")
		if ok {
			t.Error("ParseCode should return false for unknown code")
		}
	})

	t.Run("empty string returns false", func(t *testing.T) {
		_, ok := perr.ParseCode("")
		if ok {
			t.Error("ParseCode should return false for empty string")
		}
	})

	t.Run("lowercase version returns false", func(t *testing.T) {
		_, ok := perr.ParseCode("encoding_invalid")
		if ok {
			t.Error("ParseCode should return false for lowercase code")
		}
	})
}

// TestDomainCodes verifies codes are grouped by domain correctly.
func TestDomainCodes(t *testing.T) {
	domains := map[string][]perr.Code{
		"ENCODING_": {
			perr.ENCODING_INVALID,
			perr.ENCODING_IO,
			perr.ENCODING_TYPE_MISMATCH,
			perr.ENCODING_INTERNAL,
		},
		"PROCESSING_": {
			perr.PROCESSING_CONFIG,
			perr.PROCESSING_STATE,
			perr.PROCESSING_RUNTIME,
			perr.PROCESSING_GROUP,
			perr.PROCESSING_INTERNAL,
		},
		"SERVICE_": {
			perr.SERVICE_VALIDATION,
			perr.SERVICE_RESOURCE,
			perr.SERVICE_REGISTRY,
			perr.SERVICE_INTERNAL,
		},
		"DATA_": {
			perr.DATA_FILE,
			perr.DATA_PARSE,
			perr.DATA_CONFIG,
			perr.DATA_CALCULATION,
			perr.DATA_INTERNAL,
		},
		"CLI_": {
			perr.CLI_INPUT,
			perr.CLI_OUTPUT,
			perr.CLI_COMMAND,
			perr.CLI_INTERNAL,
		},
		"PULSE_": {
			perr.PULSE_IMPORT_SCHEMA_AMBIGUOUS,
			perr.PULSE_IMPORT_ROW_ERROR,
			perr.PULSE_EXPORT_ROW_ERROR,
			perr.PULSE_IMPORT_CATEGORICAL_OVERFLOW,
			perr.PULSE_IMPORT_CATEGORICAL_UNBOUNDED,
			perr.PULSE_IMPORT_DESCRIPTION_TOO_LONG,
			perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL,
			perr.PULSE_FIELD_DESCRIPTION_LOW_QUALITY,
		},
	}

	for prefix, codes := range domains {
		for _, code := range codes {
			if !strings.HasPrefix(string(code), prefix) {
				t.Errorf("code %q should have prefix %q", code, prefix)
			}
		}
	}
}

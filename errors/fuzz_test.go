package errors_test

import (
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// FuzzParseCode fuzzes the string-to-code parsing function.
func FuzzParseCode(f *testing.F) {
	// Seed with known codes
	for _, code := range perr.AllCodes() {
		f.Add(string(code))
	}
	// Seed with edge cases
	f.Add("")
	f.Add("NOT_REAL")
	f.Add("encoding_invalid")
	f.Add("ENCODING_INVALID\x00")
	f.Add("PULSE_")
	f.Add("FRONTEND_RENDERING")

	f.Fuzz(func(t *testing.T, s string) {
		code, ok := perr.ParseCode(s)
		if ok {
			// If parsing succeeds, the round-trip must hold
			if string(code) != s {
				t.Errorf("ParseCode(%q) = %q, round-trip failed", s, code)
			}
		}
		// If not ok, that's fine — unknown strings are expected to fail
	})
}

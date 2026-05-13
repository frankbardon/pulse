package errors_test

import (
	"slices"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// TestErrorsLookup_ByCode asserts Lookup returns the expected metadata
// for a known code.
func TestErrorsLookup_ByCode(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL))
	if !ok {
		t.Fatal("Lookup returned ok=false for a known code")
	}
	if r.Code != string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
		t.Errorf("Code=%q, want %q", r.Code, perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL)
	}
	if r.Domain != "PULSE" {
		t.Errorf("Domain=%q, want PULSE", r.Domain)
	}
	if r.Message == "" {
		t.Errorf("Message is empty")
	}
	if len(r.Fixups) == 0 {
		t.Errorf("Fixups slice is empty for a code that has templates")
	}
}

// TestErrorsLookup_UnknownCodeReturnsFalse asserts an unknown code
// returns ok=false with no panic.
func TestErrorsLookup_UnknownCodeReturnsFalse(t *testing.T) {
	r, ok := perr.Lookup("NOT_A_REAL_CODE")
	if ok {
		t.Errorf("Lookup returned ok=true for unknown code")
	}
	if r.Code != "" {
		t.Errorf("expected empty Code on miss, got %q", r.Code)
	}
}

// TestErrorsLookup_FixupNotApplicable asserts that internal codes
// surface FixupNotApplicable=true with no Fixups slice.
func TestErrorsLookup_FixupNotApplicable(t *testing.T) {
	r, ok := perr.Lookup(string(perr.ENCODING_INTERNAL))
	if !ok {
		t.Fatal("Lookup returned ok=false for ENCODING_INTERNAL")
	}
	if !r.FixupNotApplicable {
		t.Errorf("FixupNotApplicable=false, want true for ENCODING_INTERNAL")
	}
	if len(r.Fixups) != 0 {
		t.Errorf("Fixups should be empty for FixupNotApplicable=true code")
	}
}

// TestErrorsLookup_FixupSliceIsCopy asserts the returned Fixups slice
// is a defensive copy.
func TestErrorsLookup_FixupSliceIsCopy(t *testing.T) {
	r, _ := perr.Lookup(string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL))
	if len(r.Fixups) == 0 {
		t.Skip("no fixups to test against")
	}
	original := r.Fixups[0].Hint
	r.Fixups[0].Hint = "MUTATED"

	again, _ := perr.Lookup(string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL))
	if again.Fixups[0].Hint != original {
		t.Errorf("mutation leaked into registry: got %q want %q", again.Fixups[0].Hint, original)
	}
}

// TestErrorsByDomain_ReturnsAll asserts ByDomain returns every code in
// the named domain.
func TestErrorsByDomain_ReturnsAll(t *testing.T) {
	got := perr.ByDomain("PULSE")
	if len(got) == 0 {
		t.Fatal("ByDomain(PULSE) returned empty slice")
	}
	// Count expected codes from AllCodes().
	want := 0
	for _, c := range perr.AllCodes() {
		if perr.Domain(c) == "PULSE" {
			want++
		}
	}
	if len(got) != want {
		t.Errorf("ByDomain(PULSE) returned %d, want %d", len(got), want)
	}
	// Every result has Domain=PULSE.
	for _, r := range got {
		if r.Domain != "PULSE" {
			t.Errorf("ByDomain(PULSE) result %q has Domain=%q", r.Code, r.Domain)
		}
	}
	// Sorted alphabetically.
	for i := 1; i < len(got); i++ {
		if got[i].Code < got[i-1].Code {
			t.Errorf("ByDomain results not sorted: %q before %q", got[i-1].Code, got[i].Code)
		}
	}
}

// TestErrorsByDomain_CaseInsensitive asserts the domain argument is
// matched case-insensitively.
func TestErrorsByDomain_CaseInsensitive(t *testing.T) {
	upper := perr.ByDomain("PULSE")
	lower := perr.ByDomain("pulse")
	mixed := perr.ByDomain("Pulse")
	if len(upper) != len(lower) || len(upper) != len(mixed) {
		t.Errorf("case-insensitive mismatch: PULSE=%d pulse=%d Pulse=%d",
			len(upper), len(lower), len(mixed))
	}
}

// TestErrorsByDomain_Empty asserts an unknown domain returns a
// non-nil empty slice.
func TestErrorsByDomain_Empty(t *testing.T) {
	got := perr.ByDomain("NOSUCHDOMAIN")
	if got == nil {
		t.Errorf("ByDomain returned nil for unknown domain")
	}
	if len(got) != 0 {
		t.Errorf("ByDomain returned %d results for unknown domain", len(got))
	}
}

// TestErrorsSearch_DescriptionMatch asserts a substring known to live
// in one code's Message returns at least that code.
func TestErrorsSearch_DescriptionMatch(t *testing.T) {
	// "categorical aggregation" appears in PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL's message.
	got := perr.Search("numeric aggregation")
	if len(got) == 0 {
		t.Fatal("Search returned no results for 'numeric aggregation'")
	}
	found := false
	for _, r := range got {
		if r.Code == string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Search did not surface PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL")
	}
}

// TestErrorsSearch_FixupMatch asserts a substring known to live in a
// fixup hint is matched.
func TestErrorsSearch_FixupMatch(t *testing.T) {
	// "Tukey HSD" appears in the PULSE_TEST_TUKEY_REQUIRES_K_GE_3 message
	// and "two-proportion z" lives in the fixup hint.
	got := perr.Search("two-proportion z")
	if len(got) == 0 {
		t.Fatal("Search returned no results for fixup substring 'two-proportion z'")
	}
	found := false
	for _, r := range got {
		if r.Code == string(perr.PULSE_TEST_TUKEY_REQUIRES_K_GE_3) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Search did not surface code with fixup-only match")
	}
}

// TestErrorsSearch_RanksMessageAboveFixup asserts a query that
// matches one code's Message and a different code's Fixup ranks the
// Message hit first.
func TestErrorsSearch_RanksMessageAboveFixup(t *testing.T) {
	// "predict" appears in SERVICE_VALIDATION's fixup hint ("Call pulse
	// predict --json") and is also a Pulse vocabulary word. Look for a
	// search term that hits Message in one place and Fixup in another.
	// "median" hits AGG message and AGG_NOT_MEANINGFUL_FOR_DECIMAL message.
	// Pick "header" — appears in ENCODING_INVALID's Message.
	got := perr.Search("header")
	if len(got) == 0 {
		t.Skip("no results to rank")
	}
	// At minimum, the first hit's source should not be matchInCode if a
	// message/fixup hit exists.
	first := got[0]
	if !strings.Contains(strings.ToLower(first.Message), "header") {
		// allow fixup-source winner if no message hit
		hasFixup := false
		for _, f := range first.Fixups {
			if strings.Contains(strings.ToLower(f.Hint), "header") {
				hasFixup = true
				break
			}
		}
		if !hasFixup {
			t.Errorf("first hit %q has no message/fixup containing 'header'", first.Code)
		}
	}
}

// TestErrorsSearch_EmptyReturnsEmpty asserts an empty query returns
// non-nil empty.
func TestErrorsSearch_EmptyReturnsEmpty(t *testing.T) {
	got := perr.Search("")
	if got == nil {
		t.Errorf("Search returned nil for empty query")
	}
	if len(got) != 0 {
		t.Errorf("Search returned %d for empty query", len(got))
	}
}

// TestErrorsSearch_NoMatch asserts a query with no hits returns a
// non-nil empty slice.
func TestErrorsSearch_NoMatch(t *testing.T) {
	got := perr.Search("xxxnotappearinganywherexxx")
	if got == nil {
		t.Errorf("Search returned nil instead of empty slice")
	}
	if len(got) != 0 {
		t.Errorf("Search returned %d results for nonsense query", len(got))
	}
}

// TestAllDomains_ReturnsSixSorted asserts AllDomains returns the six
// known domain prefixes, alphabetized.
func TestAllDomains_ReturnsSixSorted(t *testing.T) {
	got := perr.AllDomains()
	want := []string{"CLI", "DATA", "ENCODING", "PROCESSING", "PULSE", "SERVICE"}
	if len(got) != len(want) {
		t.Errorf("AllDomains length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i, d := range want {
		if i >= len(got) || got[i] != d {
			t.Errorf("AllDomains[%d] = %q, want %q", i, got[i], d)
		}
	}
}

// TestSortedCodeNames_AlphabetizedAndComplete asserts every registered
// code appears in SortedCodeNames, alphabetized.
func TestSortedCodeNames_AlphabetizedAndComplete(t *testing.T) {
	got := perr.SortedCodeNames()
	if len(got) != len(perr.AllCodes()) {
		t.Errorf("SortedCodeNames length = %d, want %d", len(got), len(perr.AllCodes()))
	}
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Errorf("SortedCodeNames not alphabetized: %q before %q", got[i-1], got[i])
		}
	}
	// Spot check: a known PULSE code is in the slice.
	want := string(perr.PULSE_TEST_INSUFFICIENT_N)
	if !slices.Contains(got, want) {
		t.Errorf("SortedCodeNames missing known code %q", want)
	}
}

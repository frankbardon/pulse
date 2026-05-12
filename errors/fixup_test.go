package errors_test

import (
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// TestCodesHaveFixups is a non-skippable CI gate: every entry in
// allCodes MUST have a row in the metadata table, and every row must
// either carry at least one Fixup or be explicitly tagged
// FixupNotApplicable. Absence of a fixup IS a bug — the tag is the
// honest "this code has no mechanical repair" signal.
func TestCodesHaveFixups(t *testing.T) {
	for _, code := range perr.AllCodes() {
		m, ok := perr.MetadataFor(code)
		if !ok {
			t.Errorf("code %s has no metadata entry; add a row to codeMetadata", code)
			continue
		}
		if strings.TrimSpace(m.Message) == "" {
			t.Errorf("code %s has empty Message in metadata", code)
		}
		if m.FixupNotApplicable {
			if len(m.Fixups) != 0 {
				t.Errorf("code %s has FixupNotApplicable=true but Fixups is non-empty; pick one", code)
			}
			continue
		}
		if len(m.Fixups) == 0 {
			t.Errorf("code %s has no Fixups and is not tagged FixupNotApplicable; either author a hint or set the flag", code)
			continue
		}
		for i, f := range m.Fixups {
			if strings.TrimSpace(f.Hint) == "" {
				t.Errorf("code %s fixup[%d] has empty Hint", code, i)
			}
			if !validAction(f.Action) {
				t.Errorf("code %s fixup[%d] has unknown Action=%q", code, i, f.Action)
			}
		}
	}
}

// validAction returns true if a is one of the registered FixupAction
// values.
func validAction(a perr.FixupAction) bool {
	switch a {
	case perr.FixupReplaceField,
		perr.FixupReplaceOperator,
		perr.FixupRemoveParam,
		perr.FixupSetDefault,
		perr.FixupRequiresReschema:
		return true
	}
	return false
}

// TestCodeFixupReturnsTemplates asserts Code.Fixup returns the metadata
// templates verbatim for codes that have them, and nil for codes tagged
// FixupNotApplicable.
func TestCodeFixupReturnsTemplates(t *testing.T) {
	// Code with mechanical fixups.
	got := perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL.Fixup(nil)
	if len(got) == 0 {
		t.Errorf("expected fixups for PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL, got none")
	}
	for _, f := range got {
		if f.Action != perr.FixupReplaceOperator && f.Action != perr.FixupReplaceField {
			t.Errorf("unexpected action %q on PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL fixup", f.Action)
		}
	}

	// Internal code is FixupNotApplicable.
	if got := perr.ENCODING_INTERNAL.Fixup(nil); got != nil {
		t.Errorf("expected nil fixups for ENCODING_INTERNAL (FixupNotApplicable), got %d", len(got))
	}

	// Unknown code returns nil.
	if got := perr.Code("NOT_A_REAL_CODE").Fixup(nil); got != nil {
		t.Errorf("expected nil fixups for unknown code, got %d", len(got))
	}
}

// TestCodeFixupIsCopy asserts the returned slice is independent of the
// internal template list — callers must not be able to mutate the
// registry by mutating the returned slice.
func TestCodeFixupIsCopy(t *testing.T) {
	got := perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL.Fixup(nil)
	if len(got) == 0 {
		t.Skip("no fixups to test mutation against")
	}
	original := got[0].Hint
	got[0].Hint = "MUTATED"

	again := perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL.Fixup(nil)
	if again[0].Hint != original {
		t.Errorf("mutation of returned slice leaked into registry: got %q, want %q", again[0].Hint, original)
	}
}

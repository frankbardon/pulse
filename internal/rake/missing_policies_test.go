package rake

import (
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestMissingPolicies_ListerMatchesCompile: every listed policy compiles,
// the default first, the slice fresh; an unlisted spelling is refused
// with the message naming every listed one.
func TestMissingPolicies_ListerMatchesCompile(t *testing.T) {
	t.Parallel()

	got := MissingPolicies()
	if !slices.Equal(got, []string{MissingRefuse, MissingBase}) {
		t.Fatalf("MissingPolicies() = %v", got)
	}
	got[0] = "mutated"
	if MissingPolicies()[0] != MissingRefuse {
		t.Fatal("MissingPolicies shares its backing array")
	}
	for _, m := range MissingPolicies() {
		r := baseReq()
		r.Missing = m
		if _, err := Compile(r, refSchema()); err != nil {
			t.Errorf("Missing %q: %v", m, err)
		}
	}
	r := baseReq()
	r.Missing = "drop"
	_, err := Compile(r, refSchema())
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RAKE_INVALID || ce.Message != `missing must be "refuse" or "base"` {
		t.Fatalf("unlisted policy: %v", err)
	}
}

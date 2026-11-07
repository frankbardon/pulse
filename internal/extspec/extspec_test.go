package extspec

import (
	stderrors "errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

type spec struct {
	K    int
	Tags []string
}

// TestRefusal pins the one Validate-verdict rule predict and the
// runtime share: nil hook / nil spec / accept → nil; a coded error
// (wrapped too) kept as is; an uncoded error and a panic →
// PULSE_EXTENSION_SPEC_REFUSED with the site's details (name only when
// set, panic on a panic).
func TestRefusal(t *testing.T) {
	t.Parallel()

	site := Site{Category: "regression", Type: "REG_ACME_X", Slot: "regressions", Name: "fit"}
	coded := errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, "k is out of range", map[string]any{"k": 3})
	cases := []struct {
		name     string
		validate func(*spec) error
		spec     *spec
		site     Site
		want     *errors.CodedError
	}{
		{name: "nil_hook", validate: nil, spec: &spec{}, site: site},
		{name: "nil_spec", validate: func(*spec) error { return stderrors.New("x") }, spec: nil, site: site},
		{name: "accept", validate: func(*spec) error { return nil }, spec: &spec{}, site: site},
		{name: "coded_kept", validate: func(*spec) error { return coded }, spec: &spec{}, site: site, want: coded},
		{name: "coded_wrapped", validate: func(*spec) error { return fmt.Errorf("outer: %w", coded) }, spec: &spec{}, site: site, want: coded},
		{name: "uncoded", validate: func(s *spec) error { return fmt.Errorf("k must be even, got %d", s.K) }, spec: &spec{K: 3}, site: site,
			want: errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_SPEC_REFUSED, "regression REG_ACME_X refused its spec: k must be even, got 3",
				map[string]any{"category": "regression", "type": "REG_ACME_X", "slot": "regressions", "name": "fit"})},
		{name: "uncoded_unnamed", validate: func(*spec) error { return stderrors.New("no") }, spec: &spec{},
			site: Site{Category: "regression", Type: "REG_ACME_X", Slot: "regressions"},
			want: errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_SPEC_REFUSED, "regression REG_ACME_X refused its spec: no",
				map[string]any{"category": "regression", "type": "REG_ACME_X", "slot": "regressions"})},
		{name: "panic", validate: func(*spec) error { panic("boom") }, spec: &spec{}, site: site,
			want: errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_SPEC_REFUSED, "regression REG_ACME_X Validate panicked: boom",
				map[string]any{"category": "regression", "type": "REG_ACME_X", "slot": "regressions", "name": "fit", "panic": "boom"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Refusal(tc.validate, tc.spec, tc.site)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("Refusal = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("Refusal = nil, want %v", tc.want)
			}
			if got.Code != tc.want.Code || got.Message != tc.want.Message || !reflect.DeepEqual(got.Details, tc.want.Details) {
				t.Errorf("Refusal = %s %q %v, want %s %q %v", got.Code, got.Message, got.Details, tc.want.Code, tc.want.Message, tc.want.Details)
			}
		})
	}
}

// TestRefusal_SeesACopy: the hook judges a shallow copy, so a hook
// writing a field cannot change the spec the caller goes on to run.
func TestRefusal_SeesACopy(t *testing.T) {
	t.Parallel()

	s := &spec{K: 1}
	var seen *spec
	if got := Refusal(func(c *spec) error { seen = c; c.K = 42; return nil }, s, Site{}); got != nil {
		t.Fatalf("Refusal = %v", got)
	}
	if s.K != 1 {
		t.Errorf("caller's spec.K = %d after the hook wrote its copy, want 1", s.K)
	}
	if seen == s {
		t.Error("hook received the caller's pointer, want a copy")
	}
}

package pulse_test

import (
	"errors"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
)

// TestExtensions_NilFactoryRefusedAtValidation pins the uniform
// missing_factory refusal: every operator category whose registration
// carries a single Factory refuses a nil one in validateExtensions —
// PULSE_EXTENSION_PARAM_INVALID, details {category, name, index,
// reason: "missing_factory"}, the matrix shape — instead of letting it
// reach the probe as a recovered PULSE_EXTENSION_FACTORY_PANIC.
func TestExtensions_NilFactoryRefusedAtValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		category string
		name     string
		ext      pulse.Extensions
	}{
		{"aggregator", "AGG_ACME_NIL", pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{{Name: "AGG_ACME_NIL"}}}},
		{"attribute", "ATTR_ACME_NIL", pulse.Extensions{Attributes: []pulse.AttributeRegistration{{Name: "ATTR_ACME_NIL"}}}},
		{"filterer", "FILTER_ACME_NIL", pulse.Extensions{Filterers: []pulse.FiltererRegistration{{Name: "FILTER_ACME_NIL"}}}},
		{"grouper", "GROUP_ACME_NIL", pulse.Extensions{Groupers: []pulse.GrouperRegistration{{Name: "GROUP_ACME_NIL"}}}},
		{"window", "WIN_ACME_NIL", pulse.Extensions{Windows: []pulse.WindowRegistration{{Name: "WIN_ACME_NIL"}}}},
		{"feature", "FEAT_ACME_NIL", pulse.Extensions{Features: []pulse.FeatureRegistration{{Name: "FEAT_ACME_NIL"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.category, func(t *testing.T) {
			_, err := pulse.New(newTestOptions(tc.ext))
			assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
			var ce *perr.CodedError
			if !errors.As(err, &ce) {
				t.Fatalf("not a CodedError: %v", err)
			}
			want := map[string]any{"category": tc.category, "name": tc.name, "index": 0, "reason": "missing_factory"}
			for k, v := range want {
				if ce.Details[k] != v {
					t.Fatalf("details.%s = %v, want %v (details %v)", k, ce.Details[k], v, ce.Details)
				}
			}
		})
	}
}

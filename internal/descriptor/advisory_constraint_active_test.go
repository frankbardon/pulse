package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/internal/regbounds"
	"github.com/frankbardon/pulse/types"
)

// TestConstraintActiveAdvisoryPassesProseLint: the result-only
// PULSE_ADVISORY_CONSTRAINT_ACTIVE message (internal/regbounds, attached
// by the service, never by a predict rule) passes the guidance prose
// lint the predict advisories pass, for one and for several held
// predictors.
func TestConstraintActiveAdvisoryPassesProseLint(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"tv", "search"},
		Bounds: &types.RegressionBounds{Nonnegative: []string{"tv", "search"}}}
	for _, active := range [][]string{{"tv"}, {"tv", "search"}} {
		msg, _ := regbounds.Advisory(spec, "regressions[0]", active)
		if hits := LintGuidanceText(msg); len(hits) > 0 {
			t.Errorf("message %q: lint hits %+v", msg, hits)
		}
	}
}

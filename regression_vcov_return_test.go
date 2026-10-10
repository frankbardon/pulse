package pulse_test

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestReturn_RegressionVcov: the opt-in regression covariance rides the
// response-shaping surface like any other slot — present under the
// default and the standard preset (the MCP default, so an opted-in
// vcov is not silently dropped there), absent under minimal, and an
// exclude of regressions[*].vcov drops exactly that matrix (absent on
// the wire, never null) while its correlation stays.
func TestReturn_RegressionVcov(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	mk := func(ret *types.Return) *types.Request {
		return &types.Request{
			Cohort:      &types.Cohort{Filename: cohort},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Vcov: true}},
			Return:      ret,
		}
	}
	for name, c := range map[string]struct {
		ret        *types.Return
		vcov, corr bool
	}{
		"default":      {nil, true, true},
		"standard":     {&types.Return{Preset: types.ReturnPresetStandard}, true, true},
		"minimal":      {&types.Return{Preset: types.ReturnPresetMinimal}, false, false},
		"exclude vcov": {&types.Return{Exclude: []string{"regressions[*].vcov"}}, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			resp, b := processJSON(t, p, mk(c.ret))
			s := string(b)
			if got := strings.Contains(s, `"vcov"`); got != c.vcov {
				t.Errorf("wire vcov present = %v, want %v: %s", got, c.vcov, s)
			}
			if got := strings.Contains(s, `"correlation"`); got != c.corr {
				t.Errorf("wire correlation present = %v, want %v: %s", got, c.corr, s)
			}
			if got := resp.Regressions[0].Vcov != nil; got != c.vcov {
				t.Errorf("Go Vcov present = %v, want %v", got, c.vcov)
			}
			if strings.Contains(s, `"vcov":null`) {
				t.Errorf("excluded vcov written as null: %s", s)
			}
		})
	}
}

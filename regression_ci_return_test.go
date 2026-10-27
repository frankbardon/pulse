package pulse_test

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestReturn_RegressionIntervals: ci_lower / ci_upper / df / conf_level
// ride Request.Return like the other regression outputs — in the
// default and standard presets, out of minimal, each removable by an
// exclude path and selectable by an include one; an excluded key is
// ABSENT on the wire (never null) and absent from the Go result.
func TestReturn_RegressionIntervals(t *testing.T) {
	t.Parallel()

	p, _, cohort := acceptanceCohort(t)
	all := []string{"ci_lower", "ci_upper", "df", "conf_level"}
	for name, c := range map[string]struct {
		ret  *types.Return
		want []string
	}{
		"default":          {nil, all},
		"standard":         {&types.Return{Preset: types.ReturnPresetStandard}, all},
		"minimal":          {&types.Return{Preset: types.ReturnPresetMinimal}, nil},
		"exclude ci_lower": {&types.Return{Exclude: []string{"regressions[*].ci_lower"}}, []string{"ci_upper", "df", "conf_level"}},
		"exclude df":       {&types.Return{Exclude: []string{"regressions[*].df", "regressions[*].conf_level"}}, []string{"ci_lower", "ci_upper"}},
		"minimal + include": {&types.Return{Preset: types.ReturnPresetMinimal,
			Include: []string{"regressions[*].ci_lower", "regressions[*].ci_upper"}}, []string{"ci_lower", "ci_upper"}},
	} {
		t.Run(name, func(t *testing.T) {
			resp, b := processJSON(t, p, &types.Request{
				Cohort:      &types.Cohort{Filename: cohort},
				Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}},
				Return:      c.ret,
			})
			s := string(b)
			r := resp.Regressions[0]
			goPresent := map[string]bool{
				"ci_lower": r.CILower != nil, "ci_upper": r.CIUpper != nil,
				"df": r.DF != 0, "conf_level": r.ConfLevel != 0,
			}
			for _, k := range all {
				want := false
				for _, w := range c.want {
					want = want || w == k
				}
				if got := strings.Contains(s, `"`+k+`":`); got != want {
					t.Errorf("wire %s present = %v, want %v: %s", k, got, want, s)
				}
				if goPresent[k] != want {
					t.Errorf("Go %s present = %v, want %v", k, goPresent[k], want)
				}
				if strings.Contains(s, `"`+k+`":null`) {
					t.Errorf("%s written as null: %s", k, s)
				}
			}
		})
	}
}

package mcpserve_test

import (
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcpserve"
	"github.com/spf13/afero"
)

// TestDescribe_FoldsTheFeatureProfile asserts Describe reports the
// EFFECTIVE cohort-scan setting — Options OR the profile's
// behaviour.disable_cohort_scan — and names the loaded profile.
func TestDescribe_FoldsTheFeatureProfile(t *testing.T) {
	build := func(fp *pulse.FeatureProfile) *pulse.Pulse {
		t.Helper()
		p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: fp})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return p
	}
	scanOff := &pulse.FeatureProfileBehaviour{DisableCohortScan: true}

	cases := []struct {
		name string
		p    *pulse.Pulse
		opts mcpserve.Options
		want mcpserve.ServeInfo
	}{
		{"no profile", build(nil), mcpserve.Options{},
			mcpserve.ServeInfo{CohortScan: true}},
		{"no profile, option off", build(nil), mcpserve.Options{DisableCohortScan: true},
			mcpserve.ServeInfo{CohortScan: false}},
		{"unnamed profile with cohort_resources leaves scan on", build(&pulse.FeatureProfile{Features: []string{"mcp_extra:cohort_resources"}}), mcpserve.Options{},
			mcpserve.ServeInfo{CohortScan: true, FeatureProfileLoaded: true}},
		{"profile omitting cohort_resources turns scan off", build(&pulse.FeatureProfile{Features: []string{}}), mcpserve.Options{},
			mcpserve.ServeInfo{CohortScan: false, FeatureProfileLoaded: true}},
		{"profile turns scan off", build(&pulse.FeatureProfile{Profile: "self-serve", Features: []string{"mcp_extra:cohort_resources"}, Behaviour: scanOff}), mcpserve.Options{},
			mcpserve.ServeInfo{CohortScan: false, FeatureProfileLoaded: true, FeatureProfile: "self-serve"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpserve.Describe(tc.p, tc.opts); got != tc.want {
				t.Errorf("Describe = %+v, want %+v", got, tc.want)
			}
		})
	}
}

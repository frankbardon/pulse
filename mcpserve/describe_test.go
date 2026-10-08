package mcpserve_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcpserve"
	"github.com/frankbardon/pulse/types"
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
			got := mcpserve.Describe(tc.p, tc.opts)
			if got.CohortScan != tc.want.CohortScan || got.FeatureProfileLoaded != tc.want.FeatureProfileLoaded ||
				got.FeatureProfile != tc.want.FeatureProfile {
				t.Errorf("Describe = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDescribe_DefaultReturnPrecedence pins ServeInfo.DefaultReturn to
// the MCP return-default order: the serving option, then the instance
// default (pulse.Options.DefaultReturn over the feature profile's
// `return`), then the built-in standard preset.
func TestDescribe_DefaultReturnPrecedence(t *testing.T) {
	build := func(o pulse.Options) *pulse.Pulse {
		t.Helper()
		o.FS = afero.NewMemMapFs()
		p, err := pulse.New(o)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return p
	}
	minimalProfile := &pulse.FeatureProfile{
		Features: []string{"mcp_extra:cohort_resources"},
		Return:   &types.Return{Preset: types.ReturnPresetMinimal},
	}
	cases := []struct {
		name string
		p    *pulse.Pulse
		opt  types.ReturnPreset
		want types.ReturnPreset
	}{
		{"built-in", build(pulse.Options{}), "", types.ReturnPresetStandard},
		{"feature profile", build(pulse.Options{FeatureProfile: minimalProfile}), "", types.ReturnPresetMinimal},
		{"instance option beats profile",
			build(pulse.Options{FeatureProfile: minimalProfile, DefaultReturn: &types.Return{Preset: types.ReturnPresetFull}}),
			"", types.ReturnPresetFull},
		{"serving option beats instance",
			build(pulse.Options{FeatureProfile: minimalProfile, DefaultReturn: &types.Return{Preset: types.ReturnPresetFull}}),
			types.ReturnPresetStandard, types.ReturnPresetStandard},
		{"serving option, no instance default", build(pulse.Options{}), types.ReturnPresetFull, types.ReturnPresetFull},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpserve.Describe(tc.p, mcpserve.Options{DefaultReturn: tc.opt}).DefaultReturn; got != tc.want {
				t.Errorf("DefaultReturn = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDescribe_LimitsMatchManifest: ServeInfo.Limits is the manifest's
// `limits` block for the same instance — the effective values, tuned
// keys included, in the same order and shape.
func TestDescribe_LimitsMatchManifest(t *testing.T) {
	for _, lim := range []pulse.Limits{{}, {MaxGroups: 7, RequestTimeout: 3 * time.Second, MaxMatrixDim: pulse.Unlimited}} {
		p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Limits: lim})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		got := mcpserve.Describe(p, mcpserve.Options{}).Limits
		want := p.Manifest(t.Context()).Limits
		if len(got) == 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("Limits(%+v)\n got  %+v\n want %+v", lim, got, want)
		}
	}
}

package pulse

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestFeatureProfile_ReturnRoundTrips: a `return` section decodes
// through ParseFeatureProfile, is stored by New and copied out by
// FeatureProfile() without aliasing the caller's slices.
func TestFeatureProfile_ReturnRoundTrips(t *testing.T) {
	fp, err := ParseFeatureProfile([]byte(`{"features": ["capability:process", "AGG_COUNT"],
		"return": {"preset": "standard", "include": ["components"], "exclude": ["metadata"], "precision": 6}}`))
	if err != nil {
		t.Fatalf("ParseFeatureProfile: %v", err)
	}
	want := &types.Return{Preset: types.ReturnPresetStandard, Include: []string{"components"}, Exclude: []string{"metadata"}, Precision: 6}
	if !reflect.DeepEqual(fp.Return, want) {
		t.Fatalf("decoded return = %+v; want %+v", fp.Return, want)
	}
	p, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: fp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fp.Return.Include[0] = "mutated"
	got, ok := p.FeatureProfile()
	if !ok || !reflect.DeepEqual(got.Return, want) {
		t.Fatalf("stored return = %+v; want %+v (caller mutation must not leak)", got.Return, want)
	}
	if d := p.svc.InstanceSnapshot().DefaultReturn(); !reflect.DeepEqual(d, want) {
		t.Errorf("instance default = %+v; want the profile's %+v", d, want)
	}
}

// TestFeatureProfile_ReturnRefused: a profile `return` the profile's own
// instance cannot resolve — a path a hidden feature owns, a bad preset —
// is PULSE_FEATURE_PROFILE_INVALID reason invalid_return carrying the
// resolver's code, from New and CheckFeatureProfile alike.
func TestFeatureProfile_ReturnRefused(t *testing.T) {
	cases := []struct {
		name     string
		ret      *types.Return
		features []string
		wantCode errors.Code
	}{
		{"hidden matrices", &types.Return{Include: []string{"matrices"}}, []string{"capability:process"}, errors.PULSE_RETURN_PATH_UNKNOWN},
		{"bad preset", &types.Return{Preset: "tiny"}, []string{"capability:process"}, errors.PULSE_RETURN_INVALID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fp := &FeatureProfile{Features: tc.features, Return: tc.ret}
			_, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: fp})
			ce := requireProfileInvalid(t, err, featureProfileReasonInvalidReturn)
			if got := ce.Details["return_code"]; got != string(tc.wantCode) {
				t.Errorf("return_code = %v; want %s", got, tc.wantCode)
			}
			_, cerr := CheckFeatureProfile(fp, FeatureProfileCheckOptions{})
			if cerr == nil || cerr.Error() != err.Error() {
				t.Errorf("CheckFeatureProfile = %v; want New's %v", cerr, err)
			}
		})
	}
	// Not vacuous: the same path resolves once the feature is enabled.
	fp := &FeatureProfile{Features: []string{"capability:process", "capability:matrices"}, Return: &types.Return{Include: []string{"matrices"}}}
	if _, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: fp}); err != nil {
		t.Fatalf("enabled matrices: %v", err)
	}
}

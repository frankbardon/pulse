package pulse

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestFeatureProfile_LimitsResolve: a profile `limits` key feeds
// Pulse.Limits(); a non-zero Options.Limits value overrides it per
// field; an omitted or 0 key resolves to the built-in default; -1 and
// "unlimited" mean no limit.
func TestFeatureProfile_LimitsResolve(t *testing.T) {
	body := `{"features": [], "limits": {"max_groups": 5, "max_crosstab_cells": 0,
		"max_matrix_dim": -1, "request_timeout": "30s"}}`

	p, err := New(Options{FS: memFsWith(t, map[string]string{"p.json": body}), FeatureProfileFile: "p.json"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := p.Limits()
	want := Limits{
		RequestTimeout:     30 * time.Second,
		MaxGroups:          5,
		MaxCrosstabCells:   DefaultMaxCrosstabCells,
		MaxEstimatedMemory: DefaultMaxEstimatedMemory,
		MaxMatrixDim:       Unlimited,
		MaxComposeSlots:    DefaultMaxComposeSlots,
		MaxChainStages:     DefaultMaxChainStages,
		MaxJoinBuildRows:   DefaultMaxJoinBuildRows,
	}
	if got != want {
		t.Fatalf("Limits() = %+v; want %+v", got, want)
	}

	p, err = New(Options{
		FS:                 memFsWith(t, map[string]string{"p.json": body}),
		FeatureProfileFile: "p.json",
		Limits:             Limits{MaxGroups: 7},
	})
	if err != nil {
		t.Fatalf("New with Options.Limits: %v", err)
	}
	if got := p.Limits().MaxGroups; got != 7 {
		t.Errorf("MaxGroups = %d; want Options' 7 over the profile's 5", got)
	}
	if got := p.Limits().RequestTimeout; got != 30*time.Second {
		t.Errorf("RequestTimeout = %v; want the profile's 30s (Options left it 0)", got)
	}
}

// TestFeatureProfile_LimitsRequestTimeoutForms pins the accepted
// request_timeout spellings.
func TestFeatureProfile_LimitsRequestTimeoutForms(t *testing.T) {
	cases := map[string]time.Duration{
		"":          DefaultRequestTimeout,
		"0":         DefaultRequestTimeout,
		"-1":        Unlimited,
		"unlimited": Unlimited,
		"1m30s":     90 * time.Second,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			fp := &FeatureProfile{Features: []string{}, Limits: &FeatureProfileLimits{RequestTimeout: in}}
			p, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: fp})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := p.Limits().RequestTimeout; got != want {
				t.Errorf("RequestTimeout = %v; want %v", got, want)
			}
		})
	}
}

// TestFeatureProfile_LimitsRefused: a value below -1 or an unparseable
// request_timeout is PULSE_FEATURE_PROFILE_INVALID reason invalid_limits
// naming the key, from New and CheckFeatureProfile alike.
func TestFeatureProfile_LimitsRefused(t *testing.T) {
	cases := []struct {
		name      string
		limits    FeatureProfileLimits
		wantLimit string
		wantValue any
	}{
		{"max_groups -2", FeatureProfileLimits{MaxGroups: -2}, "max_groups", int64(-2)},
		{"max_join_build_rows -9", FeatureProfileLimits{MaxGroups: 3, MaxJoinBuildRows: -9}, "max_join_build_rows", int64(-9)},
		{"request_timeout unparseable", FeatureProfileLimits{RequestTimeout: "soon"}, "request_timeout", "soon"},
		{"request_timeout negative", FeatureProfileLimits{RequestTimeout: "-2s"}, "request_timeout", "-2s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := tc.limits
			fp := &FeatureProfile{Features: []string{}, Limits: &l}
			_, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: fp})
			ce := requireProfileInvalid(t, err, featureProfileReasonInvalidLimits)
			if got := ce.Details["limit"]; got != tc.wantLimit {
				t.Errorf("details.limit = %v; want %s", got, tc.wantLimit)
			}
			if got := ce.Details["value"]; got != tc.wantValue {
				t.Errorf("details.value = %#v; want %#v", got, tc.wantValue)
			}
			report, cerr := CheckFeatureProfile(fp, FeatureProfileCheckOptions{})
			if cerr == nil || cerr.Error() != err.Error() {
				t.Errorf("CheckFeatureProfile = %v; want New's %v", cerr, err)
			}
			if report.Valid {
				t.Error("CheckFeatureProfile report is Valid for a refused profile")
			}
		})
	}
	// The file arm names the path.
	_, err := New(Options{
		FS:                 memFsWith(t, map[string]string{"p.json": `{"features": [], "limits": {"max_groups": -2}}`}),
		FeatureProfileFile: "p.json",
	})
	if ce := requireProfileInvalid(t, err, featureProfileReasonInvalidLimits); ce.Details["path"] != "p.json" {
		t.Errorf("details.path = %v; want p.json", ce.Details["path"])
	}
}

// TestFeatureProfile_LimitsUnknownKeyRefused: the section decodes
// strictly — an unknown key inside it is unknown_key, from the file arm
// and ParseFeatureProfile alike.
func TestFeatureProfile_LimitsUnknownKeyRefused(t *testing.T) {
	body := `{"features": [], "limits": {"max_groups": 5, "max_rows": 1}}`
	_, err := ParseFeatureProfile([]byte(body))
	requireProfileInvalid(t, err, featureProfileReasonUnknownKey)
	_, err = New(Options{FS: memFsWith(t, map[string]string{"p.json": body}), FeatureProfileFile: "p.json"})
	requireProfileInvalid(t, err, featureProfileReasonUnknownKey)
}

// TestFeatureProfile_LimitsRoundTrip: the section decodes through
// ParseFeatureProfile, marshals back to the same snake_case keys, is
// stored by New and copied out by FeatureProfile() without aliasing the
// caller's value.
func TestFeatureProfile_LimitsRoundTrip(t *testing.T) {
	body := `{"features": [], "limits": {"request_timeout": "45s", "max_groups": 5, "max_join_build_rows": -1}}`
	fp, err := ParseFeatureProfile([]byte(body))
	if err != nil {
		t.Fatalf("ParseFeatureProfile: %v", err)
	}
	want := &FeatureProfileLimits{RequestTimeout: "45s", MaxGroups: 5, MaxJoinBuildRows: -1}
	if !reflect.DeepEqual(fp.Limits, want) {
		t.Fatalf("decoded limits = %+v; want %+v", fp.Limits, want)
	}
	raw, err := json.Marshal(fp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	again, err := ParseFeatureProfile(raw)
	if err != nil {
		t.Fatalf("re-parse %s: %v", raw, err)
	}
	if !reflect.DeepEqual(again.Limits, want) {
		t.Fatalf("round-tripped limits = %+v; want %+v (json %s)", again.Limits, want, raw)
	}

	p, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: fp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fp.Limits.MaxGroups = 999
	got, ok := p.FeatureProfile()
	if !ok || !reflect.DeepEqual(got.Limits, want) {
		t.Fatalf("stored limits = %+v; want %+v (caller mutation must not leak)", got.Limits, want)
	}
	got.Limits.MaxGroups = 1234
	if again, _ := p.FeatureProfile(); again.Limits.MaxGroups != 5 {
		t.Errorf("FeatureProfile() copy aliases the stored limits")
	}
	if got := p.Limits().MaxGroups; got != 5 {
		t.Errorf("Limits().MaxGroups = %d; want 5 (caller mutation after New must not apply)", got)
	}
}

// TestFeatureProfile_LimitsNotInDigest: limits are behaviour, not
// features — the feature-set digest ignores them.
func TestFeatureProfile_LimitsNotInDigest(t *testing.T) {
	plain, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: &FeatureProfile{Features: []string{}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	limited, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: &FeatureProfile{
		Features: []string{},
		Limits:   &FeatureProfileLimits{MaxGroups: 5, RequestTimeout: "1s"},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if plain.Limits() == limited.Limits() {
		t.Fatal("profile limits had no effect; the digest comparison would be vacuous")
	}
	if a, b := plain.FeatureSetDigest(), limited.FeatureSetDigest(); a != b {
		t.Errorf("feature_set_digest moved with limits: %s != %s", a, b)
	}
}

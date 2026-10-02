package pulse

import (
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// memFsWith returns a fresh hermetic filesystem holding files.
func memFsWith(t *testing.T, files map[string]string) afero.Fs {
	t.Helper()
	mfs := fs.NewMemMap().Fs()
	for path, body := range files {
		if err := afero.WriteFile(mfs, path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return mfs
}

func requireProfileInvalid(t *testing.T, err error, wantReason string) *errors.CodedError {
	t.Helper()
	if err == nil {
		t.Fatalf("New succeeded; want PULSE_FEATURE_PROFILE_INVALID (%s)", wantReason)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v is not a *errors.CodedError", err)
	}
	if ce.Code != errors.PULSE_FEATURE_PROFILE_INVALID {
		t.Fatalf("code = %s; want PULSE_FEATURE_PROFILE_INVALID (err: %v)", ce.Code, err)
	}
	if got := ce.Details["reason"]; got != wantReason {
		t.Fatalf("reason = %v; want %s (err: %v)", got, wantReason, err)
	}
	return ce
}

// TestFeatureProfile_InvalidFile covers every structural fault reachable
// through Options.FeatureProfileFile.
func TestFeatureProfile_InvalidFile(t *testing.T) {
	cases := []struct {
		name   string
		body   *string // nil: file absent
		reason string
	}{
		{"missing file", nil, featureProfileReasonFileUnreadable},
		{"malformed json", profileBody(`{"features": [`), featureProfileReasonMalformedJSON},
		{"not an object", profileBody(`["AGG_SUM"]`), featureProfileReasonMalformedJSON},
		{"trailing data", profileBody(`{"features": []} {}`), featureProfileReasonMalformedJSON},
		{"wrong features type", profileBody(`{"features": "AGG_SUM"}`), featureProfileReasonMalformedJSON},
		{"unknown top-level key", profileBody(`{"features": [], "extra": 1}`), featureProfileReasonUnknownKey},
		{"reserved limits key", profileBody(`{"features": [], "limits": {}}`), featureProfileReasonUnknownKey},
		{"reserved return key", profileBody(`{"features": [], "return": {}}`), featureProfileReasonUnknownKey},
		{"unknown behaviour key", profileBody(`{"features": [], "behaviour": {"disable_everything": true}}`), featureProfileReasonUnknownKey},
		{"missing features", profileBody(`{"profile": "x"}`), featureProfileReasonMissingFeatures},
		{"null features", profileBody(`{"features": null}`), featureProfileReasonMissingFeatures},
		{"null document", profileBody(`null`), featureProfileReasonMissingFeatures},
		{"duplicate feature", profileBody(`{"features": ["AGG_SUM", "AGG_MEAN", "AGG_SUM"]}`), featureProfileReasonDuplicate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			if tc.body != nil {
				files["profiles/p.json"] = *tc.body
			}
			_, err := New(Options{FS: memFsWith(t, files), FeatureProfileFile: "profiles/p.json"})
			ce := requireProfileInvalid(t, err, tc.reason)
			if got := ce.Details["path"]; got != "profiles/p.json" {
				t.Errorf("details.path = %v; want profiles/p.json", got)
			}
		})
	}
}

// TestFeatureProfile_InvalidValue covers the structural faults reachable
// through the Go-value form and the both-set conflict.
func TestFeatureProfile_InvalidValue(t *testing.T) {
	cases := []struct {
		name   string
		opts   Options
		reason string
	}{
		{"nil features", Options{FeatureProfile: &FeatureProfile{Profile: "x"}}, featureProfileReasonMissingFeatures},
		{"duplicate feature", Options{FeatureProfile: &FeatureProfile{Features: []string{"B", "A", "B", "A", "C"}}}, featureProfileReasonDuplicate},
		{"both options set", Options{
			FeatureProfile:     &FeatureProfile{Features: []string{}},
			FeatureProfileFile: "profiles/p.json",
		}, featureProfileReasonBothSet},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.FS = memFsWith(t, map[string]string{"profiles/p.json": `{"features": []}`})
			_, err := New(tc.opts)
			requireProfileInvalid(t, err, tc.reason)
		})
	}
}

// TestFeatureProfile_DuplicatesListsEveryName asserts the first failing
// class reports every instance, sorted, not just the first repeat.
func TestFeatureProfile_DuplicatesListsEveryName(t *testing.T) {
	_, err := New(Options{
		FS:             memFsWith(t, nil),
		FeatureProfile: &FeatureProfile{Features: []string{"B", "A", "B", "A", "C"}},
	})
	ce := requireProfileInvalid(t, err, featureProfileReasonDuplicate)
	if got, want := ce.Details["duplicates"], []string{"A", "B"}; !reflect.DeepEqual(got, want) {
		t.Errorf("details.duplicates = %v; want %v", got, want)
	}
}

// TestFeatureProfile_ValidFileStored asserts a well-formed file is read
// through the instance Fs (it exists only in the MemMap) and stored.
func TestFeatureProfile_ValidFileStored(t *testing.T) {
	body := `{"profile": "self-serve", "written_with": "not-a-version", "features": ["AGG_SUM", "capability:process"],
		"behaviour": {"disable_cohort_scan": true}}`
	p, err := New(Options{
		FS:                 memFsWith(t, map[string]string{"profiles/p.json": body}),
		FeatureProfileFile: "profiles/p.json",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := &FeatureProfile{
		Profile:     "self-serve",
		WrittenWith: "not-a-version",
		Features:    []string{"AGG_SUM", "capability:process"},
		Behaviour:   &FeatureProfileBehaviour{DisableCohortScan: true},
	}
	if !reflect.DeepEqual(p.featureProfile, want) {
		t.Errorf("stored profile = %+v; want %+v", p.featureProfile, want)
	}
}

// TestFeatureProfile_EmptyFeaturesValid: an empty array is allowed in
// both forms.
func TestFeatureProfile_EmptyFeaturesValid(t *testing.T) {
	if _, err := New(Options{FS: memFsWith(t, map[string]string{"p.json": `{"features": []}`}), FeatureProfileFile: "p.json"}); err != nil {
		t.Errorf("file with empty features: %v", err)
	}
	if _, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: &FeatureProfile{Features: []string{}}}); err != nil {
		t.Errorf("value with empty features: %v", err)
	}
}

// TestFeatureProfile_ValueIsCopied asserts the instance keeps its own
// copy: mutating the caller's profile after New changes nothing.
func TestFeatureProfile_ValueIsCopied(t *testing.T) {
	in := &FeatureProfile{Features: []string{"AGG_SUM", "capability:process"}, Behaviour: &FeatureProfileBehaviour{}}
	p, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: in})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	in.Features[0] = "AGG_MEAN"
	in.Behaviour.DisableDefaults = true
	if p.featureProfile.Features[0] != "AGG_SUM" || p.featureProfile.Behaviour.DisableDefaults {
		t.Errorf("stored profile aliases the caller's value: %+v", p.featureProfile)
	}
}

// TestFeatureProfile_BehaviourOR asserts a profile switch turns the
// engine switch on and Options cannot force it back off.
func TestFeatureProfile_BehaviourOR(t *testing.T) {
	on := &FeatureProfileBehaviour{DisableDefaults: true, DisableComponents: true, DisableProjection: true}
	cases := []struct {
		name          string
		opts          Options
		behaviour     *FeatureProfileBehaviour
		wantDefaults  bool
		wantComps     bool
		wantProjected bool
	}{
		{"no behaviour defers to options", Options{}, nil, false, false, true},
		{"profile turns every switch on", Options{}, on, true, true, false},
		{"options on, profile off stays on", Options{DisableDefaults: true, DisableComponents: true, DisableProjection: true},
			&FeatureProfileBehaviour{}, true, true, false},
		{"deprecated ProjectBufferedFields cannot re-enable projection", Options{ProjectBufferedFields: true},
			&FeatureProfileBehaviour{DisableProjection: true}, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.FS = memFsWith(t, nil)
			tc.opts.FeatureProfile = &FeatureProfile{Features: []string{}, Behaviour: tc.behaviour}
			p, err := New(tc.opts)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := p.svc.DefaultsDisabled(); got != tc.wantDefaults {
				t.Errorf("DefaultsDisabled = %v; want %v", got, tc.wantDefaults)
			}
			if got := p.svc.DisableComponents(); got != tc.wantComps {
				t.Errorf("DisableComponents = %v; want %v", got, tc.wantComps)
			}
			if got := p.svc.ProjectBufferedFields(); got != tc.wantProjected {
				t.Errorf("ProjectBufferedFields = %v; want %v", got, tc.wantProjected)
			}
		})
	}
}

// TestFeatureProfile_NoProfileUnchanged asserts the zero Options store
// no profile and leave every switch at its default.
func TestFeatureProfile_NoProfileUnchanged(t *testing.T) {
	p, err := New(Options{FS: memFsWith(t, nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.featureProfile != nil {
		t.Errorf("featureProfile = %+v; want nil", p.featureProfile)
	}
	if p.svc.DefaultsDisabled() || p.svc.DisableComponents() || !p.svc.ProjectBufferedFields() {
		t.Errorf("switches moved without a profile")
	}
}

// TestFeatureProfile_NotReadFromEnv asserts pulse.New never consults the
// environment for a profile: a bad profile named there is ignored.
func TestFeatureProfile_NotReadFromEnv(t *testing.T) {
	mfs := memFsWith(t, map[string]string{"bad.json": `{`})
	t.Setenv("PULSE_FEATURE_PROFILE", "bad.json")
	t.Setenv("PULSE_FEATURE_PROFILE_FILE", "bad.json")
	p, err := New(Options{FS: mfs})
	if err != nil {
		t.Fatalf("New read a profile from the environment: %v", err)
	}
	if p.featureProfile != nil {
		t.Errorf("featureProfile = %+v; want nil", p.featureProfile)
	}
}

// TestFeatureProfile_ValidatedAfterExtensions asserts profile
// validation runs after extension validation: a bad extension wins.
func TestFeatureProfile_ValidatedAfterExtensions(t *testing.T) {
	_, err := New(Options{
		FS:                 memFsWith(t, nil),
		FeatureProfileFile: "absent.json",
		Extensions: Extensions{Aggregators: []AggregatorRegistration{
			{Name: "not-a-valid-name"},
		}},
	})
	if err == nil {
		t.Fatal("New succeeded; want an extension error")
	}
	var ce *errors.CodedError
	if stderrors.As(err, &ce) && ce.Code == errors.PULSE_FEATURE_PROFILE_INVALID {
		t.Fatalf("profile validated before extensions: %v", err)
	}
}

func profileBody(s string) *string { return &s }

// TestParseFeatureProfile_SharesFileArmRefusals asserts the public
// decoder and the Options.FeatureProfileFile arm refuse the same bodies
// with the same reason — they are one decode, so an OS-path entry point
// (pulse mcp --feature-profile) cannot accept what the file arm refuses.
func TestParseFeatureProfile_SharesFileArmRefusals(t *testing.T) {
	bodies := []string{
		`{"features": [`,
		`["AGG_SUM"]`,
		`{"features": []} {}`,
		`{"features": "AGG_SUM"}`,
		`{"features": [], "extra": 1}`,
		`{"features": [], "limits": {}}`,
		`{"features": [], "behaviour": {"disable_everything": true}}`,
		`{"profile": "x"}`,
		`{"features": null}`,
		`null`,
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			_, fileErr := New(Options{FS: memFsWith(t, map[string]string{"p.json": body}), FeatureProfileFile: "p.json"})
			if fileErr == nil {
				t.Fatal("file arm accepted the body")
			}
			var fileCE *errors.CodedError
			if !stderrors.As(fileErr, &fileCE) {
				t.Fatalf("file arm error %v is not coded", fileErr)
			}

			fp, err := ParseFeatureProfile([]byte(body))
			if fp != nil {
				t.Errorf("ParseFeatureProfile returned a profile alongside the error: %+v", fp)
			}
			ce := requireProfileInvalid(t, err, fileCE.Details["reason"].(string))
			if _, ok := ce.Details["path"]; ok {
				t.Errorf("ParseFeatureProfile details carry a path: %v", ce.Details)
			}
		})
	}
}

// TestParseFeatureProfile_ValidRoundTripsThroughNew asserts a parsed
// profile is the same value the file arm stores, and that New still runs
// the shape class over it (ParseFeatureProfile only decodes).
func TestParseFeatureProfile_ValidRoundTripsThroughNew(t *testing.T) {
	body := `{"profile": "self-serve", "features": ["AGG_SUM", "capability:process"], "behaviour": {"disable_cohort_scan": true}}`
	fp, err := ParseFeatureProfile([]byte(body))
	if err != nil {
		t.Fatalf("ParseFeatureProfile: %v", err)
	}
	fromFile, err := New(Options{FS: memFsWith(t, map[string]string{"p.json": body}), FeatureProfileFile: "p.json"})
	if err != nil {
		t.Fatalf("New(file): %v", err)
	}
	fromValue, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: fp})
	if err != nil {
		t.Fatalf("New(value): %v", err)
	}
	if !reflect.DeepEqual(fromFile.featureProfile, fromValue.featureProfile) {
		t.Errorf("parsed profile stored %+v; file arm stored %+v", fromValue.featureProfile, fromFile.featureProfile)
	}

	dup, err := ParseFeatureProfile([]byte(`{"features": ["AGG_SUM", "AGG_SUM"]}`))
	if err != nil {
		t.Fatalf("ParseFeatureProfile(duplicate): %v", err)
	}
	_, err = New(Options{FS: memFsWith(t, nil), FeatureProfile: dup})
	requireProfileInvalid(t, err, featureProfileReasonDuplicate)
}

package pulse

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/buildinfo"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// injectFutureFeature adds a built-in capability row introduced in since
// to both the lookup and the table listing, restoring them on cleanup.
func injectFutureFeature(t *testing.T, name, since string) {
	t.Helper()
	prevLookup, prevList := lookupBuiltinFeature, listBuiltinFeatureNames
	lookupBuiltinFeature = func(n string) (descx.Feature, bool) {
		if n == name {
			return descx.Feature{
				Name:      name,
				Kind:      descx.FeatureKindCapability,
				Since:     since,
				DependsOn: [][]string{{"capability:process"}},
			}, true
		}
		return prevLookup(n)
	}
	listBuiltinFeatureNames = func() []string { return append(prevList(), name) }
	t.Cleanup(func() { lookupBuiltinFeature, listBuiltinFeatureNames = prevLookup, prevList })
}

func codedOf(t *testing.T, err error) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v is not a *errors.CodedError", err)
	}
	return ce
}

func TestInitFeatureProfile_ListsEveryOfferedFeature(t *testing.T) {
	defer buildinfo.SetForTest("1.0.0-alpha.2")()
	fp, err := InitFeatureProfile("", profileExt("capability:process"))
	if err != nil {
		t.Fatalf("InitFeatureProfile: %v", err)
	}
	want := descx.SortFeatureNames(append(descx.ReachedFeatureNames("1.0.0-alpha.2"), profileExtAgg))
	if !reflect.DeepEqual(fp.Features, want) {
		t.Fatalf("features = %d names; want %d in canonical order", len(fp.Features), len(want))
	}
	if fp.WrittenWith != "1.0.0" {
		t.Errorf("written_with = %q; want the running release core 1.0.0", fp.WrittenWith)
	}
	if fp.Profile != "" || fp.Behaviour != nil {
		t.Errorf("unseeded init carries label %q / behaviour %v; want none", fp.Profile, fp.Behaviour)
	}

	bare, err := InitFeatureProfile("")
	if err != nil {
		t.Fatalf("InitFeatureProfile without extensions: %v", err)
	}
	for _, n := range bare.Features {
		if n == profileExtAgg {
			t.Errorf("init without extensions lists %s", profileExtAgg)
		}
	}
}

func TestInitFeatureProfile_WrittenWithDevelBuild(t *testing.T) {
	defer buildinfo.SetForTest("devel+0123456789ab")()
	fp, err := InitFeatureProfile("")
	if err != nil {
		t.Fatalf("InitFeatureProfile: %v", err)
	}
	if fp.WrittenWith != descx.BuiltinFeatureSince {
		t.Errorf("written_with = %q; want %q", fp.WrittenWith, descx.BuiltinFeatureSince)
	}
}

// TestInitFeatureProfile_RoundTripsThroughCheck: init → JSON → strict
// decode → check (and pulse.New) succeeds, with and without extensions,
// seeded or not.
func TestInitFeatureProfile_RoundTripsThroughCheck(t *testing.T) {
	cases := []struct {
		name string
		from string
		ext  []Extensions
	}{
		{name: "full"},
		{name: "full-with-extension", ext: []Extensions{profileExt("capability:process")}},
		{name: "from-minimal", from: "minimal"},
		{name: "from-survey-crosstab", from: "survey-crosstab"},
		{name: "from-read-only-analyst", from: "read-only-analyst"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fp, err := InitFeatureProfile(tc.from, tc.ext...)
			if err != nil {
				t.Fatalf("InitFeatureProfile: %v", err)
			}
			raw, err := json.Marshal(fp)
			if err != nil {
				t.Fatal(err)
			}
			back, err := ParseFeatureProfile(raw)
			if err != nil {
				t.Fatalf("ParseFeatureProfile(init output): %v", err)
			}
			var ext Extensions
			if len(tc.ext) > 0 {
				ext = tc.ext[0]
			}
			report, err := CheckFeatureProfile(back, FeatureProfileCheckOptions{Extensions: ext})
			if err != nil || !report.Valid {
				t.Fatalf("CheckFeatureProfile(init output) = %+v, %v", report, err)
			}
			if _, err := New(Options{FS: memFsWith(t, nil), Extensions: ext, FeatureProfile: back}); err != nil {
				t.Fatalf("pulse.New(init output): %v", err)
			}
		})
	}
}

func TestInitFeatureProfile_From(t *testing.T) {
	defer buildinfo.SetForTest("1.0.0")()
	ex, err := ExampleFeatureProfile("minimal")
	if err != nil {
		t.Fatal(err)
	}
	fp, err := InitFeatureProfile("minimal", profileExt("capability:process"))
	if err != nil {
		t.Fatalf("InitFeatureProfile(minimal): %v", err)
	}
	if fp.Profile != "minimal" {
		t.Errorf("profile = %q; want the example's label", fp.Profile)
	}
	if !reflect.DeepEqual(fp.Features, descx.SortFeatureNames(ex.Features)) {
		t.Errorf("features = %v; want the example's %v", fp.Features, ex.Features)
	}

	_, err = InitFeatureProfile("no-such-example")
	ce := codedOf(t, err)
	if ce.Code != errors.PULSE_FEATURE_PROFILE_INVALID || ce.Details["reason"] != featureProfileReasonUnknownExample {
		t.Errorf("unknown example = %s %v; want INVALID unknown_example", ce.Code, ce.Details)
	}
}

// TestCheckFeatureProfile_MatchesNew: every class fails with the same
// coded error, message and details as pulse.New for the same profile.
func TestCheckFeatureProfile_MatchesNew(t *testing.T) {
	cases := map[string]struct {
		ext      Extensions
		features []string
	}{
		"nil-features":       {features: nil},
		"duplicate":          {features: []string{"capability:process", "capability:process"}},
		"unknown":            {features: []string{"capability:process", "AGG_NOPE", "process", "AGG_*"}},
		"core-surface":       {features: []string{"inspect"}},
		"dependency":         {features: []string{"AGG_SUM", "OVERLAY_T_CELL"}},
		"bad-ext-depends":    {ext: profileExt("AGG_TYPO"), features: []string{"capability:process"}},
		"ext-dependency":     {ext: profileExt("AGG_WELFORD"), features: []string{"capability:process", profileExtAgg}},
		"valid-with-ext":     {ext: profileExt("capability:process"), features: []string{"capability:process", profileExtAgg}},
		"valid-empty":        {features: []string{}},
		"unknown-before-dep": {features: []string{"AGG_SUM", "AGG_NOPE"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var features []string
			if tc.features != nil {
				features = append([]string{}, tc.features...)
			}
			fp := &FeatureProfile{Features: features}
			_, newErr := New(Options{FS: memFsWith(t, nil), Extensions: tc.ext, FeatureProfile: fp})
			report, checkErr := CheckFeatureProfile(fp, FeatureProfileCheckOptions{Extensions: tc.ext})
			if report == nil {
				t.Fatal("report is nil")
			}
			if (newErr == nil) != (checkErr == nil) {
				t.Fatalf("New err = %v; Check err = %v", newErr, checkErr)
			}
			if report.Valid != (checkErr == nil) {
				t.Errorf("Valid = %v with err %v", report.Valid, checkErr)
			}
			if newErr == nil {
				return
			}
			n, c := codedOf(t, newErr), codedOf(t, checkErr)
			if n.Code != c.Code || n.Message != c.Message || !reflect.DeepEqual(n.Details, c.Details) {
				t.Errorf("Check = %s %q %v;\nNew   = %s %q %v", c.Code, c.Message, c.Details, n.Code, n.Message, n.Details)
			}
		})
	}
}

func TestCheckFeatureProfile_Offline(t *testing.T) {
	fp := &FeatureProfile{Features: []string{
		"capability:process", profileExtAgg, "AGG_PULSE_THING", "SYNTH_ACME_DIST", "AGG_NOPE",
	}}

	// Offline: only the policy-conforming, non-reserved name is relaxed.
	report, err := CheckFeatureProfile(fp, FeatureProfileCheckOptions{Offline: true})
	ce := codedOf(t, err)
	if ce.Code != errors.PULSE_FEATURE_PROFILE_UNKNOWN {
		t.Fatalf("code = %s; want UNKNOWN", ce.Code)
	}
	got := ce.Details["names"].([]string)
	if want := []string{"AGG_NOPE", "AGG_PULSE_THING", "SYNTH_ACME_DIST"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unknown names = %v; want %v", got, want)
	}
	if !reflect.DeepEqual(report.Unverified, []string{profileExtAgg}) {
		t.Errorf("unverified = %v; want [%s]", report.Unverified, profileExtAgg)
	}

	// Online: the same name is an error.
	_, err = CheckFeatureProfile(fp, FeatureProfileCheckOptions{})
	if names := codedOf(t, err).Details["names"].([]string); sort.SearchStrings(names, profileExtAgg) == len(names) || names[sort.SearchStrings(names, profileExtAgg)] != profileExtAgg {
		t.Errorf("online unknown names = %v; want %s among them", names, profileExtAgg)
	}

	// Offline and otherwise valid: valid, with one coded warning.
	report, err = CheckFeatureProfile(&FeatureProfile{Features: []string{"capability:process", profileExtAgg}},
		FeatureProfileCheckOptions{Offline: true})
	if err != nil || !report.Valid {
		t.Fatalf("offline check = %+v, %v; want valid", report, err)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("warnings = %v; want one", report.Warnings)
	}
	w := report.Warnings[0]
	if w.Code != string(errors.PULSE_FEATURE_PROFILE_UNKNOWN) || w.Details["name"] != profileExtAgg ||
		w.Details["reason"] != featureUnknownUnverifiedExtension || w.Message != profileExtAgg+": "+unverifiedExtensionMessage {
		t.Errorf("warning = %+v", w)
	}

	// An unverified name still needs a request host.
	_, err = CheckFeatureProfile(&FeatureProfile{Features: []string{profileExtAgg}}, FeatureProfileCheckOptions{Offline: true})
	if c := codedOf(t, err); c.Code != errors.PULSE_FEATURE_PROFILE_DEPENDENCY {
		t.Errorf("unverified without host = %s; want DEPENDENCY", c.Code)
	}

	// A registered extension is verified, not warned about.
	report, err = CheckFeatureProfile(&FeatureProfile{Features: []string{"capability:process", profileExtAgg}},
		FeatureProfileCheckOptions{Offline: true, Extensions: profileExt()})
	if err != nil || len(report.Warnings) != 0 || len(report.Unverified) != 0 {
		t.Errorf("registered extension offline = %+v, %v; want no warning", report, err)
	}
}

func TestCheckFeatureProfile_OfflineKeepsNewerBuiltinAnError(t *testing.T) {
	const future = "AGG_FUTURE_THING"
	injectFutureFeature(t, future, "9.0.0")
	defer buildinfo.SetForTest("1.0.0")()
	report, err := CheckFeatureProfile(&FeatureProfile{Features: []string{"capability:process", future}},
		FeatureProfileCheckOptions{Offline: true})
	if len(report.Unverified) != 0 || len(report.Warnings) != 0 {
		t.Errorf("newer built-in reported unverified: %v / %v", report.Unverified, report.Warnings)
	}
	ce := codedOf(t, err)
	if ce.Code != errors.PULSE_FEATURE_PROFILE_UNKNOWN || unknownEntries(t, ce)[future]["reason"] != featureUnknownNewer {
		t.Errorf("newer built-in offline = %s %v; want UNKNOWN newer_than_running", ce.Code, ce.Details)
	}
}

func TestCheckFeatureProfile_NilProfile(t *testing.T) {
	report, err := CheckFeatureProfile(nil, FeatureProfileCheckOptions{})
	if report == nil || report.Valid {
		t.Fatalf("report = %+v; want non-nil, invalid", report)
	}
	if codedOf(t, err).Code != errors.PULSE_FEATURE_PROFILE_INVALID {
		t.Errorf("nil profile = %v; want INVALID", err)
	}
}

func TestDiffFeatureProfile(t *testing.T) {
	const future = "capability:from_the_future"
	injectFutureFeature(t, future, "1.2.0")
	defer buildinfo.SetForTest("devel")()

	missingOf := func(d *FeatureProfileDiff) map[string]FeatureProfileMissing {
		out := map[string]FeatureProfileMissing{}
		for _, m := range d.Missing {
			out[m.Name] = m
		}
		return out
	}

	fp := &FeatureProfile{WrittenWith: "1.1.0", Features: []string{"capability:process", "AGG_SUM", "BOGUS_NAME", "process"}}
	d, err := DiffFeatureProfile(fp, profileExt())
	if err != nil {
		t.Fatalf("DiffFeatureProfile: %v", err)
	}
	m := missingOf(d)
	if f, ok := m[future]; !ok || !f.New || f.Since != "1.2.0" || f.Kind != "capability" {
		t.Errorf("future feature = %+v (present %v); want new, since 1.2.0", f, ok)
	}
	if f := m["AGG_COUNT"]; f.New || f.Since != descx.BuiltinFeatureSince || f.Category != "AGG" || f.Kind != "operator" {
		t.Errorf("AGG_COUNT = %+v; want not new", f)
	}
	if f, ok := m[profileExtAgg]; !ok || f.New || f.Since != "" {
		t.Errorf("extension = %+v (present %v); want listed, never new", f, ok)
	}
	if _, ok := m["AGG_SUM"]; ok {
		t.Error("a listed feature is reported missing")
	}
	var names []string
	for _, mm := range d.Missing {
		names = append(names, mm.Name)
	}
	if !reflect.DeepEqual(names, descx.SortFeatureNames(names)) {
		t.Error("missing is not in canonical order")
	}
	want := []FeatureProfileUnknownName{
		{Name: "BOGUS_NAME", Reason: featureUnknownUnregistered},
		{Name: "process", Reason: featureUnknownWrongKind, DidYouMean: "capability:process"},
	}
	if !reflect.DeepEqual(d.Unknown, want) {
		t.Errorf("unknown = %+v; want %+v", d.Unknown, want)
	}

	for _, ww := range []string{"", "1.2.0", "not-a-version"} {
		fp.WrittenWith = ww
		d, err := DiffFeatureProfile(fp)
		if err != nil {
			t.Fatal(err)
		}
		if f := missingOf(d)[future]; f.New {
			t.Errorf("written_with %q: future feature flagged new", ww)
		}
	}

	if _, err := DiffFeatureProfile(&FeatureProfile{}); codedOf(t, err).Code != errors.PULSE_FEATURE_PROFILE_INVALID {
		t.Errorf("nil features = %v; want INVALID", err)
	}
}

func TestDescribeFeatureProfile(t *testing.T) {
	fp := &FeatureProfile{Features: []string{
		"OVERLAY_T_CELL", profileExtAgg, "capability:process", "AGG_SUM", "BOGUS_NAME",
	}}
	d, err := DescribeFeatureProfile(fp, profileExt("AGG_WELFORD"))
	if err != nil {
		t.Fatalf("DescribeFeatureProfile: %v", err)
	}
	if len(d.Features) != len(fp.Features) {
		t.Fatalf("described %d; want every one of %d entries", len(d.Features), len(fp.Features))
	}
	by := map[string]FeatureDescription{}
	var order []string
	for _, f := range d.Features {
		by[f.Name] = f
		order = append(order, f.Name)
	}
	if !reflect.DeepEqual(order, descx.SortFeatureNames(fp.Features)) {
		t.Errorf("order = %v; want canonical", order)
	}

	hosts := descx.RequestHostCapabilities()
	if f := by["AGG_SUM"]; f.Kind != "operator" || f.Category != "AGG" || f.Source != featureSourceBuiltin ||
		f.Since != descx.BuiltinFeatureSince || !reflect.DeepEqual(f.DependsOn, [][]string{hosts}) || f.Unknown != nil {
		t.Errorf("AGG_SUM = %+v", f)
	}
	wantCell, _ := descx.FeatureDependencies("OVERLAY_T_CELL")
	if f := by["OVERLAY_T_CELL"]; !reflect.DeepEqual(f.DependsOn, wantCell) {
		t.Errorf("OVERLAY_T_CELL deps = %v; want %v", f.DependsOn, wantCell)
	}
	if f := by["capability:process"]; f.Kind != "capability" || f.Category != "" || len(f.DependsOn) != 0 || f.DependsOn == nil {
		t.Errorf("capability:process = %+v", f)
	}
	if f := by[profileExtAgg]; f.Source != featureSourceExtension || f.Since != "" ||
		!reflect.DeepEqual(f.DependsOn, [][]string{hosts, {"AGG_WELFORD"}}) {
		t.Errorf("extension = %+v", f)
	}
	if f := by["BOGUS_NAME"]; f.Source != featureSourceUnknown || f.Unknown == nil || f.Unknown.Reason != featureUnknownUnregistered {
		t.Errorf("unknown = %+v", f)
	}
}

// TestFeatureProfileTooling_JSONShape pins the wire keys and the
// never-null slices the CLI's --json envelopes rely on.
func TestFeatureProfileTooling_JSONShape(t *testing.T) {
	fp := &FeatureProfile{Features: []string{}}
	check, _ := CheckFeatureProfile(fp, FeatureProfileCheckOptions{})
	diff, _ := DiffFeatureProfile(fp)
	desc, _ := DescribeFeatureProfile(fp)
	cases := map[string]struct {
		v    any
		keys []string
	}{
		"check":    {check, []string{"version", "valid", "features", "unverified", "warnings"}},
		"diff":     {diff, []string{"version", "missing", "unknown"}},
		"describe": {desc, []string{"version", "features"}},
	}
	for name, tc := range cases {
		raw, err := json.Marshal(tc.v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range tc.keys {
			if v, ok := m[k]; !ok || v == nil {
				t.Errorf("%s: key %q = %v (present %v); want non-null", name, k, v, ok)
			}
		}
	}
}

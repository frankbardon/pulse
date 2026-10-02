package pulse

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/buildinfo"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

type profileStubAgg struct{}

func (profileStubAgg) Aggregate(extend.Rows, string) (float64, error) { return 0, nil }

func profileStubAggFactory(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
	return profileStubAgg{}, nil
}

type profileStubFilter struct{}

func (profileStubFilter) Build(*types.Filterer, *encoding.Schema) (extend.FilterFunc, error) {
	return func(extend.Record) (bool, error) { return true, nil }, nil
}

func profileStubFilterFactory() extend.FiltererBuilder { return profileStubFilter{} }

const profileExtAgg = "AGG_ACME_SPECIAL"

func profileExt(dependsOn ...string) Extensions {
	return Extensions{Aggregators: []AggregatorRegistration{{
		Name:      profileExtAgg,
		Factory:   profileStubAggFactory,
		DependsOn: dependsOn,
	}}}
}

func newWithProfile(t *testing.T, ext Extensions, features ...string) error {
	t.Helper()
	_, err := New(Options{
		FS:             memFsWith(t, nil),
		Extensions:     ext,
		FeatureProfile: &FeatureProfile{Features: append([]string{}, features...)},
	})
	return err
}

func requireCode(t *testing.T, err error, want errors.Code) *errors.CodedError {
	t.Helper()
	if err == nil {
		t.Fatalf("New succeeded; want %s", want)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v is not a *errors.CodedError", err)
	}
	if ce.Code != want {
		t.Fatalf("code = %s; want %s (err: %v, details: %v)", ce.Code, want, err, ce.Details)
	}
	return ce
}

// unknownEntries returns the "unknown" detail keyed by name.
func unknownEntries(t *testing.T, ce *errors.CodedError) map[string]map[string]any {
	t.Helper()
	list, ok := ce.Details["unknown"].([]map[string]any)
	if !ok {
		t.Fatalf("unknown detail = %T; want []map[string]any", ce.Details["unknown"])
	}
	out := map[string]map[string]any{}
	for _, e := range list {
		out[e["name"].(string)] = e
	}
	return out
}

// TestProfileRejectsPatterns: wildcards are never expanded — every
// pattern is an unknown name, and every one is reported.
func TestProfileRejectsPatterns(t *testing.T) {
	patterns := []string{"TEST_*", "capability:*", "AGG_?UM", "io_format:[cj]sv", "*"}
	ce := requireCode(t, newWithProfile(t, Extensions{}, append(patterns, "capability:process")...),
		errors.PULSE_FEATURE_PROFILE_UNKNOWN)
	got := unknownEntries(t, ce)
	if len(got) != len(patterns) {
		t.Fatalf("unknown entries = %v; want one per pattern %v", got, patterns)
	}
	for _, p := range patterns {
		if r := got[p]["reason"]; r != featureUnknownPattern {
			t.Errorf("%q reason = %v; want %s", p, r, featureUnknownPattern)
		}
	}
}

// TestFeatureProfile_UnknownReasons covers each unknown class with its
// classification and suggestion.
func TestFeatureProfile_UnknownReasons(t *testing.T) {
	cases := []struct {
		name, reason, didYouMean string
	}{
		{"AGG_NOT_A_THING", featureUnknownUnregistered, ""},
		{"operator:AGG_SUM", featureUnknownWrongKind, "AGG_SUM"},
		{"capability:AGG_SUM", featureUnknownWrongKind, "AGG_SUM"},
		{"process", featureUnknownWrongKind, "capability:process"},
		{"io_format:process", featureUnknownWrongKind, "capability:process"},
		{"operator:" + profileExtAgg, featureUnknownWrongKind, profileExtAgg},
		{"inspect", featureUnknownCoreSurface, ""},
		{"capability:manifest", featureUnknownCoreSurface, ""},
		{"SYNTH_ACME_DIST", featureUnknownUnregistered, ""},
	}
	ext := profileExt()
	ext.SynthDistributions = []DistributionRegistration{{Name: "SYNTH_ACME_DIST"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ce := requireCode(t, newWithProfile(t, ext, tc.name, "capability:process"),
				errors.PULSE_FEATURE_PROFILE_UNKNOWN)
			e := unknownEntries(t, ce)[tc.name]
			if e == nil {
				t.Fatalf("%q not reported: %v", tc.name, ce.Details)
			}
			if e["reason"] != tc.reason {
				t.Errorf("reason = %v; want %s", e["reason"], tc.reason)
			}
			if tc.didYouMean != "" && e["did_you_mean"] != tc.didYouMean {
				t.Errorf("did_you_mean = %v; want %s", e["did_you_mean"], tc.didYouMean)
			}
		})
	}
}

// TestFeatureProfile_UnknownListsEveryName: the whole class is
// reported, sorted, with the running version.
func TestFeatureProfile_UnknownListsEveryName(t *testing.T) {
	ce := requireCode(t, newWithProfile(t, Extensions{}, "ZZZ", "capability:process", "AAA", "inspect"),
		errors.PULSE_FEATURE_PROFILE_UNKNOWN)
	if got, want := ce.Details["names"], []string{"AAA", "ZZZ", "inspect"}; !reflect.DeepEqual(got, want) {
		t.Errorf("names = %v; want %v", got, want)
	}
	if ce.Details["version"] != Version() {
		t.Errorf("version = %v; want %s", ce.Details["version"], Version())
	}
}

// TestFeatureProfile_FutureSince injects a built-in row from a later
// release: it is unknown to an older running build, and resolves for a
// pre-release of its own line or a devel build.
func TestFeatureProfile_FutureSince(t *testing.T) {
	const future = "capability:from_the_future"
	prev := lookupBuiltinFeature
	lookupBuiltinFeature = func(name string) (descx.Feature, bool) {
		if name == future {
			return descx.Feature{Name: future, Kind: descx.FeatureKindCapability, Since: "1.2.0"}, true
		}
		return prev(name)
	}
	defer func() { lookupBuiltinFeature = prev }()

	for _, running := range []string{"1.0.0", "1.1.9", "v1.1.0-alpha.3-4-gdeadbee"} {
		t.Run("rejects/"+running, func(t *testing.T) {
			defer buildinfo.SetForTest(running)()
			ce := requireCode(t, newWithProfile(t, Extensions{}, future), errors.PULSE_FEATURE_PROFILE_UNKNOWN)
			e := unknownEntries(t, ce)[future]
			if e["reason"] != featureUnknownNewer || e["since"] != "1.2.0" {
				t.Errorf("entry = %v; want reason %s since 1.2.0", e, featureUnknownNewer)
			}
			if ce.Details["version"] != running {
				t.Errorf("version = %v; want %s", ce.Details["version"], running)
			}
		})
	}
	for _, running := range []string{"1.2.0-alpha.2", "1.2.0", "2.0.0", "devel", "devel+0123456789ab"} {
		t.Run("accepts/"+running, func(t *testing.T) {
			defer buildinfo.SetForTest(running)()
			if err := newWithProfile(t, Extensions{}, future); err != nil {
				t.Errorf("New: %v", err)
			}
		})
	}
}

// TestFeatureProfile_PreReleaseAcceptsBaseline: a 1.0.0 pre-release
// build offers the Since 1.0.0 baseline.
func TestFeatureProfile_PreReleaseAcceptsBaseline(t *testing.T) {
	restore := buildinfo.SetForTest("1.0.0-alpha.2")
	if err := newWithProfile(t, Extensions{}, "AGG_SUM", "capability:process"); err != nil {
		t.Fatalf("New: %v", err)
	}
	restore()
	// The suffix is cut, not treated as unparseable-therefore-newest: a
	// pre-release of an OLDER line still lacks the baseline.
	defer buildinfo.SetForTest("0.99.0-rc.1")()
	ce := requireCode(t, newWithProfile(t, Extensions{}, "AGG_SUM", "capability:process"), errors.PULSE_FEATURE_PROFILE_UNKNOWN)
	if e := unknownEntries(t, ce)["AGG_SUM"]; e["reason"] != featureUnknownNewer {
		t.Errorf("AGG_SUM entry = %v; want %s", e, featureUnknownNewer)
	}
}

func TestSinceReached(t *testing.T) {
	cases := []struct {
		since, running string
		want           bool
	}{
		{"1.0.0", "1.0.0", true},
		{"1.0.0", "1.0.0-alpha.2", true},
		{"1.0.0", "v1.0.0-alpha.1-5-gabc1234-dirty", true},
		{"1.0.0", "1.0.0+build.7", true},
		{"1.0.0", "0.39.1", false},
		{"1.3.0", "1.2.9", false},
		{"1.3.0", "1.10.0", true},
		{"1.0.5", "1.0.4", false},
		{"2.0.0", "1.99.99", false},
		{"9.9.9", "devel", true},
		{"9.9.9", "devel+0123456789ab", true},
		{"9.9.9", "0123456789ab", true},
		{"9.9.9", "1234567", true},
		{"9.9.9", "", true},
		{"9.9.9", "not a version", true},
		{"9.9.9", "v0.0.0-20261002141941-62da1f744ed5", true},
		{"bogus", "devel", false},
	}
	for _, tc := range cases {
		if got := sinceReached(tc.since, tc.running); got != tc.want {
			t.Errorf("sinceReached(%q, %q) = %v; want %v", tc.since, tc.running, got, tc.want)
		}
	}
}

// TestFeatureProfile_Dependency reports every unmet group, naming the
// enabled feature and the group.
func TestFeatureProfile_Dependency(t *testing.T) {
	ce := requireCode(t, newWithProfile(t, Extensions{}, "AGG_SUM", "OVERLAY_T_CELL", "capability:sample"),
		errors.PULSE_FEATURE_PROFILE_DEPENDENCY)
	unmet, ok := ce.Details["unmet"].([]map[string]any)
	if !ok {
		t.Fatalf("unmet detail = %T", ce.Details["unmet"])
	}
	hosts := descx.RequestHostCapabilities()
	tCellHosts, _ := descx.FeatureDependencies("OVERLAY_T_CELL")
	want := []map[string]any{
		{"feature": "AGG_SUM", "requires_any_of": hosts},
	}
	for _, g := range tCellHosts {
		want = append(want, map[string]any{"feature": "OVERLAY_T_CELL", "requires_any_of": g})
	}
	if !reflect.DeepEqual(unmet, want) {
		t.Errorf("unmet = %v; want %v", unmet, want)
	}
	if got := ce.Details["features"]; !reflect.DeepEqual(got, []string{"AGG_SUM", "OVERLAY_T_CELL"}) {
		t.Errorf("features = %v", got)
	}

	if err := newWithProfile(t, Extensions{}, "AGG_SUM", "capability:compose"); err != nil {
		t.Errorf("any-of group met by compose: %v", err)
	}
}

// TestFeatureProfile_ClassOrder: INVALID beats UNKNOWN beats DEPENDENCY.
func TestFeatureProfile_ClassOrder(t *testing.T) {
	requireProfileInvalid(t, newWithProfile(t, Extensions{}, "AGG_SUM", "AGG_SUM", "NOPE"), featureProfileReasonDuplicate)
	requireCode(t, newWithProfile(t, Extensions{}, "AGG_SUM", "NOPE"), errors.PULSE_FEATURE_PROFILE_UNKNOWN)
	requireCode(t, newWithProfile(t, Extensions{}, "AGG_SUM"), errors.PULSE_FEATURE_PROFILE_DEPENDENCY)
}

// TestFeatureProfile_Extensions: an extension omitted from the profile
// is fine, a listed one resolves, and its DependsOn plus the request
// host group are enforced only when the profile enables it.
func TestFeatureProfile_Extensions(t *testing.T) {
	ext := profileExt("AGG_WELFORD")
	if err := newWithProfile(t, ext, "AGG_SUM", "capability:process"); err != nil {
		t.Errorf("extension omitted: %v", err)
	}
	if err := newWithProfile(t, ext, profileExtAgg, "AGG_WELFORD", "capability:process"); err != nil {
		t.Errorf("extension listed with its dependency: %v", err)
	}
	ce := requireCode(t, newWithProfile(t, ext, profileExtAgg, "capability:process"), errors.PULSE_FEATURE_PROFILE_DEPENDENCY)
	want := []map[string]any{{"feature": profileExtAgg, "requires_any_of": []string{"AGG_WELFORD"}}}
	if got := ce.Details["unmet"]; !reflect.DeepEqual(got, want) {
		t.Errorf("unmet = %v; want %v", got, want)
	}
	ce = requireCode(t, newWithProfile(t, profileExt(), profileExtAgg), errors.PULSE_FEATURE_PROFILE_DEPENDENCY)
	want = []map[string]any{{"feature": profileExtAgg, "requires_any_of": descx.RequestHostCapabilities()}}
	if got := ce.Details["unmet"]; !reflect.DeepEqual(got, want) {
		t.Errorf("unmet = %v; want %v", got, want)
	}
}

// TestExtensionDependsOnMustBeKnown: a DependsOn entry naming no known
// feature fails pulse.New even without a profile; built-ins and other
// extension operators are known.
func TestExtensionDependsOnMustBeKnown(t *testing.T) {
	_, err := New(Options{FS: memFsWith(t, nil), Extensions: profileExt("AGG_NOPE", "operator:AGG_SUM")})
	ce := requireCode(t, err, errors.PULSE_FEATURE_PROFILE_UNKNOWN)
	got := unknownEntries(t, ce)
	if len(got) != 2 {
		t.Fatalf("unknown = %v; want both entries", got)
	}
	for name, reason := range map[string]string{"AGG_NOPE": featureUnknownUnregistered, "operator:AGG_SUM": featureUnknownWrongKind} {
		e := got[name]
		if e["reason"] != reason || e["extension"] != profileExtAgg || e["category"] != string(categoryAggregator) {
			t.Errorf("%s entry = %v; want reason %s on %s", name, e, reason, profileExtAgg)
		}
	}

	ext := profileExt("AGG_SUM", "capability:process", "FILTER_ACME_OTHER")
	ext.Filterers = []FiltererRegistration{{Name: "FILTER_ACME_OTHER", Factory: profileStubFilterFactory}}
	if _, err := New(Options{FS: memFsWith(t, nil), Extensions: ext}); err != nil {
		t.Errorf("known DependsOn refused: %v", err)
	}
}

// TestFeatureProfile_FilterToFileRequiresFilterExpression: FilterToFile
// compiles every filterer into an engine-internal FILTER_EXPRESSION, so
// a profile enabling capability:filter_to_file without FILTER_EXPRESSION
// is refused at pulse.New — never reaching a run-time "unknown filter
// type" that would also leak the hidden name. With both, a request using
// only a visible FILTER_INCLUDE runs.
func TestFeatureProfile_FilterToFileRequiresFilterExpression(t *testing.T) {
	base := []string{"capability:process", "capability:filter_to_file", "FILTER_INCLUDE"}
	ce := requireCode(t, newWithProfile(t, Extensions{}, base...), errors.PULSE_FEATURE_PROFILE_DEPENDENCY)
	want := []map[string]any{{"feature": "capability:filter_to_file", "requires_any_of": []string{"FILTER_EXPRESSION"}}}
	if got := ce.Details["unmet"]; !reflect.DeepEqual(got, want) {
		t.Errorf("unmet = %v; want %v", got, want)
	}

	p, err := New(Options{
		FS:             parityFS(t),
		FeatureProfile: &FeatureProfile{Features: append(base, "FILTER_EXPRESSION")},
	})
	if err != nil {
		t.Fatalf("New with FILTER_EXPRESSION: %v", err)
	}
	res, err := p.FilterToFileWithRequest(context.Background(), &FilterToFileRequest{
		SourcePath: parityCohort,
		OutputDir:  "out",
		Filterers:  []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"south"}}},
	})
	if err != nil {
		t.Fatalf("FilterToFileWithRequest: %v", err)
	}
	if res.RowCount <= 0 {
		t.Errorf("RowCount = %d; want > 0", res.RowCount)
	}
}

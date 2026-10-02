package pulse

import (
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// featureSetFixtures are the private fixture profiles later stories
// golden manifest / schema output against.
var featureSetFixtures = []string{"minimal", "survey-crosstab", "empty"}

const featureSetFixtureDir = "descriptor/testdata/profiles/"

type featureSetStubAgg struct{}

func (featureSetStubAgg) Aggregate(extend.Rows, string) (float64, error) { return 1, nil }

func featureSetStubAggFactory(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
	return featureSetStubAgg{}, nil
}

func newFixturePulse(t *testing.T, name string, opts Options) *Pulse {
	t.Helper()
	opts.FS = afero.NewReadOnlyFs(afero.NewOsFs())
	opts.FeatureProfileFile = featureSetFixtureDir + name + ".json"
	p, err := New(opts)
	if err != nil {
		t.Fatalf("New with fixture %s: %v", name, err)
	}
	return p
}

// TestFeatureSet_FixturesLoad asserts each fixture profile loads through
// the file arm and validates at pulse.New, and its enabled set is
// exactly its list.
func TestFeatureSet_FixturesLoad(t *testing.T) {
	for _, name := range featureSetFixtures {
		t.Run(name, func(t *testing.T) {
			p := newFixturePulse(t, name, Options{})
			fp, ok := p.FeatureProfile()
			if !ok || fp.Profile != name {
				t.Fatalf("FeatureProfile = (%+v, %v), want label %q", fp, ok, name)
			}
			want := append([]string{}, fp.Features...)
			sort.Strings(want)
			if got := p.svc.InstanceSnapshot().EnabledNames(); !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
				t.Errorf("enabled = %v, want %v", got, want)
			}
		})
	}
	minimal := newFixturePulse(t, "minimal", Options{}).svc.InstanceSnapshot()
	for _, n := range []string{"capability:process", "AGG_SUM", "GROUP_CATEGORY"} {
		if !minimal.Enabled(n) {
			t.Errorf("minimal does not enable %s", n)
		}
	}
	survey := newFixturePulse(t, "survey-crosstab", Options{}).svc.InstanceSnapshot()
	for _, n := range []string{"capability:crosstab", "OVERLAY_PAIRWISE_WELCH_T", "AGG_WELFORD", "TEST_T"} {
		if !survey.Enabled(n) {
			t.Errorf("survey-crosstab does not enable %s", n)
		}
	}
	if !survey.Behaviour().DisableProjection {
		t.Error("survey-crosstab behaviour not folded into the snapshot")
	}
}

// TestFeatureSet_DefaultIsFullRegistry: without a profile the enabled
// set is every built-in feature (all reached at the running build) plus
// every registered extension operator, and nothing is hidden.
func TestFeatureSet_DefaultIsFullRegistry(t *testing.T) {
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: Extensions{Aggregators: []AggregatorRegistration{
		{Name: "AGG_ACME_SCORE", Description: "Stub.", Factory: featureSetStubAggFactory},
	}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	snap := p.svc.InstanceSnapshot()
	want := append(descx.FeatureNames(), "AGG_ACME_SCORE")
	sort.Strings(want)
	if got := snap.EnabledNames(); !slices.Equal(got, want) {
		t.Errorf("default enabled set differs from the full registry: got %d names, want %d", len(got), len(want))
	}
	if h := snap.HiddenNames(); len(h) != 0 {
		t.Errorf("default instance hides %v", h)
	}
	if !snap.Enabled("AGG_ACME_SCORE") || !p.svc.Extensions().HasAggregator("AGG_ACME_SCORE") {
		t.Error("default instance dropped a registered extension")
	}
	if d := p.FeatureSetDigest(); !strings.HasPrefix(d, "fs1:") || len(d) != len("fs1:")+64 {
		t.Errorf("FeatureSetDigest = %q, want fs1:<64 hex>", d)
	}
}

// TestFeatureSetDigest_DistinguishesInstances: deterministic per
// configuration; differs between the default and each fixture, between
// fixtures, and between instances differing only in a behaviour switch.
func TestFeatureSetDigest_DistinguishesInstances(t *testing.T) {
	newP := func(opts Options) *Pulse {
		t.Helper()
		opts.FS = memFsWith(t, nil)
		p, err := New(opts)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return p
	}
	def := newP(Options{}).FeatureSetDigest()
	if again := newP(Options{}).FeatureSetDigest(); again != def {
		t.Fatalf("digest not deterministic: %s vs %s", def, again)
	}
	seen := map[string]string{def: "default"}
	for _, name := range featureSetFixtures {
		d := newFixturePulse(t, name, Options{}).FeatureSetDigest()
		if d != newFixturePulse(t, name, Options{}).FeatureSetDigest() {
			t.Errorf("%s digest not deterministic", name)
		}
		if prev, dup := seen[d]; dup {
			t.Errorf("%s digest equals %s", name, prev)
		}
		seen[d] = name
	}
	for name, opts := range map[string]Options{
		"DisableDefaults":   {DisableDefaults: true},
		"DisableComponents": {DisableComponents: true},
		"DisableProjection": {DisableProjection: true},
	} {
		if newP(opts).FeatureSetDigest() == def {
			t.Errorf("Options.%s alone did not change the digest", name)
		}
	}
	// The profile's behaviour counts like Options: a profile switch and
	// the matching Options switch on the same feature list agree.
	list := []string{"capability:process"}
	viaProfile := newP(Options{FeatureProfile: &FeatureProfile{Features: list, Behaviour: &FeatureProfileBehaviour{DisableDefaults: true}}}).FeatureSetDigest()
	viaOptions := newP(Options{DisableDefaults: true, FeatureProfile: &FeatureProfile{Features: list}}).FeatureSetDigest()
	if viaProfile != viaOptions {
		t.Error("effective behaviour differs between profile and Options spelling of the same switch")
	}
	scanOff := newP(Options{FeatureProfile: &FeatureProfile{Features: list, Behaviour: &FeatureProfileBehaviour{DisableCohortScan: true}}}).FeatureSetDigest()
	if scanOff == newP(Options{FeatureProfile: &FeatureProfile{Features: list}}).FeatureSetDigest() {
		t.Error("behaviour.disable_cohort_scan did not change the digest")
	}
}

// TestFeatureSet_HiddenExtensionDropped: an extension operator the
// profile omits is absent from the runtime registry and the snapshot,
// while a DependsOn naming it (from another hidden extension) still
// validates because DependsOn is checked against ALL registrations.
func TestFeatureSet_HiddenExtensionDropped(t *testing.T) {
	ext := Extensions{Aggregators: []AggregatorRegistration{
		{Name: "AGG_ACME_BASE", Description: "Stub.", Factory: featureSetStubAggFactory},
		{Name: "AGG_ACME_DERIVED", Description: "Stub.", Factory: featureSetStubAggFactory, DependsOn: []string{"AGG_ACME_BASE"}},
		{Name: "AGG_ACME_KEPT", Description: "Stub.", Factory: featureSetStubAggFactory},
	}}
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: ext,
		FeatureProfile: &FeatureProfile{Features: []string{"capability:process", "AGG_ACME_KEPT"}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	reg := p.svc.Extensions()
	snap := p.svc.InstanceSnapshot()
	for _, hidden := range []types.AggregationType{"AGG_ACME_BASE", "AGG_ACME_DERIVED"} {
		if reg.HasAggregator(hidden) {
			t.Errorf("hidden %s registered at runtime", hidden)
		}
		if !snap.Hidden(string(hidden)) || snap.Enabled(string(hidden)) {
			t.Errorf("%s not reported hidden", hidden)
		}
	}
	if !reg.HasAggregator("AGG_ACME_KEPT") {
		t.Error("enabled extension dropped from the runtime registry")
	}
	var names []string
	for _, m := range p.svc.ExtensionsSnapshot().Aggregators {
		names = append(names, string(m.Name))
	}
	if !reflect.DeepEqual(names, []string{"AGG_ACME_KEPT"}) {
		t.Errorf("snapshot aggregators = %v, want only AGG_ACME_KEPT", names)
	}
	// The caller's Extensions value is untouched.
	if len(ext.Aggregators) != 3 {
		t.Error("withoutHiddenExtensions mutated the caller's registrations")
	}
}

// TestFeatureProfileAccessor: a copy with a profile, (nil, false)
// without one.
func TestFeatureProfileAccessor(t *testing.T) {
	bare, err := New(Options{FS: memFsWith(t, nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if fp, ok := bare.FeatureProfile(); ok || fp != nil {
		t.Errorf("profile-free FeatureProfile = (%+v, %v), want (nil, false)", fp, ok)
	}
	p, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: &FeatureProfile{
		Profile: "x", Features: []string{"capability:process"}, Behaviour: &FeatureProfileBehaviour{}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, ok := p.FeatureProfile()
	if !ok || got.Profile != "x" || !slices.Equal(got.Features, []string{"capability:process"}) {
		t.Fatalf("FeatureProfile = (%+v, %v)", got, ok)
	}
	got.Features[0] = "mutated"
	got.Behaviour.DisableDefaults = true
	got.Profile = "y"
	again, _ := p.FeatureProfile()
	if again.Features[0] != "capability:process" || again.Behaviour.DisableDefaults || again.Profile != "x" {
		t.Errorf("FeatureProfile returned an alias: %+v", again)
	}
}

package pulse_test

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/processing/window"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
)

// ---- Stubs for categories not yet helper-stubbed ---------------------

type stubGrouper struct{}

func (stubGrouper) Group(records []*processing.Record, field string) (map[string][]*processing.Record, error) {
	_, _ = records, field
	return nil, nil
}

func (stubGrouper) KeyForRow(*processing.Record, string) (string, bool, error) {
	return "", false, nil
}

func stubGrouperFactory(*types.Group, *encoding.Schema) (processing.Grouper, error) {
	return stubGrouper{}, nil
}

type stubWindowComputer struct{}

func (stubWindowComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	_, _, _ = rows, partitions, label
	return nil
}

func stubWindowFactory(*types.Window, window.WindowOptions) (window.WindowComputer, error) {
	return stubWindowComputer{}, nil
}

// ---- Validation: tier-2 + tier-empty + tier-unknown variants ---------

func TestExtensions_TestTierPostMissingFactory(t *testing.T) {
	ext := pulse.Extensions{
		Tests: []pulse.TestRegistration{{
			Name: "TEST_ACME_BRAND_POST",
			Tier: pulse.TestTierPost,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_TestTierPostWithRowFactory(t *testing.T) {
	ext := pulse.Extensions{
		Tests: []pulse.TestRegistration{{
			Name:        "TEST_ACME_BRAND_POST",
			Tier:        pulse.TestTierPost,
			PostFactory: stubPostTestFactory,
			RowFactory:  stubRowTestFactory,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_TestTierEmpty(t *testing.T) {
	ext := pulse.Extensions{
		Tests: []pulse.TestRegistration{{
			Name:       "TEST_ACME_BRAND_NIL",
			RowFactory: stubRowTestFactory,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_TestTierUnknown(t *testing.T) {
	ext := pulse.Extensions{
		Tests: []pulse.TestRegistration{{
			Name:       "TEST_ACME_BRAND_X",
			Tier:       "tier99",
			RowFactory: stubRowTestFactory,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

// ---- buildRuntimeExtensions: every category populated -----------------

func TestExtensions_RuntimeRegistryAllCategoriesPopulated(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			{Name: "AGG_ACME_BRAND_SCORE", Factory: stubAggregatorFactory, Streamable: true},
		},
		Attributes: []pulse.AttributeRegistration{
			{Name: "ATTR_ACME_BRAND_BOOST", Factory: stubAttributeFactory, Mode: pulse.AttributeModeTwoPass},
		},
		Filterers: []pulse.FiltererRegistration{
			{Name: "FILTER_ACME_BRAND_HIGH", Factory: stubFiltererFactory},
		},
		Groupers: []pulse.GrouperRegistration{
			{Name: "GROUP_ACME_BRAND_PARITY", Factory: stubGrouperFactory},
		},
		Windows: []pulse.WindowRegistration{
			{Name: "WIN_ACME_BRAND_CONST", Factory: stubWindowFactory},
		},
		Features: []pulse.FeatureRegistration{
			{Name: "FEAT_ACME_BRAND_DOUBLE", Factory: stubFeatureFactory},
		},
		Tests: []pulse.TestRegistration{
			{Name: "TEST_ACME_BRAND_ROW", Tier: pulse.TestTierRow, RowFactory: stubRowTestFactory, Streamable: true},
			{Name: "TEST_ACME_BRAND_POST", Tier: pulse.TestTierPost, PostFactory: stubPostTestFactory},
		},
		SynthDistributions: []pulse.DistributionRegistration{
			{Name: "SYNTH_ACME_BRAND_PARETO", Description: "ACME Pareto override"},
		},
		ExprFunctions: []pulse.ExprFunction{
			{Name: "rank_familiarity", Fn: func(args ...any) (any, error) { return args[0], nil }},
		},
		LookupTables: map[string]pulse.LookupTable{
			"adjustments": {Rows: map[string]float64{"x": 1}},
		},
	}
	p, err := pulse.New(newTestOptions(ext))
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	reg := p.Service().Extensions()
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}

	cases := []struct {
		name string
		got  bool
	}{
		{"AGG_ACME_BRAND_SCORE", reg.HasAggregator("AGG_ACME_BRAND_SCORE")},
		{"ATTR_ACME_BRAND_BOOST", reg.HasAttribute("ATTR_ACME_BRAND_BOOST")},
		{"FILTER_ACME_BRAND_HIGH", reg.HasFilterer("FILTER_ACME_BRAND_HIGH")},
		{"GROUP_ACME_BRAND_PARITY", reg.HasGrouper("GROUP_ACME_BRAND_PARITY")},
		{"WIN_ACME_BRAND_CONST", reg.HasWindow("WIN_ACME_BRAND_CONST")},
		{"FEAT_ACME_BRAND_DOUBLE", reg.HasFeature("FEAT_ACME_BRAND_DOUBLE")},
		{"TEST_ACME_BRAND_ROW", reg.HasRowTest("TEST_ACME_BRAND_ROW")},
		{"TEST_ACME_BRAND_POST", reg.HasPostTest("TEST_ACME_BRAND_POST")},
	}
	for _, c := range cases {
		if !c.got {
			t.Errorf("%s did not resolve via overlay", c.name)
		}
	}
	// Streamable overlay was populated for each category — sanity-
	// check filterer (defaults streamable=true) and tier-2 test
	// (forced false).
	if !reg.IsStreamable("filterer", "FILTER_ACME_BRAND_HIGH") {
		t.Error("filterer streamable overlay should be true")
	}
	if reg.IsStreamable("test", "TEST_ACME_BRAND_POST") {
		t.Error("tier-2 test must be reported non-streamable")
	}
	if !reg.IsStreamable("test", "TEST_ACME_BRAND_ROW") {
		t.Error("tier-1 test with Streamable=true must surface as streamable")
	}

	// Snapshot built and reachable from descriptor side — exercises
	// the per-category snapshot loops in buildExtensionsSnapshot.
	snap := p.Service().ExtensionsSnapshot()
	if snap == nil {
		t.Fatal("expected non-nil ExtensionsSnapshot")
	}
	checkSnapshotPresent(t, snap.Aggregators, "AGG_ACME_BRAND_SCORE")
	checkSnapshotPresent(t, snap.Attributes, "ATTR_ACME_BRAND_BOOST")
	checkSnapshotPresent(t, snap.Filterers, "FILTER_ACME_BRAND_HIGH")
	checkSnapshotPresent(t, snap.Groupers, "GROUP_ACME_BRAND_PARITY")
	checkSnapshotPresent(t, snap.Windows, "WIN_ACME_BRAND_CONST")
	checkSnapshotPresent(t, snap.Features, "FEAT_ACME_BRAND_DOUBLE")
	checkSnapshotPresent(t, snap.Tests, "TEST_ACME_BRAND_ROW")
	checkSnapshotPresent(t, snap.Tests, "TEST_ACME_BRAND_POST")
	checkSnapshotPresent(t, snap.SynthDistributions, "SYNTH_ACME_BRAND_PARETO")
	if len(snap.ExprFunctions) == 0 || snap.ExprFunctions[0].Name != "rank_familiarity" {
		t.Errorf("ExprFunctions snapshot incomplete: %v", snap.ExprFunctions)
	}
	if len(snap.LookupTables) == 0 || snap.LookupTables[0].Name != "adjustments" {
		t.Errorf("LookupTables snapshot incomplete: %v", snap.LookupTables)
	}
}

func checkSnapshotPresent(t *testing.T, metas []descriptor.OperatorMeta, name string) {
	t.Helper()
	for _, m := range metas {
		if m.Name == name {
			return
		}
	}
	t.Errorf("snapshot missing operator %q", name)
}

// ---- ExtensionsAware probe: factory panic & nil returns ---------------

// stubPanicAttributeFactory raises a panic so safeBuildAttribute
// observes the recover() path (the 70%-covered branch in
// extensions_probe.go).
func stubPanicAttributeFactory(*types.Attribute, *encoding.Schema) (processing.AttributeComputer, error) {
	panic("intentional probe panic")
}

func TestExtensions_ProbeAttribute_FactoryPanicCaught(t *testing.T) {
	ext := pulse.Extensions{
		Attributes: []pulse.AttributeRegistration{{
			Name:    "ATTR_ACME_BRAND_PANIC",
			Factory: stubPanicAttributeFactory,
			Mode:    pulse.AttributeModeRowLocal,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_FACTORY_PANIC)
}

// Catches the "factory returned nil" branch in safeBuildAttribute.
func stubNilAttributeFactory(*types.Attribute, *encoding.Schema) (processing.AttributeComputer, error) {
	return nil, nil
}

func TestExtensions_ProbeAttribute_FactoryReturnsNil(t *testing.T) {
	ext := pulse.Extensions{
		Attributes: []pulse.AttributeRegistration{{
			Name:    "ATTR_ACME_BRAND_NIL",
			Factory: stubNilAttributeFactory,
			Mode:    pulse.AttributeModeRowLocal,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	if err == nil {
		t.Fatal("expected error when attribute factory returns nil computer")
	}
}

// ---- Synth registration round-trip (covers SynthDistributions path) --

func TestExtensions_SynthDistributionRegistrationStable(t *testing.T) {
	ext := pulse.Extensions{
		SynthDistributions: []pulse.DistributionRegistration{
			{
				Name:        "SYNTH_ACME_BRAND_CUSTOM",
				Description: "ACME custom distribution.",
				Params: []pulse.ParamMeta{
					{Name: "alpha", JSONType: "number", Required: true},
				},
			},
		},
	}
	p, err := pulse.New(newTestOptions(ext))
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	snap := p.Service().ExtensionsSnapshot()
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	found := false
	for _, m := range snap.SynthDistributions {
		if m.Name == "SYNTH_ACME_BRAND_CUSTOM" {
			found = true
			if len(m.Params) == 0 || m.Params[0].Name != "alpha" {
				t.Errorf("alpha param missing in snapshot: %+v", m.Params)
			}
		}
	}
	if !found {
		t.Error("custom synth distribution missing from snapshot")
	}
	// Sanity: built-in pareto remains reachable via synth manifest;
	// the override is name-only at this phase.
	if !slices.Contains(synth.AllDistributions(), "pareto") {
		t.Error("built-in pareto must still be enumerated")
	}
}

// ---- LookupTable empty-name path (validateLookupTables 87.5%) --------

func TestExtensions_LookupTableEmptyName(t *testing.T) {
	ext := pulse.Extensions{
		LookupTables: map[string]pulse.LookupTable{
			"   ": {Rows: map[string]float64{"k": 1}},
		},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

// ---- parseNamespace edge case: synth distribution names ---------------

func TestExtensions_SynthDistributionNamespaceParsed(t *testing.T) {
	ext := pulse.Extensions{
		SynthDistributions: []pulse.DistributionRegistration{
			{Name: "SYNTH_ACME_BRAND_FOO"},
		},
	}
	p, err := pulse.New(newTestOptions(ext))
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	snap := p.Service().ExtensionsSnapshot()
	if snap == nil {
		t.Fatal("snap nil")
	}
	if len(snap.SynthDistributions) == 0 {
		t.Fatal("synth distributions empty")
	}
	if snap.SynthDistributions[0].Namespace != "ACME" {
		t.Errorf("expected ACME namespace, got %q", snap.SynthDistributions[0].Namespace)
	}
}

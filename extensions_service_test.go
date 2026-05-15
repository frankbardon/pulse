package pulse_test

import (
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestExtensions_RegistryInstalledOnService asserts that pulse.New
// wires the converted ExtensionRegistry into the underlying service
// and that lookups resolve via the overlay.
func TestExtensions_RegistryInstalledOnService(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			{
				Name:        "AGG_ACME_BRAND_SCORE",
				Description: "ACME brand composite score.",
				Factory:     stubAggregatorFactory,
				Streamable:  true,
			},
		},
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New rejected valid extension: %v", err)
	}
	reg := p.Service().Extensions()
	if reg == nil {
		t.Fatal("expected non-nil ExtensionRegistry on service after registration")
	}
	if _, ok := reg.LookupAggregator("AGG_ACME_BRAND_SCORE"); !ok {
		t.Fatal("custom aggregator did not resolve via service registry")
	}
	if !reg.IsStreamable("aggregator", "AGG_ACME_BRAND_SCORE") {
		t.Error("expected custom aggregator to honour Streamable=true via overlay")
	}
}

// TestExtensions_ZeroValueProducesNilRegistry asserts that zero-value
// Options produces a nil ExtensionRegistry on the service — the
// runtime takes the built-in-only path with no overlay lookups.
func TestExtensions_ZeroValueProducesNilRegistry(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("pulse.New rejected zero options: %v", err)
	}
	if reg := p.Service().Extensions(); reg != nil {
		t.Errorf("expected nil ExtensionRegistry for zero-value Extensions, got %#v", reg)
	}
}

// TestExtensions_RegistryIsolationAcrossInstances asserts that two
// pulse.New calls with different Extensions inputs hold disjoint
// registries — required so a process can host multiple Pulse
// services without leaking registrations across them.
func TestExtensions_RegistryIsolationAcrossInstances(t *testing.T) {
	a, err := pulse.New(pulse.Options{
		FS: afero.NewMemMapFs(),
		Extensions: pulse.Extensions{
			Aggregators: []pulse.AggregatorRegistration{
				{Name: "AGG_ACME_A", Factory: stubAggregatorFactory},
			},
		},
	})
	if err != nil {
		t.Fatalf("pulse a: %v", err)
	}
	b, err := pulse.New(pulse.Options{
		FS: afero.NewMemMapFs(),
		Extensions: pulse.Extensions{
			Aggregators: []pulse.AggregatorRegistration{
				{Name: "AGG_ACME_B", Factory: stubAggregatorFactory},
			},
		},
	})
	if err != nil {
		t.Fatalf("pulse b: %v", err)
	}

	regA := a.Service().Extensions()
	regB := b.Service().Extensions()
	if _, ok := regA.LookupAggregator("AGG_ACME_B"); ok {
		t.Error("instance a saw instance b's overlay")
	}
	if _, ok := regB.LookupAggregator("AGG_ACME_A"); ok {
		t.Error("instance b saw instance a's overlay")
	}
}

// TestExtensions_RegistryFallsThroughToBuiltins asserts that the
// installed overlay still resolves built-in names through the
// fallthrough path. This is critical: a custom registration must
// never shadow the built-in registry from view, only override it.
func TestExtensions_RegistryFallsThroughToBuiltins(t *testing.T) {
	p, err := pulse.New(pulse.Options{
		FS: afero.NewMemMapFs(),
		Extensions: pulse.Extensions{
			Aggregators: []pulse.AggregatorRegistration{
				{Name: "AGG_ACME_BRAND_SCORE", Factory: stubAggregatorFactory},
			},
		},
	})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	reg := p.Service().Extensions()
	if _, ok := reg.LookupAggregator(types.AGG_COUNT); !ok {
		t.Error("overlay path must fall through to built-in AGG_COUNT")
	}
	if _, ok := reg.LookupAttribute(types.ATTR_ZSCORE); !ok {
		t.Error("overlay path must fall through to built-in ATTR_ZSCORE")
	}
}

// TestExtensions_OnlyExprEntriesYieldsRegistry asserts that expr-
// side entries on their own still produce a non-nil
// ExtensionRegistry — the expr environment cannot reach them
// otherwise. Operator-category maps stay empty.
func TestExtensions_OnlyExprEntriesYieldsRegistry(t *testing.T) {
	p, err := pulse.New(pulse.Options{
		FS: afero.NewMemMapFs(),
		Extensions: pulse.Extensions{
			ExprFunctions: []pulse.ExprFunction{
				{Name: "rank_familiarity", Fn: func(args ...any) (any, error) { return args[0], nil }},
			},
			LookupTables: map[string]pulse.LookupTable{
				"adjustments": {Rows: map[string]float64{"x": 1}},
			},
		},
	})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	reg := p.Service().Extensions()
	if reg == nil {
		t.Fatal("expected non-nil ExtensionRegistry when expr entries registered")
	}
	if len(reg.ExprFunctions) != 1 || reg.ExprFunctions[0].Name != "rank_familiarity" {
		t.Errorf("ExprFunctions = %+v; want one rank_familiarity entry", reg.ExprFunctions)
	}
	if _, ok := reg.LookupTables["adjustments"]; !ok {
		t.Errorf("LookupTables missing adjustments; got %v", reg.LookupTables)
	}
}

// TestExtensions_AttributeStreamabilityFromMode asserts that the
// registered AttributeMode flows into the runtime Streamable
// override: row_local / two_pass are streamable, buffered is not.
func TestExtensions_AttributeStreamabilityFromMode(t *testing.T) {
	for _, tc := range []struct {
		mode  pulse.AttributeMode
		label string
		want  bool
	}{
		{pulse.AttributeModeRowLocal, "ROW_LOCAL", true},
		{pulse.AttributeModeTwoPass, "TWO_PASS", true},
		{pulse.AttributeModeBuffered, "BUFFERED", false},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			name := types.AttributeType("ATTR_ACME_" + tc.label)
			p, err := pulse.New(pulse.Options{
				FS: afero.NewMemMapFs(),
				Extensions: pulse.Extensions{
					Attributes: []pulse.AttributeRegistration{{
						Name:    name,
						Factory: stubAttributeFactory,
						Mode:    tc.mode,
					}},
				},
			})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			got := p.Service().Extensions().IsStreamable("attribute", string(name))
			if got != tc.want {
				t.Errorf("mode=%s: IsStreamable=%v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}

// stubAggregatorFactoryRef is needed to keep the processing import
// non-empty in this file when other helpers stay test-local. Pinned
// here so a future refactor that drops the last processing reference
// still surfaces a meaningful build error.
var _ = processing.AggregatorFactory(nil)

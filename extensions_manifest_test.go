package pulse_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// TestExtensions_Manifest_EmissionPopulatesAllCategories asserts that
// a manifest fetched from a Pulse instance with extensions registered
// surfaces every category in Manifest.Extensions, with Names parsed
// into Namespaces and Params copied through.
func TestExtensions_Manifest_EmissionPopulatesAllCategories(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:        "AGG_ACME_BRAND_SCORE",
			Description: "ACME brand composite",
			Factory:     stubAggregatorFactory,
			Streamable:  true,
			Accepts:     []encoding.FieldType{encoding.FieldTypeF64},
			Params:      []pulse.ParamMeta{{Name: "weights", JSONType: "array"}},
		}},
		Attributes: []pulse.AttributeRegistration{{
			Name:        "ATTR_ACME_ADJUSTMENT",
			Description: "ACME adjustment",
			Factory:     stubAttributeFactory,
			Mode:        pulse.AttributeModeRowLocal,
		}},
		ExprFunctions: []pulse.ExprFunction{{
			Name:        "rank_familiarity",
			Description: "rank familiarity helper",
			Signature:   "rank_familiarity(v float64) float64",
			Fn:          func(v float64) float64 { return v },
			Pure:        true,
		}},
		LookupTables: map[string]pulse.LookupTable{
			"adjustments": {
				Description: "wave calibration multipliers",
				Rows:        map[string]float64{"k": 1},
			},
		},
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	manifest := p.Manifest(context.Background())

	if len(manifest.Extensions.Aggregators) != 1 {
		t.Fatalf("expected 1 aggregator in manifest, got %d", len(manifest.Extensions.Aggregators))
	}
	agg := manifest.Extensions.Aggregators[0]
	if agg.Name != "AGG_ACME_BRAND_SCORE" || agg.Namespace != "ACME" {
		t.Errorf("agg name/namespace = %s/%s, want AGG_ACME_BRAND_SCORE/ACME", agg.Name, agg.Namespace)
	}
	if !agg.Streamable {
		t.Error("agg.Streamable = false, want true")
	}
	if len(agg.Params) != 1 || agg.Params[0].Name != "weights" {
		t.Errorf("agg params = %+v, want one weights param", agg.Params)
	}
	if len(agg.Accepts) != 1 {
		t.Errorf("agg accepts = %v; want one f64 entry", agg.Accepts)
	}

	if len(manifest.Extensions.Attributes) != 1 {
		t.Fatalf("expected 1 attribute in manifest, got %d", len(manifest.Extensions.Attributes))
	}
	attr := manifest.Extensions.Attributes[0]
	if attr.Mode != string(pulse.AttributeModeRowLocal) {
		t.Errorf("attr.Mode = %q, want row_local", attr.Mode)
	}

	if len(manifest.Extensions.ExprFunctions) != 1 {
		t.Fatalf("expected 1 expr function, got %d", len(manifest.Extensions.ExprFunctions))
	}
	if manifest.Extensions.ExprFunctions[0].Signature == "" {
		t.Error("expr function signature lost in manifest")
	}

	if len(manifest.Extensions.LookupTables) != 1 {
		t.Fatalf("expected 1 lookup table, got %d", len(manifest.Extensions.LookupTables))
	}
	if !manifest.Extensions.LookupTables[0].HasRowsData {
		t.Error("LookupTable.HasRowsData = false, want true (Rows is populated)")
	}
}

// TestExtensions_Manifest_EmptyWhenNoExtensions asserts the manifest's
// Extensions block is present but empty (every slice is []) when no
// extensions are registered. Stable JSON shape across hosts.
func TestExtensions_Manifest_EmptyWhenNoExtensions(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	manifest := p.Manifest(context.Background())
	em := manifest.Extensions
	if em.Aggregators == nil || len(em.Aggregators) != 0 {
		t.Errorf("Aggregators = %v; want empty non-nil slice", em.Aggregators)
	}
	if em.ExprFunctions == nil || len(em.ExprFunctions) != 0 {
		t.Errorf("ExprFunctions = %v; want empty non-nil slice", em.ExprFunctions)
	}
	if em.LookupTables == nil || len(em.LookupTables) != 0 {
		t.Errorf("LookupTables = %v; want empty non-nil slice", em.LookupTables)
	}
}

// TestExtensions_Manifest_DeterministicSort asserts that the
// Extensions sub-slices are sorted by Name regardless of registration
// order. The manifest must byte-match across runs.
func TestExtensions_Manifest_DeterministicSort(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			{Name: "AGG_ACME_ZULU", Factory: stubAggregatorFactory},
			{Name: "AGG_ACME_ALPHA", Factory: stubAggregatorFactory},
			{Name: "AGG_ACME_MIKE", Factory: stubAggregatorFactory},
		},
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	manifest := p.Manifest(context.Background())
	names := make([]string, len(manifest.Extensions.Aggregators))
	for i, a := range manifest.Extensions.Aggregators {
		names[i] = a.Name
	}
	want := []string{"AGG_ACME_ALPHA", "AGG_ACME_MIKE", "AGG_ACME_ZULU"}
	for i := range names {
		if names[i] != want[i] {
			t.Errorf("manifest order[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

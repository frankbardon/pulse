package descriptor

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

func componentKeyNames(s descriptor.ComponentSchema) []string {
	out := make([]string, 0, len(s.Keys))
	for _, k := range s.Keys {
		out = append(out, k.Name)
	}
	return out
}

// TestManifestMatrixComponentSchemasComplete: every matrix operator
// declares its Response.Components.Matrices entry — the floor
// {n, n_null, n_listwise_dropped}, the optional pairwise and weighted
// keys (types.MatrixComponents wire order), then its own keys — Mergeable, and the top-level
// components_schemas.matrices mirrors the per-operator entry.
func TestManifestMatrixComponentSchemasComplete(t *testing.T) {
	m := BuildManifest()
	want := map[types.MatrixType][]string{
		types.MAT_COVARIANCE:  {"n", "n_null", "n_listwise_dropped", "min_pair_n", "max_pair_n", "sum_weights", "n_eff", "n_weight_invalid", "ddof"},
		types.MAT_CORRELATION: {"n", "n_null", "n_listwise_dropped", "min_pair_n", "max_pair_n", "sum_weights", "n_eff", "n_weight_invalid"},
		// The floor counts the folded columns (members and outside
		// controls).
		types.MAT_PARTIAL_CORRELATION: {"n", "n_null", "n_listwise_dropped", "min_pair_n", "max_pair_n", "sum_weights", "n_eff", "n_weight_invalid"},
		// The minres fit behind omega: optional, absent when no fit ran.
		types.MAT_RELIABILITY: {"n", "n_null", "n_listwise_dropped", "min_pair_n", "max_pair_n", "sum_weights", "n_eff", "n_weight_invalid", "iterations", "converged"},
	}
	if len(want) != len(types.AllMatrixTypes()) {
		t.Fatalf("table covers %d matrix types, registry has %d", len(want), len(types.AllMatrixTypes()))
	}
	if got := len(m.ComponentsSchemas.Matrices); got != len(types.AllMatrixTypes()) {
		t.Fatalf("components_schemas.matrices has %d entries, want %d", got, len(types.AllMatrixTypes()))
	}
	for _, meta := range m.Matrices {
		keys, ok := want[types.MatrixType(meta.Name)]
		if !ok {
			t.Fatalf("unexpected matrix operator %q", meta.Name)
		}
		if got := componentKeyNames(meta.ComponentSchema); !reflect.DeepEqual(got, keys) {
			t.Errorf("%s keys = %v, want %v", meta.Name, got, keys)
		}
		if meta.ComponentSchema.Mergeability != descriptor.Mergeable {
			t.Errorf("%s mergeability = %q, want mergeable", meta.Name, meta.ComponentSchema.Mergeability)
		}
		for _, k := range meta.ComponentSchema.Keys {
			floor := k.Name == "n" || k.Name == "n_null" || k.Name == "n_listwise_dropped" || k.Name == "ddof"
			if k.Optional == floor {
				t.Errorf("%s key %s optional = %v", meta.Name, k.Name, k.Optional)
			}
		}
		if !reflect.DeepEqual(m.ComponentsSchemas.Matrices[meta.Name], meta.ComponentSchema) {
			t.Errorf("%s: components_schemas.matrices entry differs from the per-operator one", meta.Name)
		}
	}
}

// TestManifestMatrixComponentSchemas_WeightingHidden: with
// capability:weighting hidden the weighted floor keys are not
// declared (an instance that cannot weight never emits them), on
// either surface.
func TestManifestMatrixComponentSchemas_WeightingHidden(t *testing.T) {
	var enabled []string
	for _, n := range FeatureNames() {
		if n != featWeighting {
			enabled = append(enabled, n)
		}
	}
	m := BuildManifestForInstance(NewInstanceSnapshot(nil, FeatureSet{Enabled: enabled, Hidden: []string{featWeighting}}))
	if len(m.Matrices) != len(types.AllMatrixTypes()) {
		t.Fatalf("instance lists %d matrix operators, want %d", len(m.Matrices), len(types.AllMatrixTypes()))
	}
	for _, meta := range m.Matrices {
		for _, s := range []descriptor.ComponentSchema{meta.ComponentSchema, m.ComponentsSchemas.Matrices[meta.Name]} {
			for _, k := range s.Keys {
				if k.Name == "sum_weights" || k.Name == "n_eff" || k.Name == "n_weight_invalid" {
					t.Errorf("%s declares %s with capability:weighting hidden", meta.Name, k.Name)
				}
			}
		}
	}
}

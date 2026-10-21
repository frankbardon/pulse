package descriptor

import (
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// hasKey reports whether schema declares a key named name.
func hasKey(schema descriptor.ComponentSchema, name string) bool {
	return slices.ContainsFunc(schema.Keys, func(k descriptor.ComponentKey) bool { return k.Name == name })
}

// TestAggregatorSchemaIndex_BuiltOncePerWeightingVariant (#390, U51
// E1-S2): predict's built-in aggregator schema map is built once per
// weighting variant — the same map on every call — and equals a fresh
// build of the table it caches: the weighted floor keys spliced in when
// capability:weighting is enabled, absent when it is hidden.
// aggregatorComponentSchemaIndex picks the variant off the instance.
func TestAggregatorSchemaIndex_BuiltOncePerWeightingVariant(t *testing.T) {
	for _, weighted := range []bool{false, true} {
		a, b := builtinAggregatorSchemaIndex(weighted), builtinAggregatorSchemaIndex(weighted)
		if reflect.ValueOf(a).UnsafePointer() != reflect.ValueOf(b).UnsafePointer() {
			t.Errorf("weighted=%v: two calls built two maps, want the one cached map", weighted)
		}
		fresh := aggregatorCapabilityTable()
		if weighted {
			fresh = aggregatorCapabilities()
		}
		if !reflect.DeepEqual(a, componentSchemaIndexOf(fresh)) {
			t.Errorf("weighted=%v: the cached map differs from a fresh build of its table", weighted)
		}
		if got := hasKey(a[string(types.AGG_SUM)], "sum_weights"); got != weighted {
			t.Errorf("weighted=%v: AGG_SUM carries sum_weights = %v", weighted, got)
		}
	}
	if reflect.ValueOf(builtinAggregatorSchemaIndex(true)).UnsafePointer() == reflect.ValueOf(builtinAggregatorSchemaIndex(false)).UnsafePointer() {
		t.Fatal("the weighted and unweighted variants share one map")
	}

	sum := string(types.AGG_SUM)
	if !hasKey(aggregatorComponentSchemaIndex(nil)[sum], "sum_weights") {
		t.Error("default instance: AGG_SUM lacks the weighted floor keys")
	}
	hidden := &PredictOptions{Instance: scopedExcept(featWeighting)}
	if hasKey(aggregatorComponentSchemaIndex(hidden)[sum], "sum_weights") {
		t.Error("capability:weighting hidden: AGG_SUM still carries the weighted floor keys")
	}
	if got := AggregatorMergeability(sum); got != builtinAggregatorSchemaIndex(true)[sum].Mergeability || got == "" {
		t.Errorf("AggregatorMergeability(AGG_SUM) = %q", got)
	}
	if got := AggregatorMergeability("AGG_NOT_REGISTERED"); got != "" {
		t.Errorf("AggregatorMergeability(unknown) = %q, want empty", got)
	}
}

// TestAggregatorSchemaIndex_SharedMapIsolated (#390): the cached map is
// shared, so nothing a caller is handed may alias it — the manifest's
// fresh table (which it filters, sorts and stamps), a predict result's
// per-slot schema (its consumer may mutate it) and an
// extension-carrying index (a copy with the extension's schema) all
// leave the cached map untouched.
func TestAggregatorSchemaIndex_SharedMapIsolated(t *testing.T) {
	sum := string(types.AGG_SUM)
	snapshot := func() descriptor.ComponentSchema {
		cs := builtinAggregatorSchemaIndex(true)[sum]
		cs.Keys = slices.Clone(cs.Keys)
		return cs
	}
	want := snapshot()

	// The manifest path: a fresh table, mutated freely.
	for _, op := range aggregatorCapabilities() {
		if op.Name == sum {
			op.ComponentSchema.Keys[0].Name = "manifest-mutated"
		}
	}

	// A predict result's per-slot schema.
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}}
	result := &descriptor.PredictResult{}
	populateAggregationPredicts(result, req, schema, nil)
	if len(result.Aggregations) != 1 || len(result.Aggregations[0].ComponentSchema.Keys) == 0 {
		t.Fatalf("predict aggregations = %+v", result.Aggregations)
	}
	result.Aggregations[0].ComponentSchema.Keys[0].Name = "result-mutated"

	// An extension aggregator's declared schema rides a copy.
	ext := "AGG_ACME_TALLY"
	opts := &PredictOptions{Extensions: &ExtensionsSnapshot{
		Aggregators:      []descriptor.OperatorMeta{{Name: ext}},
		ComponentSchemas: map[string]descriptor.ComponentSchema{ext: {Mergeability: descriptor.Mergeable}},
	}}
	withExt := aggregatorComponentSchemaIndex(opts)
	if _, ok := withExt[ext]; !ok {
		t.Fatal("the extension's schema is missing from its index")
	}
	if _, ok := builtinAggregatorSchemaIndex(true)[ext]; ok {
		t.Fatal("the extension's schema leaked into the shared built-in map")
	}
	withExt[sum] = descriptor.ComponentSchema{}

	if got := builtinAggregatorSchemaIndex(true)[sum]; !reflect.DeepEqual(got, want) {
		t.Fatalf("the cached AGG_SUM schema changed under a caller's mutation:\n got %+v\nwant %+v", got, want)
	}
	// With no extension schema the shared map itself is returned.
	if reflect.ValueOf(aggregatorComponentSchemaIndex(&PredictOptions{Extensions: &ExtensionsSnapshot{}})).UnsafePointer() != reflect.ValueOf(builtinAggregatorSchemaIndex(true)).UnsafePointer() {
		t.Error("an index with no extension schema copied the shared map")
	}
}

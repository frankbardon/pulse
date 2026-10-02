package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func dummyAggregatorFactory(*types.Aggregation, *encoding.Schema) (Aggregator, error) {
	return nil, nil
}

func TestExtensionRegistry_NilFallsThroughToBuiltin(t *testing.T) {
	var r *ExtensionRegistry
	if _, ok := r.LookupAggregator(types.AGG_COUNT); !ok {
		t.Fatal("nil registry must fall through to built-in aggregator registry")
	}
	if _, ok := r.LookupAttribute(types.ATTR_FORMULA); !ok {
		t.Fatal("nil registry must fall through to built-in attribute registry")
	}
	if _, ok := r.LookupFilterer(types.FILTER_INCLUDE); !ok {
		t.Fatal("nil registry must fall through to built-in filterer registry")
	}
	if _, ok := r.LookupGrouper(types.GROUP_CATEGORY); !ok {
		t.Fatal("nil registry must fall through to built-in grouper registry")
	}
	if _, ok := r.LookupWindow(types.WIN_LAG); !ok {
		t.Fatal("nil registry must fall through to built-in window registry")
	}
	if _, ok := r.LookupFeature(types.FEAT_LOG); !ok {
		t.Fatal("nil registry must fall through to built-in feature registry")
	}
	if _, ok := r.LookupRowTest(types.TEST_T); !ok {
		t.Fatal("nil registry must fall through to built-in row-test registry")
	}
	if _, ok := r.LookupPostTest(types.TEST_TUKEY_HSD); !ok {
		t.Fatal("nil registry must fall through to built-in post-test registry")
	}
}

func TestExtensionRegistry_OverlayWinsOverBuiltin(t *testing.T) {
	// Built-in AGG_COUNT exists; overlay shadows it. Lookup must
	// return the overlay factory, not the built-in.
	sentinel := AggregatorFactory(func(*types.Aggregation, *encoding.Schema) (Aggregator, error) {
		return nil, nil
	})
	r := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			types.AGG_COUNT: sentinel,
		},
	}
	got, ok := r.LookupAggregator(types.AGG_COUNT)
	if !ok {
		t.Fatal("expected overlay aggregator to resolve")
	}
	// Function values cannot be == compared safely in Go; assert
	// they have the same package-level pointer by calling each and
	// confirming the overlay we set is reachable.
	if fp1, fp2 := funcPointer(got), funcPointer(sentinel); fp1 != fp2 {
		t.Errorf("overlay factory not returned by LookupAggregator (got %#x, want %#x)", fp1, fp2)
	}
}

func TestExtensionRegistry_CustomAggregatorResolves(t *testing.T) {
	r := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			"AGG_ACME_BRAND_SCORE": dummyAggregatorFactory,
		},
	}
	if _, ok := r.LookupAggregator("AGG_ACME_BRAND_SCORE"); !ok {
		t.Fatal("custom aggregator not resolved via overlay")
	}
	if _, ok := r.LookupAggregator("AGG_ACME_NOT_REGISTERED"); ok {
		t.Fatal("unknown aggregator should not resolve")
	}
}

func TestExtensionRegistry_IsStreamableOverridesBuiltin(t *testing.T) {
	// AGG_MEDIAN is built-in non-streamable. An overlay marking it
	// streamable must be observed by IsStreamable. Useful when an
	// embedder replaces a buffered operator with a streaming-capable
	// variant.
	r := &ExtensionRegistry{
		Streamable: map[string]bool{
			StreamabilityKey("aggregator", "AGG_MEDIAN"): true,
		},
	}
	if !r.IsStreamable("aggregator", "AGG_MEDIAN") {
		t.Fatal("overlay must override built-in AGG_MEDIAN streamability")
	}
}

func TestExtensionRegistry_IsStreamableFallsBackToTypeSwitch(t *testing.T) {
	r := &ExtensionRegistry{}
	if !r.IsStreamable("aggregator", "AGG_COUNT") {
		t.Error("AGG_COUNT must be streamable via built-in fallback")
	}
	if r.IsStreamable("aggregator", "AGG_MEDIAN") {
		t.Error("AGG_MEDIAN must NOT be streamable via built-in fallback")
	}
	if r.IsStreamable("unknown_category", "FOO") {
		t.Error("unknown category must report not streamable")
	}
}

func TestExtensionRegistry_IsolationBetweenRegistries(t *testing.T) {
	// Two registries with different overlays must not leak entries.
	a := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			"AGG_ACME_A": dummyAggregatorFactory,
		},
	}
	b := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			"AGG_ACME_B": dummyAggregatorFactory,
		},
	}
	if _, ok := a.LookupAggregator("AGG_ACME_B"); ok {
		t.Error("registry a should not see registry b's overlay")
	}
	if _, ok := b.LookupAggregator("AGG_ACME_A"); ok {
		t.Error("registry b should not see registry a's overlay")
	}
}

func TestExtensionRegistry_CustomNamesEnumerateOverlayOnly(t *testing.T) {
	r := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			"AGG_ACME_FOO": dummyAggregatorFactory,
			"AGG_ACME_BAR": dummyAggregatorFactory,
		},
	}
	names := r.CustomAggregatorNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 custom aggregator names, got %d (%v)", len(names), names)
	}
	for _, n := range names {
		if _, ok := r.Aggregators[n]; !ok {
			t.Errorf("CustomAggregatorNames returned %q not present in overlay", n)
		}
	}
}

// funcPointer extracts the underlying function pointer of an
// AggregatorFactory for identity comparison. Go does not support
// function == directly, so this helper bridges via reflection.
func funcPointer(f AggregatorFactory) uintptr {
	if f == nil {
		return 0
	}
	return funcPointerImpl(f)
}

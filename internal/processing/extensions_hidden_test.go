package processing

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/types"
)

// hiddenSet is a feature-set predicate over a fixed name list.
func hiddenSet(names ...string) func(string) bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return func(n string) bool { return m[n] }
}

// hiddenLookupCase is one registry lookup a hidden name must miss.
type hiddenLookupCase struct {
	name   string // the built-in the case hides
	lookup func(r *ExtensionRegistry, name string) bool
}

var hiddenLookupCases = []hiddenLookupCase{
	{"AGG_MAX", func(r *ExtensionRegistry, n string) bool {
		_, ok := r.LookupAggregator(types.AggregationType(n))
		return ok
	}},
	{"ATTR_ZSCORE", func(r *ExtensionRegistry, n string) bool {
		_, ok := r.LookupAttribute(types.AttributeType(n))
		return ok
	}},
	{"FILTER_EXCLUDE", func(r *ExtensionRegistry, n string) bool { _, ok := r.LookupFilterer(types.FiltererType(n)); return ok }},
	{"GROUP_ROUNDED", func(r *ExtensionRegistry, n string) bool { _, ok := r.LookupGrouper(types.GroupType(n)); return ok }},
	{"WIN_RANK", func(r *ExtensionRegistry, n string) bool { _, ok := r.LookupWindow(types.WindowType(n)); return ok }},
	{"FEAT_LOG", func(r *ExtensionRegistry, n string) bool { _, ok := r.LookupFeature(types.FeatureType(n)); return ok }},
	{"TEST_SHAPIRO_WILK", func(r *ExtensionRegistry, n string) bool { _, ok := r.LookupRowTest(types.TestType(n)); return ok }},
	{"TEST_TREND", func(r *ExtensionRegistry, n string) bool { _, ok := r.LookupPostTest(types.TestType(n)); return ok }},
	{"AGG_MAX", func(r *ExtensionRegistry, n string) bool { return r.HasAggregator(types.AggregationType(n)) }},
	{"GROUP_ROUNDED", func(r *ExtensionRegistry, n string) bool { return r.HasGrouper(types.GroupType(n)) }},
}

// TestExtensionRegistry_HiddenLookupsMiss: each of the 8 Lookup* sites
// (and the Has* helpers over them) answers a hidden built-in exactly as
// a never-registered name — not found — while the same built-in still
// resolves without the predicate, on a nil registry and on a registry
// that hides something else.
func TestExtensionRegistry_HiddenLookupsMiss(t *testing.T) {
	for _, tc := range hiddenLookupCases {
		t.Run(tc.name, func(t *testing.T) {
			var nilReg *ExtensionRegistry
			if !tc.lookup(nilReg, tc.name) {
				t.Fatalf("%s does not resolve on a nil registry; the case is vacuous", tc.name)
			}
			if !tc.lookup(nilReg.WithHidden(hiddenSet("SOMETHING_ELSE")), tc.name) {
				t.Errorf("%s stopped resolving under an unrelated hidden set", tc.name)
			}
			scoped := nilReg.WithHidden(hiddenSet(tc.name))
			if tc.lookup(scoped, tc.name) {
				t.Errorf("hidden %s still resolves", tc.name)
			}
			if tc.lookup(scoped, tc.name+"_NEVER_REGISTERED") {
				t.Errorf("never-registered name resolves")
			}
		})
	}
}

// TestExtensionRegistry_HiddenRoutingFacts: the streamability,
// mergeability, margin-class and two-pass facts that pick an execution
// path report a hidden built-in the way they report a never-registered
// name, so a hidden name cannot take a different route to its error.
func TestExtensionRegistry_HiddenRoutingFacts(t *testing.T) {
	const never = "_NEVER_REGISTERED"
	scoped := (*ExtensionRegistry)(nil).WithHidden(hiddenSet("AGG_SUM", "GROUP_CATEGORY", "ATTR_ZSCORE", "TEST_T", "AGG_AVERAGE"))

	for _, c := range []struct{ category, name string }{
		{"aggregator", "AGG_SUM"}, {"grouper", "GROUP_CATEGORY"},
		{"attribute", "ATTR_ZSCORE"}, {"test", "TEST_T"},
	} {
		var nilReg *ExtensionRegistry
		if !nilReg.IsStreamable(c.category, c.name) {
			t.Fatalf("%s not streamable unscoped; vacuous", c.name)
		}
		if got, want := scoped.IsStreamable(c.category, c.name), scoped.IsStreamable(c.category, c.name+never); got != want {
			t.Errorf("IsStreamable(%s) = %v, never-registered = %v", c.name, got, want)
		}
	}
	for _, c := range []struct{ category, name string }{{"aggregator", "AGG_SUM"}, {"grouper", "GROUP_CATEGORY"}} {
		if !(*ExtensionRegistry)(nil).IsMergeable(c.category, c.name) {
			t.Fatalf("%s not mergeable unscoped; vacuous", c.name)
		}
		if got, want := scoped.IsMergeable(c.category, c.name), scoped.IsMergeable(c.category, c.name+never); got != want {
			t.Errorf("IsMergeable(%s) = %v, never-registered = %v", c.name, got, want)
		}
	}
	if types.AGG_AVERAGE.MarginReducibility() == types.MarginRecompute {
		t.Fatal("AGG_AVERAGE is already recompute; pick a fusable aggregator")
	}
	if got, want := scoped.AggregatorMarginReducibility(types.AGG_AVERAGE), scoped.AggregatorMarginReducibility(types.AggregationType("AGG_AVERAGE"+never)); got != want {
		t.Errorf("AggregatorMarginReducibility(hidden) = %q, never-registered = %q", got, want)
	}
	if !(*ExtensionRegistry)(nil).attributeRequiresTwoPass(types.ATTR_ZSCORE) {
		t.Fatal("ATTR_ZSCORE not two-pass unscoped; vacuous")
	}
	if scoped.attributeRequiresTwoPass(types.ATTR_ZSCORE) {
		t.Error("hidden ATTR_ZSCORE still takes the two-pass drive")
	}
}

// TestExtensionRegistry_HiddenMergeRefusal: the merge gate (shard /
// parallel-decode fan-out and the ProcessChain stage gate) refuses a
// request naming a hidden built-in with the reason it gives a
// never-registered name, per category — including ATTR_FORMULA, which
// the gate otherwise admits from its built-in row-local list.
func TestExtensionRegistry_HiddenMergeRefusal(t *testing.T) {
	const never = "_NEVER_REGISTERED"
	base := func() *types.Request {
		return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x"}}}
	}
	cases := []struct {
		name string
		set  func(r *types.Request, name string)
	}{
		{"AGG_SUM", func(r *types.Request, n string) {
			r.Aggregations = []*types.Aggregation{{Type: types.AggregationType(n), Field: "x"}}
		}},
		{"GROUP_CATEGORY", func(r *types.Request, n string) {
			r.Groups = []*types.Group{{Type: types.GroupType(n), Field: "x"}}
		}},
		{"FILTER_INCLUDE", func(r *types.Request, n string) {
			r.Filterers = []*types.Filterer{{Type: types.FiltererType(n), Field: "x"}}
		}},
		{"ATTR_FORMULA", func(r *types.Request, n string) {
			r.Attributes = []*types.Attribute{{Type: types.AttributeType(n), Field: "x"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scoped := (*ExtensionRegistry)(nil).WithHidden(hiddenSet(tc.name))
			visible := base()
			tc.set(visible, tc.name)
			if r := mergegate.MergeRefusal(visible, nil, (*ExtensionRegistry)(nil).mergeFacts()); r != "" {
				t.Fatalf("%s refused unscoped (%s); vacuous", tc.name, r)
			}
			hidden := base()
			tc.set(hidden, tc.name)
			unknown := base()
			tc.set(unknown, tc.name+never)
			got := mergegate.MergeRefusal(hidden, nil, scoped.mergeFacts())
			want := mergegate.MergeRefusal(unknown, nil, scoped.mergeFacts())
			if want == "" {
				t.Fatal("never-registered name merges; vacuous")
			}
			if strings.ReplaceAll(want, tc.name+never, tc.name) != got {
				t.Errorf("hidden refusal %q, never-registered %q", got, want)
			}
		})
	}
}

// TestExtensionRegistry_WithHiddenNilPredicate: no predicate returns
// the registry unchanged — a profile-free instance keeps a nil registry.
func TestExtensionRegistry_WithHiddenNilPredicate(t *testing.T) {
	var nilReg *ExtensionRegistry
	if got := nilReg.WithHidden(nil); got != nil {
		t.Errorf("nil.WithHidden(nil) = %p, want nil", got)
	}
	r := &ExtensionRegistry{}
	if got := r.WithHidden(nil); got != r {
		t.Error("WithHidden(nil) copied the registry")
	}
	scoped := r.WithHidden(hiddenSet("AGG_SUM"))
	if scoped == r || r.hidden != nil {
		t.Error("WithHidden mutated its receiver")
	}
}

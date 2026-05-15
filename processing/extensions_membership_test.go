package processing

import (
	"testing"

	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/processing/window"
	"github.com/frankbardon/pulse/types"
)

// Coverage for Has* / Custom*Names public surface on
// ExtensionRegistry. Each Has* must observe both the overlay (when the
// name is overlay-only) and the built-in registry (when the name is a
// stock operator). Each Custom*Names must enumerate overlay-only
// entries — nil receiver yields nil.

func TestExtensionRegistry_HasAggregator_OverlayAndBuiltin(t *testing.T) {
	r := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			"AGG_ACME_SCORE": dummyAggregatorFactory,
		},
	}
	if !r.HasAggregator("AGG_ACME_SCORE") {
		t.Error("HasAggregator must see overlay entry")
	}
	if !r.HasAggregator(types.AGG_COUNT) {
		t.Error("HasAggregator must fall through to built-in registry")
	}
	if r.HasAggregator("AGG_ACME_MISSING") {
		t.Error("unknown aggregator must not resolve")
	}
}

func TestExtensionRegistry_HasAttribute_OverlayAndBuiltin(t *testing.T) {
	r := &ExtensionRegistry{
		Attributes: map[types.AttributeType]AttributeFactory{
			"ATTR_ACME_BOOST": fakeBoostFactory,
		},
	}
	if !r.HasAttribute("ATTR_ACME_BOOST") {
		t.Error("HasAttribute must see overlay entry")
	}
	if !r.HasAttribute(types.ATTR_FORMULA) {
		t.Error("HasAttribute must fall through to built-in registry")
	}
	if r.HasAttribute("ATTR_ACME_MISSING") {
		t.Error("unknown attribute must not resolve")
	}
}

func TestExtensionRegistry_HasFilterer_OverlayAndBuiltin(t *testing.T) {
	r := &ExtensionRegistry{
		Filterers: map[types.FiltererType]FiltererFactory{
			"FILTER_ACME_HIGH": keepHighFilterFactory,
		},
	}
	if !r.HasFilterer("FILTER_ACME_HIGH") {
		t.Error("HasFilterer must see overlay entry")
	}
	if !r.HasFilterer(types.FILTER_INCLUDE) {
		t.Error("HasFilterer must fall through to built-in registry")
	}
	if r.HasFilterer("FILTER_ACME_MISSING") {
		t.Error("unknown filterer must not resolve")
	}
}

func TestExtensionRegistry_HasGrouper_OverlayAndBuiltin(t *testing.T) {
	r := &ExtensionRegistry{
		Groupers: map[types.GroupType]GrouperFactory{
			"GROUP_ACME_PARITY": parityGrouperFactory,
		},
	}
	if !r.HasGrouper("GROUP_ACME_PARITY") {
		t.Error("HasGrouper must see overlay entry")
	}
	if !r.HasGrouper(types.GROUP_CATEGORY) {
		t.Error("HasGrouper must fall through to built-in registry")
	}
	if r.HasGrouper("GROUP_ACME_MISSING") {
		t.Error("unknown grouper must not resolve")
	}
}

func TestExtensionRegistry_HasWindow_OverlayAndBuiltin(t *testing.T) {
	r := &ExtensionRegistry{
		Windows: map[types.WindowType]window.WindowFactory{
			"WIN_ACME_CONST": constWindowFactory,
		},
	}
	if !r.HasWindow("WIN_ACME_CONST") {
		t.Error("HasWindow must see overlay entry")
	}
	if !r.HasWindow(types.WIN_LAG) {
		t.Error("HasWindow must fall through to built-in window registry")
	}
	if r.HasWindow("WIN_ACME_MISSING") {
		t.Error("unknown window must not resolve")
	}
}

func TestExtensionRegistry_HasFeature_OverlayAndBuiltin(t *testing.T) {
	r := &ExtensionRegistry{
		Features: map[types.FeatureType]feature.Factory{
			"FEAT_ACME_DOUBLE": doubleFeatureFactory,
		},
	}
	if !r.HasFeature("FEAT_ACME_DOUBLE") {
		t.Error("HasFeature must see overlay entry")
	}
	if !r.HasFeature(types.FEAT_LOG) {
		t.Error("HasFeature must fall through to built-in feature registry")
	}
	if r.HasFeature("FEAT_ACME_MISSING") {
		t.Error("unknown feature must not resolve")
	}
}

func TestExtensionRegistry_HasRowTest_OverlayAndBuiltin(t *testing.T) {
	r := &ExtensionRegistry{
		RowTests: map[types.TestType]RowTestFactory{
			"TEST_ACME_CONST": constRowTestFactory,
		},
	}
	if !r.HasRowTest("TEST_ACME_CONST") {
		t.Error("HasRowTest must see overlay entry")
	}
	if !r.HasRowTest(types.TEST_T) {
		t.Error("HasRowTest must fall through to built-in row-test registry")
	}
	if r.HasRowTest("TEST_ACME_MISSING") {
		t.Error("unknown row test must not resolve")
	}
}

func TestExtensionRegistry_HasPostTest_OverlayAndBuiltin(t *testing.T) {
	r := &ExtensionRegistry{
		PostTests: map[types.TestType]PostTestFactory{
			"TEST_ACME_POST": constPostTestFactory,
		},
	}
	if !r.HasPostTest("TEST_ACME_POST") {
		t.Error("HasPostTest must see overlay entry")
	}
	if !r.HasPostTest(types.TEST_TUKEY_HSD) {
		t.Error("HasPostTest must fall through to built-in post-test registry")
	}
	if r.HasPostTest("TEST_ACME_MISSING") {
		t.Error("unknown post test must not resolve")
	}
}

// Custom*Names cases — nil receiver, empty overlay, and populated.

func TestExtensionRegistry_CustomAttributeNames_NilAndPopulated(t *testing.T) {
	var nilR *ExtensionRegistry
	if got := nilR.CustomAttributeNames(); got != nil {
		t.Errorf("nil receiver CustomAttributeNames = %v, want nil", got)
	}
	r := &ExtensionRegistry{
		Attributes: map[types.AttributeType]AttributeFactory{
			"ATTR_ACME_BOOST": fakeBoostFactory,
		},
	}
	got := r.CustomAttributeNames()
	if len(got) != 1 || got[0] != "ATTR_ACME_BOOST" {
		t.Errorf("CustomAttributeNames = %v, want [ATTR_ACME_BOOST]", got)
	}
}

func TestExtensionRegistry_CustomFiltererNames_NilAndPopulated(t *testing.T) {
	var nilR *ExtensionRegistry
	if got := nilR.CustomFiltererNames(); got != nil {
		t.Errorf("nil receiver CustomFiltererNames = %v, want nil", got)
	}
	r := &ExtensionRegistry{
		Filterers: map[types.FiltererType]FiltererFactory{
			"FILTER_ACME_HIGH": keepHighFilterFactory,
		},
	}
	got := r.CustomFiltererNames()
	if len(got) != 1 || got[0] != "FILTER_ACME_HIGH" {
		t.Errorf("CustomFiltererNames = %v, want [FILTER_ACME_HIGH]", got)
	}
}

func TestExtensionRegistry_CustomGrouperNames_NilAndPopulated(t *testing.T) {
	var nilR *ExtensionRegistry
	if got := nilR.CustomGrouperNames(); got != nil {
		t.Errorf("nil receiver CustomGrouperNames = %v, want nil", got)
	}
	r := &ExtensionRegistry{
		Groupers: map[types.GroupType]GrouperFactory{
			"GROUP_ACME_PARITY": parityGrouperFactory,
		},
	}
	got := r.CustomGrouperNames()
	if len(got) != 1 || got[0] != "GROUP_ACME_PARITY" {
		t.Errorf("CustomGrouperNames = %v, want [GROUP_ACME_PARITY]", got)
	}
}

func TestExtensionRegistry_CustomWindowNames_NilAndPopulated(t *testing.T) {
	var nilR *ExtensionRegistry
	if got := nilR.CustomWindowNames(); got != nil {
		t.Errorf("nil receiver CustomWindowNames = %v, want nil", got)
	}
	r := &ExtensionRegistry{
		Windows: map[types.WindowType]window.WindowFactory{
			"WIN_ACME_CONST": constWindowFactory,
		},
	}
	got := r.CustomWindowNames()
	if len(got) != 1 || got[0] != "WIN_ACME_CONST" {
		t.Errorf("CustomWindowNames = %v, want [WIN_ACME_CONST]", got)
	}
}

func TestExtensionRegistry_CustomFeatureNames_NilAndPopulated(t *testing.T) {
	var nilR *ExtensionRegistry
	if got := nilR.CustomFeatureNames(); got != nil {
		t.Errorf("nil receiver CustomFeatureNames = %v, want nil", got)
	}
	r := &ExtensionRegistry{
		Features: map[types.FeatureType]feature.Factory{
			"FEAT_ACME_DOUBLE": doubleFeatureFactory,
		},
	}
	got := r.CustomFeatureNames()
	if len(got) != 1 || got[0] != "FEAT_ACME_DOUBLE" {
		t.Errorf("CustomFeatureNames = %v, want [FEAT_ACME_DOUBLE]", got)
	}
}

func TestExtensionRegistry_CustomRowTestNames_NilAndPopulated(t *testing.T) {
	var nilR *ExtensionRegistry
	if got := nilR.CustomRowTestNames(); got != nil {
		t.Errorf("nil receiver CustomRowTestNames = %v, want nil", got)
	}
	r := &ExtensionRegistry{
		RowTests: map[types.TestType]RowTestFactory{
			"TEST_ACME_CONST": constRowTestFactory,
		},
	}
	got := r.CustomRowTestNames()
	if len(got) != 1 || got[0] != "TEST_ACME_CONST" {
		t.Errorf("CustomRowTestNames = %v, want [TEST_ACME_CONST]", got)
	}
}

func TestExtensionRegistry_CustomPostTestNames_NilAndPopulated(t *testing.T) {
	var nilR *ExtensionRegistry
	if got := nilR.CustomPostTestNames(); got != nil {
		t.Errorf("nil receiver CustomPostTestNames = %v, want nil", got)
	}
	r := &ExtensionRegistry{
		PostTests: map[types.TestType]PostTestFactory{
			"TEST_ACME_POST": constPostTestFactory,
		},
	}
	got := r.CustomPostTestNames()
	if len(got) != 1 || got[0] != "TEST_ACME_POST" {
		t.Errorf("CustomPostTestNames = %v, want [TEST_ACME_POST]", got)
	}
}

// CustomAggregatorNames nil-receiver — closes the 83.3% gap.

func TestExtensionRegistry_CustomAggregatorNames_NilReceiver(t *testing.T) {
	var nilR *ExtensionRegistry
	if got := nilR.CustomAggregatorNames(); got != nil {
		t.Errorf("nil receiver CustomAggregatorNames = %v, want nil", got)
	}
}

// LookupWindow / LookupFeature for unknown name (overlay miss + built-in
// miss) — covers the "false" branch which was at 50% before.

func TestExtensionRegistry_LookupWindow_Unknown(t *testing.T) {
	r := &ExtensionRegistry{}
	if _, ok := r.LookupWindow("WIN_ACME_NOTFOUND"); ok {
		t.Error("LookupWindow on unknown name must return false")
	}
}

func TestExtensionRegistry_LookupFeature_Unknown(t *testing.T) {
	r := &ExtensionRegistry{}
	if _, ok := r.LookupFeature("FEAT_ACME_NOTFOUND"); ok {
		t.Error("LookupFeature on unknown name must return false")
	}
}

// IsStreamable overlay-miss + unknown-name path — covers the false-overlay
// + per-type Streamable() = false branch on unknown name.

func TestExtensionRegistry_IsStreamable_UnknownNames(t *testing.T) {
	r := &ExtensionRegistry{}
	cases := []struct{ cat, name string }{
		{"attribute", "ATTR_ACME_NONE"},
		{"filterer", "FILTER_ACME_NONE"},
		{"grouper", "GROUP_ACME_NONE"},
		{"window", "WIN_ACME_NONE"},
		{"feature", "FEAT_ACME_NONE"},
		{"test", "TEST_ACME_NONE"},
	}
	for _, c := range cases {
		if r.IsStreamable(c.cat, c.name) {
			t.Errorf("IsStreamable(%q, %q) = true, want false", c.cat, c.name)
		}
	}
}

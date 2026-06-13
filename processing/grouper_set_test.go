package processing

import (
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestGroupSetValue_AtomicMaskKeys(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // VISA, MC → "MC|VISA"
		makeSetRecord(schema, 0b0011),
		makeSetRecord(schema, 0b0100), // AMEX → "AMEX"
	}
	groups, err := g.Group(recs, "tags")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(groups["MC|VISA"]) != 2 {
		t.Errorf("MC|VISA bucket size = %d, want 2", len(groups["MC|VISA"]))
	}
	if len(groups["AMEX"]) != 1 {
		t.Errorf("AMEX bucket size = %d, want 1", len(groups["AMEX"]))
	}
	if len(groups) != 2 {
		keys := make([]string, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Errorf("bucket count = %d (%s), want 2", len(groups), strings.Join(keys, ","))
	}
}

func TestGroupSetPerElement_RowFansIntoMultipleBuckets(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // VISA + MC
		makeSetRecord(schema, 0b0100), // AMEX
		makeSetRecord(schema, 0b0000), // empty — contributes to nothing
	}
	groups, err := g.Group(recs, "tags")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	want := map[string]int{"VISA": 1, "MC": 1, "AMEX": 1}
	for k, n := range want {
		if got := len(groups[k]); got != n {
			t.Errorf("bucket %s = %d, want %d", k, got, n)
		}
	}
	if _, present := groups[""]; present {
		t.Error("empty-mask row should not contribute to a bucket")
	}
}

func TestGroupSetPerElement_KeysForRow(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, _ := newSetPerElementGrouper(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, schema)
	mg, ok := g.(MultiKeyStreamingGrouper)
	if !ok {
		t.Fatal("setPerElementGrouper must implement MultiKeyStreamingGrouper")
	}
	keys, ok, err := mg.KeysForRow(makeSetRecord(schema, 0b0101), "tags")
	if err != nil {
		t.Fatalf("KeysForRow: %v", err)
	}
	if !ok {
		t.Fatal("ok=false for non-empty mask")
	}
	sort.Strings(keys)
	want := []string{"AMEX", "VISA"}
	if !sliceEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}

	keys, ok, _ = mg.KeysForRow(makeSetRecord(schema, 0), "tags")
	if ok {
		t.Errorf("ok=true for empty mask, want false; keys=%v", keys)
	}
}

func TestGroupSetPerElement_IncludeFilters(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{
		Type:    types.GROUP_SET_PER_ELEMENT,
		Field:   "tags",
		Include: []string{"VISA", "AMEX"},
	}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // VISA + MC → keeps VISA, drops MC
		makeSetRecord(schema, 0b0100), // AMEX → kept
		makeSetRecord(schema, 0b0010), // MC alone → drops entirely
	}
	groups, err := g.Group(recs, "tags")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if _, present := groups["MC"]; present {
		t.Error("MC bucket should be absent under Include=[VISA,AMEX]")
	}
	if len(groups["VISA"]) != 1 {
		t.Errorf("VISA bucket size = %d, want 1", len(groups["VISA"]))
	}
	if len(groups["AMEX"]) != 1 {
		t.Errorf("AMEX bucket size = %d, want 1", len(groups["AMEX"]))
	}
	if len(groups) != 2 {
		t.Errorf("bucket count = %d, want 2 (VISA, AMEX)", len(groups))
	}

	mg := g.(MultiKeyStreamingGrouper)
	keys, ok, err := mg.KeysForRow(makeSetRecord(schema, 0b0010), "tags")
	if err != nil {
		t.Fatalf("KeysForRow: %v", err)
	}
	if ok {
		t.Errorf("MC-only row should be skipped under Include, got keys=%v", keys)
	}
}

func TestGroupSetValue_IncludeFiltersCompositeKey(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{
		Type:    types.GROUP_SET_VALUE,
		Field:   "tags",
		Include: []string{"MC|VISA"},
	}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // MC|VISA — kept
		makeSetRecord(schema, 0b0011),
		makeSetRecord(schema, 0b0100), // AMEX — dropped
	}
	groups, err := g.Group(recs, "tags")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(groups["MC|VISA"]) != 2 {
		t.Errorf("MC|VISA bucket size = %d, want 2", len(groups["MC|VISA"]))
	}
	if _, present := groups["AMEX"]; present {
		t.Error("AMEX bucket should be absent under Include=[MC|VISA]")
	}
	if len(groups) != 1 {
		t.Errorf("bucket count = %d, want 1", len(groups))
	}
}

func TestGroupCategory_IncludeFiltersLabels(t *testing.T) {
	schema := categoricalSchema()
	g, err := newCategoryGrouper(&types.Group{
		Type:    types.GROUP_CATEGORY,
		Field:   "brand",
		Include: []string{"Apple", "Google"},
	}, schema)
	if err != nil {
		t.Fatalf("newCategoryGrouper: %v", err)
	}
	recs := []*Record{
		NewRecord(schema, map[string]float64{"brand": 0}), // Apple
		NewRecord(schema, map[string]float64{"brand": 1}), // Samsung — dropped
		NewRecord(schema, map[string]float64{"brand": 2}), // Google
		NewRecord(schema, map[string]float64{"brand": 0}), // Apple
	}
	groups, err := g.Group(recs, "brand")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if len(groups["Apple"]) != 2 {
		t.Errorf("Apple bucket size = %d, want 2", len(groups["Apple"]))
	}
	if len(groups["Google"]) != 1 {
		t.Errorf("Google bucket size = %d, want 1", len(groups["Google"]))
	}
	if _, present := groups["Samsung"]; present {
		t.Error("Samsung bucket should be absent under Include=[Apple,Google]")
	}
	if len(groups) != 2 {
		t.Errorf("bucket count = %d, want 2", len(groups))
	}

	sg := g.(StreamableGrouper)
	_, err = sg.KeyFor(NewRecord(schema, map[string]float64{"brand": 1})) // Samsung
	if err != ErrGrouperKeyNull {
		t.Errorf("KeyFor(Samsung) under Include should return ErrGrouperKeyNull, got %v", err)
	}
}

func TestGroupCategory_EmptyIncludeIsByteIdentical(t *testing.T) {
	schema := categoricalSchema()
	gBaseline := makeGrouper(t, types.GROUP_CATEGORY, "brand", 0, schema)
	gInclude, err := newCategoryGrouper(&types.Group{
		Type:    types.GROUP_CATEGORY,
		Field:   "brand",
		Include: nil,
	}, schema)
	if err != nil {
		t.Fatalf("newCategoryGrouper: %v", err)
	}
	recs := []*Record{
		NewRecord(schema, map[string]float64{"brand": 0}),
		NewRecord(schema, map[string]float64{"brand": 1}),
		NewRecord(schema, map[string]float64{"brand": 2}),
	}
	gb, _ := gBaseline.Group(recs, "brand")
	gi, _ := gInclude.Group(recs, "brand")
	if len(gb) != len(gi) {
		t.Errorf("bucket count diverged: baseline=%d include=%d", len(gb), len(gi))
	}
	for k, v := range gb {
		if len(gi[k]) != len(v) {
			t.Errorf("bucket %s size diverged: baseline=%d include=%d", k, len(v), len(gi[k]))
		}
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

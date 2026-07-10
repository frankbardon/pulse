package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// bucketKeyOrder extracts the ordered bucket keys from a Components()
// map's "buckets" slice for order assertions.
func bucketKeyOrder(t *testing.T, comp map[string]any) []string {
	t.Helper()
	raw, ok := comp["buckets"].([]map[string]any)
	if !ok {
		t.Fatalf("buckets slice missing / wrong type: %T", comp["buckets"])
	}
	keys := make([]string, 0, len(raw))
	for _, b := range raw {
		k, _ := b["key"].(string)
		keys = append(keys, k)
	}
	return keys
}

// TestGroupCategory_ComponentsIncludeOrder — GROUP_CATEGORY Components()
// emits buckets in include order, not alphabetical, when Include is set.
// Include order ["Google","Apple"] differs from alphabetical ["Apple","Google"].
func TestGroupCategory_ComponentsIncludeOrder(t *testing.T) {
	schema := categoricalSchema()
	g, err := newCategoryGrouper(&types.Group{
		Type:    types.GROUP_CATEGORY,
		Field:   "brand",
		Include: []string{"Google", "Apple"},
	}, schema)
	if err != nil {
		t.Fatalf("newCategoryGrouper: %v", err)
	}
	recs := []*Record{
		NewRecord(schema, map[string]float64{"brand": 0}), // Apple
		NewRecord(schema, map[string]float64{"brand": 2}), // Google
		NewRecord(schema, map[string]float64{"brand": 0}), // Apple
	}
	if _, err := g.Group(recs, "brand"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	got := bucketKeyOrder(t, comp)
	want := []string{"Google", "Apple"}
	if !sliceEqual(got, want) {
		t.Errorf("bucket order = %v, want %v (include order)", got, want)
	}
}

// TestGroupCategory_ComponentsDuplicateIncludeBucketsOnce — a duplicated
// include value buckets once, at its first occurrence position.
func TestGroupCategory_ComponentsDuplicateIncludeBucketsOnce(t *testing.T) {
	schema := categoricalSchema()
	g, err := newCategoryGrouper(&types.Group{
		Type:    types.GROUP_CATEGORY,
		Field:   "brand",
		Include: []string{"Google", "Apple", "Google"},
	}, schema)
	if err != nil {
		t.Fatalf("newCategoryGrouper: %v", err)
	}
	recs := []*Record{
		NewRecord(schema, map[string]float64{"brand": 0}), // Apple
		NewRecord(schema, map[string]float64{"brand": 2}), // Google
	}
	if _, err := g.Group(recs, "brand"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	got := bucketKeyOrder(t, comp)
	want := []string{"Google", "Apple"}
	if !sliceEqual(got, want) {
		t.Errorf("bucket order = %v, want %v (duplicate collapses to first position)", got, want)
	}
}

// TestGroupCategory_ComponentsZeroRecordIncludeDropped — an include value
// with no matching records is dropped, not emitted as an empty bucket.
func TestGroupCategory_ComponentsZeroRecordIncludeDropped(t *testing.T) {
	schema := categoricalSchema()
	g, err := newCategoryGrouper(&types.Group{
		Type:    types.GROUP_CATEGORY,
		Field:   "brand",
		Include: []string{"Google", "Samsung", "Apple"},
	}, schema)
	if err != nil {
		t.Fatalf("newCategoryGrouper: %v", err)
	}
	recs := []*Record{
		NewRecord(schema, map[string]float64{"brand": 0}), // Apple
		NewRecord(schema, map[string]float64{"brand": 2}), // Google
		// no Samsung rows
	}
	if _, err := g.Group(recs, "brand"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	got := bucketKeyOrder(t, comp)
	want := []string{"Google", "Apple"}
	if !sliceEqual(got, want) {
		t.Errorf("bucket order = %v, want %v (zero-record include dropped)", got, want)
	}
}

// TestGroupSetValue_ComponentsIncludeOrder — GROUP_SET_VALUE Components()
// emits composite-key buckets in include order.
func TestGroupSetValue_ComponentsIncludeOrder(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{
		Type:    types.GROUP_SET_VALUE,
		Field:   "tags",
		Include: []string{"AMEX", "MC|VISA"},
	}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // MC|VISA
		makeSetRecord(schema, 0b0100), // AMEX
		makeSetRecord(schema, 0b0011), // MC|VISA
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	got := bucketKeyOrder(t, comp)
	want := []string{"AMEX", "MC|VISA"}
	if !sliceEqual(got, want) {
		t.Errorf("bucket order = %v, want %v (include order)", got, want)
	}
}

// TestGroupSetValue_ComponentsDuplicateIncludeBucketsOnce — duplicate
// composite include value buckets once at first position.
func TestGroupSetValue_ComponentsDuplicateIncludeBucketsOnce(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{
		Type:    types.GROUP_SET_VALUE,
		Field:   "tags",
		Include: []string{"AMEX", "MC|VISA", "AMEX"},
	}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // MC|VISA
		makeSetRecord(schema, 0b0100), // AMEX
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	got := bucketKeyOrder(t, comp)
	want := []string{"AMEX", "MC|VISA"}
	if !sliceEqual(got, want) {
		t.Errorf("bucket order = %v, want %v", got, want)
	}
}

// TestGroupSetValue_ComponentsZeroRecordIncludeDropped — include composite
// with no rows is dropped.
func TestGroupSetValue_ComponentsZeroRecordIncludeDropped(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{
		Type:    types.GROUP_SET_VALUE,
		Field:   "tags",
		Include: []string{"AMEX", "DISC", "MC|VISA"},
	}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // MC|VISA
		makeSetRecord(schema, 0b0100), // AMEX
		// no DISC rows
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	got := bucketKeyOrder(t, comp)
	want := []string{"AMEX", "MC|VISA"}
	if !sliceEqual(got, want) {
		t.Errorf("bucket order = %v, want %v (zero-record include dropped)", got, want)
	}
}

// TestGroupSetPerElement_ComponentsIncludeOrder — GROUP_SET_PER_ELEMENT
// Components() emits per-label buckets in include order (not dict order)
// when Include is active. Dict order is VISA(0),MC(1),AMEX(2),DISC(3);
// include order ["AMEX","VISA"] must win.
func TestGroupSetPerElement_ComponentsIncludeOrder(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{
		Type:    types.GROUP_SET_PER_ELEMENT,
		Field:   "tags",
		Include: []string{"AMEX", "VISA"},
	}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0101), // VISA + AMEX
		makeSetRecord(schema, 0b0100), // AMEX
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	got := bucketKeyOrder(t, comp)
	want := []string{"AMEX", "VISA"}
	if !sliceEqual(got, want) {
		t.Errorf("bucket order = %v, want %v (include order beats dict order)", got, want)
	}
}

// TestGroupSetPerElement_ComponentsDictOrderWhenNoInclude — without an
// Include list the per-element grouper keeps its dict-index emission
// order (byte-identity guard for the no-include path).
func TestGroupSetPerElement_ComponentsDictOrderWhenNoInclude(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{
		Type:  types.GROUP_SET_PER_ELEMENT,
		Field: "tags",
	}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0101), // VISA(0) + AMEX(2)
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	got := bucketKeyOrder(t, comp)
	want := []string{"VISA", "AMEX"} // dict-index order 0,2
	if !sliceEqual(got, want) {
		t.Errorf("bucket order = %v, want %v (dict order)", got, want)
	}
}

// TestGroupSetPerElement_ComponentsDuplicateIncludeBucketsOnce — duplicate
// include label buckets once at first position.
func TestGroupSetPerElement_ComponentsDuplicateIncludeBucketsOnce(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{
		Type:    types.GROUP_SET_PER_ELEMENT,
		Field:   "tags",
		Include: []string{"AMEX", "VISA", "AMEX"},
	}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0101), // VISA + AMEX
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	got := bucketKeyOrder(t, comp)
	want := []string{"AMEX", "VISA"}
	if !sliceEqual(got, want) {
		t.Errorf("bucket order = %v, want %v", got, want)
	}
}

// TestGroupSetPerElement_ComponentsZeroRecordIncludeDropped — include label
// with no rows is dropped.
func TestGroupSetPerElement_ComponentsZeroRecordIncludeDropped(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{
		Type:    types.GROUP_SET_PER_ELEMENT,
		Field:   "tags",
		Include: []string{"AMEX", "DISC", "VISA"},
	}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0101), // VISA + AMEX (no DISC)
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	got := bucketKeyOrder(t, comp)
	want := []string{"AMEX", "VISA"}
	if !sliceEqual(got, want) {
		t.Errorf("bucket order = %v, want %v (zero-record include dropped)", got, want)
	}
}

// TestOrderKeysByInclude_NilFilterSortsAlphabetical — the shared helper
// funnels a nil/inactive filter through sort.Strings (byte-identity).
func TestOrderKeysByInclude_NilFilterSortsAlphabetical(t *testing.T) {
	got := orderKeysByInclude(nil, []string{"c", "a", "b"})
	want := []string{"a", "b", "c"}
	if !sliceEqual(got, want) {
		t.Errorf("nil filter order = %v, want %v (sort.Strings)", got, want)
	}
}

// TestOrderKeysByInclude_ActiveOrdersByIndexWithDefensiveTail — an active
// filter orders present keys by include index; any non-member straggler
// funnels to a sort.Strings tail.
func TestOrderKeysByInclude_ActiveOrdersByIndexWithDefensiveTail(t *testing.T) {
	f := buildIncludeFilter([]string{"z", "a"})
	// "m" is a non-member straggler (should never happen post-KeyFor, but
	// the defensive tail must sort it after the members).
	got := orderKeysByInclude(f, []string{"a", "m", "z"})
	want := []string{"z", "a", "m"}
	if !sliceEqual(got, want) {
		t.Errorf("active filter order = %v, want %v", got, want)
	}
}

// TestIncludeFilterOf_ExtensionGrouperFallsThrough — a grouper that does
// not implement IncludeOrdered yields a nil filter, so orderKeysByInclude
// sorts alphabetically (extension byte-identity).
func TestIncludeFilterOf_ExtensionGrouperFallsThrough(t *testing.T) {
	var g Grouper = extNoIncludeGrouper{}
	if f := includeFilterOf(g); f != nil {
		t.Fatalf("includeFilterOf(extension) = %v, want nil", f)
	}
	got := orderKeysByInclude(includeFilterOf(g), []string{"c", "a", "b"})
	want := []string{"a", "b", "c"}
	if !sliceEqual(got, want) {
		t.Errorf("extension order = %v, want %v", got, want)
	}
}

// extNoIncludeGrouper is a minimal Grouper that does NOT implement
// IncludeOrdered — models an embedder-registered extension grouper.
type extNoIncludeGrouper struct{}

func (extNoIncludeGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	return map[string][]*Record{}, nil
}

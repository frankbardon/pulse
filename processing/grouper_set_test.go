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

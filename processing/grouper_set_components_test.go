package processing

import (
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// E2-S6 — MetaGrouper.Components() emission for GROUP_SET_VALUE and
// GROUP_SET_PER_ELEMENT. Exercises the operator-specific keys only;
// the universal floor ({total_n, n_null}) is filled by the
// orchestrator (verified end-to-end via service/ tests).
//
// Reuses makeSetTestSchema / makeSetRecord from aggregator_set_test.go
// (set_u8 "tags" field with dict {VISA, MC, AMEX, DISC}).
//
// Bit map for the shared fixture:
//   bit 0 = VISA, bit 1 = MC, bit 2 = AMEX, bit 3 = DISC

// --- GROUP_SET_VALUE -----------------------------------------------

// makeSetU16Schema builds a one-field schema with a set_u16 column
// whose dictionary holds nine fruits — exercises the set_u16 wire
// width as a sibling fixture to makeSetTestSchema (set_u8).
func makeSetU16Schema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{
		"APPLE", "BANANA", "CHERRY", "DATE", "ELDERBERRY",
		"FIG", "GRAPE", "HONEYDEW", "KIWI",
	} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add(%s): %v", v, err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU16, Dictionary: dict},
		},
	}
}

func TestGrouper_SetValue_Components_EmptyCohort(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	if _, err := g.Group([]*Record{}, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	meta, ok := g.(MetaGrouper)
	if !ok {
		t.Fatalf("setValueGrouper does not implement MetaGrouper")
	}
	op, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	wantKeys := []string{"buckets", "n_empty_mask"}
	if got := mapKeysSortedAny(op); !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("Components keys = %v, want %v", got, wantKeys)
	}
	if n, _ := op["n_empty_mask"].(int); n != 0 {
		t.Errorf("n_empty_mask = %d, want 0", n)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 0 {
		t.Errorf("buckets len = %d, want 0", len(buckets))
	}
	if buckets == nil {
		t.Errorf("buckets nil; want non-nil empty slice")
	}
}

func TestGrouper_SetValue_Components_AllEmptyMasks(t *testing.T) {
	// Every row carries mask == 0. Each contributes to the "" bucket
	// and bumps n_empty_mask.
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0),
		makeSetRecord(schema, 0),
		makeSetRecord(schema, 0),
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if n, _ := op["n_empty_mask"].(int); n != 3 {
		t.Errorf("n_empty_mask = %d, want 3", n)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 1 {
		t.Fatalf("buckets len = %d, want 1 (empty-mask bucket)", len(buckets))
	}
	b := buckets[0]
	if k, _ := b["key"].(string); k != "" {
		t.Errorf("bucket[0] key = %q, want \"\"", k)
	}
	if m, _ := b["mask"].(uint64); m != 0 {
		t.Errorf("bucket[0] mask = %d, want 0", m)
	}
	if c, _ := b["count"].(int); c != 3 {
		t.Errorf("bucket[0] count = %d, want 3", c)
	}
	labels, _ := b["labels"].([]string)
	if len(labels) != 0 {
		t.Errorf("bucket[0] labels = %v, want []", labels)
	}
}

func TestGrouper_SetValue_Components_SingleElementSelections(t *testing.T) {
	// Each row selects a single distinct bit. Verify per-bucket
	// mask + count + decoded labels and zero n_empty_mask.
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0001), // VISA
		makeSetRecord(schema, 0b0010), // MC
		makeSetRecord(schema, 0b0001), // VISA
		makeSetRecord(schema, 0b0100), // AMEX
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if n, _ := op["n_empty_mask"].(int); n != 0 {
		t.Errorf("n_empty_mask = %d, want 0", n)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 3 {
		t.Fatalf("buckets len = %d, want 3", len(buckets))
	}
	// Sorted by key: "AMEX", "MC", "VISA"
	wantOrder := []struct {
		key   string
		mask  uint64
		count int
		label string
	}{
		{"AMEX", 0b0100, 1, "AMEX"},
		{"MC", 0b0010, 1, "MC"},
		{"VISA", 0b0001, 2, "VISA"},
	}
	for i, want := range wantOrder {
		b := buckets[i]
		if k, _ := b["key"].(string); k != want.key {
			t.Errorf("bucket[%d] key = %q, want %q", i, k, want.key)
		}
		if m, _ := b["mask"].(uint64); m != want.mask {
			t.Errorf("bucket[%d] mask = %d, want %d", i, m, want.mask)
		}
		if c, _ := b["count"].(int); c != want.count {
			t.Errorf("bucket[%d] count = %d, want %d", i, c, want.count)
		}
		labels, _ := b["labels"].([]string)
		if len(labels) != 1 || labels[0] != want.label {
			t.Errorf("bucket[%d] labels = %v, want [%q]", i, labels, want.label)
		}
	}
}

func TestGrouper_SetValue_Components_FullWidthSelection(t *testing.T) {
	// One row sets every dictionary bit. Verify the mask, labels,
	// and that the bucket key is the pipe-joined sorted label list.
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b1111), // VISA + MC + AMEX + DISC
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 1 {
		t.Fatalf("buckets len = %d, want 1", len(buckets))
	}
	b := buckets[0]
	// Sorted label order alphabetically: AMEX|DISC|MC|VISA
	if k, _ := b["key"].(string); k != "AMEX|DISC|MC|VISA" {
		t.Errorf("bucket key = %q, want %q", k, "AMEX|DISC|MC|VISA")
	}
	if m, _ := b["mask"].(uint64); m != 0b1111 {
		t.Errorf("bucket mask = %d, want %d", m, 0b1111)
	}
	if c, _ := b["count"].(int); c != 1 {
		t.Errorf("bucket count = %d, want 1", c)
	}
	labels, _ := b["labels"].([]string)
	// labels emitted in ascending bit order (resolveMaskLabels contract).
	wantLabels := []string{"VISA", "MC", "AMEX", "DISC"}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Errorf("bucket labels = %v, want %v (ascending bit order)", labels, wantLabels)
	}
}

func TestGrouper_SetValue_Components_Mixed(t *testing.T) {
	// Mixed: distinct masks, repeated masks, and one empty mask.
	// Verifies bucket dedup (rows sharing a mask collapse to one
	// bucket), label resolution per bucket, and n_empty_mask.
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // VISA + MC → "MC|VISA"
		makeSetRecord(schema, 0b0011),
		makeSetRecord(schema, 0b0011),
		makeSetRecord(schema, 0b0100), // AMEX → "AMEX"
		makeSetRecord(schema, 0b0000), // empty
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if n, _ := op["n_empty_mask"].(int); n != 1 {
		t.Errorf("n_empty_mask = %d, want 1", n)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 3 {
		t.Fatalf("buckets len = %d, want 3", len(buckets))
	}
	// Sorted by key alphabetically: "", "AMEX", "MC|VISA"
	wantOrder := []struct {
		key     string
		mask    uint64
		count   int
		nLabels int
	}{
		{"", 0, 1, 0},
		{"AMEX", 0b0100, 1, 1},
		{"MC|VISA", 0b0011, 3, 2},
	}
	for i, want := range wantOrder {
		b := buckets[i]
		if k, _ := b["key"].(string); k != want.key {
			t.Errorf("bucket[%d] key = %q, want %q", i, k, want.key)
		}
		if m, _ := b["mask"].(uint64); m != want.mask {
			t.Errorf("bucket[%d] mask = %d, want %d", i, m, want.mask)
		}
		if c, _ := b["count"].(int); c != want.count {
			t.Errorf("bucket[%d] count = %d, want %d", i, c, want.count)
		}
		labels, _ := b["labels"].([]string)
		if len(labels) != want.nLabels {
			t.Errorf("bucket[%d] labels = %v (len=%d), want %d entries",
				i, labels, len(labels), want.nLabels)
		}
	}
}

func TestGrouper_SetValue_Components_SetU16Field(t *testing.T) {
	// Same operator on a set_u16 field — verifies the dictionary
	// path is wire-width agnostic. Bits 0, 4, 8 ≡ APPLE, ELDERBERRY,
	// KIWI in the makeSetU16Schema dictionary.
	schema := makeSetU16Schema(t)
	g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	mask := uint64(1)<<0 | uint64(1)<<4 | uint64(1)<<8 // APPLE | ELDERBERRY | KIWI
	recs := []*Record{
		makeSetRecord(schema, mask),
		makeSetRecord(schema, mask),
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 1 {
		t.Fatalf("buckets len = %d, want 1", len(buckets))
	}
	b := buckets[0]
	if m, _ := b["mask"].(uint64); m != mask {
		t.Errorf("bucket mask = %d, want %d", m, mask)
	}
	if c, _ := b["count"].(int); c != 2 {
		t.Errorf("bucket count = %d, want 2", c)
	}
	labels, _ := b["labels"].([]string)
	wantLabels := []string{"APPLE", "ELDERBERRY", "KIWI"}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Errorf("bucket labels = %v, want %v", labels, wantLabels)
	}
}

// --- GROUP_SET_PER_ELEMENT -----------------------------------------

func TestGrouper_SetPerElement_Components_EmptyCohort(t *testing.T) {
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	if _, err := g.Group([]*Record{}, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	meta, ok := g.(MetaGrouper)
	if !ok {
		t.Fatalf("setPerElementGrouper does not implement MetaGrouper")
	}
	op, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	wantKeys := []string{"buckets", "total_label_observations"}
	if got := mapKeysSortedAny(op); !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("Components keys = %v, want %v", got, wantKeys)
	}
	if n, _ := op["total_label_observations"].(int); n != 0 {
		t.Errorf("total_label_observations = %d, want 0", n)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 0 {
		t.Errorf("buckets len = %d, want 0", len(buckets))
	}
	if buckets == nil {
		t.Errorf("buckets nil; want non-nil empty slice")
	}
}

func TestGrouper_SetPerElement_Components_SingleBucketShared(t *testing.T) {
	// All rows carry the same single-bit mask. Per-label count must
	// equal row count; total_label_observations == TotalN here since
	// every row contributes to exactly one bucket.
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0001), // VISA
		makeSetRecord(schema, 0b0001),
		makeSetRecord(schema, 0b0001),
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if n, _ := op["total_label_observations"].(int); n != 3 {
		t.Errorf("total_label_observations = %d, want 3", n)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 1 {
		t.Fatalf("buckets len = %d, want 1", len(buckets))
	}
	b := buckets[0]
	if k, _ := b["key"].(string); k != "VISA" {
		t.Errorf("bucket[0] key = %q, want %q", k, "VISA")
	}
	if l, _ := b["label"].(string); l != "VISA" {
		t.Errorf("bucket[0] label = %q, want %q", l, "VISA")
	}
	if c, _ := b["count"].(int); c != 3 {
		t.Errorf("bucket[0] count = %d, want 3", c)
	}
	if di, _ := b["dict_index"].(int); di != 0 {
		t.Errorf("bucket[0] dict_index = %d, want 0 (VISA is bit 0)", di)
	}
}

func TestGrouper_SetPerElement_Components_MultiBitFanOut(t *testing.T) {
	// Rows with popcount > 1 fan into multiple buckets.
	// total_label_observations must EXCEED row count (TotalN).
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // VISA + MC → 2 obs
		makeSetRecord(schema, 0b0110), // MC + AMEX → 2 obs
		makeSetRecord(schema, 0b0001), // VISA → 1 obs
	}
	// Total rows = 3, but total observations = 2 + 2 + 1 = 5.
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	totalObs, _ := op["total_label_observations"].(int)
	if totalObs != 5 {
		t.Errorf("total_label_observations = %d, want 5", totalObs)
	}
	if totalObs <= len(recs) {
		t.Errorf("total_label_observations (%d) must exceed row count (%d) on multi-bit rows",
			totalObs, len(recs))
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 3 {
		t.Fatalf("buckets len = %d, want 3 (VISA, MC, AMEX)", len(buckets))
	}
	// Buckets ordered by dict_index ascending → VISA(0), MC(1), AMEX(2).
	wantOrder := []struct {
		label string
		count int
		dict  int
	}{
		{"VISA", 2, 0},
		{"MC", 2, 1},
		{"AMEX", 1, 2},
	}
	for i, want := range wantOrder {
		b := buckets[i]
		if k, _ := b["key"].(string); k != want.label {
			t.Errorf("bucket[%d] key = %q, want %q", i, k, want.label)
		}
		if l, _ := b["label"].(string); l != want.label {
			t.Errorf("bucket[%d] label = %q, want %q", i, l, want.label)
		}
		if c, _ := b["count"].(int); c != want.count {
			t.Errorf("bucket[%d] count = %d, want %d", i, c, want.count)
		}
		if di, _ := b["dict_index"].(int); di != want.dict {
			t.Errorf("bucket[%d] dict_index = %d, want %d", i, di, want.dict)
		}
	}
}

func TestGrouper_SetPerElement_Components_EmptyMasksDoNotCount(t *testing.T) {
	// Empty-mask rows contribute to zero buckets. Verify they are
	// absent from total_label_observations.
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0001), // VISA
		makeSetRecord(schema, 0),      // empty — skipped
		makeSetRecord(schema, 0),      // empty — skipped
		makeSetRecord(schema, 0b0010), // MC
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if n, _ := op["total_label_observations"].(int); n != 2 {
		t.Errorf("total_label_observations = %d, want 2 (empty masks excluded)", n)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 2 {
		t.Fatalf("buckets len = %d, want 2", len(buckets))
	}
}

func TestGrouper_SetPerElement_Components_DictIndexMatchesDictOrder(t *testing.T) {
	// Verify dict_index matches the bit position (== dictionary order)
	// for every observed label. Uses the wider set_u16 fixture so
	// indices 4+ are exercised.
	schema := makeSetU16Schema(t)
	g, err := newSetPerElementGrouper(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	// Set bits 0, 4, 8 → APPLE, ELDERBERRY, KIWI
	recs := []*Record{
		makeSetRecord(schema, uint64(1)<<0|uint64(1)<<4|uint64(1)<<8),
	}
	if _, err := g.Group(recs, "tags"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 3 {
		t.Fatalf("buckets len = %d, want 3", len(buckets))
	}
	wantOrder := []struct {
		label string
		dict  int
	}{
		{"APPLE", 0},
		{"ELDERBERRY", 4},
		{"KIWI", 8},
	}
	for i, want := range wantOrder {
		b := buckets[i]
		if l, _ := b["label"].(string); l != want.label {
			t.Errorf("bucket[%d] label = %q, want %q", i, l, want.label)
		}
		if di, _ := b["dict_index"].(int); di != want.dict {
			t.Errorf("bucket[%d] dict_index = %d, want %d (bit position == dict order)",
				i, di, want.dict)
		}
	}
}

func TestGrouper_SetPerElement_Components_StreamingKeysForRow(t *testing.T) {
	// Drive the multi-key streaming path directly via KeysForRow
	// (not Group) and verify Components() reflects the streaming
	// fold identically to the buffered Group() path.
	schema := makeSetTestSchema(t)
	g, err := newSetPerElementGrouper(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetPerElementGrouper: %v", err)
	}
	mg, ok := g.(MultiKeyStreamingGrouper)
	if !ok {
		t.Fatal("setPerElementGrouper must implement MultiKeyStreamingGrouper")
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // VISA + MC
		makeSetRecord(schema, 0b0100), // AMEX
		makeSetRecord(schema, 0b0001), // VISA
	}
	for _, r := range recs {
		if _, _, err := mg.KeysForRow(r, "tags"); err != nil {
			t.Fatalf("KeysForRow: %v", err)
		}
	}
	op, _ := g.(MetaGrouper).Components()
	if n, _ := op["total_label_observations"].(int); n != 4 {
		t.Errorf("total_label_observations = %d, want 4 (streaming fold)", n)
	}
	// VISA: 2, MC: 1, AMEX: 1
	buckets, _ := op["buckets"].([]map[string]any)
	counts := make(map[string]int)
	for _, b := range buckets {
		label, _ := b["label"].(string)
		c, _ := b["count"].(int)
		counts[label] = c
	}
	want := map[string]int{"VISA": 2, "MC": 1, "AMEX": 1}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("per-label counts = %v, want %v", counts, want)
	}
}

func TestGrouper_SetValue_Components_StreamingKeyForRow(t *testing.T) {
	// Drive the streaming path via KeyForRow (not Group) and verify
	// liveBuckets / nEmptyMask track the same way as the buffered
	// Group() path.
	schema := makeSetTestSchema(t)
	g, err := newSetValueGrouper(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, schema)
	if err != nil {
		t.Fatalf("newSetValueGrouper: %v", err)
	}
	sg, ok := g.(StreamingGrouper)
	if !ok {
		t.Fatal("setValueGrouper must implement StreamingGrouper")
	}
	recs := []*Record{
		makeSetRecord(schema, 0b0011), // MC|VISA
		makeSetRecord(schema, 0b0011), // MC|VISA
		makeSetRecord(schema, 0),      // empty
	}
	for _, r := range recs {
		if _, _, err := sg.KeyForRow(r, "tags"); err != nil {
			t.Fatalf("KeyForRow: %v", err)
		}
	}
	op, _ := g.(MetaGrouper).Components()
	if n, _ := op["n_empty_mask"].(int); n != 1 {
		t.Errorf("n_empty_mask = %d, want 1 (streaming fold)", n)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	// Sorted by key: "", "MC|VISA"
	keys := []string{}
	for _, b := range buckets {
		k, _ := b["key"].(string)
		keys = append(keys, k)
	}
	sort.Strings(keys)
	wantKeys := []string{"", "MC|VISA"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("bucket keys = %v, want %v", keys, wantKeys)
	}
}

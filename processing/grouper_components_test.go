package processing

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// E2-S4 — MetaGrouper.Components() emission for GROUP_CATEGORY and
// GROUP_DATE. These tests exercise the operator-specific keys only;
// the universal floor ({total_n, n_null}) is filled by the
// orchestrator and is verified end-to-end via
// service/process_components_test.go's TestService_Process_Components_*
// suites at the boundary where Process drives both.

// --- GROUP_CATEGORY ------------------------------------------------

// makeCategoricalSchemaWithDictSize builds a categoricalSchema-style
// schema whose dictionary carries dictSize entries. Used to exercise
// the dict_size key across the u8 (<256) and u32 (>65535) width
// boundaries.
func makeCategoricalSchemaWithDictSize(t *testing.T, fieldType encoding.FieldType, dictSize int) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < dictSize; i++ {
		// Synth label "LBL_<i>" — content irrelevant, only count matters.
		_, err := dict.Add("LBL_" + intToStr(i))
		if err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "brand", Type: fieldType, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
}

// intToStr is a tiny helper to avoid importing strconv just for the
// synth label loop above.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 12)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

func TestGrouper_Category_Components_EmptyCohort(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "age", 0, schema)

	if _, err := g.Group([]*Record{}, "age"); err != nil {
		t.Fatalf("Group: %v", err)
	}

	meta, ok := g.(MetaGrouper)
	if !ok {
		t.Fatalf("categoryGrouper does not implement MetaGrouper")
	}
	op, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	wantKeys := []string{"buckets", "dict_size"}
	got := mapKeysSortedAny(op)
	if !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("Components keys = %v, want %v", got, wantKeys)
	}
	if ds, _ := op["dict_size"].(int); ds != 0 {
		t.Errorf("dict_size = %d, want 0 (non-categorical field)", ds)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 0 {
		t.Errorf("buckets len = %d, want 0", len(buckets))
	}
	if buckets == nil {
		t.Errorf("buckets nil; want non-nil empty slice")
	}
}

func TestGrouper_Category_Components_CategoricalSmallCohort(t *testing.T) {
	schema := categoricalSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "brand", 0, schema)

	records := make([]*Record, 5)
	records[0] = NewRecord(schema, map[string]float64{"brand": 0, "score": 10})
	records[1] = NewRecord(schema, map[string]float64{"brand": 1, "score": 20})
	records[2] = NewRecord(schema, map[string]float64{"brand": 0, "score": 30})
	records[3] = NewRecord(schema, map[string]float64{"brand": 2, "score": 40})
	records[4] = NewRecord(schema, map[string]float64{"brand": 1, "score": 50})

	if _, err := g.Group(records, "brand"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	meta := g.(MetaGrouper)
	op, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}

	// dict_size: 3 (Apple, Samsung, Google)
	if ds, _ := op["dict_size"].(int); ds != 3 {
		t.Errorf("dict_size = %d, want 3", ds)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 3 {
		t.Fatalf("buckets len = %d, want 3 (sorted)", len(buckets))
	}
	// Buckets emit sorted by key. Schema dict order is Apple, Samsung,
	// Google → sorted alphabetically: Apple, Google, Samsung.
	wantCounts := map[string]int{"Apple": 2, "Google": 1, "Samsung": 2}
	for _, b := range buckets {
		key, _ := b["key"].(string)
		label, _ := b["label"].(string)
		count, _ := b["count"].(int)
		if key != label {
			t.Errorf("bucket key=%q label=%q; want key == label", key, label)
		}
		if wantCounts[key] != count {
			t.Errorf("bucket %q count = %d, want %d", key, count, wantCounts[key])
		}
	}
	// Buckets must be sorted.
	if buckets[0]["key"] != "Apple" || buckets[1]["key"] != "Google" || buckets[2]["key"] != "Samsung" {
		t.Errorf("bucket key order = [%s %s %s], want [Apple Google Samsung]",
			buckets[0]["key"], buckets[1]["key"], buckets[2]["key"])
	}
}

func TestGrouper_Category_Components_AllNullInputs(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "age", 0, schema)

	records := make([]*Record, 4)
	for i := range records {
		records[i] = NewRecordWithNulls(schema,
			map[string]float64{"age": 0},
			map[string]bool{"age": true})
	}
	if _, err := g.Group(records, "age"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	meta := g.(MetaGrouper)
	op, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 0 {
		t.Errorf("buckets len = %d, want 0 (all null inputs)", len(buckets))
	}
	// dict_size still 0 — numeric field, no dictionary.
	if ds, _ := op["dict_size"].(int); ds != 0 {
		t.Errorf("dict_size = %d, want 0", ds)
	}
}

func TestGrouper_Category_Components_NarrowDict_U8(t *testing.T) {
	// 3-entry dict, well below 256 (u8 cap).
	schema := makeCategoricalSchemaWithDictSize(t, encoding.FieldTypeCategoricalU8, 3)
	g := makeGrouper(t, types.GROUP_CATEGORY, "brand", 0, schema)
	records := []*Record{
		NewRecord(schema, map[string]float64{"brand": 0}),
		NewRecord(schema, map[string]float64{"brand": 1}),
		NewRecord(schema, map[string]float64{"brand": 2}),
	}
	if _, err := g.Group(records, "brand"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	meta := g.(MetaGrouper)
	op, _ := meta.Components()
	if ds, _ := op["dict_size"].(int); ds != 3 {
		t.Errorf("dict_size = %d, want 3 (narrow dict)", ds)
	}
}

func TestGrouper_Category_Components_WideDict_U32(t *testing.T) {
	// Dictionary cardinality > 65535 — exercises the categorical_u32
	// width and verifies dict_size is reported as the underlying
	// dictionary Count(), not capped to u16.
	const dictSize = 65540
	schema := makeCategoricalSchemaWithDictSize(t, encoding.FieldTypeCategoricalU32, dictSize)
	g := makeGrouper(t, types.GROUP_CATEGORY, "brand", 0, schema)
	// Two rows referencing dict indices in the wide range.
	records := []*Record{
		NewRecord(schema, map[string]float64{"brand": 0}),
		NewRecord(schema, map[string]float64{"brand": 65539}),
	}
	if _, err := g.Group(records, "brand"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	meta := g.(MetaGrouper)
	op, _ := meta.Components()
	if ds, _ := op["dict_size"].(int); ds != dictSize {
		t.Errorf("dict_size = %d, want %d (wide u32 dict)", ds, dictSize)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 2 {
		t.Errorf("buckets len = %d, want 2", len(buckets))
	}
}

// --- GROUP_DATE ----------------------------------------------------

// dateGrouperWithComponent constructs a dateGrouper with the supplied
// component via the grouper factory. Mirrors makeGrouper but accepts
// a params blob for the date component.
func dateGrouperWithComponent(t *testing.T, schema *encoding.Schema, field, component string) Grouper {
	t.Helper()
	factory := grouperRegistry[types.GROUP_DATE]
	if factory == nil {
		t.Fatalf("no grouper registered for GROUP_DATE")
	}
	params := []byte(`{"component":"` + component + `"}`)
	g, err := factory(&types.Group{
		Type:   types.GROUP_DATE,
		Field:  field,
		Params: params,
	}, schema)
	if err != nil {
		t.Fatalf("create date grouper (%s): %v", component, err)
	}
	return g
}

// daysSinceEpoch converts a YYYY-MM-DD string into days since the
// Unix epoch, matching the dateGrouper's input contract.
func daysSinceEpoch(t *testing.T, ymd string) float64 {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", ymd)
	if err != nil {
		t.Fatalf("parse %q: %v", ymd, err)
	}
	return float64(parsed.Unix() / 86400)
}

func TestGrouper_Date_Components_DayGranularity(t *testing.T) {
	schema := dateSchema()
	g := dateGrouperWithComponent(t, schema, "enrolled", "day")
	records := []*Record{
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-15")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-16")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-17")}),
	}
	if _, err := g.Group(records, "enrolled"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()

	if g, _ := op["granularity"].(string); g != "day" {
		t.Errorf("granularity = %q, want %q", g, "day")
	}
	if n, _ := op["n_buckets"].(int); n != 3 {
		t.Errorf("n_buckets = %d, want 3", n)
	}
	if rs, _ := op["range_start"].(string); rs != "2024-03-15" {
		t.Errorf("range_start = %q, want %q", rs, "2024-03-15")
	}
	if re, _ := op["range_end"].(string); re != "2024-03-17" {
		t.Errorf("range_end = %q, want %q", re, "2024-03-17")
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 3 {
		t.Fatalf("buckets len = %d, want 3", len(buckets))
	}
	// Per-bucket period_start == period_end == key for day granularity.
	for _, b := range buckets {
		ps, _ := b["period_start"].(string)
		pe, _ := b["period_end"].(string)
		key, _ := b["key"].(string)
		if ps != key || pe != key {
			t.Errorf("day bucket %q: period_start=%q period_end=%q; want both = key", key, ps, pe)
		}
		if c, _ := b["count"].(int); c != 1 {
			t.Errorf("bucket %q count = %d, want 1", key, c)
		}
	}
}

func TestGrouper_Date_Components_WeekGranularity(t *testing.T) {
	schema := dateSchema()
	g := dateGrouperWithComponent(t, schema, "enrolled", "week")
	// 2024-03-15 (Fri, ISO week 11), 2024-03-16 (Sat, week 11),
	// 2024-03-18 (Mon, week 12), 2024-03-19 (Tue, week 12).
	records := []*Record{
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-15")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-16")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-18")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-19")}),
	}
	if _, err := g.Group(records, "enrolled"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if g, _ := op["granularity"].(string); g != "week" {
		t.Errorf("granularity = %q, want %q", g, "week")
	}
	if n, _ := op["n_buckets"].(int); n != 2 {
		t.Errorf("n_buckets = %d, want 2", n)
	}
	// range_start = Monday of earliest week = 2024-03-11.
	// range_end   = Sunday of latest week  = 2024-03-24.
	if rs, _ := op["range_start"].(string); rs != "2024-03-11" {
		t.Errorf("range_start = %q, want %q", rs, "2024-03-11")
	}
	if re, _ := op["range_end"].(string); re != "2024-03-24" {
		t.Errorf("range_end = %q, want %q", re, "2024-03-24")
	}
	buckets, _ := op["buckets"].([]map[string]any)
	wantCounts := map[string][3]string{
		"2024-W11": {"2024-03-11", "2024-03-17", "2"},
		"2024-W12": {"2024-03-18", "2024-03-24", "2"},
	}
	for _, b := range buckets {
		key, _ := b["key"].(string)
		want, ok := wantCounts[key]
		if !ok {
			t.Errorf("unexpected week bucket %q", key)
			continue
		}
		if ps, _ := b["period_start"].(string); ps != want[0] {
			t.Errorf("bucket %q period_start = %q, want %q", key, ps, want[0])
		}
		if pe, _ := b["period_end"].(string); pe != want[1] {
			t.Errorf("bucket %q period_end = %q, want %q", key, pe, want[1])
		}
		if c, _ := b["count"].(int); intToStr(c) != want[2] {
			t.Errorf("bucket %q count = %d, want %s", key, c, want[2])
		}
	}
}

func TestGrouper_Date_Components_MonthGranularity(t *testing.T) {
	schema := dateSchema()
	g := dateGrouperWithComponent(t, schema, "enrolled", "month")
	// March + April 2024.
	records := []*Record{
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-01")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-15")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-31")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-04-15")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-04-30")}),
	}
	if _, err := g.Group(records, "enrolled"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if g, _ := op["granularity"].(string); g != "month" {
		t.Errorf("granularity = %q, want %q", g, "month")
	}
	if n, _ := op["n_buckets"].(int); n != 2 {
		t.Errorf("n_buckets = %d, want 2", n)
	}
	if rs, _ := op["range_start"].(string); rs != "2024-03-01" {
		t.Errorf("range_start = %q, want %q", rs, "2024-03-01")
	}
	if re, _ := op["range_end"].(string); re != "2024-04-30" {
		t.Errorf("range_end = %q, want %q", re, "2024-04-30")
	}
	buckets, _ := op["buckets"].([]map[string]any)
	want := map[string][3]string{
		"2024-03": {"2024-03-01", "2024-03-31", "3"},
		"2024-04": {"2024-04-01", "2024-04-30", "2"},
	}
	for _, b := range buckets {
		key, _ := b["key"].(string)
		w := want[key]
		if ps, _ := b["period_start"].(string); ps != w[0] {
			t.Errorf("bucket %q period_start = %q, want %q", key, ps, w[0])
		}
		if pe, _ := b["period_end"].(string); pe != w[1] {
			t.Errorf("bucket %q period_end = %q, want %q", key, pe, w[1])
		}
		if c, _ := b["count"].(int); intToStr(c) != w[2] {
			t.Errorf("bucket %q count = %d, want %s", key, c, w[2])
		}
	}
}

func TestGrouper_Date_Components_QuarterGranularity(t *testing.T) {
	schema := dateSchema()
	g := dateGrouperWithComponent(t, schema, "enrolled", "quarter")
	// Q1 2024 (Jan-Mar) + Q2 2024 (Apr-Jun) — 4 + 4 rows.
	records := []*Record{
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-01-15")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-02-20")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-25")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-03-31")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-04-01")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-05-10")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-06-15")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-06-30")}),
	}
	if _, err := g.Group(records, "enrolled"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if g, _ := op["granularity"].(string); g != "quarter" {
		t.Errorf("granularity = %q, want %q", g, "quarter")
	}
	if n, _ := op["n_buckets"].(int); n != 2 {
		t.Errorf("n_buckets = %d, want 2", n)
	}
	if rs, _ := op["range_start"].(string); rs != "2024-01-01" {
		t.Errorf("range_start = %q, want %q", rs, "2024-01-01")
	}
	if re, _ := op["range_end"].(string); re != "2024-06-30" {
		t.Errorf("range_end = %q, want %q", re, "2024-06-30")
	}
	buckets, _ := op["buckets"].([]map[string]any)
	want := map[string][3]string{
		"2024-Q1": {"2024-01-01", "2024-03-31", "4"},
		"2024-Q2": {"2024-04-01", "2024-06-30", "4"},
	}
	for _, b := range buckets {
		key, _ := b["key"].(string)
		w := want[key]
		if ps, _ := b["period_start"].(string); ps != w[0] {
			t.Errorf("bucket %q period_start = %q, want %q", key, ps, w[0])
		}
		if pe, _ := b["period_end"].(string); pe != w[1] {
			t.Errorf("bucket %q period_end = %q, want %q", key, pe, w[1])
		}
		if c, _ := b["count"].(int); intToStr(c) != w[2] {
			t.Errorf("bucket %q count = %d, want %s", key, c, w[2])
		}
	}
}

func TestGrouper_Date_Components_YearGranularity(t *testing.T) {
	schema := dateSchema()
	g := dateGrouperWithComponent(t, schema, "enrolled", "year")
	records := []*Record{
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2023-01-15")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2023-06-30")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2023-12-31")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-01-01")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-07-04")}),
		NewRecord(schema, map[string]float64{"enrolled": daysSinceEpoch(t, "2024-12-31")}),
	}
	if _, err := g.Group(records, "enrolled"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if g, _ := op["granularity"].(string); g != "year" {
		t.Errorf("granularity = %q, want %q", g, "year")
	}
	if n, _ := op["n_buckets"].(int); n != 2 {
		t.Errorf("n_buckets = %d, want 2", n)
	}
	if rs, _ := op["range_start"].(string); rs != "2023-01-01" {
		t.Errorf("range_start = %q, want %q", rs, "2023-01-01")
	}
	if re, _ := op["range_end"].(string); re != "2024-12-31" {
		t.Errorf("range_end = %q, want %q", re, "2024-12-31")
	}
	buckets, _ := op["buckets"].([]map[string]any)
	want := map[string][3]string{
		"2023": {"2023-01-01", "2023-12-31", "3"},
		"2024": {"2024-01-01", "2024-12-31", "3"},
	}
	for _, b := range buckets {
		key, _ := b["key"].(string)
		w := want[key]
		if ps, _ := b["period_start"].(string); ps != w[0] {
			t.Errorf("bucket %q period_start = %q, want %q", key, ps, w[0])
		}
		if pe, _ := b["period_end"].(string); pe != w[1] {
			t.Errorf("bucket %q period_end = %q, want %q", key, pe, w[1])
		}
		if c, _ := b["count"].(int); intToStr(c) != w[2] {
			t.Errorf("bucket %q count = %d, want %s", key, c, w[2])
		}
	}
}

// --- shared helpers -------------------------------------------------

// mapKeysSortedAny returns the sorted key set of m, mirroring the
// helper from service/process_components_test.go but local to
// processing/ so this file stays self-contained.
func mapKeysSortedAny(m map[string]any) []string {
	if len(m) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

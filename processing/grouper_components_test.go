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

// --- GROUP_RANGE ---------------------------------------------------
//
// E2-S5 — MetaGrouper.Components() emission for the three edge-based
// groupers. RANGE / ROUNDED declare ComponentsMergeability=Mergeable
// (per-bucket counts fold across chunks); QUANTILE is None
// (needs a sorted view of the full input). The buffered-only emission
// contract for QUANTILE is exercised here; the streaming-path per-
// chunk omission lands in E4-S4 wiring.

func TestGrouper_Range_Components_EmptyCohort(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "score", 10, schema)

	if _, err := g.Group([]*Record{}, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	meta, ok := g.(MetaGrouper)
	if !ok {
		t.Fatalf("rangeGrouper does not implement MetaGrouper")
	}
	op, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}

	wantKeys := []string{
		"buckets", "edges", "interval", "n_buckets",
		"overflow_count", "range_max", "range_min", "underflow_count",
	}
	got := mapKeysSortedAny(op)
	if !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("Components keys = %v, want %v", got, wantKeys)
	}
	if iv, _ := op["interval"].(float64); iv != 10 {
		t.Errorf("interval = %v, want 10", iv)
	}
	if n, _ := op["n_buckets"].(int); n != 0 {
		t.Errorf("n_buckets = %d, want 0", n)
	}
	if rmin, _ := op["range_min"].(float64); rmin != 0 {
		t.Errorf("range_min = %v, want 0 (no rows observed)", rmin)
	}
	if rmax, _ := op["range_max"].(float64); rmax != 0 {
		t.Errorf("range_max = %v, want 0 (no rows observed)", rmax)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 0 {
		t.Errorf("buckets len = %d, want 0", len(buckets))
	}
	if buckets == nil {
		t.Errorf("buckets nil; want non-nil empty slice")
	}
	edges, _ := op["edges"].([]float64)
	if len(edges) != 0 {
		t.Errorf("edges len = %d, want 0", len(edges))
	}
	if edges == nil {
		t.Errorf("edges nil; want non-nil empty slice")
	}
	if uc, _ := op["underflow_count"].(int); uc != 0 {
		t.Errorf("underflow_count = %d, want 0", uc)
	}
	if oc, _ := op["overflow_count"].(int); oc != 0 {
		t.Errorf("overflow_count = %d, want 0", oc)
	}
}

func TestGrouper_Range_Components_AllInRange(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "score", 10, schema)
	records := makeRecords(schema, "score", []float64{5, 12, 18, 25, 33, 47})
	if _, err := g.Group(records, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if iv, _ := op["interval"].(float64); iv != 10 {
		t.Errorf("interval = %v, want 10", iv)
	}
	if rmin, _ := op["range_min"].(float64); rmin != 5 {
		t.Errorf("range_min = %v, want 5 (smallest observed)", rmin)
	}
	if rmax, _ := op["range_max"].(float64); rmax != 47 {
		t.Errorf("range_max = %v, want 47 (largest observed)", rmax)
	}
	// Auto-detected bounds → no out-of-range rows.
	if uc, _ := op["underflow_count"].(int); uc != 0 {
		t.Errorf("underflow_count = %d, want 0 (all in range)", uc)
	}
	if oc, _ := op["overflow_count"].(int); oc != 0 {
		t.Errorf("overflow_count = %d, want 0 (all in range)", oc)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	// Expected bins: 0-10 (1), 10-20 (2), 20-30 (1), 30-40 (1), 40-50 (1).
	wantBuckets := []struct {
		key   string
		low   float64
		high  float64
		count int
	}{
		{"0-10", 0, 10, 1},
		{"10-20", 10, 20, 2},
		{"20-30", 20, 30, 1},
		{"30-40", 30, 40, 1},
		{"40-50", 40, 50, 1},
	}
	if len(buckets) != len(wantBuckets) {
		t.Fatalf("buckets len = %d, want %d", len(buckets), len(wantBuckets))
	}
	for i, want := range wantBuckets {
		got := buckets[i]
		if k, _ := got["key"].(string); k != want.key {
			t.Errorf("bucket[%d] key = %q, want %q", i, k, want.key)
		}
		if l, _ := got["low"].(float64); l != want.low {
			t.Errorf("bucket[%d] low = %v, want %v", i, l, want.low)
		}
		if h, _ := got["high"].(float64); h != want.high {
			t.Errorf("bucket[%d] high = %v, want %v", i, h, want.high)
		}
		if c, _ := got["count"].(int); c != want.count {
			t.Errorf("bucket[%d] count = %d, want %d", i, c, want.count)
		}
	}
	// Edges = sorted lows + top high → [0, 10, 20, 30, 40, 50].
	edges, _ := op["edges"].([]float64)
	wantEdges := []float64{0, 10, 20, 30, 40, 50}
	if !reflect.DeepEqual(edges, wantEdges) {
		t.Errorf("edges = %v, want %v", edges, wantEdges)
	}
}

func TestGrouper_Range_Components_NegativeValues(t *testing.T) {
	// Cross-zero coverage — verifies the bucket-low numeric sort
	// keeps "-20--10" before "-10-0" rather than relying on the
	// lexical string ordering that would group all "-" prefixes
	// together.
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "score", 10, schema)
	records := makeRecords(schema, "score", []float64{-15, -5, 5, 15})
	if _, err := g.Group(records, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if rmin, _ := op["range_min"].(float64); rmin != -15 {
		t.Errorf("range_min = %v, want -15", rmin)
	}
	if rmax, _ := op["range_max"].(float64); rmax != 15 {
		t.Errorf("range_max = %v, want 15", rmax)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 4 {
		t.Fatalf("buckets len = %d, want 4", len(buckets))
	}
	wantOrder := []string{"-20--10", "-10-0", "0-10", "10-20"}
	for i, want := range wantOrder {
		if got, _ := buckets[i]["key"].(string); got != want {
			t.Errorf("bucket[%d] key = %q, want %q", i, got, want)
		}
	}
	edges, _ := op["edges"].([]float64)
	wantEdges := []float64{-20, -10, 0, 10, 20}
	if !reflect.DeepEqual(edges, wantEdges) {
		t.Errorf("edges = %v, want %v", edges, wantEdges)
	}
}

func TestGrouper_Range_Components_NullValuesSkipped(t *testing.T) {
	// Null inputs participate in n_null (orchestrator floor) but
	// never widen range_min/range_max or land in a bucket.
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "score", 10, schema)
	records := makeRecordsWithNulls(schema, "score",
		[]float64{5, 0, 25}, []int{1})
	if _, err := g.Group(records, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if rmin, _ := op["range_min"].(float64); rmin != 5 {
		t.Errorf("range_min = %v, want 5 (null skipped)", rmin)
	}
	if rmax, _ := op["range_max"].(float64); rmax != 25 {
		t.Errorf("range_max = %v, want 25", rmax)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 2 {
		t.Fatalf("buckets len = %d, want 2 (null skipped)", len(buckets))
	}
}

func TestGrouper_Range_Components_SingleBucket(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "score", 10, schema)
	records := makeRecords(schema, "score", []float64{42, 45, 49})
	if _, err := g.Group(records, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 1 {
		t.Fatalf("buckets len = %d, want 1", len(buckets))
	}
	if k, _ := buckets[0]["key"].(string); k != "40-50" {
		t.Errorf("bucket key = %q, want %q", k, "40-50")
	}
	if c, _ := buckets[0]["count"].(int); c != 3 {
		t.Errorf("bucket count = %d, want 3", c)
	}
	edges, _ := op["edges"].([]float64)
	wantEdges := []float64{40, 50}
	if !reflect.DeepEqual(edges, wantEdges) {
		t.Errorf("edges = %v, want %v", edges, wantEdges)
	}
}

// --- GROUP_ROUNDED -------------------------------------------------

func TestGrouper_Rounded_Components_EmptyCohort(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "score", 5, schema)
	if _, err := g.Group([]*Record{}, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	meta, ok := g.(MetaGrouper)
	if !ok {
		t.Fatalf("roundedGrouper does not implement MetaGrouper")
	}
	op, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}

	wantKeys := []string{"buckets", "edges", "precision"}
	got := mapKeysSortedAny(op)
	if !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("Components keys = %v, want %v", got, wantKeys)
	}
	if p, _ := op["precision"].(float64); p != 5 {
		t.Errorf("precision = %v, want 5", p)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 0 {
		t.Errorf("buckets len = %d, want 0", len(buckets))
	}
	if buckets == nil {
		t.Errorf("buckets nil; want non-nil empty slice")
	}
	edges, _ := op["edges"].([]float64)
	if len(edges) != 0 {
		t.Errorf("edges len = %d, want 0", len(edges))
	}
	if edges == nil {
		t.Errorf("edges nil; want non-nil empty slice")
	}
}

func TestGrouper_Rounded_Components_MultipleBuckets(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "score", 10, schema)
	// 23,27 → 20; 35 → 30; 12 → 10; 48 → 40.
	records := makeRecords(schema, "score", []float64{23, 27, 35, 12, 48})
	if _, err := g.Group(records, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if p, _ := op["precision"].(float64); p != 10 {
		t.Errorf("precision = %v, want 10", p)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 4 {
		t.Fatalf("buckets len = %d, want 4", len(buckets))
	}
	wantBuckets := []struct {
		key   string
		low   float64
		count int
	}{
		{"10", 10, 1},
		{"20", 20, 2},
		{"30", 30, 1},
		{"40", 40, 1},
	}
	for i, want := range wantBuckets {
		got := buckets[i]
		if k, _ := got["key"].(string); k != want.key {
			t.Errorf("bucket[%d] key = %q, want %q", i, k, want.key)
		}
		if l, _ := got["low"].(float64); l != want.low {
			t.Errorf("bucket[%d] low = %v, want %v", i, l, want.low)
		}
		if h, _ := got["high"].(float64); h != want.low {
			// ROUNDED: low == high (point on value axis).
			t.Errorf("bucket[%d] high = %v, want %v (== low for ROUNDED)", i, h, want.low)
		}
		if c, _ := got["count"].(int); c != want.count {
			t.Errorf("bucket[%d] count = %d, want %d", i, c, want.count)
		}
	}
	edges, _ := op["edges"].([]float64)
	wantEdges := []float64{10, 20, 30, 40}
	if !reflect.DeepEqual(edges, wantEdges) {
		t.Errorf("edges = %v, want %v", edges, wantEdges)
	}
}

func TestGrouper_Rounded_Components_SingleBucket(t *testing.T) {
	// Values within `interval` of one scalar all collapse to the
	// same bucket; ROUNDED is a single-point partition.
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "score", 10, schema)
	records := makeRecords(schema, "score", []float64{21, 23, 27, 29})
	if _, err := g.Group(records, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 1 {
		t.Fatalf("buckets len = %d, want 1", len(buckets))
	}
	if k, _ := buckets[0]["key"].(string); k != "20" {
		t.Errorf("bucket key = %q, want %q", k, "20")
	}
	if c, _ := buckets[0]["count"].(int); c != 4 {
		t.Errorf("bucket count = %d, want 4", c)
	}
	edges, _ := op["edges"].([]float64)
	wantEdges := []float64{20}
	if !reflect.DeepEqual(edges, wantEdges) {
		t.Errorf("edges = %v, want %v", edges, wantEdges)
	}
}

func TestGrouper_Rounded_Components_FloatInterval(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "score", 0.5, schema)
	records := makeRecords(schema, "score", []float64{0.1, 0.3, 0.6, 0.8, 1.2})
	if _, err := g.Group(records, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 3 {
		t.Fatalf("buckets len = %d, want 3", len(buckets))
	}
	wantLows := []float64{0, 0.5, 1}
	for i, want := range wantLows {
		if l, _ := buckets[i]["low"].(float64); l != want {
			t.Errorf("bucket[%d] low = %v, want %v", i, l, want)
		}
	}
}

// --- GROUP_QUANTILE ------------------------------------------------

func TestGrouper_Quantile_Components_EmptyCohort(t *testing.T) {
	// Empty input: Group() still runs (frozen state stamped), but
	// the resulting buckets / edges slices are empty. The schema
	// contract emits the keys with empty payloads so downstream
	// consumers see a uniform shape.
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "score", 4, schema)
	if _, err := g.Group([]*Record{}, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	meta, ok := g.(MetaGrouper)
	if !ok {
		t.Fatalf("quantileGrouper does not implement MetaGrouper")
	}
	op, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	if op == nil {
		t.Fatalf("Components returned nil; want emitted shape after Group()")
	}
	wantKeys := []string{"buckets", "edges", "method", "n_quantiles"}
	got := mapKeysSortedAny(op)
	if !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("Components keys = %v, want %v", got, wantKeys)
	}
	if n, _ := op["n_quantiles"].(int); n != 4 {
		t.Errorf("n_quantiles = %d, want 4", n)
	}
	if m, _ := op["method"].(string); m != "linear" {
		t.Errorf("method = %q, want %q", m, "linear")
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 0 {
		t.Errorf("buckets len = %d, want 0", len(buckets))
	}
	if buckets == nil {
		t.Errorf("buckets nil; want non-nil empty slice")
	}
	edges, _ := op["edges"].([]float64)
	if len(edges) != 0 {
		t.Errorf("edges len = %d, want 0", len(edges))
	}
	if edges == nil {
		t.Errorf("edges nil; want non-nil empty slice")
	}
}

func TestGrouper_Quantile_Components_BeforeGroup(t *testing.T) {
	// Calling Components() before Group() collapses to (nil, nil)
	// per the universal-floor convention — the orchestrator still
	// emits TotalN / NNull.
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "score", 4, schema)
	op, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	if op != nil {
		t.Errorf("Components before Group() = %v, want nil", op)
	}
}

func TestGrouper_Quantile_Components_Quartiles(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "score", 4, schema)
	// 8 sorted values → 2 per quartile.
	records := makeRecords(schema, "score", []float64{10, 20, 30, 40, 50, 60, 70, 80})
	if _, err := g.Group(records, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if n, _ := op["n_quantiles"].(int); n != 4 {
		t.Errorf("n_quantiles = %d, want 4", n)
	}
	if m, _ := op["method"].(string); m != "linear" {
		t.Errorf("method = %q, want %q", m, "linear")
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 4 {
		t.Fatalf("buckets len = %d, want 4", len(buckets))
	}
	wantBuckets := []struct {
		key   string
		low   float64
		high  float64
		count int
	}{
		{"Q1", 10, 20, 2},
		{"Q2", 30, 40, 2},
		{"Q3", 50, 60, 2},
		{"Q4", 70, 80, 2},
	}
	for i, want := range wantBuckets {
		got := buckets[i]
		if k, _ := got["key"].(string); k != want.key {
			t.Errorf("bucket[%d] key = %q, want %q", i, k, want.key)
		}
		if l, _ := got["low"].(float64); l != want.low {
			t.Errorf("bucket[%d] low = %v, want %v", i, l, want.low)
		}
		if h, _ := got["high"].(float64); h != want.high {
			t.Errorf("bucket[%d] high = %v, want %v", i, h, want.high)
		}
		if c, _ := got["count"].(int); c != want.count {
			t.Errorf("bucket[%d] count = %d, want %d", i, c, want.count)
		}
	}
	edges, _ := op["edges"].([]float64)
	wantEdges := []float64{10, 30, 50, 70, 80}
	if !reflect.DeepEqual(edges, wantEdges) {
		t.Errorf("edges = %v, want %v", edges, wantEdges)
	}
}

func TestGrouper_Quantile_Components_Deciles(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "score", 10, schema)
	// 20 sorted values → 2 per decile.
	values := make([]float64, 20)
	for i := 0; i < 20; i++ {
		values[i] = float64(i + 1)
	}
	records := makeRecords(schema, "score", values)
	if _, err := g.Group(records, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	if n, _ := op["n_quantiles"].(int); n != 10 {
		t.Errorf("n_quantiles = %d, want 10", n)
	}
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 10 {
		t.Fatalf("buckets len = %d, want 10", len(buckets))
	}
	// D1..D10 — keys must use the "D" prefix.
	for i, b := range buckets {
		wantKey := "D" + intToStr(i+1)
		if k, _ := b["key"].(string); k != wantKey {
			t.Errorf("bucket[%d] key = %q, want %q", i, k, wantKey)
		}
		if c, _ := b["count"].(int); c != 2 {
			t.Errorf("bucket[%d] count = %d, want 2", i, c)
		}
	}
	// Edge count = n_buckets + 1.
	edges, _ := op["edges"].([]float64)
	if len(edges) != 11 {
		t.Errorf("edges len = %d, want 11", len(edges))
	}
}

func TestGrouper_Quantile_Components_NonStandardCount(t *testing.T) {
	// 3 buckets uses the generic "B" prefix.
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "score", 3, schema)
	records := makeRecords(schema, "score", []float64{1, 2, 3, 4, 5, 6})
	if _, err := g.Group(records, "score"); err != nil {
		t.Fatalf("Group: %v", err)
	}
	op, _ := g.(MetaGrouper).Components()
	buckets, _ := op["buckets"].([]map[string]any)
	if len(buckets) != 3 {
		t.Fatalf("buckets len = %d, want 3", len(buckets))
	}
	wantKeys := []string{"B1", "B2", "B3"}
	for i, want := range wantKeys {
		if k, _ := buckets[i]["key"].(string); k != want {
			t.Errorf("bucket[%d] key = %q, want %q", i, k, want)
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

package processing

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// grouper_bucket_order_test.go pins the ordered-keys hook (keyOrderer,
// model-workbench E2-S3): GROUP_QUANTILE ranks by bucket id,
// GROUP_RANGE by numeric low edge, and the fused crosstab arm reproduces
// the buffered order — partial normalisation margins included.

func TestQuantileGrouper_KeyRank(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		buckets int
		key     string
		rank    float64
		ok      bool
	}{
		{10, "D1", 1, true},
		{10, "D10", 10, true},
		{10, "D2", 2, true},
		{4, "Q3", 3, true},
		{100, "P100", 100, true},
		{7, "B7", 7, true},
		{10, "D0", 0, false},
		{10, "D11", 0, false},
		{10, "Q1", 0, false},
		{10, "D", 0, false},
		{10, "Dx", 0, false},
		{10, "D-1", 0, false},
	} {
		g := &quantileGrouper{buckets: tc.buckets}
		rank, ok := g.keyRank(tc.key)
		if ok != tc.ok || (ok && rank != tc.rank) {
			t.Errorf("buckets %d keyRank(%q) = (%v, %v), want (%v, %v)", tc.buckets, tc.key, rank, ok, tc.rank, tc.ok)
		}
	}
}

func TestRangeGrouper_KeyRank(t *testing.T) {
	t.Parallel()

	g := &rangeGrouper{interval: 5}
	for _, tc := range []struct {
		key  string
		rank float64
		ok   bool
	}{
		{"0-5", 0, true},
		{"10-15", 10, true},
		{"-5-0", -5, true},
		{"-20--15", -20, true},
		{"-0.5-0", -0.5, true},
		{"2.5-5", 2.5, true},
		{"100-105", 100, true},
		{"NaN-NaN", 0, false},
		{"abc", 0, false},
		{"5", 0, false},
		{"", 0, false},
	} {
		rank, ok := g.keyRank(tc.key)
		if ok != tc.ok || (ok && rank != tc.rank) {
			t.Errorf("keyRank(%q) = (%v, %v), want (%v, %v)", tc.key, rank, ok, tc.rank, tc.ok)
		}
	}
}

// TestOrderKeys_BucketOrder: the grouped exit's ordering of a shuffled
// key set — bucket order for the two hook groupers, an unranked
// straggler last, alphabetical for a grouper without the hook.
func TestOrderKeys_BucketOrder(t *testing.T) {
	t.Parallel()

	shuffle := func(in []string) []string {
		out := append([]string(nil), in...)
		rand.New(rand.NewSource(7)).Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return out
	}
	deciles := []string{"D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10"}
	if got := orderKeysByInclude(includeFilterOf(&quantileGrouper{buckets: 10}), shuffle(deciles)); !reflect.DeepEqual(got, deciles) {
		t.Errorf("deciles = %v", got)
	}
	ranges := []string{"-20--10", "-10-0", "0-10", "2.5-5", "10-20", "100-110", "zz"}
	if got := orderKeysByInclude(includeFilterOf(&rangeGrouper{interval: 10}), shuffle(ranges)); !reflect.DeepEqual(got, ranges) {
		t.Errorf("ranges = %v", got)
	}
	// No hook: alphabetical, byte-identically (sort.Strings).
	cat := &categoryGrouper{}
	if f := includeFilterOf(cat); f != nil {
		t.Fatalf("category grouper without include has an order filter: %+v", f)
	}
	if got := orderKeysByInclude(includeFilterOf(cat), shuffle(deciles)); got[1] != "D10" {
		t.Errorf("hook-less grouper = %v, want alphabetical", got)
	}
	// GROUP_CATEGORY over numeric keys (#788): by value, a key that does
	// not parse (and NaN) after every ranked key, alphabetically.
	numbers := []string{"-10", "-5", "0", "2", "9.5", "10", "23", "100", "NaN", "x"}
	if got := orderKeysByInclude(includeFilterOf(&categoryGrouper{numericKeys: true}), shuffle(numbers)); !reflect.DeepEqual(got, numbers) {
		t.Errorf("numeric category keys = %v, want %v", got, numbers)
	}
}

// TestCategoryGrouper_NumericKeysFromSchema (#788): the value order is
// a schema-time verdict — on for every field whose keys are formatted
// numbers (a number, a date, an off-schema derived column, no schema at
// all), off for a dictionary categorical, whose label keys stay
// alphabetical (no order filter at all). An Include list still wins.
func TestCategoryGrouper_NumericKeysFromSchema(t *testing.T) {
	t.Parallel()
	dict := encoding.NewDictionary()
	for _, l := range []string{"10", "9"} {
		if _, err := dict.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "num", Type: encoding.FieldTypeU32},
		{Name: "day", Type: encoding.FieldTypeDate},
	}}
	for field, want := range map[string]bool{"cat": false, "num": true, "day": true, "derived_hr": true} {
		g, err := newCategoryGrouper(&types.Group{Type: types.GROUP_CATEGORY, Field: field}, schema)
		if err != nil {
			t.Fatal(err)
		}
		if f := keyOrderFilter(g); (f != nil) != want {
			t.Errorf("%s: key order filter %v, want ordered=%v", field, f, want)
		}
	}
	if g, err := newCategoryGrouper(&types.Group{Type: types.GROUP_CATEGORY, Field: "num"}, nil); err != nil || keyOrderFilter(g) == nil {
		t.Errorf("no schema: %v, %v; want the value order", g, err)
	}
	// A dictionary categorical whose labels look numeric stays byte-wise.
	cat, _ := newCategoryGrouper(&types.Group{Type: types.GROUP_CATEGORY, Field: "cat"}, schema)
	if got := orderKeysByInclude(includeFilterOf(cat), []string{"9", "10"}); !reflect.DeepEqual(got, []string{"10", "9"}) {
		t.Errorf("categorical labels = %v, want byte-wise", got)
	}
	// An include list wins over the value order.
	inc, _ := newCategoryGrouper(&types.Group{Type: types.GROUP_CATEGORY, Field: "num", Include: []string{"10", "2"}}, schema)
	if got := orderKeysByInclude(includeFilterOf(inc), []string{"2", "10"}); !reflect.DeepEqual(got, []string{"10", "2"}) {
		t.Errorf("include order = %v, want [10 2]", got)
	}
}

// TestFusedCrosstab_BucketOrderMatchesBuffered: a GROUP_RANGE row axis
// (keys "0-10" … "100-110", which alphabetical order scrambles) emits in
// bucket order on both arms — matrix shape, and long shape with a
// level-0 normalisation whose partial margin rows the fused arm orders
// in buildPartialAxisFromKeys.
func TestFusedCrosstab_BucketOrderMatchesBuffered(t *testing.T) {
	t.Parallel()

	schema := fusedCrosstabSchema(t)
	recs := fusedCrosstabRecords(schema)
	rangeRow := func() *types.Group { return &types.Group{Type: types.GROUP_RANGE, Field: "value", Interval: 10} }
	want := []string{"0-10", "10-20", "20-30", "30-40", "40-50", "50-60", "100-110"}

	matrix := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{rangeRow()},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
		Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
		Shape:   types.CrosstabShapeMatrix,
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
	}}
	b := runBufferedCrosstab(t, schema, matrix, recs)
	f := runFusedCrosstab(t, schema, matrix, recs)
	assertMatrixEqual(t, b.Crosstab.Matrix, f.Crosstab.Matrix)
	got := make([]string, 0, len(f.Crosstab.Matrix.RowKeys))
	for _, k := range f.Crosstab.Matrix.RowKeys {
		got = append(got, k[0].(string))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fused range rows = %v, want %v", got, want)
	}

	zero := 0
	long := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:           []*types.Group{rangeRow(), {Type: types.GROUP_CATEGORY, Field: "segment"}},
		Columns:        []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
		Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		Shape:          types.CrosstabShapeLong,
		Normalize:      types.CrosstabNormalizeRow,
		NormalizeLevel: &zero,
		Margins:        types.CrosstabMargins{Rows: true, Grand: true},
	}}
	bl, fl := runBufferedCrosstab(t, schema, long, recs).Data, runFusedCrosstab(t, schema, long, recs).Data
	assertLongRowsEqual(t, bl, fl) // values; order checked below
	coords := func(rows []map[string]any) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = fmt.Sprint(r["_margin"], "|", r["value"], "|", r["segment"])
		}
		return out
	}
	if b, f := coords(bl), coords(fl); !reflect.DeepEqual(b, f) {
		t.Errorf("long row order differs:\nbuffered %v\nfused    %v", b, f)
	}
	var partial []string
	for _, r := range fl {
		if r["_margin"] == "row_at_0" {
			partial = append(partial, r["value"].(string))
		}
	}
	if !reflect.DeepEqual(partial, want) {
		t.Errorf("fused partial margin rows = %v, want %v", partial, want)
	}
}

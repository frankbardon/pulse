package processing

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/numrange"
	"github.com/frankbardon/pulse/types"
)

// grouper_numeric_ranges_test.go pins GROUP_NUMERIC_RANGES (model-workbench
// E2-S4): half-open matching, the shared PULSE_RANGE_* refusals, typed
// range tables (a kind mismatch refused in both directions), supplied-order
// emission through keyRank, and GROUP_ROUNDED's numeric key order.

func numericRangesGroup(params string) *types.Group {
	return &types.Group{Type: types.GROUP_NUMERIC_RANGES, Field: "score", Params: json.RawMessage(params)}
}

func fp(v float64) *float64 { return &v }

// splitParams is the integer train/holdout idiom: [0, 91) and [91, 104).
const splitParams = `{"ranges":[{"label":"train","start":0,"end":91},{"label":"holdout","start":91,"end":104}]}`

func TestGrouper_NumericRanges_HalfOpenKeys(t *testing.T) {
	t.Parallel()

	g, err := grouperRegistry[types.GROUP_NUMERIC_RANGES](numericRangesGroup(splitParams), numericSchema())
	if err != nil {
		t.Fatal(err)
	}
	sg := g.(StreamableGrouper)
	schema := numericSchema()
	for _, tc := range []struct {
		v    float64
		want string
	}{
		{0, "train"}, {90, "train"}, {90.999, "train"},
		{91, "holdout"}, {103.5, "holdout"},
		{104, "unmatched"}, {-1, "unmatched"}, {-0.0001, "unmatched"},
	} {
		rec := makeRecords(schema, "score", []float64{tc.v})[0]
		got, err := sg.KeyFor(rec)
		if err != nil || got != tc.want {
			t.Errorf("KeyFor(%v) = %q, %v; want %q", tc.v, got, err, tc.want)
		}
	}
	null := NewRecordWithNulls(schema, map[string]float64{"score": 0}, map[string]bool{"score": true})
	if _, err := sg.KeyFor(null); err != ErrGrouperKeyNull {
		t.Errorf("null KeyFor err = %v, want ErrGrouperKeyNull", err)
	}

	// Open bounds reach ±inf.
	open, err := grouperRegistry[types.GROUP_NUMERIC_RANGES](numericRangesGroup(`{"ranges":[{"label":"lo","end":0},{"label":"hi","start":0}],"unmatched_label":"none"}`), schema)
	if err != nil {
		t.Fatal(err)
	}
	for v, want := range map[float64]string{-1e300: "lo", -0.5: "lo", 0: "hi", 1e300: "hi"} {
		got, _ := open.(StreamableGrouper).KeyFor(makeRecords(schema, "score", []float64{v})[0])
		if got != want {
			t.Errorf("open KeyFor(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestGrouper_NumericRanges_Refusals(t *testing.T) {
	t.Parallel()

	factory := grouperRegistry[types.GROUP_NUMERIC_RANGES]
	for _, tc := range []struct {
		name   string
		params string
		code   errors.Code
	}{
		{"overlap", `{"ranges":[{"label":"a","start":0,"end":92},{"label":"b","start":91,"end":104}]}`, errors.PULSE_RANGE_OVERLAP},
		{"two_open_lower", `{"ranges":[{"label":"a","end":5},{"label":"b","end":10}]}`, errors.PULSE_RANGE_OVERLAP},
		{"duplicate", `{"ranges":[{"label":"a","start":0,"end":5},{"label":"a","start":5,"end":10}]}`, errors.PULSE_RANGE_DUPLICATE_LABEL},
		{"inverted", `{"ranges":[{"label":"a","start":5,"end":1}]}`, errors.PULSE_RANGE_INVALID},
		{"empty_interval", `{"ranges":[{"label":"a","start":5,"end":5}]}`, errors.PULSE_RANGE_INVALID},
		{"string_bound", `{"ranges":[{"label":"a","start":"2024-01-01"}]}`, errors.PROCESSING_CONFIG},
		{"both_sources", `{"ranges":[{"label":"a","start":0}],"table":"t"}`, errors.PULSE_RANGE_SOURCE_AMBIGUOUS},
		{"no_source", `{}`, errors.PULSE_RANGE_SOURCE_AMBIGUOUS},
		{"unmatched_collision", `{"ranges":[{"label":"unmatched","start":0}]}`, errors.PROCESSING_CONFIG},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := factory(numericRangesGroup(tc.params), numericSchema())
			if !errors.HasCode(err, tc.code) {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
	// Adjacent half-open ranges are NOT an overlap.
	if _, err := factory(numericRangesGroup(splitParams), numericSchema()); err != nil {
		t.Fatalf("adjacent [0,91) [91,104): %v", err)
	}
	// Numeric fields only.
	for _, schemaField := range []struct {
		name  string
		group *types.Group
	}{
		{"date", &types.Group{Type: types.GROUP_NUMERIC_RANGES, Field: "enrolled", Params: json.RawMessage(splitParams)}},
		{"categorical", &types.Group{Type: types.GROUP_NUMERIC_RANGES, Field: "brand", Params: json.RawMessage(splitParams)}},
	} {
		schema := dateSchema()
		if schemaField.name == "categorical" {
			schema = categoricalSchema()
		}
		if _, err := factory(schemaField.group, schema); !errors.HasCode(err, errors.PULSE_FIELD_TYPE_UNSUPPORTED) {
			t.Errorf("%s field: err = %v, want PULSE_FIELD_TYPE_UNSUPPORTED", schemaField.name, err)
		}
	}
}

func typedTableRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{RangeTables: map[string]RangeTable{
		"split": {NumericRanges: []numrange.Spec{
			{Label: "train", Start: fp(0), End: fp(91)},
			{Label: "holdout", Start: fp(91), End: fp(104)},
		}},
		"fiscal": fiscalTableRegistry().RangeTables["fiscal"],
	}}
}

func TestGrouper_NumericRanges_TableKinds(t *testing.T) {
	t.Parallel()

	exts := typedTableRegistry()
	factory := grouperRegistry[types.GROUP_NUMERIC_RANGES]

	g, err := factory(numericRangesGroup(`{"table":"split"}`), numericSchema())
	if err != nil {
		t.Fatal(err)
	}
	ApplyGrouperExtensions(g, exts)
	inline, _ := factory(numericRangesGroup(splitParams), numericSchema())
	recs := makeRecords(numericSchema(), "score", []float64{3, 90, 91, 100, 104, -2})
	tg, err := g.Group(recs, "score")
	if err != nil {
		t.Fatal(err)
	}
	ig, _ := inline.Group(recs, "score")
	if len(tg) != len(ig) {
		t.Fatalf("table buckets %v != inline %v", tg, ig)
	}
	for k := range ig {
		if len(tg[k]) != len(ig[k]) {
			t.Errorf("bucket %q: table %d inline %d", k, len(tg[k]), len(ig[k]))
		}
	}
	// The resolved table's rank is supplied order, unmatched last.
	ko := g.(keyOrderer)
	for key, want := range map[string]float64{"train": 0, "holdout": 1, "unmatched": 2} {
		if r, ok := ko.keyRank(key); !ok || r != want {
			t.Errorf("table keyRank(%q) = %v, %v; want %v", key, r, ok, want)
		}
	}

	// Numeric grouper on a date table.
	g, _ = factory(numericRangesGroup(`{"table":"fiscal"}`), numericSchema())
	ApplyGrouperExtensions(g, exts)
	if _, err := g.(StreamableGrouper).KeyFor(recs[0]); !errors.HasCode(err, errors.PULSE_RANGE_TABLE_KIND_MISMATCH) {
		t.Errorf("numeric grouper on date table: err = %v", err)
	}
	// Unknown table.
	g, _ = factory(numericRangesGroup(`{"table":"nope"}`), numericSchema())
	ApplyGrouperExtensions(g, exts)
	if _, err := g.(StreamableGrouper).KeyFor(recs[0]); !errors.HasCode(err, errors.PULSE_RANGE_TABLE_UNKNOWN) {
		t.Errorf("unknown table: err = %v", err)
	}
	// Date grouper and date filter on a numeric table.
	dg, err := grouperRegistry[types.GROUP_DATE_RANGES](&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: tableParams(t, "split")}, dateSchema())
	if err != nil {
		t.Fatal(err)
	}
	ApplyGrouperExtensions(dg, exts)
	drec := makeRecords(dateSchema(), "enrolled", []float64{epochDays(2024, 2, 1)})[0]
	if _, err := dg.(StreamableGrouper).KeyFor(drec); !errors.HasCode(err, errors.PULSE_RANGE_TABLE_KIND_MISMATCH) {
		t.Errorf("date grouper on numeric table: err = %v", err)
	}
	if _, err := resolveDateRangeSpecs("FILTER_DATE_RANGES", nil, "split", exts); !errors.HasCode(err, errors.PULSE_RANGE_TABLE_KIND_MISMATCH) {
		t.Errorf("date filter on numeric table: err = %v", err)
	}
}

func TestNumericRangesGrouper_KeyRankAndOrder(t *testing.T) {
	t.Parallel()

	g, err := grouperRegistry[types.GROUP_NUMERIC_RANGES](numericRangesGroup(
		`{"ranges":[{"label":"z_first","start":10,"end":20},{"label":"a_second","end":10},{"label":"m_third","start":20}],"unmatched_label":"none"}`), numericSchema())
	if err != nil {
		t.Fatal(err)
	}
	ko := g.(keyOrderer)
	if _, ok := ko.keyRank("stray"); ok {
		t.Error("an unconfigured key is ranked")
	}
	want := []string{"z_first", "a_second", "m_third", "none", "stray"}
	shuffled := append([]string(nil), want...)
	rand.New(rand.NewSource(3)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	if got := orderKeysByInclude(includeFilterOf(g), shuffled); !reflect.DeepEqual(got, want) {
		t.Errorf("ordered = %v, want %v", got, want)
	}
	comps, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatal(err)
	}
	if comps["n_ranges"] != 3 || comps["unmatched_label"] != "none" {
		t.Errorf("components = %v", comps)
	}
}

func TestRoundedGrouper_KeyRank(t *testing.T) {
	t.Parallel()

	g := &roundedGrouper{interval: 0.5}
	for _, tc := range []struct {
		key  string
		rank float64
		ok   bool
	}{
		{"-5", -5, true}, {"9.5", 9.5, true}, {"10", 10, true}, {"0", 0, true},
		{"NaN", 0, false}, {"abc", 0, false}, {"", 0, false},
	} {
		r, ok := g.keyRank(tc.key)
		if ok != tc.ok || (ok && r != tc.rank) {
			t.Errorf("keyRank(%q) = %v, %v; want %v, %v", tc.key, r, ok, tc.rank, tc.ok)
		}
	}
	want := []string{"-10", "-5", "-0.5", "0", "9.5", "10", "100", "zz"}
	shuffled := append([]string(nil), want...)
	rand.New(rand.NewSource(11)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	if got := orderKeysByInclude(includeFilterOf(g), shuffled); !reflect.DeepEqual(got, want) {
		t.Errorf("rounded order = %v, want %v", got, want)
	}
}

package pulse

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// numeric_ranges_test.go pins GROUP_NUMERIC_RANGES and typed range tables
// (model-workbench E2-S4, #395) end to end: supplied-order emission on
// every arm (buffered and streamed, one file and a shard archive, both
// crosstab arms) for an inline and a table source, GROUP_ROUNDED's
// numeric key order on the same arms, predict ≡ runtime for every new
// refusal, and the table typing (loader, validation, RangeTables kind).

func fp64(v float64) *float64 { return &v }

// nrParams lists the ranges out of value order, so supplied order is
// neither value order nor alphabetical.
const nrParams = `{"ranges":[{"label":"z_hi","start":10},{"label":"a_lo","end":-5},{"label":"m_mid","start":-5,"end":10}],"unmatched_label":"none"}`

var nrWant = []string{"z_hi", "a_lo", "m_mid"}

// roundedWant is GROUP_ROUNDED interval 5 over x = -15…24 in numeric order;
// alphabetical order would put "-10" before "-15" and "10" before "5".
var roundedWant = []string{"-15", "-10", "-5", "0", "5", "10", "15", "20"}

func nrTables() map[string]RangeTable {
	return map[string]RangeTable{
		"bands": {NumericRanges: []NumericRangeSpec{
			{Label: "z_hi", Start: fp64(10)},
			{Label: "a_lo", End: fp64(-5)},
			{Label: "m_mid", Start: fp64(-5), End: fp64(10)},
		}},
		"quarters": {Ranges: []DateRangeSpec{{Label: "q1", Start: fiscalStrptrLocal("2024-01-01"), End: fiscalStrptrLocal("2024-03-31")}}},
	}
}

func fiscalStrptrLocal(s string) *string { return &s }

// nrEngine is bucketOrderEngine with the typed tables registered; it also
// returns the fs so predict can read the flat cohort's bytes.
func nrEngine(t *testing.T, opts Options) (*Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	opts.FS = fsys
	opts.Extensions.RangeTables = nrTables()
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	buildCohort(t, p, "flat.pulse", bucketOrderSchema(), CohortBuilderOptions{}, bucketOrderRows())
	buildCohort(t, p, "arch.pulse", bucketOrderSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 13}}, bucketOrderRows())
	return p, fsys
}

func nrGroup(params string) *types.Group {
	return &types.Group{Type: types.GROUP_NUMERIC_RANGES, Field: "x", Params: []byte(params)}
}

func roundedGroup() *types.Group {
	return &types.Group{Type: types.GROUP_ROUNDED, Field: "x", Interval: 5}
}

func TestNumericRanges_SuppliedOrderEveryArm(t *testing.T) {
	p, _ := nrEngine(t, Options{})
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		group func() *types.Group
		want  []string
	}{
		{"inline", func() *types.Group { return nrGroup(nrParams) }, nrWant},
		{"table", func() *types.Group { return nrGroup(`{"table":"bands","unmatched_label":"none"}`) }, nrWant},
		{"rounded", roundedGroup, roundedWant},
	} {
		for _, cohort := range []string{"flat.pulse", "arch.pulse"} {
			t.Run(tc.name+"/"+cohort, func(t *testing.T) {
				resp, err := p.Process(ctx, bucketOrderGroupReq(cohort, tc.group()))
				if err != nil {
					t.Fatal(err)
				}
				if got := columnOf(resp.Data, "x"); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("buffered rows = %v, want %v", got, tc.want)
				}
				if got := columnOf(streamRows(t, p, bucketOrderGroupReq(cohort, tc.group())), "x"); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("streamed rows = %v, want %v", got, tc.want)
				}
			})
		}
	}

	// Half-open counts: x = -15…24 → a_lo [-inf,-5) holds -15…-6 (10),
	// m_mid [-5,10) holds -5…9 (15), z_hi [10,inf) holds 10…24 (15).
	resp, err := p.Process(ctx, &Request{
		Cohort:       &types.Cohort{Filename: "flat.pulse"},
		Groups:       []*types.Group{nrGroup(nrParams)},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range resp.Data {
		got[fmt.Sprint(r["x"])] = fmt.Sprint(r["n"])
	}
	if want := map[string]string{"a_lo": "10", "m_mid": "15", "z_hi": "15"}; !reflect.DeepEqual(got, want) {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

func TestNumericRanges_CrosstabAxesBothArms(t *testing.T) {
	region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	for _, disable := range []bool{false, true} {
		p, _ := nrEngine(t, Options{DisableCrosstabFusion: disable})
		for _, cohort := range []string{"flat.pulse", "arch.pulse"} {
			for _, tc := range []struct {
				name  string
				group func() *types.Group
				want  []string
			}{
				{"inline", func() *types.Group { return nrGroup(nrParams) }, nrWant},
				{"table", func() *types.Group { return nrGroup(`{"table":"bands","unmatched_label":"none"}`) }, nrWant},
				{"rounded", roundedGroup, roundedWant},
			} {
				name := fmt.Sprintf("fusion_disabled=%v/%s/%s", disable, cohort, tc.name)
				req := &Request{
					Cohort: &types.Cohort{Filename: cohort},
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{tc.group()},
						Columns: []*types.Group{region},
						Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "s", Label: "n"},
					},
				}
				if !disable && cohort == "flat.pulse" {
					pr, err := p.Predict(context.Background(), req)
					if err != nil {
						t.Fatal(err)
					}
					if pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
						t.Fatalf("%s: crosstab not fusable (%v) — the fused arm is untested", name, pr.CrosstabFusionReasons)
					}
				}
				resp, err := p.Process(context.Background(), req)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if got := axisStrings(resp.Crosstab.Matrix.RowKeys, 0); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("%s rows = %v, want %v", name, got, tc.want)
				}
			}
		}
	}
}

// TestNumericRanges_RefusalsPredictMatchesRuntime: every refusal the
// numeric grouper (and a date-range operator on a numeric table) raises,
// Process and predict raise under the same code, predict exactly once.
func TestNumericRanges_RefusalsPredictMatchesRuntime(t *testing.T) {
	p, fsys := nrEngine(t, Options{})
	writeParityDatesCohort(t, fsys)
	for _, tc := range []struct {
		name string
		req  *Request
		code errors.Code
	}{
		{"overlap", bucketOrderGroupReq("flat.pulse", nrGroup(`{"ranges":[{"label":"a","end":5},{"label":"b","start":4}]}`)), errors.PULSE_RANGE_OVERLAP},
		{"duplicate", bucketOrderGroupReq("flat.pulse", nrGroup(`{"ranges":[{"label":"a","end":5},{"label":"a","start":5}]}`)), errors.PULSE_RANGE_DUPLICATE_LABEL},
		{"invalid", bucketOrderGroupReq("flat.pulse", nrGroup(`{"ranges":[{"label":"a","start":5,"end":5}]}`)), errors.PULSE_RANGE_INVALID},
		{"ambiguous", bucketOrderGroupReq("flat.pulse", nrGroup(`{"ranges":[{"label":"a","end":5}],"table":"bands"}`)), errors.PULSE_RANGE_SOURCE_AMBIGUOUS},
		{"malformed", bucketOrderGroupReq("flat.pulse", nrGroup(`{"ranges":[{"label":"a","end":"5"}]}`)), errors.PROCESSING_CONFIG},
		{"unknown table", bucketOrderGroupReq("flat.pulse", nrGroup(`{"table":"nope"}`)), errors.PULSE_RANGE_TABLE_UNKNOWN},
		{"date table", bucketOrderGroupReq("flat.pulse", nrGroup(`{"table":"quarters"}`)), errors.PULSE_RANGE_TABLE_KIND_MISMATCH},
		{"unmatched collides with table label", bucketOrderGroupReq("flat.pulse", nrGroup(`{"table":"bands","unmatched_label":"a_lo"}`)), errors.PROCESSING_CONFIG},
		{"categorical field", bucketOrderGroupReq("flat.pulse", &types.Group{Type: types.GROUP_NUMERIC_RANGES, Field: "region", Params: []byte(nrParams)}), errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"crosstab axis", &Request{Cohort: &types.Cohort{Filename: "flat.pulse"}, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{nrGroup(`{"table":"quarters"}`)},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "s", Label: "n"},
		}}, errors.PULSE_RANGE_TABLE_KIND_MISMATCH},
		// The other direction: a date-range operator naming a numeric table.
		// (The kind is judged before the field type, so the numeric field
		// here is not what refuses.)
		{"date grouper on numeric table", &Request{
			Cohort:       &types.Cohort{Filename: parityDatesCohort},
			Groups:       []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "day", Params: []byte(`{"table":"bands"}`)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age", Label: "n"}},
		}, errors.PULSE_RANGE_TABLE_KIND_MISMATCH},
		{"date filter on numeric table", &Request{
			Cohort:       &types.Cohort{Filename: parityDatesCohort},
			Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "day", Params: []byte(`{"table":"bands"}`)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age", Label: "n"}},
		}, errors.PULSE_RANGE_TABLE_KIND_MISMATCH},
		{"date grouper unknown table", &Request{
			Cohort:       &types.Cohort{Filename: parityDatesCohort},
			Groups:       []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "day", Params: []byte(`{"table":"nope"}`)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age", Label: "n"}},
		}, errors.PULSE_RANGE_TABLE_UNKNOWN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, rerr := p.Process(context.Background(), tc.req)
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != tc.code {
				t.Fatalf("runtime: %v, want %s", rerr, tc.code)
			}
			data, err := afero.ReadFile(fsys, tc.req.Cohort.Filename)
			if err != nil {
				t.Fatal(err)
			}
			env, err := p.PredictBytes(context.Background(), data, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(tc.code) || env.Errors[0].Message != ce.Message {
				t.Errorf("predict errors %v, want one %s %q", env.Errors, tc.code, ce.Message)
			}
		})
	}
}

func TestRangeTables_TypedByBounds(t *testing.T) {
	// Programmatic: a numeric table registers and reports its kind.
	p, _ := nrEngine(t, Options{})
	infos := p.RangeTables()
	kinds := map[string]string{}
	for _, in := range infos {
		kinds[in.Name] = in.Kind
		if in.Kind == "numeric" && (len(in.NumericRanges) != 3 || in.Ranges != nil || in.RangeCount != 3) {
			t.Errorf("numeric info = %+v", in)
		}
		if in.Kind == "date" && (len(in.Ranges) != 1 || in.NumericRanges != nil || in.RangeCount != 1) {
			t.Errorf("date info = %+v", in)
		}
	}
	if want := map[string]string{"bands": "numeric", "quarters": "date"}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
	m := p.Manifest(context.Background())
	for _, rt := range m.Extensions.RangeTables {
		if rt.Kind != kinds[rt.Name] {
			t.Errorf("manifest %s kind %q, want %q", rt.Name, rt.Kind, kinds[rt.Name])
		}
	}

	// Validation: the numeric compile pass runs at New; both kinds at once
	// is refused.
	for _, tc := range []struct {
		name  string
		table RangeTable
		code  errors.Code
	}{
		{"numeric overlap", RangeTable{NumericRanges: []NumericRangeSpec{{Label: "a", End: fp64(5)}, {Label: "b", Start: fp64(4)}}}, errors.PULSE_RANGE_OVERLAP},
		{"numeric inverted", RangeTable{NumericRanges: []NumericRangeSpec{{Label: "a", Start: fp64(5), End: fp64(1)}}}, errors.PULSE_RANGE_INVALID},
		{"both kinds", RangeTable{Ranges: []DateRangeSpec{{Label: "d"}}, NumericRanges: []NumericRangeSpec{{Label: "n"}}}, errors.PULSE_EXTENSION_PARAM_INVALID},
	} {
		_, err := New(Options{FS: afero.NewMemMapFs(), Extensions: Extensions{RangeTables: map[string]RangeTable{"t": tc.table}}})
		if !errors.HasCode(err, tc.code) {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.code)
		}
	}
	// Adjacent half-open ranges are valid.
	if _, err := New(Options{FS: afero.NewMemMapFs(), Extensions: Extensions{RangeTables: map[string]RangeTable{"t": {
		NumericRanges: []NumericRangeSpec{{Label: "train", Start: fp64(0), End: fp64(91)}, {Label: "holdout", Start: fp64(91), End: fp64(104)}},
	}}}}); err != nil {
		t.Errorf("adjacent numeric table: %v", err)
	}

	// The directory loader types each file by its bounds.
	dir := t.TempDir()
	files := map[string]string{
		"split.json":    `[{"label":"train","start":0,"end":91},{"label":"holdout","start":91,"end":104}]`,
		"bands.json":    `{"description":"score bands","ranges":[{"label":"lo","end":50},{"label":"hi","start":50}]}`,
		"quarters.json": `[{"label":"Q1","start":"2024-01-01","end":"2024-03-31"}]`,
		"open.json":     `[{"label":"all","start":null}]`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{RangeTablesDir: dir}
	if err := loadRangeTablesFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for name, tbl := range opts.Extensions.RangeTables {
		got[name] = tbl.Kind()
	}
	if want := map[string]string{"split": "numeric", "bands": "numeric", "quarters": "date", "open": "date"}; !reflect.DeepEqual(got, want) {
		t.Errorf("loaded kinds = %v, want %v", got, want)
	}
	if b := opts.Extensions.RangeTables["bands"]; b.Description != "score bands" || *b.NumericRanges[1].Start != 50 {
		t.Errorf("bands = %+v", b)
	}

	// A file mixing date and numeric bounds hard-fails naming the path.
	mixed := t.TempDir()
	if err := os.WriteFile(filepath.Join(mixed, "mixed.json"), []byte(`[{"label":"a","start":0},{"label":"b","start":"2024-01-01"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts = Options{RangeTablesDir: mixed}
	if err := loadRangeTablesFromDir(&opts); err == nil {
		t.Error("a mixed-kind table file loaded")
	}
}

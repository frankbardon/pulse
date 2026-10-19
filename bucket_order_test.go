package pulse

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// bucket_order_test.go pins bucket-ordered emission (model-workbench
// E2-S3, #346): GROUP_QUANTILE buckets come out D1…D10 and GROUP_RANGE
// buckets by numeric low edge — negatives included — on every arm that
// emits grouped rows (buffered, streamed, sharded, a Request.Groups
// composite) and on both crosstab arms (buffered and fused). Keys never
// change; an explicit crosstab Include and an explicit Request.Sort still
// win.

// bucketOrderRangeKeys is GROUP_RANGE interval 5 over x = -15…24, in
// bucket order. Alphabetical order would put "-10--5" before "-15--10"
// and "10-15" before "5-10".
var bucketOrderRangeKeys = []string{"-15--10", "-10--5", "-5-0", "0-5", "5-10", "10-15", "15-20", "20-25"}

// bucketOrderDeciles is GROUP_QUANTILE interval 10 over 40 distinct s.
var bucketOrderDeciles = []string{"D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10"}

func bucketOrderSchema() encoding.Schema {
	fields := []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "s", Type: encoding.FieldTypeF64},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Bucket-order column " + fields[i].Name + " (E2-S3 fixture)."
		off += fields[i].Type.ByteSize()
	}
	return encoding.Schema{Fields: fields}
}

// bucketOrderRows: x = i-15 walks -15…24 in order; s is a permutation of
// 1…40 so insertion order never equals decile order; region alternates.
func bucketOrderRows() []CohortRow {
	rows := make([]CohortRow, 40)
	for i := range rows {
		rows[i] = CohortRow{float64(i) - 15, float64((i*7)%40 + 1), []string{"north", "south"}[i%2]}
	}
	return rows
}

// bucketOrderEngine builds the fixture twice — one file and a 4-shard
// archive — and returns an engine over them.
func bucketOrderEngine(t *testing.T, opts Options) *Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	opts.FS = fsys
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	buildCohort(t, p, "flat.pulse", bucketOrderSchema(), CohortBuilderOptions{}, bucketOrderRows())
	buildCohort(t, p, "arch.pulse", bucketOrderSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 13}}, bucketOrderRows())
	return p
}

func bucketOrderGroupReq(cohort string, groups ...*types.Group) *Request {
	return &Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Groups:       groups,
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "sum"}},
	}
}

func rangeGroup() *types.Group {
	return &types.Group{Type: types.GROUP_RANGE, Field: "x", Interval: 5}
}

func decileGroup() *types.Group {
	return &types.Group{Type: types.GROUP_QUANTILE, Field: "s", Interval: 10}
}

func columnOf(rows []map[string]any, col string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprint(r[col])
	}
	return out
}

// streamRows runs req through ProcessStreamResult and returns its rows.
func streamRows(t *testing.T, p *Pulse, req *Request) []map[string]any {
	t.Helper()
	res, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	var rows []map[string]any
	for chunk := range res.Chunks {
		rows = append(rows, chunk.Data)
	}
	if term := <-res.Done; term.Status != StreamCompleted {
		t.Fatalf("stream status = %v (err=%v)", term.Status, term.Error)
	}
	return rows
}

// TestBucketOrder_GroupedRowsEveryArm: a single quantile or range grouper
// emits its Data rows in bucket order — buffered over one file, buffered
// over a shard archive, and streamed over both.
func TestBucketOrder_GroupedRowsEveryArm(t *testing.T) {
	p := bucketOrderEngine(t, Options{})
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		group func() *types.Group
		col   string
		want  []string
	}{
		{"range", rangeGroup, "x", bucketOrderRangeKeys},
		{"quantile", decileGroup, "s", bucketOrderDeciles},
	} {
		for _, cohort := range []string{"flat.pulse", "arch.pulse"} {
			t.Run(tc.name+"/"+cohort, func(t *testing.T) {
				resp, err := p.Process(ctx, bucketOrderGroupReq(cohort, tc.group()))
				if err != nil {
					t.Fatal(err)
				}
				if got := columnOf(resp.Data, tc.col); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("buffered rows = %v\nwant %v", got, tc.want)
				}
				if got := columnOf(streamRows(t, p, bucketOrderGroupReq(cohort, tc.group())), tc.col); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("streamed rows = %v\nwant %v", got, tc.want)
				}
			})
		}
	}
}

// TestBucketOrder_MultiGrouperComposite: a Request.Groups composite
// orders each position by its own grouper — the range position by low
// edge, the category position alphabetically, the decile position by id.
func TestBucketOrder_MultiGrouperComposite(t *testing.T) {
	p := bucketOrderEngine(t, Options{})
	region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}

	var want []string
	for _, k := range bucketOrderRangeKeys {
		want = append(want, k+"|north", k+"|south")
	}
	for _, cohort := range []string{"flat.pulse", "arch.pulse"} {
		req := bucketOrderGroupReq(cohort, rangeGroup(), region)
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(resp.Data))
		for i, r := range resp.Data {
			got[i] = fmt.Sprint(r["x"]) + "|" + fmt.Sprint(r["region"])
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s range × region = %v\nwant %v", cohort, got, want)
		}
		streamed := streamRows(t, p, bucketOrderGroupReq(cohort, rangeGroup(), region))
		for i, r := range streamed {
			streamed[i] = map[string]any{"k": fmt.Sprint(r["x"]) + "|" + fmt.Sprint(r["region"])}
		}
		if s := columnOf(streamed, "k"); !reflect.DeepEqual(s, want) {
			t.Errorf("%s streamed range × region = %v\nwant %v", cohort, s, want)
		}
	}

	// region × decile: within each region the deciles ascend by id.
	resp, err := p.Process(context.Background(), bucketOrderGroupReq("flat.pulse", region, decileGroup()))
	if err != nil {
		t.Fatal(err)
	}
	type pair struct {
		region string
		id     int
	}
	got := make([]pair, len(resp.Data))
	for i, r := range resp.Data {
		id, err := strconv.Atoi(strings.TrimPrefix(fmt.Sprint(r["s"]), "D"))
		if err != nil {
			t.Fatalf("decile key %v: %v", r["s"], err)
		}
		got[i] = pair{fmt.Sprint(r["region"]), id}
	}
	if len(got) < 10 || !sort.SliceIsSorted(got, func(i, j int) bool {
		if got[i].region != got[j].region {
			return got[i].region < got[j].region
		}
		return got[i].id < got[j].id
	}) {
		t.Errorf("region × decile rows not in (region, decile id) order: %v", got)
	}
}

// TestBucketOrder_ExplicitSortWins: Request.Sort still reorders rows.
func TestBucketOrder_ExplicitSortWins(t *testing.T) {
	p := bucketOrderEngine(t, Options{})
	req := bucketOrderGroupReq("flat.pulse", decileGroup())
	req.Sort = []types.OrderKey{{Field: "sum", Desc: true}}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, len(bucketOrderDeciles))
	for i, k := range bucketOrderDeciles {
		want[len(want)-1-i] = k
	}
	if got := columnOf(resp.Data, "s"); !reflect.DeepEqual(got, want) {
		t.Errorf("sum-desc rows = %v, want %v", got, want)
	}
	// Sorting on the key column itself follows the grouper's key order
	// (D1, D2, …, D10), never the key strings (D1, D10, …) — and desc
	// reverses it.
	for _, desc := range []bool{false, true} {
		req = bucketOrderGroupReq("flat.pulse", decileGroup())
		req.Sort = []types.OrderKey{{Field: "s", Desc: desc}}
		resp, err = p.Process(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		want := append([]string(nil), bucketOrderDeciles...)
		if desc {
			slices.Reverse(want)
		}
		if got := columnOf(resp.Data, "s"); !reflect.DeepEqual(got, want) {
			t.Errorf("key-column sort (desc=%v) = %v, want the bucket order %v", desc, got, want)
		}
	}
}

func axisStrings(keys []types.AxisKey, pos int) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fmt.Sprint(k[pos])
	}
	return out
}

// TestBucketOrder_CrosstabAxesBothArms: a crosstab axis over a range or
// quantile grouper follows bucket order on the buffered arm and on the
// fused arm (range is fusable; predict says so), over a file and an
// archive; an explicit axis Include still leads.
func TestBucketOrder_CrosstabAxesBothArms(t *testing.T) {
	region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	for _, disable := range []bool{false, true} {
		p := bucketOrderEngine(t, Options{DisableCrosstabFusion: disable})
		for _, cohort := range []string{"flat.pulse", "arch.pulse"} {
			name := fmt.Sprintf("fusion_disabled=%v/%s", disable, cohort)
			req := &Request{
				Cohort: &types.Cohort{Filename: cohort},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{rangeGroup()},
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
					t.Fatalf("range × region crosstab not fusable (%v) — the fused arm is untested", pr.CrosstabFusionReasons)
				}
			}
			resp, err := p.Process(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if got := axisStrings(resp.Crosstab.Matrix.RowKeys, 0); !reflect.DeepEqual(got, bucketOrderRangeKeys) {
				t.Errorf("%s range rows = %v\nwant %v", name, got, bucketOrderRangeKeys)
			}

			// Long shape with a two-position row axis and a level-0
			// normalisation: the partial (range-only) margin rows also
			// follow bucket order.
			zero := 0
			long := &Request{
				Cohort: &types.Cohort{Filename: cohort},
				Crosstab: &types.CrosstabSpec{
					Rows:           []*types.Group{rangeGroup(), region},
					Columns:        []*types.Group{region},
					Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "s", Label: "n"},
					Shape:          types.CrosstabShapeLong,
					Normalize:      types.CrosstabNormalizeRow,
					NormalizeLevel: &zero,
					Margins:        types.CrosstabMargins{Rows: true},
				},
			}
			lresp, err := p.Process(context.Background(), long)
			if err != nil {
				t.Fatal(err)
			}
			var seen []string
			for _, r := range lresp.Data {
				k := fmt.Sprint(r["x"])
				if len(seen) == 0 || seen[len(seen)-1] != k {
					seen = append(seen, k)
				}
			}
			if len(seen) < len(bucketOrderRangeKeys) || !reflect.DeepEqual(seen[:len(bucketOrderRangeKeys)], bucketOrderRangeKeys) {
				t.Errorf("%s long cell rows by range = %v\nwant leading %v", name, seen, bucketOrderRangeKeys)
			}

			// Decile columns (buffered only: quantile is not streamable).
			dq := &Request{
				Cohort: &types.Cohort{Filename: cohort},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{region},
					Columns: []*types.Group{decileGroup()},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "s", Label: "n"},
				},
			}
			dresp, err := p.Process(context.Background(), dq)
			if err != nil {
				t.Fatal(err)
			}
			if got := axisStrings(dresp.Crosstab.Matrix.ColumnKeys, 0); !reflect.DeepEqual(got, bucketOrderDeciles) {
				t.Errorf("%s decile columns = %v\nwant %v", name, got, bucketOrderDeciles)
			}

			// An explicit axis Include still leads the order.
			inc := rangeGroup()
			inc.Include = []string{"10-15", "-15--10"}
			ireq := &Request{
				Cohort: &types.Cohort{Filename: cohort},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{inc},
					Columns: []*types.Group{region},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "s", Label: "n"},
				},
			}
			iresp, err := p.Process(context.Background(), ireq)
			if err != nil {
				t.Fatal(err)
			}
			if got := axisStrings(iresp.Crosstab.Matrix.RowKeys, 0); len(got) < 2 || got[0] != "10-15" || got[1] != "-15--10" {
				t.Errorf("%s include-ordered range rows = %v, want leading [10-15 -15--10]", name, got)
			}
		}
	}
}

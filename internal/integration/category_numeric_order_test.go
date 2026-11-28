package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/types"
)

// category_numeric_order_test.go pins #788: GROUP_CATEGORY over a field
// whose keys are formatted numbers — an integer column, an
// ATTR_DATE_PART hour or weekday — emits and sorts its keys by VALUE
// ("0", "1", "2", …, "10"), as GROUP_ROUNDED and GROUP_DATE keys
// already do; over a dictionary categorical the keys stay alphabetical,
// byte-identically. The verdict is the schema's (the field is no
// dictionary categorical), never the data's.

const cnoRows = 96

// cnoCohort writes cno.pulse (cnoRows rows) and a three-shard archive
// of the same rows, cno_archive.pulse, on one MemMapFs. Row i: ts is
// 2024-01-01T00:00Z + i·25h (hour i%24, ~100 days so every weekday),
// n = i%21 − 5 (−5 … 15), region a dictionary categorical whose
// dictionary order ("west", "east", "north") is not alphabetical.
func cnoCohort(t *testing.T) afero.Fs {
	t.Helper()
	mem := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "ts", Type: encoding.FieldTypeDateTime, Description: "Moment the order was placed"},
		{Name: "n", Type: encoding.FieldTypeF64, Description: "Net change in units on the order"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region the order is booked in"},
	}}
	regions := []string{"west", "east", "north"}
	write := func(path string, from, to int) {
		b, err := p.NewCohortBuilder(ctx, path, schema, pulse.CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i := from; i < to; i++ {
			if err := b.Append(pulse.CohortRow{int64(1704067200 + i*90000), float64(i%21 - 5), regions[i%3]}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
	write("cno.pulse", 0, cnoRows)
	shards := []string{"cno_s0.pulse", "cno_s1.pulse", "cno_s2.pulse"}
	for i, s := range shards {
		write(s, i*cnoRows/3, (i+1)*cnoRows/3)
	}
	if _, err := p.CreateShardArchive(ctx, "cno_archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	return mem
}

// cnoRequest groups by key (an ATTR_DATE_PART label or a field) under
// a count, with both date parts derived.
func cnoRequest(path, key string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_DATE_PART, Field: "ts", Label: "hr", Params: json.RawMessage(`{"part": "hour"}`)},
			{Type: types.ATTR_DATE_PART, Field: "ts", Label: "wd", Params: json.RawMessage(`{"part": "iso_weekday"}`)},
		},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: key}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n", Label: "rows"}},
	}
}

// cnoKeys is the key column of rows, in row order.
func cnoKeys(rows []map[string]any, col string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprint(r[col]))
	}
	return out
}

// cnoRange is the decimal keys lo … hi.
func cnoRange(lo, hi int) []string {
	var out []string
	for v := lo; v <= hi; v++ {
		out = append(out, strconv.Itoa(v))
	}
	return out
}

// cnoComponentKeys is the GROUP_CATEGORY components bucket keys of resp.
func cnoComponentKeys(t *testing.T, resp *types.Response) []string {
	t.Helper()
	if resp.Components == nil || len(resp.Components.Groupers) != 1 {
		t.Fatalf("components groupers = %+v, want one", resp.Components)
	}
	buckets, _ := resp.Components.Groupers[0].Operator["buckets"].([]map[string]any)
	out := make([]string, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, fmt.Sprint(b["key"]))
	}
	return out
}

// TestGroupCategory_NumericKeysSortByValue: hour (0 … 23), weekday
// (1 … 7) and an integer column with negatives (−5 … 15) emit by value
// in Data and in the components buckets, Request.Sort on the key column
// orders by value in both directions, and a crosstab axis over the hour
// follows the same order; the dictionary categorical stays alphabetical.
func TestGroupCategory_NumericKeysSortByValue(t *testing.T) {
	t.Parallel()
	mem := cnoCohort(t)
	p, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reverse := func(s []string) []string {
		out := make([]string, len(s))
		for i, v := range s {
			out[len(s)-1-i] = v
		}
		return out
	}
	for key, want := range map[string][]string{
		"hr":     cnoRange(0, 23),
		"wd":     cnoRange(1, 7),
		"n":      cnoRange(-5, 15),
		"region": {"east", "north", "west"},
	} {
		resp, err := p.Process(ctx, cnoRequest("cno.pulse", key))
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if got := cnoKeys(resp.Data, key); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: Data keys = %v, want %v", key, got, want)
		}
		if got := cnoComponentKeys(t, resp); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: components bucket keys = %v, want %v", key, got, want)
		}
		for _, desc := range []bool{false, true} {
			req := cnoRequest("cno.pulse", key)
			req.Sort = []types.OrderKey{{Field: key, Desc: desc}}
			sorted, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("%s sort: %v", key, err)
			}
			w := want
			if desc {
				w = reverse(want)
			}
			if got := cnoKeys(sorted.Data, key); !reflect.DeepEqual(got, w) {
				t.Errorf("%s: Request.Sort desc=%v keys = %v, want %v", key, desc, got, w)
			}
		}
	}
	xt, err := p.Process(ctx, &types.Request{
		Cohort: &types.Cohort{Filename: "cno.pulse"},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_DATE_PART, Field: "ts", Label: "hr", Params: json.RawMessage(`{"part": "hour"}`)},
		},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "hr"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
		},
	})
	if err != nil {
		t.Fatalf("crosstab: %v", err)
	}
	var rows, cols []string
	for _, k := range xt.Crosstab.Matrix.RowKeys {
		rows = append(rows, fmt.Sprint(k[0]))
	}
	for _, k := range xt.Crosstab.Matrix.ColumnKeys {
		cols = append(cols, fmt.Sprint(k[0]))
	}
	if !reflect.DeepEqual(rows, cnoRange(0, 23)) {
		t.Errorf("crosstab hour rows = %v, want 0 … 23", rows)
	}
	if !reflect.DeepEqual(cols, []string{"east", "north", "west"}) {
		t.Errorf("crosstab region columns = %v, want alphabetical", cols)
	}
}

// TestGroupCategory_NumericKeyOrderWorkerInvariant: the value order is
// the grouper's configuration, never the run's: the shard archive under
// every ShardWorkers count (and DecodeWorkers 7) emits the hour, the
// weekday and the integer keys — and every count — bit for bit as the
// single file read serially.
func TestGroupCategory_NumericKeyOrderWorkerInvariant(t *testing.T) {
	t.Parallel()
	mem := cnoCohort(t)
	cohorts := map[invariance.Layout]invCohort{
		invariance.File:    {mem, "cno.pulse"},
		invariance.Archive: {mem, "cno_archive.pulse"},
	}
	for _, key := range []string{"hr", "wd", "n"} {
		invariance.Run(t, invariance.Family[[]map[string]any]{
			Name:   "category_numeric_order/" + key,
			Groups: []invariance.Group{invariance.ArchiveGroup()},
			Run: func(t *testing.T, k invariance.Knobs) invariance.Result[[]map[string]any] {
				co := cohorts[k.Layout]
				p, arm := invEngine(t, pulse.Options{}, co, k)
				resp, err := p.Process(context.Background(), cnoRequest(co.path, key))
				if err != nil {
					t.Fatalf("%s: %v", k, err)
				}
				return invariance.Result[[]map[string]any]{Answer: resp.Data, Arm: arm()}
			},
			Check: func(t *testing.T, _ invariance.Knobs, ref []map[string]any) {
				keys := cnoKeys(ref, key)
				if len(keys) < 7 {
					t.Fatalf("%s: %d keys, want at least 7", key, len(keys))
				}
				prev := 0.0
				for i, s := range keys {
					v, err := strconv.ParseFloat(s, 64)
					if err != nil || (i > 0 && v <= prev) {
						t.Fatalf("%s: keys %v not in value order", key, keys)
					}
					prev = v
				}
			},
		})
	}
}

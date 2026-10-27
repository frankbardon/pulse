//go:build heavy

package service

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"testing"
	"time"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestTimeZone_ParallelDecodeLocalDay: the parallel buffered decode arm
// (DecodeWorkers over a >threshold single-file cohort, mmap'd off a
// real on-disk file — MemMapFs cannot reach it, so the fixture lives in
// t.TempDir() behind an OsFs-backed fs.Config) buckets GROUP_DATE day,
// labels GROUP_DATE_RANGES and keeps FILTER_DATE_RANGES rows by the
// Europe/Berlin LOCAL day, equal to a time.In reference at every worker
// count, and every worker count answers byte-identically. The gate is
// asserted per request — open for the range operators so that arm
// cannot silently degrade to serial, closed for the non-mergeable
// GROUP_DATE.
func TestTimeZone_ParallelDecodeLocalDay(t *testing.T) {
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	instants := zoneParallelInstants()
	schema := dateOpsSchema()
	path := filepath.Join(dir, "zone_parallel.pulse")
	if err := writeDateOpsCohort(osFs, path, schema, instants); err != nil {
		t.Fatal(err)
	}

	z, err := temporal.LoadZone("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	days, utcDays := map[string]float64{}, map[string]float64{}
	labels, utcLabels := map[string]float64{}, map[string]float64{}
	kept, utcKept := map[string]float64{}, map[string]float64{}
	for i, s := range instants {
		local := time.Unix(s, 0).In(z.Location()).Format("2006-01-02")
		utc := time.Unix(s, 0).UTC().Format("2006-01-02")
		days[local]++
		utcDays[utc]++
		labels[zoneParallelRangeLabel(local)]++
		utcLabels[zoneParallelRangeLabel(utc)]++
		v := float64((i*37)%1009) + 0.5 // writeDateOpsCohort's v
		if zoneParallelRangeLabel(local) != "unmatched" {
			kept["count"]++
			kept["sum"] += v
		}
		if zoneParallelRangeLabel(utc) != "unmatched" {
			utcKept["count"]++
			utcKept["sum"] += v
		}
	}
	if maps.Equal(days, utcDays) || maps.Equal(labels, utcLabels) || maps.Equal(kept, utcKept) {
		t.Fatal("fixture proves nothing: the UTC and Berlin answers coincide")
	}

	countBy := func(key string) func(t *testing.T, resp *types.Response) map[string]float64 {
		return func(t *testing.T, resp *types.Response) map[string]float64 {
			t.Helper()
			got := map[string]float64{}
			for _, row := range resp.Data {
				k, ok := row[key].(string)
				if !ok {
					t.Fatalf("row %v has no string %q", row, key)
				}
				got[k] += zoneParallelNumber(t, row["count"])
			}
			return got
		}
	}
	// parallel: the gate must be OPEN (the arm really fans out). The
	// parallel arms are mergeable-only: GROUP_DATE_RANGES and the
	// filter fan out, while GROUP_DATE is not mergeable
	// (types.GroupType.Mergeable), so its row asserts the gate CLOSED —
	// it runs serial whatever DecodeWorkers says, still by local day.
	cases := []struct {
		name     string
		parallel bool
		want     map[string]float64
		extract  func(t *testing.T, resp *types.Response) map[string]float64
		mk       func() *types.Request
	}{
		{"FILTER_DATE_RANGES", true, kept, func(t *testing.T, resp *types.Response) map[string]float64 {
			t.Helper()
			if len(resp.Data) != 1 {
				t.Fatalf("want one ungrouped row, got %v", resp.Data)
			}
			return map[string]float64{"count": zoneParallelNumber(t, resp.Data[0]["count"]), "sum": zoneParallelNumber(t, resp.Data[0]["sum"])}
		}, func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path}, TimeZone: "Europe/Berlin",
				Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: json.RawMessage(zoneParallelRanges)}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "v", Label: "count"},
					{Type: types.AGG_SUM, Field: "v", Label: "sum"},
				}}
		}},
		{"GROUP_DATE/day", false, days, countBy("ts"), func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path}, TimeZone: "Europe/Berlin",
				Groups:       []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"day"}`)}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "v", Label: "count"}}}
		}},
		{"GROUP_DATE_RANGES", true, labels, countBy("ts"), func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path}, TimeZone: "Europe/Berlin",
				Groups:       []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "ts", Params: json.RawMessage(zoneParallelRanges)}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "v", Label: "count"}}}
		}},
	}

	ctx := context.Background()
	probe := New(cfg)
	cohort, err := probe.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, why := probe.canParallelDecode(tc.mk(), cohort.Schema(), cohort, 4, zoneParallelRows)
			if ok != tc.parallel {
				t.Fatalf("parallel decode gate open=%v (%s), want %v", ok, why, tc.parallel)
			}
			var serial []byte
			for _, workers := range []int{1, 2, 4, 7, 0} {
				svc := New(cfg)
				svc.SetDecodeWorkers(workers)
				resp, err := svc.Process(ctx, tc.mk())
				if err != nil {
					t.Fatalf("workers=%d: %v", workers, err)
				}
				if got := tc.extract(t, resp); !maps.Equal(got, tc.want) {
					t.Fatalf("workers=%d: got %v\n want (time.In reference) %v", workers, got, tc.want)
				}
				// Every worker count answers byte-identically.
				b, err := json.Marshal(resp)
				if err != nil {
					t.Fatal(err)
				}
				if workers == 1 {
					serial = b
				} else if string(b) != string(serial) {
					t.Fatalf("workers=%d response differs from serial:\n got %s\nwant %s", workers, b, serial)
				}
			}
		})
	}
}

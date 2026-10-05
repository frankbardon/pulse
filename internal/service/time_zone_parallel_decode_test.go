package service

import (
	"context"
	"encoding/json"
	"maps"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// zoneParallelRows sits just above parallelDecodeRecordThreshold so the
// buffered Process path fans out per-segment decode.
const zoneParallelRows = parallelDecodeRecordThreshold + 3_331

// zoneParallelInstants returns zoneParallelRows seeded-random instants
// spread over 2026 — both Europe/Berlin DST changes inside, and in
// random order so every decode segment straddles many offset spans.
func zoneParallelInstants() []int64 {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	r := rand.New(rand.NewPCG(14, 4))
	out := make([]int64, zoneParallelRows)
	for i := range out {
		out[i] = start + r.Int64N(end-start)
	}
	return out
}

// zoneParallelRanges labels the two DST days and the evening before
// each, so a UTC-day bug moves rows between labels and "unmatched".
const zoneParallelRanges = `{"ranges":[` +
	`{"label":"spring_eve","start":"2026-03-28","end":"2026-03-28"},` +
	`{"label":"spring","start":"2026-03-29","end":"2026-03-29"},` +
	`{"label":"fall_eve","start":"2026-10-24","end":"2026-10-24"},` +
	`{"label":"fall","start":"2026-10-25","end":"2026-10-25"},` +
	`{"label":"summer","start":"2026-06-01","end":"2026-08-31"}]}`

func zoneParallelRangeLabel(day string) string {
	switch {
	case day == "2026-03-28":
		return "spring_eve"
	case day == "2026-03-29":
		return "spring"
	case day == "2026-10-24":
		return "fall_eve"
	case day == "2026-10-25":
		return "fall"
	case day >= "2026-06-01" && day <= "2026-08-31":
		return "summer"
	}
	return "unmatched"
}

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
	if testing.Short() {
		t.Skip("skipping >100K-record parallel decode fixture in -short mode")
	}
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

func zoneParallelNumber(t *testing.T, v any) float64 {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case uint64:
		return float64(x)
	}
	t.Fatalf("not a number: %v (%T)", v, v)
	return 0
}

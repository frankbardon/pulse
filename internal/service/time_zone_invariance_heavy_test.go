//go:build heavy

package service

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"testing"
	"time"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestTimeZoneInvariance_Heavy is the zone family on the shared
// worker-invariance harness: the parallel buffered decode arm
// (DecodeWorkers 2/3/7/8 over a >threshold single-file cohort, mmap'd
// off a real on-disk file — MemMapFs cannot reach it, so the fixture
// lives in t.TempDir() behind an OsFs-backed fs.Config) buckets
// GROUP_DATE day, labels GROUP_DATE_RANGES and keeps FILTER_DATE_RANGES
// rows by the Europe/Berlin LOCAL day: the serial reference equals a
// time.In reference (on a fixture whose UTC answer differs), and every
// worker count answers the same whole response. Each cell names its arm
// — parallel decode for the range operators, so that arm cannot
// silently degrade to serial; buffered (serial) for the non-mergeable
// GROUP_DATE, whatever DecodeWorkers says.
func TestTimeZoneInvariance_Heavy(t *testing.T) {
	t.Parallel()

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
	// parallel: the arm really fans out. The parallel arms are
	// mergeable-only: GROUP_DATE_RANGES and the filter fan out, while
	// GROUP_DATE is not mergeable (types.GroupType.Mergeable), so its
	// cells run serial whatever DecodeWorkers says, still by local day.
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

	big := svcInvCohort{cfg, path}
	for _, tc := range cases {
		arm := observe.ArmBuffered
		if tc.parallel {
			arm = observe.ArmParallelDecode
		}
		decode := invariance.DecodeGroup("decode", invariance.BigFile)
		for i := range decode.Cells {
			decode.Cells[i].Arm = arm
		}
		invariance.Run(t, invariance.Family[*types.Response]{
			Name:   tc.name,
			Groups: []invariance.Group{decode},
			Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
				return svcInvProcess(t, big, k, tc.mk(), nil)
			},
			Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
				if got := tc.extract(t, resp); !maps.Equal(got, tc.want) {
					t.Fatalf("got %v\n want (time.In reference) %v", got, tc.want)
				}
			},
		})
	}
}

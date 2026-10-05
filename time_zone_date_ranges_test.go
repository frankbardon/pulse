package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Labeled date ranges and OVERLAY_YOY over a Europe/Berlin-zoned
// `datetime` (U14 E1-S3). Range literals are LOCAL calendar days in
// the slot's resolved zone; a named RangeTable is calendar-only and
// follows the slot zone exactly like the inline spec. Every reference
// below is computed with time.In over the embedded zone, independent
// of the operator code under test, and every fixture is checked to
// answer differently under UTC so a zone that fails to apply fails.

func berlinLocation(t *testing.T) *time.Location {
	t.Helper()
	z, err := temporal.LoadZone(berlinZone)
	if err != nil {
		t.Fatal(err)
	}
	return z.Location()
}

// TestTimeZone_DateRangeBoundsAreLocalDays: a range bound is a Berlin
// calendar day. `start: 2026-03-01` keeps 2026-02-28T23:30Z (Berlin
// 00:30 Mar 1) and drops 2026-02-28T22:30Z (Berlin 23:30 Feb 28);
// `end: 2026-02-28` is the mirror. The UTC control keeps neither /
// both, and GROUP_DATE_RANGES labels the same two instants the same
// way.
func TestTimeZone_DateRangeBoundsAreLocalDays(t *testing.T) {
	fs := afero.NewMemMapFs()
	// Row n=0 / cat "a" = 22:30Z, n=1 / cat "b" = 23:30Z.
	instants := []int64{
		time.Date(2026, 2, 28, 22, 30, 0, 0, time.UTC).Unix(),
		time.Date(2026, 2, 28, 23, 30, 0, 0, time.UTC).Unix(),
	}
	cohort, _ := importDST(t, fs, instants)
	p := zonePulse(t, fs, "")
	ctx := context.Background()

	startMar1 := json.RawMessage(`{"ranges":[{"label":"march","start":"2026-03-01"}]}`)
	endFeb28 := json.RawMessage(`{"ranges":[{"label":"feb","end":"2026-02-28"}]}`)

	filterByCat := func(params json.RawMessage, tz string) map[string]float64 {
		t.Helper()
		resp, err := p.Process(ctx, &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: countAgg(),
			Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: params, TimeZone: tz}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return dayCountsFromRows(t, resp.Data, "cat")
	}
	// groupByRange answers "label/count" → the sum of the row ids (n)
	// the label holds: n=0 is 22:30Z, n=1 is 23:30Z.
	groupByRange := func(params json.RawMessage, tz string) map[string]float64 {
		t.Helper()
		resp, err := p.Process(ctx, &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "n", Label: "count"},
				{Type: types.AGG_SUM, Field: "n", Label: "ids"},
			},
			Groups: []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "ts", Params: params, TimeZone: tz}},
		})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]float64{}
		for _, r := range resp.Data {
			out[fmt.Sprint(r["ts"])+"/"+fmt.Sprint(toFloat(t, r["count"]))] = toFloat(t, r["ids"])
		}
		return out
	}

	cases := []struct {
		name   string
		params json.RawMessage
		tz     string
		filter map[string]float64
		group  map[string]float64
	}{
		{"start/berlin", startMar1, berlinZone, map[string]float64{"b": 1}, map[string]float64{"unmatched/1": 0, "march/1": 1}},
		{"start/utc", startMar1, "", map[string]float64{}, map[string]float64{"unmatched/2": 1}},
		{"end/berlin", endFeb28, berlinZone, map[string]float64{"a": 1}, map[string]float64{"feb/1": 0, "unmatched/1": 1}},
		{"end/utc", endFeb28, "", map[string]float64{"a": 1, "b": 1}, map[string]float64{"feb/2": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filterByCat(tc.params, tc.tz); !maps.Equal(got, tc.filter) {
				t.Fatalf("FILTER_DATE_RANGES kept %v, want %v", got, tc.filter)
			}
			if got := groupByRange(tc.params, tc.tz); !maps.Equal(got, tc.group) {
				t.Fatalf("GROUP_DATE_RANGES labelled %v, want %v", got, tc.group)
			}
		})
	}
}

// dstRangeSpecs labels Berlin days around both 2026 DST changes; the
// fall range spans the 25-hour day.
const dstRangeSpecs = `[{"label":"eve","start":"2026-03-28","end":"2026-03-28"},` +
	`{"label":"spring","start":"2026-03-29","end":"2026-03-29"},` +
	`{"label":"fall","start":"2026-10-24","end":"2026-10-25"}]`

// dstRangeLabel is the reference labeller: the range whose (local)
// day set holds day, else the unmatched label.
func dstRangeLabel(day string) string {
	switch day {
	case "2026-03-28":
		return "eve"
	case "2026-03-29":
		return "spring"
	case "2026-10-24", "2026-10-25":
		return "fall"
	}
	return "unmatched"
}

// TestTimeZone_DateRangesLocalDayEveryArm: GROUP_DATE_RANGES labels and
// FILTER_DATE_RANGES keeps by Berlin day across both 2026 DST changes,
// with the ranges authored inline, registered as an
// Extensions.RangeTables entry or loaded from PULSE_RANGE_TABLES_DIR,
// and every execution arm agrees with the time.In reference.
func TestTimeZone_DateRangesLocalDayEveryArm(t *testing.T) {
	fs := afero.NewMemMapFs()
	instants := dstInstants()
	cohort, archive := importDST(t, fs, instants)
	loc := berlinLocation(t)
	ctx := context.Background()

	labels, utcLabels := map[string]float64{}, map[string]float64{}
	keptByCat, utcKeptByCat := map[string]float64{}, map[string]float64{}
	// The archive's shards are imported separately, so dstCSV restarts
	// the cat cycle at the second shard's first row.
	keptByCatArchive := map[string]float64{}
	half := len(instants) / 2
	for i, s := range instants {
		cat := []string{"a", "b", "c"}[i%3]
		archiveCat := cat
		if i >= half {
			archiveCat = []string{"a", "b", "c"}[(i-half)%3]
		}
		local := dstRangeLabel(time.Unix(s, 0).In(loc).Format("2006-01-02"))
		utc := dstRangeLabel(time.Unix(s, 0).UTC().Format("2006-01-02"))
		labels[local]++
		utcLabels[utc]++
		if local != "unmatched" {
			keptByCat[cat]++
			keptByCatArchive[archiveCat]++
		}
		if utc != "unmatched" {
			utcKeptByCat[cat]++
		}
	}
	if maps.Equal(labels, utcLabels) || maps.Equal(keptByCat, utcKeptByCat) {
		t.Fatal("fixture proves nothing: UTC and Berlin range answers coincide")
	}

	// The PULSE_RANGE_TABLES_DIR source: the env var is read at
	// pulse.New, so every instance below built after Setenv carries
	// the on-disk table "dst_disk".
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dst_disk.json"), []byte(`{"description":"DST days","ranges":`+dstRangeSpecs+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_RANGE_TABLES_DIR", dir)

	var specs []pulse.DateRangeSpec
	if err := json.Unmarshal([]byte(dstRangeSpecs), &specs); err != nil {
		t.Fatal(err)
	}
	newPulse := func(o pulse.Options) *pulse.Pulse {
		t.Helper()
		o.FS = fs
		o.Extensions.RangeTables = map[string]pulse.RangeTable{"dst": {Ranges: specs}}
		p, err := pulse.New(o)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := newPulse(pulse.Options{})
	pBuffered := newPulse(pulse.Options{DisableCrosstabFusion: true})
	pSerialShards := newPulse(pulse.Options{ShardWorkers: 1})

	sources := []struct{ name, params string }{
		{"inline", `{"ranges":` + dstRangeSpecs + `}`},
		{"table", `{"table":"dst"}`},
		{"table-dir", `{"table":"dst_disk"}`},
	}
	// The zone rides the slot or the request; both resolve the same.
	zoneSources := []struct{ name, slot, req string }{{"slot", berlinZone, ""}, {"request", "", berlinZone}}

	// shape builds the request under test: the grouper case groups by
	// range label; the filter case keeps in-range rows and groups by
	// cat. key is the response column to count by.
	type shape struct {
		name        string
		key         string
		want        map[string]float64
		wantArchive map[string]float64
		mk          func(path, params, slot, req string) *types.Request
	}
	shapes := []shape{
		{"group", "ts", labels, labels, func(path, params, slot, req string) *types.Request {
			return &types.Request{
				Cohort: &types.Cohort{Filename: path}, TimeZone: req, Aggregations: countAgg(),
				Groups: []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "ts", Params: json.RawMessage(params), TimeZone: slot}},
			}
		}},
		{"filter", "cat", keptByCat, keptByCatArchive, func(path, params, slot, req string) *types.Request {
			return &types.Request{
				Cohort: &types.Cohort{Filename: path}, TimeZone: req, Aggregations: countAgg(),
				Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: json.RawMessage(params), TimeZone: slot}},
				Groups:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			}
		}},
	}

	// crosstabOf moves the request's grouper onto the row axis and
	// crosses it with a single-bucket column so the matrix row sums
	// are the grouped counts.
	crosstabOf := func(req *types.Request) *types.Request {
		req.Crosstab = &types.CrosstabSpec{
			Rows:    req.Groups,
			Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "n", Params: json.RawMessage(`{"interval":100000}`)}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
		}
		req.Groups, req.Aggregations = nil, nil
		return req
	}

	for _, sh := range shapes {
		for _, src := range sources {
			for _, zs := range zoneSources {
				mk := func(path string) *types.Request { return sh.mk(path, src.params, zs.slot, zs.req) }
				name := sh.name + "/" + src.name + "/" + zs.name
				t.Run(name+"/process", func(t *testing.T) {
					resp, err := p.Process(ctx, mk(cohort))
					if err != nil {
						t.Fatal(err)
					}
					requireCounts(t, dayCountsFromRows(t, resp.Data, sh.key), sh.want)
				})
				t.Run(name+"/buffered", func(t *testing.T) {
					// AGG_MEDIAN is not streamable, so the request runs
					// the buffered orchestrator.
					req := mk(cohort)
					req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "n", Label: "med"})
					pr, err := p.Predict(ctx, req)
					if err != nil || !pr.Valid || pr.Streamable {
						t.Fatalf("predict: want a valid buffered request (valid=%v streamable=%v err=%v)", pr != nil && pr.Valid, pr != nil && pr.Streamable, err)
					}
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					requireCounts(t, dayCountsFromRows(t, resp.Data, sh.key), sh.want)
				})
				t.Run(name+"/stream", func(t *testing.T) {
					it, err := p.ProcessStream(ctx, mk(cohort))
					if err != nil {
						t.Fatal(err)
					}
					defer it.Close()
					var rows []map[string]any
					for {
						row, ok, err := it.Next(ctx)
						if err != nil {
							t.Fatal(err)
						}
						if !ok {
							break
						}
						rows = append(rows, row)
					}
					requireCounts(t, dayCountsFromRows(t, rows, sh.key), sh.want)
				})
				t.Run(name+"/compose-parallel", func(t *testing.T) {
					out, err := p.ComposeParallel(ctx, &types.ComposedRequest{Requests: []*types.Request{mk(cohort), mk(cohort)}}, pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
					if err != nil {
						t.Fatal(err)
					}
					for _, r := range out.Responses {
						requireCounts(t, dayCountsFromRows(t, r.Data, sh.key), sh.want)
					}
				})
				t.Run(name+"/shards-parallel", func(t *testing.T) {
					resp, err := p.Process(ctx, mk(archive))
					if err != nil {
						t.Fatal(err)
					}
					requireCounts(t, dayCountsFromRows(t, resp.Data, sh.key), sh.wantArchive)
				})
				t.Run(name+"/shards-serial", func(t *testing.T) {
					resp, err := pSerialShards.Process(ctx, mk(archive))
					if err != nil {
						t.Fatal(err)
					}
					requireCounts(t, dayCountsFromRows(t, resp.Data, sh.key), sh.wantArchive)
				})
				t.Run(name+"/crosstab-fused", func(t *testing.T) {
					pr, err := p.Predict(ctx, crosstabOf(mk(cohort)))
					if err != nil || !pr.Valid || pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
						t.Fatalf("predict: the fused arm is not taken (valid=%v reasons=%v err=%v)", pr != nil && pr.Valid, pr.CrosstabFusionReasons, err)
					}
					resp, err := p.Process(ctx, crosstabOf(mk(cohort)))
					if err != nil {
						t.Fatal(err)
					}
					requireCounts(t, dayCountsFromMatrix(t, resp), sh.want)
				})
				t.Run(name+"/crosstab-buffered", func(t *testing.T) {
					resp, err := pBuffered.Process(ctx, crosstabOf(mk(cohort)))
					if err != nil {
						t.Fatal(err)
					}
					requireCounts(t, dayCountsFromMatrix(t, resp), sh.want)
				})
			}
		}
	}
}

// yoyReference is the expected OVERLAY_YOY layer for per-key counts:
// key → count(key) / count(priorKey(key)) × 100, NaN when the prior
// key is absent — the overlay contract stated over key strings, so
// the reference does not share the handler's ordinal arithmetic.
func yoyReference(counts map[string]float64, priorKey func(string) string) map[string]float64 {
	out := map[string]float64{}
	for k, v := range counts {
		prior, ok := counts[priorKey(k)]
		if !ok {
			out[k] = math.NaN()
			continue
		}
		out[k] = v / prior * 100
	}
	return out
}

func yoyEqual(a, b map[string]float64) bool {
	return maps.EqualFunc(a, b, func(x, y float64) bool {
		return (math.IsNaN(x) && math.IsNaN(y)) || math.Abs(x-y) < 1e-9
	})
}

// TestTimeZone_YoYFollowsHostZone: OVERLAY_YOY has no zone of its own —
// it compares host key strings, so over a Berlin-zoned GROUP_DATE host
// its entries are keyed on Berlin labels and compare Berlin buckets,
// on the daily and hourly (exact-key) arms and the coarse (ordinal)
// monthly arm,
// whether the zone rides the host slot or the request. Each fixture
// puts rows at 23:30Z, which is the NEXT Berlin day (and month), so
// the UTC answer differs.
func TestTimeZone_YoYFollowsHostZone(t *testing.T) {
	loc := berlinLocation(t)
	at := func(y int, m time.Month, d, h, min int) int64 {
		return time.Date(y, m, d, h, min, 0, 0, time.UTC).Unix()
	}

	var daily []int64
	for y := 2025; y <= 2026; y++ {
		for d := 1; d <= 5; d++ {
			for range d + (y-2025)*3 {
				daily = append(daily, at(y, time.March, d, 12, 0))
			}
			for range d%2 + 1 {
				daily = append(daily, at(y, time.March, d, 23, 30)) // Berlin: day d+1
			}
		}
	}
	var monthly []int64
	for y := 2025; y <= 2026; y++ {
		for m := time.January; m <= time.December; m++ {
			for range int(m) + (y-2025)*5 {
				monthly = append(monthly, at(y, m, 15, 12, 0))
			}
			if y == 2026 && m == time.December {
				continue // keep the host keys inside 2025-01..2026-12
			}
			last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
			for range int(m)%3 + 1 {
				monthly = append(monthly, at(y, m, last, 23, 30)) // Berlin: next month
			}
		}
	}
	// hourly: every UTC hour of 2025-06-10 and 2026-06-10 (365 days
	// apart), counts varying by hour and year; Berlin (+2) relabels
	// every bucket, so the UTC layer is keyed differently.
	var hourly []int64
	for y := 2025; y <= 2026; y++ {
		for h := range 24 {
			for range h%5 + 1 + (y-2025)*(h%3) {
				hourly = append(hourly, at(y, time.June, 10, h, 15))
			}
		}
	}

	arms := []struct {
		name, component, frequency, layout string
		instants                           []int64
		priorKey                           func(string) string
	}{
		{"daily", "day", "daily", "2006-01-02", daily, func(k string) string {
			d, _ := time.Parse("2006-01-02", k)
			return d.AddDate(-1, 0, 0).Format("2006-01-02")
		}},
		{"monthly", "month", "monthly", "2006-01", monthly, func(k string) string {
			d, _ := time.Parse("2006-01", k)
			return d.AddDate(-1, 0, 0).Format("2006-01")
		}},
		// GROUP_DATE `hour` feeds the hourly exact-key arm end to end.
		{"hourly", "hour", "hourly", "2006-01-02T15", hourly, func(k string) string {
			d, _ := time.Parse("2006-01-02T15", k)
			return d.Add(-365 * 24 * time.Hour).Format("2006-01-02T15")
		}},
	}
	ctx := context.Background()
	for _, arm := range arms {
		fs := afero.NewMemMapFs()
		cohort, _ := importDST(t, fs, arm.instants)
		local, utc := map[string]float64{}, map[string]float64{}
		for _, s := range arm.instants {
			local[time.Unix(s, 0).In(loc).Format(arm.layout)]++
			utc[time.Unix(s, 0).UTC().Format(arm.layout)]++
		}
		want := yoyReference(local, arm.priorKey)
		if yoyEqual(want, yoyReference(utc, arm.priorKey)) {
			t.Fatalf("%s fixture proves nothing: UTC and Berlin YoY coincide", arm.name)
		}
		p := zonePulse(t, fs, "")
		for _, zs := range []struct{ name, slot, req string }{{"slot", berlinZone, ""}, {"request", "", berlinZone}} {
			t.Run(arm.name+"/"+zs.name, func(t *testing.T) {
				resp, err := p.Process(ctx, &types.Request{
					Cohort:       &types.Cohort{Filename: cohort},
					TimeZone:     zs.req,
					Aggregations: countAgg(),
					Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZone: zs.slot,
						Params: json.RawMessage(`{"component":"` + arm.component + `"}`)}},
					Overlays: []types.OverlaySpec{{
						Name: "yoy", Kind: types.OverlayKindYoY, Scope: types.OverlayScopeGroup,
						Ref:    types.OverlayRef{YoY: &types.OverlayYoYRef{}},
						Params: json.RawMessage(`{"frequency":"` + arm.frequency + `"}`),
					}},
				})
				if err != nil {
					t.Fatal(err)
				}
				requireCounts(t, dayCountsFromRows(t, resp.Data, "ts"), local)
				if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Series == nil {
					t.Fatalf("overlays = %+v, want one SERIES layer", resp.Overlays)
				}
				got := map[string]float64{}
				for _, e := range resp.Overlays[0].Payload.Series.Entries {
					if len(e.Key) != 1 || e.Summary.Statistic == nil {
						t.Fatalf("entry %+v: want a one-part key and a statistic", e)
					}
					got[fmt.Sprint(e.Key[0])] = *e.Summary.Statistic
				}
				if !yoyEqual(got, want) {
					t.Fatalf("YoY = %v\n want (Berlin-label reference) %v", got, want)
				}
			})
		}
	}
}

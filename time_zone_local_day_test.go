package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

const berlinZone = "Europe/Berlin"

// dstInstants returns every half hour from 06:00 UTC the day before to
// 06:00 UTC the day after each 2026 Europe/Berlin DST change (spring
// forward 2026-03-29, fall back 2026-10-25), as epoch seconds. Across
// that span the UTC day and the Berlin day disagree for one or two
// hours every evening, and the offset itself changes mid-span.
func dstInstants() []int64 {
	var out []int64
	for _, day := range []string{"2026-03-29", "2026-10-25"} {
		d, err := time.Parse("2006-01-02", day)
		if err != nil {
			panic(err)
		}
		for t := d.Add(-18 * time.Hour); !t.After(d.Add(30 * time.Hour)); t = t.Add(30 * time.Minute) {
			out = append(out, t.Unix())
		}
	}
	return out
}

// dstCSV renders instants as a CSV with a `datetime` column ts, a
// categorical cat and a unique integer n (the self-join key).
func dstCSV(instants []int64) string {
	var b strings.Builder
	b.WriteString("ts,cat,n\n")
	for i, s := range instants {
		fmt.Fprintf(&b, "%s,%s,%d\n", time.Unix(s, 0).UTC().Format(time.RFC3339), []string{"a", "b", "c"}[i%3], i)
	}
	return b.String()
}

// berlinDayCounts is the reference: rows per Berlin calendar day,
// computed with time.In over the embedded zone — independent of the
// operator code under test.
func berlinDayCounts(t *testing.T, instants []int64) map[string]float64 {
	t.Helper()
	z, err := temporal.LoadZone(berlinZone)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, s := range instants {
		out[time.Unix(s, 0).In(z.Location()).Format("2006-01-02")]++
	}
	return out
}

// importDST imports the DST fixture into fs (whole, and split into a
// two-shard archive) and returns both cohort paths.
func importDST(t *testing.T, fs afero.Fs, instants []int64) (single, archive string) {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	imp := func(name string, rows []int64) string {
		if err := afero.WriteFile(fs, name+".csv", []byte(dstCSV(rows)), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: name + ".csv"})
		if err != nil {
			t.Fatalf("ImportFile %s: %v", name, err)
		}
		return res.Path
	}
	single = imp("dst", instants)
	ins, err := p.Inspect(ctx, single)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range ins.Fields {
		if f.Name == "ts" && f.Type != "datetime" {
			t.Fatalf("ts inferred as %s, want datetime", f.Type)
		}
	}
	half := len(instants) / 2
	a, b := imp("dst_a", instants[:half]), imp("dst_b", instants[half:])
	if _, err := p.CreateShardArchive(ctx, "dst_archive.pulse", []string{a, b}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return single, "dst_archive.pulse"
}

// dayCountsFromRows reads {key → count} off grouped response rows.
func dayCountsFromRows(t *testing.T, rows []map[string]any, key string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, r := range rows {
		k, ok := r[key].(string)
		if !ok {
			t.Fatalf("row %v has no string %q key", r, key)
		}
		out[k] += toFloat(t, r["count"])
	}
	return out
}

func toFloat(t *testing.T, v any) float64 {
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

// dayCountsFromMatrix sums each crosstab row across its columns.
func dayCountsFromMatrix(t *testing.T, resp *types.Response) map[string]float64 {
	t.Helper()
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("no crosstab matrix")
	}
	m := resp.Crosstab.Matrix
	out := map[string]float64{}
	for i, rk := range m.RowKeys {
		for _, c := range m.Cells[i] {
			out[fmt.Sprint(rk[0])] += c.Scalar()
		}
	}
	return out
}

func requireCounts(t *testing.T, got, want map[string]float64) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Fatalf("day buckets = %v\n want (time.In reference) %v", got, want)
	}
}

// berlinDayGroup is GROUP_DATE day over ts carrying the zone the
// source names (slot) or none (the zone comes from the request /
// options).
func berlinDayGroup(slotTZ string) *types.Group {
	return &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"day"}`), TimeZone: slotTZ}
}

// TestTimeZone_LocalDayEveryMode: GROUP_DATE day buckets over a
// `datetime` spanning both 2026 Europe/Berlin DST changes equal a
// time.In reference in every execution mode — whether the zone comes
// from the slot, the request or Options.DefaultTimeZone — and
// FILTER_DATE_RANGES keeps exactly the rows whose Berlin day is in
// range on the filter-only arms (chain, facet). The UTC answer differs
// on this fixture, so a zone that fails to reach an arm fails here.
func TestTimeZone_LocalDayEveryMode(t *testing.T) {
	fs := afero.NewMemMapFs()
	instants := dstInstants()
	cohort, archive := importDST(t, fs, instants)
	want := berlinDayCounts(t, instants)
	ctx := context.Background()

	utc := map[string]float64{}
	for _, s := range instants {
		utc[time.Unix(s, 0).UTC().Format("2006-01-02")]++
	}
	if maps.Equal(utc, want) {
		t.Fatal("fixture proves nothing: UTC and Berlin day buckets coincide")
	}
	// The rows whose BERLIN day is 2026-03-29 (23 local hours) or
	// 2026-10-24 (cut short by the fixture's 06:00 UTC start) — the
	// filter-only modes count them; the UTC days hold more rows.
	inRange := want["2026-03-29"] + want["2026-10-24"]
	if inRange == utc["2026-03-29"]+utc["2026-10-24"] {
		t.Fatal("fixture proves nothing for the filter: UTC and Berlin in-range counts coincide")
	}
	ranges := json.RawMessage(`{"ranges":[{"label":"spring","start":"2026-03-29","end":"2026-03-29"},{"label":"fall","start":"2026-10-24","end":"2026-10-24"}]}`)

	sources := []struct{ name, slot, req, opts string }{
		{"slot", berlinZone, "", ""},
		{"request", "", berlinZone, ""},
		{"options", "", "", berlinZone},
	}
	for _, src := range sources {
		p := zonePulse(t, fs, src.opts)
		pBuffered, err := pulse.New(pulse.Options{FS: fs, DefaultTimeZone: src.opts, DisableCrosstabFusion: true})
		if err != nil {
			t.Fatal(err)
		}
		pSerialShards, err := pulse.New(pulse.Options{FS: fs, DefaultTimeZone: src.opts, ShardWorkers: 1})
		if err != nil {
			t.Fatal(err)
		}
		mk := func(path string) *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: path},
				TimeZone:     src.req,
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n", Label: "count"}},
				Groups:       []*types.Group{berlinDayGroup(src.slot)},
			}
		}
		crosstab := func() *types.Request {
			return &types.Request{
				Cohort:   &types.Cohort{Filename: cohort},
				TimeZone: src.req,
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{berlinDayGroup(src.slot)},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
				},
			}
		}

		t.Run(src.name+"/process", func(t *testing.T) {
			resp, err := p.Process(ctx, mk(cohort))
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromRows(t, resp.Data, "ts"), want)
		})
		t.Run(src.name+"/stream", func(t *testing.T) {
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
			requireCounts(t, dayCountsFromRows(t, rows, "ts"), want)
		})
		t.Run(src.name+"/compose", func(t *testing.T) {
			out, err := p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{mk(cohort)}})
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromRows(t, out.Responses[0].Data, "ts"), want)
		})
		t.Run(src.name+"/compose-parallel", func(t *testing.T) {
			out, err := p.ComposeParallel(ctx, &types.ComposedRequest{Requests: []*types.Request{mk(cohort), mk(cohort)}}, pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range out.Responses {
				requireCounts(t, dayCountsFromRows(t, r.Data, "ts"), want)
			}
		})
		t.Run(src.name+"/shards-parallel", func(t *testing.T) {
			resp, err := p.Process(ctx, mk(archive))
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromRows(t, resp.Data, "ts"), want)
		})
		t.Run(src.name+"/shards-serial", func(t *testing.T) {
			resp, err := pSerialShards.Process(ctx, mk(archive))
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromRows(t, resp.Data, "ts"), want)
		})
		t.Run(src.name+"/crosstab-fused", func(t *testing.T) {
			pr, err := p.Predict(ctx, crosstab())
			if err != nil || !pr.Valid || pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
				t.Fatalf("predict: the fused arm is not taken (valid=%v fusable=%v err=%v)", pr != nil && pr.Valid, pr != nil && pr.CrosstabFusable != nil && *pr.CrosstabFusable, err)
			}
			resp, err := p.Process(ctx, crosstab())
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromMatrix(t, resp), want)
		})
		t.Run(src.name+"/crosstab-buffered", func(t *testing.T) {
			resp, err := pBuffered.Process(ctx, crosstab())
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromMatrix(t, resp), want)
		})
		t.Run(src.name+"/join", func(t *testing.T) {
			req := mk(cohort)
			req.Joins = []*types.JoinSpec{{Right: cohort, On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: "r_"}}
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromRows(t, resp.Data, "ts"), want)
		})
		t.Run(src.name+"/join-right-field", func(t *testing.T) {
			// The joined column resolves by its joined-schema type.
			req := mk(cohort)
			req.Groups[0].Field = "r_ts"
			req.Joins = []*types.JoinSpec{{Right: cohort, On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: "r_"}}
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromRows(t, resp.Data, "r_ts"), want)
		})
		t.Run(src.name+"/chain", func(t *testing.T) {
			out, err := p.ProcessChain(ctx, &types.ChainRequest{
				Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{{Request: &types.Request{
					TimeZone:     src.req,
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n", Label: "count"}},
					Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: ranges, TimeZone: src.slot}},
				}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := toFloat(t, out.Final.Data[0]["count"]); got != inRange {
				t.Fatalf("chain count = %v, want %v (Berlin-day rows)", got, inRange)
			}
		})
		t.Run(src.name+"/facet", func(t *testing.T) {
			res, err := p.FacetSchema(ctx, &types.FacetRequest{
				Cohort:    &types.Cohort{Filename: cohort},
				Fields:    []string{"cat"},
				TimeZone:  src.req,
				Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: ranges, TimeZone: src.slot}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if float64(res.FilteredRecords) != inRange {
				t.Fatalf("facet filtered = %d, want %v (Berlin-day rows)", res.FilteredRecords, inRange)
			}
		})
		t.Run(src.name+"/predict", func(t *testing.T) {
			pr, err := p.Predict(ctx, mk(cohort))
			if err != nil || !pr.Valid {
				t.Fatalf("predict refused what the runtime runs: valid=%v err=%v", pr != nil && pr.Valid, err)
			}
			if len(pr.TimeZones) != 1 || pr.TimeZones[0].TZ == nil || *pr.TimeZones[0].TZ != berlinZone {
				t.Fatalf("TimeZones = %+v, want one slot resolving %s", pr.TimeZones, berlinZone)
			}
		})
	}
}

// TestTimeZone_LocalDayLabelsAndPeriods: the Berlin buckets carry
// local calendar labels and local period boundaries — the instant
// 2026-03-28T23:30Z lands in the 2026-03-29 bucket, whose
// period_start / period_end are that local day.
func TestTimeZone_LocalDayLabelsAndPeriods(t *testing.T) {
	fs := afero.NewMemMapFs()
	instants := []int64{
		time.Date(2026, 3, 28, 22, 30, 0, 0, time.UTC).Unix(),  // Berlin 23:30 Mar 28 (CET)
		time.Date(2026, 3, 28, 23, 30, 0, 0, time.UTC).Unix(),  // Berlin 00:30 Mar 29
		time.Date(2026, 10, 24, 22, 30, 0, 0, time.UTC).Unix(), // Berlin 00:30 Oct 25 (CEST)
	}
	cohort, _ := importDST(t, fs, instants)
	p := zonePulse(t, fs, "")
	resp, err := p.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		TimeZone:     berlinZone,
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n", Label: "count"}},
		Groups:       []*types.Group{berlinDayGroup("")},
	})
	if err != nil {
		t.Fatal(err)
	}
	requireCounts(t, dayCountsFromRows(t, resp.Data, "ts"), map[string]float64{"2026-03-28": 1, "2026-03-29": 1, "2026-10-25": 1})
	if resp.Components == nil || len(resp.Components.Groupers) != 1 {
		t.Fatalf("components = %+v", resp.Components)
	}
	buckets, _ := json.Marshal(resp.Components.Groupers[0].Operator["buckets"])
	for _, want := range []string{
		`{"count":1,"key":"2026-03-29","period_end":"2026-03-29","period_start":"2026-03-29"}`,
		`{"count":1,"key":"2026-10-25","period_end":"2026-10-25","period_start":"2026-10-25"}`,
	} {
		if !strings.Contains(string(buckets), want) {
			t.Fatalf("buckets %s lack %s", buckets, want)
		}
	}
}

// TestTimeZone_ZoneWriteLeavesCallerRequestPristine: the resolved zone
// rides on a copy of the executing request, never the caller's — the
// slot keeps an empty `tz`, the request hash (Watch) and the Compose
// hash do not move, so the request echo and the Compose echo show the
// request as written.
func TestTimeZone_ZoneWriteLeavesCallerRequestPristine(t *testing.T) {
	fs := afero.NewMemMapFs()
	cohort, _ := importDST(t, fs, dstInstants())
	ctx := context.Background()
	for _, src := range []struct{ name, req, opts string }{{"request", berlinZone, ""}, {"options", "", berlinZone}} {
		t.Run(src.name, func(t *testing.T) {
			p := zonePulse(t, fs, src.opts)
			mk := func() *types.Request {
				return &types.Request{
					Cohort:       &types.Cohort{Filename: cohort},
					TimeZone:     src.req,
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n", Label: "count"}},
					Groups:       []*types.Group{berlinDayGroup("")},
					Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: json.RawMessage(`{"ranges":[{"label":"x","start":"2026-01-01","end":"2026-12-31"}]}`)}},
				}
			}
			req := mk()
			before := req.Hash()
			if _, err := p.Process(ctx, req); err != nil {
				t.Fatal(err)
			}
			if req.Groups[0].TimeZone != "" || req.Filterers[0].TimeZone != "" || req.Hash() != before {
				t.Fatalf("caller's request mutated: group tz=%q filter tz=%q hash moved=%v", req.Groups[0].TimeZone, req.Filterers[0].TimeZone, req.Hash() != before)
			}
			composed := &types.ComposedRequest{Requests: []*types.Request{mk()}}
			cbefore := composed.Hash()
			if _, err := p.Compose(ctx, composed); err != nil {
				t.Fatal(err)
			}
			if composed.Hash() != cbefore || composed.Requests[0].Groups[0].TimeZone != "" {
				t.Fatal("Compose mutated the caller's slot")
			}
		})
	}
}

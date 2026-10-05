package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestGroupDate_HourWeekStartEveryArm: GROUP_DATE `hour` and
// `week_start: sunday` over a Berlin-zoned `datetime` spanning both 2026
// DST changes equal a time.In reference on every arm — process, stream,
// compose (serial and parallel), shard archive (parallel and serial) and
// crosstab (fused, asserted taken, and buffered). GROUP_DATE is not
// mergeable, so the parallel arms run it serially; they must agree all
// the same. The Berlin hour reference has no 2026-03-29T02 bucket and one
// merged 2026-10-25T02 bucket of both 02:xx hours.
func TestGroupDate_HourWeekStartEveryArm(t *testing.T) {
	fs := afero.NewMemMapFs()
	instants := dstInstants()
	cohort, archive := importDST(t, fs, instants)
	loc := berlinLocation(t)
	ctx := context.Background()

	cases := []struct {
		name, params string
		key          func(time.Time) string
	}{
		{"hour", `{"component":"hour"}`, func(t time.Time) string { return t.Format("2006-01-02T15") }},
		{"week-sunday", `{"component":"week","week_start":"sunday"}`, func(t time.Time) string { return weekKey(t, time.Sunday) }},
	}
	for _, tc := range cases {
		want, utc := map[string]float64{}, map[string]float64{}
		for _, s := range instants {
			want[tc.key(time.Unix(s, 0).In(loc))]++
			utc[tc.key(time.Unix(s, 0).UTC())]++
		}
		if maps.Equal(want, utc) {
			t.Fatalf("%s: fixture proves nothing — UTC and Berlin coincide", tc.name)
		}
		if tc.name == "hour" {
			if _, ok := want["2026-03-29T02"]; ok {
				t.Fatal("reference has a 2026-03-29T02 bucket; Berlin skips that hour")
			}
			if want["2026-10-25T02"] != 4 {
				t.Fatalf("reference 2026-10-25T02 = %v rows, want 4 (two half-hours of each 02:xx)", want["2026-10-25T02"])
			}
		}
		group := func() *types.Group {
			return &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(tc.params), TimeZone: berlinZone}
		}
		mk := func(path string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path}, Aggregations: countAgg(), Groups: []*types.Group{group()}}
		}
		crosstab := func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Crosstab: &types.CrosstabSpec{
				Rows: []*types.Group{group()}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
			}}
		}
		p := zonePulse(t, fs, "")
		pBuffered, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: true})
		if err != nil {
			t.Fatal(err)
		}
		pSerial, err := pulse.New(pulse.Options{FS: fs, ShardWorkers: 1})
		if err != nil {
			t.Fatal(err)
		}
		rows := func(t *testing.T, pp *pulse.Pulse, path string) map[string]float64 {
			t.Helper()
			resp, err := pp.Process(ctx, mk(path))
			if err != nil {
				t.Fatal(err)
			}
			return dayCountsFromRows(t, resp.Data, "ts")
		}
		t.Run(tc.name+"/process", func(t *testing.T) { requireCounts(t, rows(t, p, cohort), want) })
		t.Run(tc.name+"/shards-parallel", func(t *testing.T) { requireCounts(t, rows(t, p, archive), want) })
		t.Run(tc.name+"/shards-serial", func(t *testing.T) { requireCounts(t, rows(t, pSerial, archive), want) })
		t.Run(tc.name+"/stream", func(t *testing.T) {
			it, err := p.ProcessStream(ctx, mk(cohort))
			if err != nil {
				t.Fatal(err)
			}
			defer it.Close()
			var got []map[string]any
			for {
				row, ok, err := it.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					break
				}
				got = append(got, row)
			}
			requireCounts(t, dayCountsFromRows(t, got, "ts"), want)
		})
		t.Run(tc.name+"/compose", func(t *testing.T) {
			out, err := p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{mk(cohort)}})
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromRows(t, out.Responses[0].Data, "ts"), want)
		})
		t.Run(tc.name+"/compose-parallel", func(t *testing.T) {
			out, err := p.ComposeParallel(ctx, &types.ComposedRequest{Requests: []*types.Request{mk(cohort), mk(archive)}}, pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range out.Responses {
				requireCounts(t, dayCountsFromRows(t, r.Data, "ts"), want)
			}
		})
		t.Run(tc.name+"/crosstab-fused", func(t *testing.T) {
			pr, err := p.Predict(ctx, crosstab())
			if err != nil || !pr.Valid || pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
				t.Fatalf("predict: the fused arm is not taken (err=%v)", err)
			}
			resp, err := p.Process(ctx, crosstab())
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromMatrix(t, resp), want)
		})
		t.Run(tc.name+"/crosstab-buffered", func(t *testing.T) {
			resp, err := pBuffered.Process(ctx, crosstab())
			if err != nil {
				t.Fatal(err)
			}
			requireCounts(t, dayCountsFromMatrix(t, resp), want)
		})
	}
}

// TestGroupDate_CalendarParamRefusalsPredictParity: every GROUP_DATE
// params / field refusal — week_start without week, an invalid day name,
// hour on a `date` or non-temporal field, plus the pre-existing
// component and fiscal_offset rules — is PROCESSING_CONFIG with the
// same message from predict and from the runtime, on Request.Groups and
// on a crosstab axis.
func TestGroupDate_CalendarParamRefusalsPredictParity(t *testing.T) {
	fs := afero.NewMemMapFs()
	cohort := identityCohort(t, fs)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	for _, tc := range []struct{ name, field, params string }{
		{"week_start-without-week", "ts", `{"component":"day","week_start":"sunday"}`},
		{"week_start-default-component", "ts", `{"week_start":"monday"}`},
		{"week_start-bad-name", "ts", `{"component":"week","week_start":"Sunday"}`},
		{"hour-on-date", "d", `{"component":"hour"}`},
		{"hour-on-integer", "n", `{"component":"hour"}`},
		{"unknown-component", "ts", `{"component":"minute"}`},
		{"fiscal-on-hour", "ts", `{"component":"hour","fiscal_offset":3}`},
	} {
		for _, shape := range []string{"groups", "crosstab"} {
			t.Run(tc.name+"/"+shape, func(t *testing.T) {
				g := &types.Group{Type: types.GROUP_DATE, Field: tc.field, Params: json.RawMessage(tc.params)}
				req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(), Groups: []*types.Group{g}}
				if shape == "crosstab" {
					req = &types.Request{Cohort: &types.Cohort{Filename: cohort}, Crosstab: &types.CrosstabSpec{
						Rows: []*types.Group{g}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
						Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
					}}
				}
				_, rerr := p.Process(ctx, req)
				ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
				env, err := p.PredictBytes(ctx, data, req)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range env.Errors {
					if e.Code == string(errors.PROCESSING_CONFIG) && e.Message == ce.Message {
						return
					}
				}
				t.Fatalf("predict errors %s lack the runtime's %q", fmtEntries(env.Errors), ce.Message)
			})
		}
	}
	// The accepted spellings stay accepted by both.
	for _, params := range []string{`{"component":"week","week_start":"monday"}`, `{"component":"week","week_start":"sunday"}`, `{"component":"hour"}`} {
		req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(),
			Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(params)}}}
		if _, err := p.Process(ctx, req); err != nil {
			t.Fatalf("%s: runtime refused: %v", params, err)
		}
		env, err := p.PredictBytes(ctx, data, req)
		if err != nil || len(env.Errors) != 0 {
			t.Fatalf("%s: predict refused: %v %s", params, err, fmtEntries(env.Errors))
		}
	}
}

func fmtEntries[T any](entries []*T) string {
	out := "["
	for _, e := range entries {
		out += fmt.Sprintf(" %+v", *e)
	}
	return out + " ]"
}

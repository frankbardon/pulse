package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Synth time effects (U52 E1-S2): a trend, a seasonal effect and a
// planted incident window written with the rule functions trend(),
// season() and between() are each RECOVERED by an ordinary Pulse query
// over the generated cohort, on each row's LOCAL clock — the per-row
// zone field that drew the datetime is the query's tz_field.

const timeEffectsSpec = `{
  "row_count": 8000,
  "fields": [
    {"name": "site", "type": "categorical_u8", "distribution": "weighted_categorical",
     "params": {"values": ["Asia/Kolkata", "America/New_York", "Australia/Sydney"]}},
    {"name": "ts", "type": "datetime", "distribution": "uniform_datetime",
     "params": {"start": "2026-01-05", "end": "2026-06-28", "time_zone_field": "site", "dst_policy": "earlier"}},
    {"name": "on_time", "type": "f64", "distribution": "constant", "params": {"value": 0}},
    {"name": "load", "type": "f64", "distribution": "constant", "params": {"value": 0}},
    {"name": "incident", "type": "f64", "distribution": "constant", "params": {"value": 0}}
  ],
  "rules": [
    {"set_expr": {
      "on_time": "0.80 + trend(ts, 0.15 / 175)",
      "load": "season(ts, \"dow\", [1, 2, 3, 4, 5, 6, 7])"}},
    {"when": "between(ts, \"2026-05-12\", \"2026-05-14\")", "set": {"incident": 1}}
  ]
}`

func timeEffectsCohort(t *testing.T) *pulse.Pulse {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := synth.ParseSpec([]byte(timeEffectsSpec))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Synth(context.Background(), spec, "ops.pulse", pulse.SynthOptions{Seed: 52}); err != nil {
		t.Fatal(err)
	}
	return p
}

// localGroupBy runs one aggregation grouped by GROUP_DATE `component` on
// ts read on each row's own zone (tz_field site), returning key → value.
func localGroupBy(t *testing.T, p *pulse.Pulse, component, agg, field string) map[string]float64 {
	t.Helper()
	body := fmt.Sprintf(`{"cohort":{"filename":"ops.pulse"},
		"groups":[{"type":"GROUP_DATE","field":"ts","tz_field":"site","params":{"component":%q}}],
		"aggregations":[{"type":%q,"field":%q,"label":"v"}]}`, component, agg, field)
	var req types.Request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process %s/%s: %v", component, agg, err)
	}
	out := map[string]float64{}
	for _, r := range resp.Data {
		out[fmt.Sprint(r["ts"])] = toFloat(t, r["v"])
	}
	if len(out) == 0 {
		t.Fatalf("%s/%s: empty result", component, agg)
	}
	return out
}

// TestSynthTimeEffects_RecoveredByAQuery: the seasonal weights come back
// EXACTLY per local weekday, the window is exactly the three local days,
// and every local day's on_time range is exactly that day's trend step.
func TestSynthTimeEffects_RecoveredByAQuery(t *testing.T) {
	t.Parallel()
	p := timeEffectsCohort(t)

	// Seasonality: AGG_MIN = AGG_MAX = the declared weight per local
	// weekday, so a reading on any other clock (UTC) would mix weekdays.
	lo := localGroupBy(t, p, "day_of_week", "AGG_MIN", "load")
	hi := localGroupBy(t, p, "day_of_week", "AGG_MAX", "load")
	for i, wd := range []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"} {
		if lo[wd] != float64(i+1) || hi[wd] != float64(i+1) {
			t.Errorf("%s load in [%v, %v], want exactly %d", wd, lo[wd], hi[wd], i+1)
		}
	}

	// The planted window: incident is 1 on exactly the local days
	// 2026-05-12..14 and 0 on every other day.
	days := localGroupBy(t, p, "day", "AGG_AVERAGE", "incident")
	inside := 0
	for day, v := range days {
		want := 0.0
		if day >= "2026-05-12" && day <= "2026-05-14" {
			want = 1
			inside++
		}
		if v != want {
			t.Errorf("local day %s incident average %v, want %v", day, v, want)
		}
	}
	if inside != 3 {
		t.Fatalf("%d window days observed, want 3", inside)
	}

	// The trend: every row on local day k (k days after the local start)
	// has on_time in [0.80 + k·s, 0.80 + (k+1)·s) — the trend steps by
	// local day, on each row's own clock — and the fitted slope over the
	// daily means is s.
	s := 0.15 / 175
	dmin := localGroupBy(t, p, "day", "AGG_MIN", "on_time")
	dmax := localGroupBy(t, p, "day", "AGG_MAX", "on_time")
	dmean := localGroupBy(t, p, "day", "AGG_AVERAGE", "on_time")
	start, _ := time.Parse("2006-01-02", "2026-01-05")
	var sx, sy, sxx, sxy, n float64
	for day := range dmin {
		d, err := time.Parse("2006-01-02", day)
		if err != nil {
			t.Fatal(err)
		}
		k := d.Sub(start).Hours() / 24
		if floor, ceil := 0.80+k*s, 0.80+(k+1)*s; dmin[day] < floor-1e-12 || dmax[day] >= ceil+1e-12 {
			t.Errorf("local day %s on_time in [%v, %v], want within [%v, %v)", day, dmin[day], dmax[day], floor, ceil)
		}
		x := k + 0.5
		sx, sy, sxx, sxy, n = sx+x, sy+dmean[day], sxx+x*x, sxy+x*dmean[day], n+1
	}
	slope := (n*sxy - sx*sy) / (n*sxx - sx*sx)
	if math.Abs(slope-s)/s > 0.02 {
		t.Errorf("fitted daily slope %.6g, want %.6g", slope, s)
	}
	if len(dmin) < 170 {
		t.Errorf("%d local days observed, want the whole range", len(dmin))
	}
}

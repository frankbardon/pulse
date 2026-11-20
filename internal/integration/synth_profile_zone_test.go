package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Local-clock profile capture (U76 E3-S2): a cohort declaring a default
// zone field is profiled per row on it with no option, and the twin
// SpecFromProfile rebuilds carries the source's LOCAL hour and weekday
// support exactly — read back by GROUP_DATE with tz_field.

const zonedEveningSpec = `{
  "row_count": 6000,
  "fields": [
    {"name": "site", "type": "categorical_u8", "distribution": "weighted_categorical",
     "params": {"values": ["America/New_York", "Asia/Kolkata", "Australia/Sydney"]}},
    {"name": "ts", "type": "datetime", "distribution": "uniform_datetime",
     "params": {"start": "2026-01-05", "end": "2026-04-26", "time_zone_field": "site", "dst_policy": "earlier",
       "hour_weights": [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,2,2,1],
       "weekday_weights": [1, 1, 1, 1, 1, 0, 0]}}
  ]
}`

// localSupport returns the sorted keys of GROUP_DATE component on ts,
// read on each row's own zone (tz_field site), over cohort.
func localSupport(t *testing.T, p *pulse.Pulse, cohort, component string) []string {
	t.Helper()
	body := fmt.Sprintf(`{"cohort":{"filename":%q},
		"groups":[{"type":"GROUP_DATE","field":"ts","tz_field":"site","params":{"component":%q}}],
		"aggregations":[{"type":"AGG_COUNT","field":"ts","label":"n"}]}`, cohort, component)
	var req types.Request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process %s %s: %v", cohort, component, err)
	}
	var keys []string
	for _, r := range resp.Data {
		keys = append(keys, fmt.Sprint(r["ts"]))
	}
	sort.Strings(keys)
	return keys
}

// twinFromProfile profiles cohort with opts and writes the rebuilt twin.
func twinFromProfile(t *testing.T, p *pulse.Pulse, cohort, twin string, opts pulse.ProfileOptions) {
	t.Helper()
	prof, err := p.Profile(context.Background(), cohort, opts)
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := synth.SpecFromProfile(prof, 6000)
	if _, err := p.Synth(context.Background(), spec, twin, pulse.SynthOptions{Seed: 77}); err != nil {
		t.Fatalf("synth %s: %v", twin, err)
	}
}

// TestSynthProfileZone_LocalMarginalRecoveredByAQuery: the twin of a
// default-zone cohort profiled with NO option shows exactly the source's
// local hours (20..23) and weekdays (Monday..Friday) per row's zone; a
// UTC capture of the same cohort (explicit option, overriding the
// declared field) does not.
func TestSynthProfileZone_LocalMarginalRecoveredByAQuery(t *testing.T) {
	t.Parallel()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := synth.ParseSpec([]byte(zonedEveningSpec))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Synth(context.Background(), spec, "plain.pulse", pulse.SynthOptions{Seed: 76}); err != nil {
		t.Fatal(err)
	}
	declared := declareZoneField(t, fs, "plain.pulse", "declared.pulse", "site")

	wantHours := localSupport(t, p, "plain.pulse", "hour_of_day")
	wantDays := localSupport(t, p, "plain.pulse", "day_of_week")
	if len(wantHours) != 4 || len(wantDays) != 5 {
		t.Fatalf("source local support hours %v days %v; want 4 hours and 5 weekdays", wantHours, wantDays)
	}

	twinFromProfile(t, p, declared, "twin.pulse", pulse.ProfileOptions{})
	if got := localSupport(t, p, "twin.pulse", "hour_of_day"); fmt.Sprint(got) != fmt.Sprint(wantHours) {
		t.Errorf("twin local hours %v, want exactly the source's %v", got, wantHours)
	}
	if got := localSupport(t, p, "twin.pulse", "day_of_week"); fmt.Sprint(got) != fmt.Sprint(wantDays) {
		t.Errorf("twin local weekdays %v, want exactly the source's %v", got, wantDays)
	}

	twinFromProfile(t, p, declared, "utc_twin.pulse", pulse.ProfileOptions{TimeZone: "UTC"})
	if got := localSupport(t, p, "utc_twin.pulse", "hour_of_day"); fmt.Sprint(got) == fmt.Sprint(wantHours) {
		t.Errorf("a UTC capture reproduced the local hours %v; the test cannot tell the clocks apart", got)
	}
}

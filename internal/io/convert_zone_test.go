package io

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// TestConvertSourceTZ_TargetAndKeptCohortCarryInstants: under a source
// zone convert writes each naive literal as the UTC instant it names,
// passes Z / offset literals and the date column through verbatim, and
// imports the KeepPulseAt intermediate in the same zone — so the kept
// cohort stores the New York instant, not the naive-UTC reading.
func TestConvertSourceTZ_TargetAndKeptCohortCarryInstants(t *testing.T) {
	rows := repeatRows(zoneTuples, 60)
	rep, target, _ := runConvert(t, newMockReader(zoneColumns, rows), true, func(j *ConvertJob) {
		j.SourceTZ = "America/New_York"
		j.DSTPolicy = DSTPolicyLater
	})
	wantLocal := []string{"2026-07-01T16:00:00Z", "2026-01-15T13:00:00Z", "2026-11-01T06:30:00Z"}
	for i, r := range target.rows {
		k := i % 3
		if r[1] != wantLocal[k] || r[3] != wantLocal[k] {
			t.Fatalf("row %d naive cells = %v / %v, want %s", i+1, r[1], r[3], wantLocal[k])
		}
		if r[0] != zoneTuples[k][0] || r[2] != zoneTuples[k][2] {
			t.Fatalf("row %d date / stamped cells moved: %v / %v", i+1, r[0], r[2])
		}
	}
	if len(rep.ZoneWarnings) != 1 || rep.ZoneWarnings[0].Code != errors.PULSE_IMPORT_DST_RESOLVED || rep.ZoneWarnings[0].Details["ambiguous_n"] != 40 {
		t.Fatalf("ZoneWarnings = %v", rep.ZoneWarnings)
	}

	// The kept cohort: re-run on a fresh fs to read it back.
	fs := afero.NewMemMapFs()
	job := NewConvertJob(newMockReader(zoneColumns, rows), &collectWriter{})
	job.FS, job.KeepPulseAt = fs, "kept.pulse"
	job.SourceTZ, job.DSTPolicy = "America/New_York", DSTPolicyLater
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	kept := exportColumn(t, fs, "kept.pulse", 1)
	for i, want := range wantLocal {
		if kept[i] != want {
			t.Fatalf("kept cohort row %d = %v, want %s (stored in the source zone)", i+1, kept[i], want)
		}
	}
}

// TestConvertSourceTZ_DefaultPolicyRefuses: the overlap row stops the
// convert under the default policy, and no intermediate is written.
func TestConvertSourceTZ_DefaultPolicyRefuses(t *testing.T) {
	fs := afero.NewMemMapFs()
	job := NewConvertJob(newMockReader(zoneColumns, repeatRows(zoneTuples, minSampleRows)), &collectWriter{})
	job.FS, job.KeepPulseAt, job.SourceTZ = fs, "kept.pulse", "America/New_York"
	_, err := job.Run(context.Background())
	if ce := codedErr(t, err); ce.Code != errors.PULSE_IMPORT_DST_AMBIGUOUS || ce.Details["row"] != 3 {
		t.Fatalf("err = %s %v", ce.Code, ce.Details)
	}
	if ok, _ := afero.Exists(fs, "kept.pulse"); ok {
		t.Fatal("a refused convert must not write the intermediate")
	}
}

// TestConvertSourceTZ_OptionRefusals: convert judges the options exactly
// as an import does, before the target sees a header.
func TestConvertSourceTZ_OptionRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*ConvertJob)
		code errors.Code
	}{
		{"unknown zone", func(j *ConvertJob) { j.SourceTZ = "Mars/Olympus" }, errors.PULSE_TIMEZONE_UNKNOWN},
		{"date column", func(j *ConvertJob) { j.ColumnSourceTZ = map[string]string{"day": "UTC"} }, errors.SERVICE_VALIDATION},
		{"bad policy", func(j *ConvertJob) { j.DSTPolicy = "sideways" }, errors.SERVICE_VALIDATION},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &collectWriter{}
			job := NewConvertJob(newMockReader(zoneColumns, repeatRows(zoneTuples[:2], minSampleRows)), w)
			job.FS = afero.NewMemMapFs()
			tc.set(job)
			_, err := job.Run(context.Background())
			if ce := codedErr(t, err); ce.Code != tc.code {
				t.Fatalf("code = %s, want %s", ce.Code, tc.code)
			}
			if w.header != nil {
				t.Fatal("refusal reached the target")
			}
		})
	}
}

// TestConvertSourceTZ_PredictAgrees: convert predict raises the refusal
// Run raises and reports the resolved count Run reports.
func TestConvertSourceTZ_PredictAgrees(t *testing.T) {
	rows := repeatRows(zoneTuples, 60)
	job := NewConvertJob(newMockReader(zoneColumns, rows), &collectWriter{})
	job.SourceTZ = "America/New_York"
	if _, err := job.Predict(context.Background()); codedErr(t, err).Code != errors.PULSE_IMPORT_DST_AMBIGUOUS {
		t.Fatalf("predict err = %v", err)
	}
	job = NewConvertJob(newMockReader(zoneColumns, rows), &collectWriter{})
	job.SourceTZ, job.DSTPolicy = "America/New_York", DSTPolicyEarlier
	rep, err := job.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.ZoneWarnings) != 1 || rep.ZoneWarnings[0].Details["ambiguous_n"] != 40 {
		t.Fatalf("predict ZoneWarnings = %v", rep.ZoneWarnings)
	}
}

// TestConvertSourceTZ_NoZoneUnchanged: without a zone (or with UTC) the
// target text and the kept cohort are byte-identical to today's.
func TestConvertSourceTZ_NoZoneUnchanged(t *testing.T) {
	rows := repeatRows(zoneTuples, minSampleRows)
	_, base, baseKept := runConvert(t, newMockReader(zoneColumns, rows), true)
	targetRowsAreSource(t, base, rows)
	_, utc, utcKept := runConvert(t, newMockReader(zoneColumns, rows), true, func(j *ConvertJob) { j.SourceTZ = "UTC" })
	targetRowsAreSource(t, utc, rows)
	if !bytes.Equal(baseKept, utcKept) {
		t.Fatal("a UTC source zone changed the kept cohort")
	}
}

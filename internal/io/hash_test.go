package io

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestExportJob_Hash_NilSafe locks the nil-receiver contract — a nil
// *ExportJob must still produce a 32-char hex hash so callers can use
// the value as a map key without nil-check boilerplate.
func TestExportJob_Hash_NilSafe(t *testing.T) {
	t.Parallel()

	var j *ExportJob
	if got := j.Hash(); len(got) != 32 {
		t.Fatalf("nil ExportJob.Hash() must return 32-char hex, got %q (len=%d)", got, len(got))
	}
}

// TestConvertJob_Hash_NilSafe mirrors TestExportJob_Hash_NilSafe for
// ConvertJob.
func TestConvertJob_Hash_NilSafe(t *testing.T) {
	t.Parallel()

	var j *ConvertJob
	if got := j.Hash(); len(got) != 32 {
		t.Fatalf("nil ConvertJob.Hash() must return 32-char hex, got %q (len=%d)", got, len(got))
	}
}

// TestExportJob_Hash_Deterministic locks the determinism contract —
// two structurally identical ExportJobs must hash identically across
// invocations.
func TestExportJob_Hash_Deterministic(t *testing.T) {
	t.Parallel()

	a := &ExportJob{Source: "a.pulse", Includes: []string{"x", "y"}}
	b := &ExportJob{Source: "a.pulse", Includes: []string{"x", "y"}}
	if a.Hash() != b.Hash() {
		t.Fatalf("structurally identical ExportJobs must share a hash: %q vs %q", a.Hash(), b.Hash())
	}
	if got := a.Hash(); len(got) != 32 {
		t.Fatalf("ExportJob.Hash() must be 32-char hex, got %q (len=%d)", got, len(got))
	}
}

// TestConvertJob_Hash_Deterministic mirrors the ExportJob test.
func TestConvertJob_Hash_Deterministic(t *testing.T) {
	t.Parallel()

	a := &ConvertJob{KeepPulseAt: "out.pulse", Includes: []string{"x"}, SampleRows: 500}
	b := &ConvertJob{KeepPulseAt: "out.pulse", Includes: []string{"x"}, SampleRows: 500}
	if a.Hash() != b.Hash() {
		t.Fatalf("structurally identical ConvertJobs must share a hash: %q vs %q", a.Hash(), b.Hash())
	}
}

// TestExportJob_Hash_NamespaceSeparates locks the cross-type
// namespace rule — an ExportJob and a ConvertJob with overlapping
// shape must never collide.
func TestExportJob_Hash_NamespaceSeparates(t *testing.T) {
	t.Parallel()

	export := &ExportJob{Source: "a.pulse", Includes: []string{"x"}}
	convert := &ConvertJob{Includes: []string{"x"}}
	if export.Hash() == convert.Hash() {
		t.Fatalf("ExportJob and ConvertJob hashes must namespace-separate")
	}
}

// TestExportJob_IncludeOverlays_HashChanges verifies the
// IncludeOverlays slot participates in the hash. Three discriminable
// states — nil (default emit), *true (explicit emit), *false (opt
// out) — must produce three distinct hashes so a cache cannot return
// stale results across the toggle.
func TestExportJob_IncludeOverlays_HashChanges(t *testing.T) {
	t.Parallel()

	trueP := true
	falseP := false

	def := &ExportJob{Source: "a.pulse"}
	on := &ExportJob{Source: "a.pulse", IncludeOverlays: &trueP}
	off := &ExportJob{Source: "a.pulse", IncludeOverlays: &falseP}

	hDef := def.Hash()
	hOn := on.Hash()
	hOff := off.Hash()

	if hDef == hOn {
		t.Errorf("nil IncludeOverlays must hash distinctly from explicit *true: %q vs %q", hDef, hOn)
	}
	if hDef == hOff {
		t.Errorf("nil IncludeOverlays must hash distinctly from explicit *false: %q vs %q", hDef, hOff)
	}
	if hOn == hOff {
		t.Errorf("explicit *true vs *false must produce distinct hashes: %q vs %q", hOn, hOff)
	}
}

// TestConvertJob_IncludeOverlays_HashChanges mirrors the ExportJob
// test for ConvertJob — three states, three distinct hashes.
func TestConvertJob_IncludeOverlays_HashChanges(t *testing.T) {
	t.Parallel()

	trueP := true
	falseP := false

	def := &ConvertJob{KeepPulseAt: "out.pulse"}
	on := &ConvertJob{KeepPulseAt: "out.pulse", IncludeOverlays: &trueP}
	off := &ConvertJob{KeepPulseAt: "out.pulse", IncludeOverlays: &falseP}

	hDef := def.Hash()
	hOn := on.Hash()
	hOff := off.Hash()

	if hDef == hOn {
		t.Errorf("nil IncludeOverlays must hash distinctly from explicit *true: %q vs %q", hDef, hOn)
	}
	if hDef == hOff {
		t.Errorf("nil IncludeOverlays must hash distinctly from explicit *false: %q vs %q", hDef, hOff)
	}
	if hOn == hOff {
		t.Errorf("explicit *true vs *false must produce distinct hashes: %q vs %q", hOn, hOff)
	}
}

func TestExportJob_Hash_IncludeOverlaysNilByteIdentity(t *testing.T) {
	t.Parallel()

	job := &ExportJob{
		Source:   "a.pulse",
		Includes: []string{"x", "y"},
	}
	jobNil := &ExportJob{
		Source:          "a.pulse",
		Includes:        []string{"x", "y"},
		IncludeOverlays: nil,
	}
	if job.Hash() != jobNil.Hash() {
		t.Fatalf("nil-slot ExportJob.Hash() diverged from absent-slot form: %q vs %q", job.Hash(), jobNil.Hash())
	}
}

// TestConvertJob_Hash_IncludeOverlaysNilByteIdentity mirrors the
// ExportJob test for ConvertJob.
func TestConvertJob_Hash_IncludeOverlaysNilByteIdentity(t *testing.T) {
	t.Parallel()

	job := &ConvertJob{
		KeepPulseAt: "out.pulse",
		Includes:    []string{"x"},
	}
	jobNil := &ConvertJob{
		KeepPulseAt:     "out.pulse",
		Includes:        []string{"x"},
		IncludeOverlays: nil,
	}
	if job.Hash() != jobNil.Hash() {
		t.Fatalf("nil-slot ConvertJob.Hash() diverged from absent-slot form: %q vs %q", job.Hash(), jobNil.Hash())
	}
}

// TestExportJob_Hash_LabelsParticipate locks the existing Labels
// slot's participation in the hash — guards against a regression
// where adding IncludeOverlays masks Labels by accident.
func TestExportJob_Hash_LabelsParticipate(t *testing.T) {
	t.Parallel()

	noLabels := &ExportJob{Source: "a.pulse"}
	withLabels := &ExportJob{
		Source: "a.pulse",
		Labels: []*types.LabelBinding{{Field: "x", Table: "t", Mode: types.LabelModeReplace}},
	}
	if noLabels.Hash() == withLabels.Hash() {
		t.Fatalf("Labels must participate in ExportJob.Hash — no-labels vs with-labels collided")
	}
}

// TestExportJob_Hash_TimeZone: the render zone is part of the export's
// identity (different literals), and an empty one leaves the hash of
// every zone-free job exactly as it was.
func TestExportJob_Hash_TimeZone(t *testing.T) {
	t.Parallel()

	plain := &ExportJob{Source: "a.pulse"}
	legacy := types.CanonicalHash("export_job", &struct {
		Source string `json:"source,omitempty"`
	}{Source: "a.pulse"})
	if plain.Hash() != legacy {
		t.Fatalf("zone-free hash moved: %s, want %s", plain.Hash(), legacy)
	}
	zoned := &ExportJob{Source: "a.pulse", TimeZone: "Asia/Kolkata"}
	if zoned.Hash() == plain.Hash() {
		t.Fatal("TimeZone does not participate in ExportJob.Hash")
	}
}

// TestConvertJob_Hash_SourceZoneParticipates: every source-zone option
// changes what a convert writes (the target's datetime text and the kept
// cohort), so each one moves the hash; a zone-free job hashes exactly as
// the pre-zone shape did.
func TestConvertJob_Hash_SourceZoneParticipates(t *testing.T) {
	t.Parallel()

	base := &ConvertJob{KeepPulseAt: "k.pulse", Includes: []string{"x"}}
	legacy := types.CanonicalHash("convert_job", &struct {
		KeepPulseAt string   `json:"keep_pulse_at,omitempty"`
		SampleRows  int      `json:"sample_rows,omitempty"`
		Includes    []string `json:"includes,omitempty"`
	}{KeepPulseAt: "k.pulse", Includes: []string{"x"}})
	if base.Hash() != legacy {
		t.Fatal("a zone-free ConvertJob's hash moved")
	}
	seen := map[string]string{base.Hash(): "none"}
	for name, set := range map[string]func(*ConvertJob){
		"source_tz":        func(j *ConvertJob) { j.SourceTZ = "Europe/Berlin" },
		"column_source_tz": func(j *ConvertJob) { j.ColumnSourceTZ = map[string]string{"t": "Asia/Tokyo"} },
		"source_tz_field":  func(j *ConvertJob) { j.SourceTZField = "zone" },
		"dst_policy":       func(j *ConvertJob) { j.DSTPolicy = DSTPolicyLater },
	} {
		j := &ConvertJob{KeepPulseAt: "k.pulse", Includes: []string{"x"}}
		set(j)
		h := j.Hash()
		if prev, dup := seen[h]; dup {
			t.Errorf("%s hashes like %s", name, prev)
		}
		seen[h] = name
	}
}

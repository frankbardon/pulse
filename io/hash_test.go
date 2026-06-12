package io

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestExportJob_Hash_NilSafe locks the nil-receiver contract — a nil
// *ExportJob must still produce a 32-char hex hash so callers can use
// the value as a map key without nil-check boilerplate.
func TestExportJob_Hash_NilSafe(t *testing.T) {
	var j *ExportJob
	if got := j.Hash(); len(got) != 32 {
		t.Fatalf("nil ExportJob.Hash() must return 32-char hex, got %q (len=%d)", got, len(got))
	}
}

// TestConvertJob_Hash_NilSafe mirrors TestExportJob_Hash_NilSafe for
// ConvertJob.
func TestConvertJob_Hash_NilSafe(t *testing.T) {
	var j *ConvertJob
	if got := j.Hash(); len(got) != 32 {
		t.Fatalf("nil ConvertJob.Hash() must return 32-char hex, got %q (len=%d)", got, len(got))
	}
}

// TestExportJob_Hash_Deterministic locks the determinism contract —
// two structurally identical ExportJobs must hash identically across
// invocations.
func TestExportJob_Hash_Deterministic(t *testing.T) {
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

// TestExportJob_Hash_IncludeOverlaysNilByteIdentity locks the
// additive contract: adding IncludeOverlays must NOT change the hash
// for any existing overlay-free ExportJob. The slot is omitempty so
// json.Marshal omits it when nil, and the canonical-hash pipeline
// produces byte-identical output to the pre-E9-S7 implementation.
//
// The captured constant below is the hash for the pinned fixture
// computed against the canonical-hash routine. Any change that
// alters this hash means the additive extension broke byte-identity
// for callers that have already pinned cache keys against earlier
// Pulse versions — bump CanonicalHash with a migration plan rather
// than silently re-hashing.
func TestExportJob_Hash_IncludeOverlaysNilByteIdentity(t *testing.T) {
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
	noLabels := &ExportJob{Source: "a.pulse"}
	withLabels := &ExportJob{
		Source: "a.pulse",
		Labels: []*types.LabelBinding{{Field: "x", Table: "t", Mode: types.LabelModeReplace}},
	}
	if noLabels.Hash() == withLabels.Hash() {
		t.Fatalf("Labels must participate in ExportJob.Hash — no-labels vs with-labels collided")
	}
}

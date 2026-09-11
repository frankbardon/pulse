package pulse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// TestSynth_FidelityReportRecordsGenerationTimeWarnings closes the gap
// E2-S2 surfaced: the fidelity report — the one document an analyst
// keeps — recorded only the warnings the CALLER could compute before
// generation ran, so a run whose rules retired a captured relationship
// was indistinguishable in it from a run where they retired none.
//
// The two facts asserted here are exactly the two the report could not
// carry, and both exist only AFTER the spec the rules were merged into
// was generated from:
//
//  1. the conflict arbitration over the MERGED spec (a rule's pre-claim
//     retiring a captured categorical-numeric pair), which the
//     caller-supplied channel cannot hold because SpecFromProfile ran
//     before the merge, and
//  2. a rule that applied to no generated row (E2-S3), which cannot
//     exist until the run is over.
//
// The supplied channel is asserted NOT to carry them, so the test fails
// if the report is merely echoing its input.
func TestSynth_FidelityReportRecordsGenerationTimeWarnings(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const rowsPerCell = 500
	importCSVTextFixture(t, p, fs, buildTwoCategoricalNumericConflictCSV(rowsPerCell, 7), "/source.pulse", rowsPerCell*4)

	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	spec, conflictWarnings := synth.SpecFromProfile(prof, 2000)

	// The `--rules` merge happens AFTER SpecFromProfile, exactly as
	// internal/cli's `synth from-profile` does it. Rule 0 determines
	// `score` on every row, so its pre-claim retires the captured
	// categorical-numeric pair targeting it; rule 1's predicate is never
	// true.
	spec.Rules = []synth.RuleSpec{
		{Set: map[string]any{"score": 1.5}},
		{When: "score > 1e9", Set: map[string]any{"score": 2.0}},
	}

	fidelityWarnings := append(append([]string{}, prof.Warnings...), conflictWarnings...)
	for _, w := range fidelityWarnings {
		if strings.Contains(w, "rule 0") || strings.Contains(w, "never fired") {
			t.Fatalf("the pre-merge channel already carries a rule warning (%q); "+
				"the gap this test covers no longer exists as described", w)
		}
	}

	res, err := p.Synth(context.Background(), spec, "/augmented.pulse", SynthOptions{
		Seed:               11,
		SourceCohort:       "/source.pulse",
		FidelityReportPath: "/report.json",
		FidelityWarnings:   fidelityWarnings,
	})
	if err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}

	var sawClaim, sawNeverFired bool
	for _, w := range report.Warnings {
		if strings.Contains(w, "rule 0") && strings.Contains(w, "conditional relationship conflict") {
			sawClaim = true
		}
		if strings.HasPrefix(w, "rule 1 never fired") {
			sawNeverFired = true
		}
	}
	if !sawClaim {
		t.Errorf("report.warnings does not record the relationship the rules retired:\n%s",
			strings.Join(report.Warnings, "\n"))
	}
	if !sawNeverFired {
		t.Errorf("report.warnings does not record the rule that applied to nothing:\n%s",
			strings.Join(report.Warnings, "\n"))
	}

	// Every caller-supplied line survives, and nothing is doubled: the
	// two channels genuinely overlap (both derive conflict lines from
	// resolveConflicts over the same *Spec), and a document that says a
	// conflict twice is wrong in the direction that matters.
	present := make(map[string]int, len(report.Warnings))
	for _, w := range report.Warnings {
		present[w]++
	}
	for _, w := range fidelityWarnings {
		if present[w] == 0 {
			t.Errorf("caller-supplied warning dropped from the report: %q", w)
		}
	}
	for w, n := range present {
		if n > 1 {
			t.Errorf("warning recorded %d times in the report: %q", n, w)
		}
	}

	// res.Warnings is untouched — the fold reads it, never rewrites it.
	if len(res.Warnings) == 0 {
		t.Error("generation raised no warnings; the fixture no longer exercises the fold")
	}
}

// TestMergeFidelityWarnings_PreservesOrderAndCollapsesTheOverlap is the
// unit half: supplied order first, generated lines appended, exact
// duplicates dropped wherever they sit.
func TestMergeFidelityWarnings_PreservesOrderAndCollapsesTheOverlap(t *testing.T) {
	supplied := []string{"capture: thin pair a x b", "conflict: score"}
	generated := []string{"conflict: score", "rule 1 never fired", "rule 1 never fired"}

	got := mergeFidelityWarnings(supplied, generated)
	want := []string{"capture: thin pair a x b", "conflict: score", "rule 1 never fired"}
	if len(got) != len(want) {
		t.Fatalf("merged = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// No generated warnings must leave the supplied slice untouched, so
	// a plain-synthesis report is byte-identical to before.
	if merged := mergeFidelityWarnings(supplied, nil); len(merged) != 2 || merged[0] != supplied[0] {
		t.Fatalf("merge with nothing generated = %v, want the supplied slice unchanged", merged)
	}
}

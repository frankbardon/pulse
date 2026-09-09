package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// buildTwoCategoricalNumericConflictCSV writes CSV text for two
// categorical fields ("cat1", "cat2") and one numeric field ("score"),
// where score genuinely depends on BOTH categorical fields
// independently (score = base + shift1[cat1] + shift2[cat2] + noise) —
// so a `profile create --conditional` capture legitimately captures
// BOTH categorical-numeric pairs (cat1 -> score) and (cat2 -> score),
// not just one as noise. This is the genuine capture-composition
// conflict E6-S2's acceptance bar names: SpecFromProfile can only wire
// ONE of the two into drawRow's fixed categorical-numeric stage (both
// target the same row["score"] write), and resolveConflicts (E6-S1)
// is what decides which one runs and reports the other as dropped.
func buildTwoCategoricalNumericConflictCSV(rowsPerCell int, seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	cats1 := []string{"p", "q"}
	cats2 := []string{"x", "y"}
	shift1 := map[string]float64{"p": 0, "q": 40}
	shift2 := map[string]float64{"x": 0, "y": 10}
	const base = 100.0
	const std = 1.0

	var buf strings.Builder
	buf.WriteString("cat1,cat2,score\n")
	for _, c1 := range cats1 {
		for _, c2 := range cats2 {
			for r := 0; r < rowsPerCell; r++ {
				v := base + shift1[c1] + shift2[c2] + rng.NormFloat64()*std
				fmt.Fprintf(&buf, "%s,%s,%.8f\n", c1, c2, v)
			}
		}
	}
	return buf.String()
}

// TestSynth_FromProfileFidelityReport_NamesConflictingCategoricalNumericPairs
// is E6-S2's non-negotiable end-to-end acceptance bar: a profile with a
// genuine capture-composition conflict — two categorical-numeric pairs
// (cat1 -> score) and (cat2 -> score) both legitimately captured against
// the same numeric field during `--conditional` capture — must, when
// run through the same path `synth from-profile --fidelity-report`
// exercises (SpecFromProfile's new warnings return value merged with
// prof.Warnings into FidelityWarnings, exactly as internal/cli/synth.go
// does), produce a fidelity report whose Warnings section clearly names
// the conflicting field ("score") and both relationships — which one
// was kept and which was dropped. This is a real end-to-end test of the
// plumbing E6-S2 adds (SpecFromProfile -> FidelityWarnings -> the
// written report file), not a unit test of resolveConflicts itself
// (already covered by synth/conflict_test.go, E6-S1).
func TestSynth_FromProfileFidelityReport_NamesConflictingCategoricalNumericPairs(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const rowsPerCell = 500
	csvText := buildTwoCategoricalNumericConflictCSV(rowsPerCell, 7)
	importCSVTextFixture(t, p, fs, csvText, "/source.pulse", rowsPerCell*4)

	// profile create --conditional
	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.CategoricalNumericPairs) != 2 {
		t.Fatalf("expected exactly two captured categorical-numeric pairs (both targeting score), got Conditional=%+v", prof.Conditional)
	}

	// SpecFromProfile now returns the synth-time conflict warnings
	// alongside the composed Spec — this is the plumbing gap E6-S2
	// closes. Note the Spec itself still carries BOTH pairs
	// unpruned: resolveConflicts' actual pruning of which one runs
	// happens again, from scratch, inside generate() at Synth time
	// (synth/writer.go) — SpecFromProfile calling it here is purely to
	// surface the warning early enough to reach FidelityWarnings, not
	// to mutate the Spec it returns.
	spec, conflictWarnings := synth.SpecFromProfile(prof, 5000)
	if len(spec.CategoricalNumericPairs) != 2 {
		t.Fatalf("expected SpecFromProfile to leave both conflicting pairs on the Spec (pruning happens later, in generate()), got %d", len(spec.CategoricalNumericPairs))
	}
	if len(conflictWarnings) == 0 {
		t.Fatal("expected SpecFromProfile to report at least one conflict warning for the two pairs targeting score")
	}

	// The same merge internal/cli's `synth from-profile` action performs:
	// capture-time prof.Warnings first, synth-time conflictWarnings
	// second, into one FidelityWarnings slice.
	fidelityWarnings := append(append([]string{}, prof.Warnings...), conflictWarnings...)

	// synth from-profile --fidelity-report
	res, err := p.Synth(context.Background(), spec, "/augmented.pulse", SynthOptions{
		Seed:               11,
		SourceCohort:       "/source.pulse",
		FidelityReportPath: "/report.json",
		FidelityWarnings:   fidelityWarnings,
	})
	if err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	if res.FidelityReportPath != "/report.json" {
		t.Fatalf("FidelityReportPath = %q, want /report.json", res.FidelityReportPath)
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}

	if len(report.Warnings) == 0 {
		t.Fatal("expected the written fidelity report to carry the conflict warning, got none")
	}

	var conflictMsg string
	for _, w := range report.Warnings {
		if strings.Contains(w, "conditional relationship conflict") {
			conflictMsg = w
			break
		}
	}
	if conflictMsg == "" {
		t.Fatalf("no conflict warning found in report.Warnings: %+v", report.Warnings)
	}

	// The warning must clearly name the conflicting field...
	if !strings.Contains(conflictMsg, `field "score"`) {
		t.Errorf("conflict warning does not name field \"score\": %q", conflictMsg)
	}
	// ...and both relationships — which one was kept (claims it) and
	// which was dropped.
	if !strings.Contains(conflictMsg, "categorical-numeric pair (cat1 -> score)") {
		t.Errorf("conflict warning does not mention the cat1 -> score relationship: %q", conflictMsg)
	}
	if !strings.Contains(conflictMsg, "categorical-numeric pair (cat2 -> score)") {
		t.Errorf("conflict warning does not mention the cat2 -> score relationship: %q", conflictMsg)
	}
	if !strings.Contains(conflictMsg, "already claimed by") || !strings.Contains(conflictMsg, "dropping") {
		t.Errorf("conflict warning does not distinguish kept vs dropped: %q", conflictMsg)
	}

	// The pairwise section itself must reflect the SAME resolution the
	// warning describes: only the surviving relationship (cat1 -> score)
	// gets fidelity-checked. Scoring the dropped (cat2 -> score) pair too
	// would compute a delta against a relationship generate() never
	// applied — misleading, not merely redundant with the warning above.
	if len(report.CategoricalNumericPairwise) != 1 {
		t.Fatalf("expected exactly one surviving categorical-numeric pairwise entry (the conflict-dropped pair must not be scored), got %d: %+v",
			len(report.CategoricalNumericPairwise), report.CategoricalNumericPairwise)
	}
	if got := report.CategoricalNumericPairwise[0].A; got != "cat1" {
		t.Errorf("surviving pairwise entry A = %q, want %q (the relationship the conflict warning says was kept)", got, "cat1")
	}
}

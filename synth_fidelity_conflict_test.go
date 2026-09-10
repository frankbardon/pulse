package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
//
// It is also the shape the multi-predictor effort exists to fix, so the
// fixture now serves TWO tests that must be read as a pair. Under
// `--conditional` alone the arbitration below still runs and still drops
// one genuinely-measured relationship. Under `--conditional
// --fit-models` there is nothing left to arbitrate: both categoricals
// land as predictors on ONE model of `score`, so the conflict never
// arises. Nothing about the data changed between them — only whether
// the capture is able to express two predictors of one numeric at once.
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
//
// This test pins the flag-OFF path and keeps doing so unchanged. Its
// premise — that only one of the two relationships can survive — is a
// true statement about a `--conditional`-only capture and stays true;
// what the multi-predictor effort changes is that the capture no longer
// HAS to be made that way. The `--fit-models` outcome over the identical
// fixture is asserted by the sibling test below, and the two together are
// the before/after: same cohort, same two real relationships, one
// arbitration warning versus none.
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

// TestSynth_FromProfileFidelityReport_FitModelsFoldsBothPredictorsIntoOneModel
// is the sibling of the test above over the IDENTICAL fixture, and the
// end-to-end form of this effort's headline claim.
//
// `buildTwoCategoricalNumericConflictCSV` builds score = base +
// shift1[cat1] + shift2[cat2] + noise, so cat1 and cat2 are both real
// predictors of score and a `--conditional` capture legitimately measures
// both. Above, arbitration keeps one and drops the other, and the dropped
// one's structure is simply absent from the generated cohort. Here
// `--fit-models` fits ONE model carrying both, so:
//
//   - the categorical-numeric arm is not populated for score at all,
//   - resolveConflicts therefore has no competing claim to arbitrate and
//     emits no conditional-relationship-conflict warning,
//   - and the fidelity report's categorical-numeric pairwise section is
//     empty rather than carrying one surviving entry — there is no pair
//     to score, which is a different statement from "one pair was scored
//     and the other dropped" and is why this cannot be folded into the
//     test above as a table row.
//
// The coefficients are checked against the fixture's own shifts because
// "both predictors are present" is not the claim — the claim is that
// both are present AND carry the structure the source data had. A model
// listing cat2 with a coefficient of zero would satisfy a presence check
// while reproducing exactly the cohort the dropped-pair path produces.
//
// Warnings are matched on the "conditional relationship conflict"
// substring rather than by counting the slice: model-drop warnings ride
// the same channel, so a count would be asserting something about
// predictor selection rather than about arbitration.
func TestSynth_FromProfileFidelityReport_FitModelsFoldsBothPredictorsIntoOneModel(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const rowsPerCell = 500
	csvText := buildTwoCategoricalNumericConflictCSV(rowsPerCell, 7)
	importCSVTextFixture(t, p, fs, csvText, "/source.pulse", rowsPerCell*4)

	// profile create --conditional --fit-models. Both flags together is
	// the shape a user actually runs: --conditional still carries every
	// non-numeric-target relationship, which a model says nothing about.
	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{
		IncludeConditional: true,
		FitModels:          true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	// The capture still measures both pairs — the retirement is a
	// translation-time decision, not a capture-time one, and a profile
	// document that quietly stopped measuring them would be a different
	// (worse) change wearing the same test result.
	if prof.Conditional == nil || len(prof.Conditional.CategoricalNumericPairs) != 2 {
		t.Fatalf("expected both categorical-numeric pairs still captured under --fit-models, got Conditional=%+v", prof.Conditional)
	}

	var scoreModel *synth.FieldModel
	for i := range prof.Models {
		if prof.Models[i].Field == "score" {
			scoreModel = &prof.Models[i]
		}
	}
	if scoreModel == nil {
		t.Fatalf("no model captured for score; models = %+v", prof.Models)
	}

	// Both categoricals on ONE model — the whole point.
	coefByField := make(map[string]float64, len(scoreModel.Predictors))
	levelByField := make(map[string]string, len(scoreModel.Predictors))
	for _, pred := range scoreModel.Predictors {
		if pred.Kind != synth.ModelPredictorCategoricalLevel {
			t.Errorf("unexpected predictor kind %q on the score model", pred.Kind)
			continue
		}
		coefByField[pred.Field] = pred.Coefficient
		levelByField[pred.Field] = pred.Level
	}
	for _, want := range []struct {
		field string
		level string
		coef  float64
	}{
		// shift1["q"] - shift1["p"] = 40, shift2["y"] - shift2["x"] = 10,
		// each against the reference level the intercept absorbed.
		{"cat1", "q", 40},
		{"cat2", "y", 10},
	} {
		got, ok := coefByField[want.field]
		if !ok {
			t.Fatalf("%s is not a predictor on the score model; predictors = %+v (this is the conflict the effort exists to remove)",
				want.field, scoreModel.Predictors)
		}
		if levelByField[want.field] != want.level {
			t.Errorf("%s predictor level = %q, want %q", want.field, levelByField[want.field], want.level)
		}
		if math.Abs(got-want.coef) > 1.0 {
			t.Errorf("%s coefficient = %.4f, want ~%.1f (the shift the fixture built in)", want.field, got, want.coef)
		}
	}
	// base = 100, with both reference levels contributing 0.
	if math.Abs(scoreModel.Intercept-100) > 1.0 {
		t.Errorf("model intercept = %.4f, want ~100 (the fixture's base)", scoreModel.Intercept)
	}

	spec, conflictWarnings := synth.SpecFromProfile(prof, 5000)
	if len(spec.Models) != 1 || spec.Models[0].Field != "score" {
		t.Fatalf("spec.Models = %+v, want exactly the score model", spec.Models)
	}
	if n := len(spec.CategoricalNumericPairs); n != 0 {
		t.Fatalf("CategoricalNumericPairs = %d, want 0 — score landed a model, so its captured pairs retire", n)
	}
	for _, w := range conflictWarnings {
		if strings.Contains(w, "conditional relationship conflict") {
			t.Errorf("a conflict was arbitrated for a model-driven target: %q", w)
		}
	}

	fidelityWarnings := append(append([]string{}, prof.Warnings...), conflictWarnings...)

	res, err := p.Synth(context.Background(), spec, "/augmented.pulse", SynthOptions{
		Seed:               11,
		SourceCohort:       "/source.pulse",
		FidelityReportPath: "/report.json",
		FidelityWarnings:   fidelityWarnings,
	})
	if err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "conditional relationship conflict") {
			t.Errorf("generate() arbitrated a conflict SpecFromProfile did not: %q", w)
		}
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	for _, w := range report.Warnings {
		if strings.Contains(w, "conditional relationship conflict") {
			t.Errorf("the written report carries a conflict warning: %q", w)
		}
	}
	// synth_fidelity.go builds every pairwise section from
	// synth.ResolveConflicts(spec), so an empty arm must produce an
	// empty section — not a section scored against a relationship the
	// generator reached through the model instead.
	if n := len(report.CategoricalNumericPairwise); n != 0 {
		t.Errorf("categorical-numeric pairwise section has %d entries, want 0 (the arm retired): %+v",
			n, report.CategoricalNumericPairwise)
	}
}

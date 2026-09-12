package synth_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// E2-S4's committed regression.
//
// The epic's proof was measured against a real 381,324-row survey
// profile, which is 6 MB and cannot be committed. This file pins the
// same coherence properties against a profile of the same SHAPE:
//
//   - a QUESTION BLOCK — a numeric `score` plus three `packed_bool`
//     classification flags that partition it — whose four members share
//     one null rate, because the block was asked or skipped as a unit;
//   - a GATE — a never-null `asked` flag whose 0 rows are exactly the
//     rows two downstream items were not put to;
//   - fitted linear MODELS and residual correlations, so the E2-S2
//     pre-claim has something to retire.
//
// Shape equivalence is the whole point and it is GUARDED, not assumed:
// requireFixtureExercisesEveryCoherenceSlot fails loudly if the capture
// stops populating a slot. E2-S1 found that both checked-in profile
// fixtures predate the `models` section, so a round trip everyone read
// as covering model coefficients was covering an empty slice.
//
// The measured before/after on the real profile is in the PR body; the
// numbers below are this fixture's.

// coherenceFixtureRows is the generated row count every case in this
// file uses. Large enough that the block's ~20% present rate is a
// three-digit count rather than noise, small enough to stay fast.
const coherenceFixtureRows = 4000

// coherenceSeed is fixed so every case compares cohorts drawn from the
// same stream. Changing it changes every count in this file.
const coherenceSeed = 4242

// canonicalCoherenceRules is THE rule set this story recommends, in the
// order it recommends. Three properties are load-bearing and each was
// measured wrong at least once on the way here:
//
//  1. The normalisation rule is GUARDED with `!isnull(score)`. A
//     `set_expr` clears its target's null flag, so the ungated form
//     un-nulls the score on every row and takes the whole block with it
//     — measured on the real profile as 40,000 of 40,000 rows carrying
//     a block that is present on 17.4% of them.
//     TestRulesCoherence_UnguardedNormalisationUnNullsTheBlock pins it.
//
//  2. It normalises with round(), not int(). The writer stores an
//     integer field as floor(v+0.5), so round() is the expression that
//     reproduces the value the FILE will hold; int() truncates, and
//     reaches band agreement by moving the score DOWN to meet the
//     flags rather than by fixing the flags.
//     TestRulesCoherence_RoundNormalisesTheBandIntMovesTheScore pins it.
//
//  3. The band classification and the block share ONE rule. Within a
//     rule `null_together` is the last write, so the flags are computed
//     and then take the score's null decision. Split across two rules
//     in the other order the `set_expr` runs last and un-nulls all
//     three flags on every row the score was missing from.
//     TestRulesCoherence_BlockRuleOrderingIsLoadBearing pins it.
const canonicalCoherenceRules = `[
  {"when": "!isnull(score)", "set_expr": {"score": "round(score)"}},
  {"set_expr":       {"hi": "score >= 9", "mid": "score >= 7 && score < 9", "lo": "score < 7"},
   "null_together":  ["score", "hi", "mid", "lo"]},
  {"when": "asked == 0", "set_null": ["q1", "q2"]}
]`

// gateOnlyCoherenceRules isolates the gate so its effect can be compared
// against an unruled baseline drawn from the identical RNG stream.
// set_null never claims (E2-S2), so no model is retired and the stream
// does not move — which is exactly what makes `if gate then null else
// inferred` assertable cell for cell.
const gateOnlyCoherenceRules = `[{"when": "asked == 0", "set_null": ["q1", "q2"]}]`

// coherenceBlock and coherenceGate name the fixture's two structures.
var (
	coherenceFlags       = []string{"hi", "mid", "lo"}
	coherenceBlock       = []string{"score", "hi", "mid", "lo"}
	coherenceGateTargets = []string{"q1", "q2"}
)

// coherenceFixtureProfile builds a cohort with the motivating profile's
// SHAPE and captures it the way `profile create --conditional
// --fit-models --residual-correlations` does, then hands the document
// back through a JSON round trip because that is how `synth
// from-profile` receives one.
//
// It is generated rather than checked in for the reason E2-S1 gave: a
// static document freezes whatever the fitter produced on the day it was
// written, and both checked-in fixtures predate the models section
// entirely. The categorical->numeric structure is imposed with
// CategoricalNumericPairs so the between-group variance is real —
// independent marginals sit under synth's variance-explained floor and
// select no predictors at all, leaving the slot as untested as a missing
// one.
func coherenceFixtureProfile(t *testing.T) *synth.Profile {
	t.Helper()
	byRegion := func(mean, std float64) []synth.CategoricalNumericCategorySpec {
		return []synth.CategoricalNumericCategorySpec{
			{Category: "east", Mean: mean, Std: std},
			{Category: "west", Mean: mean + 2.2, Std: std},
			{Category: "north", Mean: mean - 1.8, Std: std},
		}
	}
	src := &synth.Spec{
		RowCount: 6000,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west", "north"}, "weights": []any{3.0, 2.0, 2.0}}},
			{Name: "tier", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"gold", "silver"}, "weights": []any{1.0, 1.0}}},
			// The gate. Never null, so `asked == 0` is exact on the
			// wire — a packed_bool row value is exactly 1.0 or 0.0 and
			// carries none of the pre-rounding gotcha the score does.
			{Name: "asked", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.75}},
			// The question block: one score and the three flags that
			// partition it, all four sharing one null rate.
			{Name: "score", Type: "u4", Nullable: true, NullRate: 0.80, Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 7.5, "std": 2.6, "min": 0.0, "max": 10.0}},
			{Name: "hi", Type: "packed_bool", Nullable: true, NullRate: 0.80,
				Distribution: synth.DistBernoulli, Params: map[string]any{"p": 0.47}},
			{Name: "mid", Type: "packed_bool", Nullable: true, NullRate: 0.80,
				Distribution: synth.DistBernoulli, Params: map[string]any{"p": 0.25}},
			{Name: "lo", Type: "packed_bool", Nullable: true, NullRate: 0.80,
				Distribution: synth.DistBernoulli, Params: map[string]any{"p": 0.28}},
			// The gate's targets.
			{Name: "q1", Type: "u4", Nullable: true, NullRate: 0.25, Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 4.0, "std": 1.5, "min": 1.0, "max": 7.0}},
			{Name: "q2", Type: "u4", Nullable: true, NullRate: 0.25, Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 4.0, "std": 1.5, "min": 1.0, "max": 7.0}},
		},
		CategoricalNumericPairs: []synth.CategoricalNumericPairSpec{
			{A: "region", B: "score", Categories: byRegion(7.5, 2.6)},
			{A: "region", B: "q1", Categories: byRegion(4.0, 1.5)},
			{A: "tier", B: "q2", Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "gold", Mean: 5.2, Std: 1.3}, {Category: "silver", Mean: 3.0, Std: 1.3}}},
			// Gives `hi` real between-group variance, so the fitter
			// gives it a model and the E2-S2 pre-claim has a model to
			// retire.
			{A: "region", B: "hi", Bernoulli: true, Categories: []synth.CategoricalNumericCategorySpec{
				{Category: "east", Mean: 0.30}, {Category: "west", Mean: 0.70}, {Category: "north", Mean: 0.50}}},
		},
	}
	data, _, err := synth.SynthBytes(src, synth.Options{Seed: 99})
	if err != nil {
		t.Fatalf("SynthBytes(coherence fixture cohort): %v", err)
	}
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{
		TopK:                    8,
		IncludeStats:            true,
		IncludeConditional:      true,
		FitModels:               true,
		FitResidualCorrelations: true,
		Seed:                    1,
	})
	if err != nil {
		t.Fatalf("ProfileBytes(coherence fixture): %v", err)
	}
	raw, err := json.Marshal(prof)
	if err != nil {
		t.Fatalf("json.Marshal(profile): %v", err)
	}
	var decoded synth.Profile
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(profile): %v", err)
	}
	return &decoded
}

// requireFixtureExercisesEveryCoherenceSlot is the guard E2-S1 asked for:
// a fixture that quietly stops populating a slot stops testing it, and
// the test keeps passing while reading as coverage. Every check below
// corresponds to one property the real profile has and one assertion in
// this file that is vacuous without it.
func requireFixtureExercisesEveryCoherenceSlot(t *testing.T, prof *synth.Profile, spec *synth.Spec) {
	t.Helper()

	rateOf := func(name string) float64 {
		for _, f := range prof.Fields {
			if f.Name == name {
				return f.NullRate
			}
		}
		t.Fatalf("fixture profile no longer carries field %q", name)
		return 0
	}
	// The block: four fields sharing one materially non-zero null rate.
	// Without this there is no co-missingness for null_together to
	// repair and every block assertion below is trivially satisfiable.
	base := rateOf("score")
	if base < 0.5 || base > 0.95 {
		t.Fatalf("block null rate is %.4f; the fixture needs a block that is mostly ABSENT "+
			"(a near-zero rate makes 'present together' true by default)", base)
	}
	for _, f := range coherenceBlock[1:] {
		if math.Abs(rateOf(f)-base) > 0.05 {
			t.Fatalf("block member %q has null rate %.4f against the score's %.4f; the fixture no "+
				"longer captures a shared-null-rate question block", f, rateOf(f), base)
		}
	}
	// The gate: never null itself, with targets that ARE nullable.
	if r := rateOf("asked"); r != 0 {
		t.Fatalf("gate field `asked` has null rate %.4f, want 0 — a nullable gate would make "+
			"`asked == 0` read a drawn value behind a null flag", r)
	}
	for _, f := range coherenceGateTargets {
		if rateOf(f) <= 0 {
			t.Fatalf("gate target %q is not nullable in the fixture; set_null cannot be observed on it", f)
		}
	}
	// The models: the pre-claim needs something to retire, and a
	// zero-predictor model retires nothing visible.
	if len(spec.Models) == 0 {
		t.Fatal("fixture produced no models; the E2-S2 pre-claim is untested")
	}
	flagModel := false
	for _, m := range spec.Models {
		for _, f := range coherenceFlags {
			if m.Field == f && len(m.Predictors) > 0 {
				flagModel = true
			}
		}
	}
	if !flagModel {
		t.Fatalf("no classification flag carries a predictor-bearing model (models: %s); the "+
			"band rule would retire nothing", modelFieldNames(spec))
	}
	if len(spec.ResidualCorrelations) == 0 {
		t.Fatal("fixture produced no residual correlations; the claim's third retirement is untested")
	}
	if len(spec.CategoricalNumericPairs) == 0 {
		t.Fatal("fixture produced no conditional pairs; the claim's second retirement is untested")
	}
}

func modelFieldNames(spec *synth.Spec) string {
	names := make([]string, 0, len(spec.Models))
	for _, m := range spec.Models {
		names = append(names, fmt.Sprintf("%s(%d)", m.Field, len(m.Predictors)))
	}
	return strings.Join(names, " ")
}

// coherenceRow is one decoded record: values plus the null bitmap, which
// is the half every assertion here turns on.
type coherenceRow struct {
	v map[string]float64
	n map[string]bool
}

func decodeCoherenceRows(t *testing.T, data []byte) []coherenceRow {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	rr := encoding.NewRecordReader(r, schema)
	out := make([]coherenceRow, 0, coherenceFixtureRows)
	for {
		v := make(map[string]float64)
		n := make(map[string]bool)
		if err := rr.ReadRecord(v, n); err != nil {
			break
		}
		out = append(out, coherenceRow{v, n})
	}
	return out
}

// coherenceTable is the measured table this story reports, one column
// per row of it.
type coherenceTable struct {
	rows          int
	allPresent    int // all four block fields present together
	allNull       int // all four absent together
	scoreFlagNull int // score present, a flag null
	nullFlagSet   int // score null, a flag present
	exactlyOne    int // exactly one flag set, over rows carrying all three
	bandAgrees    int // and it agrees with the score's band
	gated         int // asked == 0
	gatedAllNull  int // ... with every gate target nulled
}

func measureCoherence(rows []coherenceRow) coherenceTable {
	c := coherenceTable{rows: len(rows)}
	for _, r := range rows {
		scoreNull := r.n["score"]
		flagNulls := 0
		for _, f := range coherenceFlags {
			if r.n[f] {
				flagNulls++
			}
		}
		switch {
		case !scoreNull && flagNulls == 0:
			c.allPresent++
		case scoreNull && flagNulls == 3:
			c.allNull++
		}
		if !scoreNull && flagNulls > 0 {
			c.scoreFlagNull++
		}
		if scoreNull && flagNulls < 3 {
			c.nullFlagSet++
		}
		if flagNulls == 0 {
			set, which := 0, ""
			for _, f := range coherenceFlags {
				if r.v[f] != 0 {
					set++
					which = f
				}
			}
			if set == 1 {
				c.exactlyOne++
				if !scoreNull && which == coherenceBand(r.v["score"]) {
					c.bandAgrees++
				}
			}
		}
		if r.v["asked"] == 0 {
			c.gated++
			allNull := true
			for _, f := range coherenceGateTargets {
				if !r.n[f] {
					allNull = false
				}
			}
			if allNull {
				c.gatedAllNull++
			}
		}
	}
	return c
}

// coherenceBand is the band the rule set encodes, restated here so the
// assertion is independent of the rule string rather than a tautology
// over it.
func coherenceBand(score float64) string {
	switch {
	case score >= 9:
		return "hi"
	case score >= 7:
		return "mid"
	}
	return "lo"
}

// generateCoherence derives a spec from the fixture profile, applies the
// supplied rules THROUGH E2-S1's real loader (a bare JSON array on an
// afero filesystem, exactly as `--rules` reads one) and generates.
func generateCoherence(t *testing.T, prof *synth.Profile, rules string) ([]coherenceRow, *synth.Spec, []string) {
	t.Helper()
	spec, _ := synth.SpecFromProfile(prof, coherenceFixtureRows)
	if rules != "" {
		memfs := fs.NewMemMap().Fs()
		const path = "/rules.json"
		if err := afero.WriteFile(memfs, path, []byte(rules), 0o644); err != nil {
			t.Fatalf("WriteFile(rules): %v", err)
		}
		if err := synth.ApplyRulesFile(memfs, spec, path); err != nil {
			t.Fatalf("ApplyRulesFile: %v", err)
		}
	}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: coherenceSeed})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	rows := decodeCoherenceRows(t, data)
	if len(rows) != coherenceFixtureRows {
		t.Fatalf("decoded %d rows, want %d", len(rows), coherenceFixtureRows)
	}
	return rows, spec, res.Warnings
}

// withoutCapturedHistogram strips one field's captured per-level
// histogram from a profile, so SpecFromProfile falls back to the
// clamped-normal reconstruction for it — the shape every document
// captured before FU-03 has, and the shape a too-wide integer column
// still gets.
func withoutCapturedHistogram(prof *synth.Profile, field string) *synth.Profile {
	for i := range prof.Fields {
		if prof.Fields[i].Name == field && prof.Fields[i].Numeric != nil {
			prof.Fields[i].Numeric.Discrete = nil
		}
	}
	return prof
}

// TestRulesCoherence_DiscreteScoreNeedsNoPreRoundingNormalisation is the
// other half of the round-vs-int measurement below, and the reason that
// one now has to construct its own gotcha.
//
// `score` is a u4, so it reconstructs from its own captured histogram and
// its row value IS the value the writer stores. A band read straight off
// the row therefore agrees with the file on every row with NO
// normalisation rule at all — which is the pre-rounding gotcha being
// absent rather than worked around.
//
// Both halves are asserted together on purpose: "no normalisation needed"
// is trivially satisfiable by a fixture whose block is never present, so
// the test also requires the block to be present on a real share of rows.
func TestRulesCoherence_DiscreteScoreNeedsNoPreRoundingNormalisation(t *testing.T) {
	prof := coherenceFixtureProfile(t)

	spec, _ := synth.SpecFromProfile(prof, coherenceFixtureRows)
	var scoreDist string
	for _, f := range spec.Fields {
		if f.Name == "score" {
			scoreDist = f.Distribution
		}
	}
	if scoreDist != "discrete" {
		t.Fatalf("score reconstructed as %q, want \"discrete\" — the fixture is not exercising "+
			"the integer-histogram arm and this test says nothing", scoreDist)
	}

	unNormalised := strings.Replace(canonicalCoherenceRules,
		`  {"when": "!isnull(score)", "set_expr": {"score": "round(score)"}},`+"\n", "", 1)
	rows, _, _ := generateCoherence(t, prof, unNormalised)
	m := measureCoherence(rows)
	t.Logf("discrete score, no normalisation: agrees=%d of %d block-present row(s)",
		m.bandAgrees, m.allPresent)

	if m.allPresent < coherenceFixtureRows/10 {
		t.Fatalf("the block is present on only %d of %d rows; with so few scored rows the "+
			"agreement figure below says nothing", m.allPresent, m.rows)
	}
	if m.bandAgrees != m.allPresent {
		t.Errorf("%d of %d block-present row(s) disagree with their band with no normalisation "+
			"rule; a discrete reconstruction emits the stored integer, so there is no "+
			"pre-rounding gap for a round() rule to close",
			m.allPresent-m.bandAgrees, m.allPresent)
	}
}

// TestRulesCoherence_ProfileDerivedBlockBecomesCoherent is the epic's
// proof, reduced to a fixture of the same shape.
//
// BOTH halves are asserted in ONE test on purpose. "The ruled cohort is
// coherent" is trivially satisfiable by a fixture that was already
// coherent — which is precisely what a narrower fixture would be, since
// the defect only exists when a block's members null independently. So
// the unruled baseline must be measured and asserted BROKEN first, and
// the same four counters are then asserted repaired.
func TestRulesCoherence_ProfileDerivedBlockBecomesCoherent(t *testing.T) {
	prof := coherenceFixtureProfile(t)

	baseRows, baseSpec, _ := generateCoherence(t, prof, "")
	requireFixtureExercisesEveryCoherenceSlot(t, prof, baseSpec)
	base := measureCoherence(baseRows)

	// The BEFORE. Independent nulling makes all-four-present p^4 rather
	// than p, so the block is present on a fraction of the rows it
	// should be, and a coherent classification is rarer still.
	expectedPresent := int(float64(coherenceFixtureRows) * 0.20)
	if base.allPresent > expectedPresent/4 {
		t.Fatalf("baseline already carries the block together on %d of %d rows (~%d expected under "+
			"the captured rate); the fixture is not exercising independent nulling and every "+
			"assertion below is vacuous", base.allPresent, base.rows, expectedPresent)
	}
	if base.scoreFlagNull == 0 && base.nullFlagSet == 0 {
		t.Fatal("baseline has no partial blocks at all; the fixture is not exercising the defect")
	}
	if base.bandAgrees > base.allPresent {
		t.Fatalf("baseline band agreement (%d) exceeds its all-present count (%d) — measurement bug",
			base.bandAgrees, base.allPresent)
	}
	t.Logf("BEFORE rows=%d allPresent=%d allNull=%d scorePresentFlagNull=%d scoreNullFlagPresent=%d "+
		"exactlyOneFlag=%d bandAgrees=%d",
		base.rows, base.allPresent, base.allNull, base.scoreFlagNull, base.nullFlagSet,
		base.exactlyOne, base.bandAgrees)

	// The AFTER.
	ruledRows, _, _ := generateCoherence(t, prof, canonicalCoherenceRules)
	ruled := measureCoherence(ruledRows)
	t.Logf("AFTER  rows=%d allPresent=%d allNull=%d scorePresentFlagNull=%d scoreNullFlagPresent=%d "+
		"exactlyOneFlag=%d bandAgrees=%d",
		ruled.rows, ruled.allPresent, ruled.allNull, ruled.scoreFlagNull, ruled.nullFlagSet,
		ruled.exactlyOne, ruled.bandAgrees)

	if ruled.scoreFlagNull != 0 {
		t.Errorf("%d row(s) carry a score with a null classification flag, want 0", ruled.scoreFlagNull)
	}
	if ruled.nullFlagSet != 0 {
		t.Errorf("%d row(s) carry a classification flag with no score, want 0", ruled.nullFlagSet)
	}
	if ruled.allPresent+ruled.allNull != ruled.rows {
		t.Errorf("%d row(s) carry a partial block, want 0 (present=%d absent=%d of %d)",
			ruled.rows-ruled.allPresent-ruled.allNull, ruled.allPresent, ruled.allNull, ruled.rows)
	}
	// The block must be present at the CAPTURED rate, not merely
	// present together: nulling the whole block on every row satisfies
	// every assertion above.
	if ruled.allPresent < expectedPresent*3/4 || ruled.allPresent > expectedPresent*5/4 {
		t.Errorf("block present on %d of %d rows; the captured null rate implies ~%d",
			ruled.allPresent, ruled.rows, expectedPresent)
	}
	if ruled.exactlyOne != ruled.allPresent {
		t.Errorf("%d of %d block-present rows set exactly one classification flag, want all",
			ruled.exactlyOne, ruled.allPresent)
	}
	if ruled.bandAgrees != ruled.allPresent {
		t.Errorf("%d of %d block-present rows agree with the score's band, want all "+
			"(a shortfall here is the pre-rounding gotcha: the flag was computed off the row's "+
			"float and the file holds floor(v+0.5))", ruled.bandAgrees, ruled.allPresent)
	}
	if ruled.allPresent <= base.allPresent || ruled.bandAgrees <= base.bandAgrees {
		t.Errorf("the rules did not improve on the baseline (present %d -> %d, agrees %d -> %d)",
			base.allPresent, ruled.allPresent, base.bandAgrees, ruled.bandAgrees)
	}
}

// TestRulesCoherence_GateNullsGatedRowsAndLeavesInferredValues is the
// `if gate then null else inferred` property, and again both halves ride
// in one test: a rule that nulled EVERY row satisfies the first half
// perfectly.
//
// The non-gated half is asserted cell for cell against an unruled
// baseline drawn from the identical seed, which is only meaningful
// because set_null never claims (E2-S2) — no model is retired, so the
// RNG stream does not move and any difference is the rule's doing.
func TestRulesCoherence_GateNullsGatedRowsAndLeavesInferredValues(t *testing.T) {
	prof := coherenceFixtureProfile(t)
	baseRows, baseSpec, _ := generateCoherence(t, prof, "")
	requireFixtureExercisesEveryCoherenceSlot(t, prof, baseSpec)
	gateRows, _, _ := generateCoherence(t, prof, gateOnlyCoherenceRules)

	var gatedCells, gatedNulled, gatedPresentInBaseline int
	var keptCells, valueDiff, nullDiff int
	for i := range baseRows {
		gated := baseRows[i].v["asked"] == 0
		for _, f := range coherenceGateTargets {
			if gated {
				gatedCells++
				if gateRows[i].n[f] {
					gatedNulled++
				}
				if !baseRows[i].n[f] {
					gatedPresentInBaseline++
				}
				continue
			}
			keptCells++
			if baseRows[i].v[f] != gateRows[i].v[f] {
				valueDiff++
			}
			if baseRows[i].n[f] != gateRows[i].n[f] {
				nullDiff++
			}
		}
	}
	t.Logf("gated cells=%d nulled=%d (were present in baseline: %d) | non-gated cells=%d "+
		"value-diff=%d null-diff=%d", gatedCells, gatedNulled, gatedPresentInBaseline,
		keptCells, valueDiff, nullDiff)

	if gatedCells == 0 || keptCells == 0 {
		t.Fatalf("degenerate split: %d gated and %d non-gated cells", gatedCells, keptCells)
	}
	if gatedNulled != gatedCells {
		t.Errorf("the gate left %d of %d gated cells un-nulled", gatedCells-gatedNulled, gatedCells)
	}
	// Without this the first assertion is satisfied by a baseline that
	// had already nulled everything.
	if gatedPresentInBaseline == 0 {
		t.Error("no gated cell carried an inferred value in the baseline; the rule removed nothing")
	}
	if valueDiff != 0 || nullDiff != 0 {
		t.Errorf("the gate disturbed %d value(s) and %d null flag(s) on rows it does not gate, want 0 "+
			"— `if gate then null else inferred` means the field's own model still produces the "+
			"non-gated rows", valueDiff, nullDiff)
	}
}

// TestRulesCoherence_BandRuleRetiresTheFlagsCapturedRelationships
// confirms E2-S2's pre-claim on a PROFILE-DERIVED spec rather than a
// hand-built one: the flags the band rule determines lose their captured
// model, their conditional pair and their residual correlation, while
// the score — whose only rule reads its own target — keeps all three.
//
// Both halves in one test: a claim loop that claimed everything would
// satisfy the first half and fail the second.
func TestRulesCoherence_BandRuleRetiresTheFlagsCapturedRelationships(t *testing.T) {
	prof := coherenceFixtureProfile(t)
	_, spec, warnings := generateCoherence(t, prof, canonicalCoherenceRules)
	requireFixtureExercisesEveryCoherenceSlot(t, prof, spec)

	// resolveConflicts is a pure function of the Spec, so asking it
	// again reproduces exactly what generation used.
	_, catNum, _, _, _, _ := synth.ResolveConflicts(spec)
	survivingPair := map[string]bool{}
	for _, p := range catNum {
		survivingPair[p.B] = true
	}
	for _, f := range coherenceFlags {
		if survivingPair[f] {
			t.Errorf("conditional pair targeting %q survived, but an unconditional set_expr "+
				"overwrites it on every row", f)
		}
	}

	claimed := func(field string) bool {
		for _, w := range warnings {
			if strings.Contains(w, fmt.Sprintf("field %q is already claimed by structural rule", field)) {
				return true
			}
		}
		return false
	}
	retiredAModel := false
	for _, w := range warnings {
		if strings.Contains(w, "is already claimed by structural rule") &&
			strings.Contains(w, "dropping linear model") {
			retiredAModel = true
		}
	}
	if !retiredAModel {
		t.Errorf("no captured linear model was retired by the band rule; warnings: %v", warnings)
	}
	// The score's only rule reads its own target, so it must keep
	// everything. This is the half that fails if the self-reference
	// exclusion is removed.
	if claimed("score") {
		t.Errorf("`score` was claimed, but its only rule is a self-referential normalisation that "+
			"TRANSFORMS the drawn value rather than determining it; warnings: %v", warnings)
	}
	scoreModelled := false
	for _, m := range spec.Models {
		if m.Field == "score" {
			scoreModelled = true
		}
	}
	if !scoreModelled {
		t.Log("fixture did not fit a model for `score`; the keeps-its-model half is weaker here")
	}
}

// TestRulesCoherence_EveryCanonicalRuleFires covers E2-S4's "every rule
// reports a non-zero firing count" criterion.
//
// The count itself has no external surface — it is reachable only from
// package synth, and E2-S3 deliberately kept it off synth.Result because
// that struct is the --json payload. The externally visible equivalent
// is the ABSENCE of a `rule never fired` line, and an absence assertion
// is worth nothing unless the same test proves the line can appear. So
// the dead-rule positive control rides here rather than in its own case.
func TestRulesCoherence_EveryCanonicalRuleFires(t *testing.T) {
	prof := coherenceFixtureProfile(t)

	_, _, warnings := generateCoherence(t, prof, canonicalCoherenceRules)
	for _, w := range warnings {
		if strings.Contains(w, "never fired") {
			t.Errorf("a canonical rule applied to no generated row: %s", w)
		}
	}

	// Positive control: `asked` is a packed_bool, so 7 is unreachable.
	// If this does not warn, the assertion above is measuring nothing.
	const dead = `[
      {"when": "!isnull(score)", "set_expr": {"score": "round(score)"}},
      {"when": "asked == 7", "set_null": ["q1", "q2"]}
    ]`
	_, _, deadWarnings := generateCoherence(t, prof, dead)
	found := false
	for _, w := range deadWarnings {
		if strings.Contains(w, "never fired") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a rule that cannot fire produced no `never fired` warning, so the absence "+
			"assertion above proves nothing; warnings: %v", deadWarnings)
	}
}

// TestRulesCoherence_UnguardedNormalisationUnNullsTheBlock pins the
// silent fault the `!isnull(score)` guard exists for.
//
// A set_expr writes the value and CLEARS the null flag — deliberately,
// since a rule stating a field's value is stating the field HAS one. On
// a NULLABLE target that makes an ungated normalisation rule un-null
// every row, and because null_together then copies the score's decision
// the whole block goes with it. Nothing refuses it: the file generates
// cleanly with a question block that is suddenly universal.
func TestRulesCoherence_UnguardedNormalisationUnNullsTheBlock(t *testing.T) {
	prof := coherenceFixtureProfile(t)

	guarded, _, _ := generateCoherence(t, prof, canonicalCoherenceRules)
	unguarded, _, _ := generateCoherence(t, prof, strings.Replace(canonicalCoherenceRules,
		`{"when": "!isnull(score)", "set_expr": {"score": "round(score)"}}`,
		`{"set_expr": {"score": "round(score)"}}`, 1))

	g := measureCoherence(guarded)
	u := measureCoherence(unguarded)
	t.Logf("guarded allPresent=%d of %d | unguarded allPresent=%d of %d",
		g.allPresent, g.rows, u.allPresent, u.rows)

	if u.allPresent != u.rows {
		t.Errorf("the unguarded normalisation left %d row(s) with the block absent; the documented "+
			"fault is that it un-nulls ALL of them", u.rows-u.allPresent)
	}
	if g.allPresent >= g.rows {
		t.Errorf("the guarded form also un-nulled the block (%d of %d present) — the "+
			"`!isnull` guard is not doing its job", g.allPresent, g.rows)
	}
}

// TestRulesCoherence_RoundNormalisesTheBandIntMovesTheScore is the
// measurement behind this story's documentation change.
//
// Both int() and round() reach 100% band agreement, and that is exactly
// why the choice looks free. It is not. An integer field is stored as
// floor(v+0.5), so:
//
//   - round(v) reproduces the value the FILE will hold. The score column
//     comes out byte-identical to the un-normalised run and the FLAGS
//     move onto the right side of the band edges.
//   - int(v) truncates. The flags do not move at all — band(v) and
//     band(floor(v)) are equal for integer thresholds — and the SCORE
//     moves down instead, so agreement is bought by corrupting the
//     marginal the profile captured.
func TestRulesCoherence_RoundNormalisesTheBandIntMovesTheScore(t *testing.T) {
	// The gotcha this test measures is a property of a CONTINUOUS
	// reconstruction: the row holds the sampler's float while the wire
	// holds floor(v+0.5), so a band read off the row disagrees with the
	// band a reader computes from the file.
	//
	// Since FU-03 an integer column with at most maxDiscreteLevels
	// observed values reconstructs as `discrete`, whose sampler emits the
	// stored integer exactly, and the gotcha is GONE for it — see
	// TestRulesCoherence_DiscreteScoreNeedsNoPreRoundingNormalisation,
	// which asserts that directly on the unmodified fixture. So the
	// gotcha now has to be CONSTRUCTED here, by dropping `score`'s
	// captured histogram and letting SpecFromProfile fall back to the
	// clamped normal exactly as it does for every pre-FU-03 document.
	//
	// The advice this test pins is still live, for the three shapes that
	// still reconstruct continuously: an f32/f64 field, an integer column
	// too wide for the cap, and a hand-authored spec putting a continuous
	// distribution on an integer field.
	prof := withoutCapturedHistogram(coherenceFixtureProfile(t), "score")
	withNorm := func(expr string) string {
		if expr == "" {
			return strings.Replace(canonicalCoherenceRules,
				`  {"when": "!isnull(score)", "set_expr": {"score": "round(score)"}},`+"\n", "", 1)
		}
		return strings.Replace(canonicalCoherenceRules, `"round(score)"`, `"`+expr+`(score)"`, 1)
	}

	plainRows, _, _ := generateCoherence(t, prof, withNorm(""))
	roundRows, _, _ := generateCoherence(t, prof, withNorm("round"))
	intRows, _, _ := generateCoherence(t, prof, withNorm("int"))

	plain := measureCoherence(plainRows)
	rounded := measureCoherence(roundRows)
	truncated := measureCoherence(intRows)
	t.Logf("plain agrees=%d/%d | round agrees=%d/%d | int agrees=%d/%d",
		plain.bandAgrees, plain.allPresent, rounded.bandAgrees, rounded.allPresent,
		truncated.bandAgrees, truncated.allPresent)

	if plain.bandAgrees >= plain.allPresent {
		t.Fatalf("the un-normalised run already agrees on every row (%d of %d); this fixture is not "+
			"exercising the pre-rounding gotcha and the comparison below is vacuous",
			plain.bandAgrees, plain.allPresent)
	}
	if rounded.bandAgrees != rounded.allPresent {
		t.Errorf("round() left %d of %d block-present rows disagreeing with their band",
			rounded.allPresent-rounded.bandAgrees, rounded.allPresent)
	}
	if truncated.bandAgrees != truncated.allPresent {
		t.Errorf("int() left %d of %d block-present rows disagreeing with their band",
			truncated.allPresent-truncated.bandAgrees, truncated.allPresent)
	}

	// The distinguishing measurement: WHICH column each normalisation
	// moved relative to the un-normalised run.
	scoreMovedByRound, flagsMovedByRound := columnsMoved(plainRows, roundRows)
	scoreMovedByInt, flagsMovedByInt := columnsMoved(plainRows, intRows)
	t.Logf("vs un-normalised: round moved score on %d row(s), flags on %d | int moved score on "+
		"%d row(s), flags on %d", scoreMovedByRound, flagsMovedByRound, scoreMovedByInt, flagsMovedByInt)

	if scoreMovedByRound != 0 {
		t.Errorf("round() moved the stored score on %d row(s); it must reproduce floor(v+0.5), "+
			"which is the value the writer already stores", scoreMovedByRound)
	}
	if flagsMovedByRound == 0 {
		t.Error("round() moved no classification flag, so it cannot have repaired any disagreement")
	}
	if scoreMovedByInt == 0 {
		t.Error("int() moved no score; the documented cost of truncation is not being exercised")
	}
	if flagsMovedByInt != 0 {
		t.Errorf("int() moved %d flag value(s); band(v) and band(floor(v)) are equal for integer "+
			"thresholds, so truncation buys agreement by moving the SCORE", flagsMovedByInt)
	}
}

// columnsMoved counts the rows on which the score, and on which any
// classification flag, differ between two cohorts of equal length.
func columnsMoved(a, b []coherenceRow) (score, flags int) {
	for i := range a {
		if a[i].v["score"] != b[i].v["score"] || a[i].n["score"] != b[i].n["score"] {
			score++
		}
		for _, f := range coherenceFlags {
			if a[i].v[f] != b[i].v[f] || a[i].n[f] != b[i].n[f] {
				flags++
				break
			}
		}
	}
	return score, flags
}

// TestRulesCoherence_BlockRuleOrderingIsLoadBearing pins the third
// property of the canonical set, and the one the story's own draft got
// wrong.
//
// Within ONE rule null_together is the last write, so the flags are
// computed and then take the score's null decision. Declared as two
// rules with null_together FIRST, the set_expr runs afterwards and
// clears all three flags' null bits on every row — including the ~80%
// the score is missing from, which is the whole population the block
// exists to exclude.
func TestRulesCoherence_BlockRuleOrderingIsLoadBearing(t *testing.T) {
	prof := coherenceFixtureProfile(t)
	const wrongOrder = `[
      {"when": "!isnull(score)", "set_expr": {"score": "round(score)"}},
      {"null_together": ["score", "hi", "mid", "lo"]},
      {"set_expr": {"hi": "score >= 9", "mid": "score >= 7 && score < 9", "lo": "score < 7"}},
      {"when": "asked == 0", "set_null": ["q1", "q2"]}
    ]`
	right, _, _ := generateCoherence(t, prof, canonicalCoherenceRules)
	wrong, _, _ := generateCoherence(t, prof, wrongOrder)

	r := measureCoherence(right)
	w := measureCoherence(wrong)
	t.Logf("one-rule form: partial blocks=%d | null_together-first form: partial blocks=%d "+
		"(scoreNullFlagPresent=%d)", r.rows-r.allPresent-r.allNull,
		w.rows-w.allPresent-w.allNull, w.nullFlagSet)

	if r.rows-r.allPresent-r.allNull != 0 {
		t.Errorf("the canonical one-rule form left %d partial block(s)",
			r.rows-r.allPresent-r.allNull)
	}
	if w.nullFlagSet == 0 {
		t.Error("declaring null_together BEFORE the set_expr that writes the block members left no " +
			"row with a flag and no score — the ordering hazard this pins is gone, or the test " +
			"no longer reproduces it")
	}
}

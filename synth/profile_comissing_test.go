package synth_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/synth"
)

const blockSize = 5

// blockSourceSpec builds a cohort that genuinely CARRIES a co-missing
// question block, by generating it through the rule layer itself —
// `q1..q5` share one null decision, at 0.30 — plus two fields nulled
// INDEPENDENTLY at the same 0.30 rate.
//
// Generating the source through null_together rather than hand-encoding
// rows is deliberate and is the same choice suggestSourceSpec makes: the
// claim under test is a round trip, and a fixture built by the mechanism
// the round trip ends in is the one shape where "recovered" and
// "reproducible" mean the same thing.
//
// `solo1`/`solo2` are the discriminating half at the PUBLIC surface: on
// a 6,000-row draw they land within a few rows of the block's own null
// rate, which is exactly the coincidence a rate-only detector cannot
// tell from a question block.
func blockSourceSpec(rows int) *synth.Spec {
	spec := &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "gate", Type: "u4", Nullable: true, NullRate: 0.30,
				Distribution: synth.DistNormal,
				Params:       map[string]any{"mean": 4.0, "std": 1.5, "min": 1.0, "max": 7.0}},
		},
	}
	block := []string{"gate"}
	for i := 1; i <= blockSize; i++ {
		name := fmt.Sprintf("q%d", i)
		block = append(block, name)
		spec.Fields = append(spec.Fields, synth.FieldSpec{
			Name: name, Type: "u4", Nullable: true, NullRate: 0.30,
			Distribution: synth.DistNormal,
			Params:       map[string]any{"mean": 4.0, "std": 1.5, "min": 1.0, "max": 7.0},
		})
	}
	for _, name := range []string{"solo1", "solo2"} {
		spec.Fields = append(spec.Fields, synth.FieldSpec{
			Name: name, Type: "u4", Nullable: true, NullRate: 0.30,
			Distribution: synth.DistNormal,
			Params:       map[string]any{"mean": 4.0, "std": 1.5, "min": 1.0, "max": 7.0},
		})
	}
	spec.Rules = []synth.RuleSpec{{NullTogether: block}}
	return spec
}

func blockFixtureCandidates(t *testing.T) (*synth.Profile, []byte) {
	t.Helper()
	data, _, err := synth.SynthBytes(blockSourceSpec(6000), synth.Options{Seed: 31})
	if err != nil {
		t.Fatalf("SynthBytes(source cohort): %v", err)
	}
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{
		TopK: 8, IncludeStats: true, SuggestRules: true, Seed: 1,
	})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if len(prof.RuleCandidates) == 0 {
		t.Fatalf("fixture produced no candidates; warnings = %v", prof.Warnings)
	}
	return prof, data
}

// TestSuggestBlocks_WrittenFileGeneratesUnmodified is acceptance
// criterion 7, asserted BY GENERATION rather than by comparing shapes:
// the candidate file is written, fed to ApplyRulesFile unmodified, and
// the generated cohort is measured for the property the candidate
// claims.
//
// Both halves in ONE test, because each is trivially satisfiable by
// abandoning the other: a rule nulling every row satisfies "the block is
// coherent", and the negative control is what makes the first half
// capable of failing.
func TestSuggestBlocks_WrittenFileGeneratesUnmodified(t *testing.T) {
	prof, _ := blockFixtureCandidates(t)

	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), prof.RuleCandidates, "/candidates.json"); err != nil {
		t.Fatalf("WriteRuleCandidates: %v", err)
	}

	decoded := roundTripProfile(t, prof)
	spec, _ := synth.SpecFromProfile(decoded, 5000)
	if err := synth.ApplyRulesFile(cfg.Fs(), spec, "/candidates.json"); err != nil {
		t.Fatalf("the written candidate file was not consumed unmodified: %v", err)
	}
	if len(spec.Rules) == 0 {
		t.Fatal("ApplyRulesFile loaded no rules")
	}

	ruled, _, err := synth.SynthBytes(spec, synth.Options{Seed: 808})
	if err != nil {
		t.Fatalf("SynthBytes(ruled): %v", err)
	}
	base, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 5000)
	unruled, _, err := synth.SynthBytes(base, synth.Options{Seed: 808})
	if err != nil {
		t.Fatalf("SynthBytes(unruled): %v", err)
	}

	members := blockMemberNames(t, prof)
	if len(members) != blockSize+1 {
		t.Fatalf("detected block has %d members, want %d: %v", len(members), blockSize+1, members)
	}
	partial, rows := partialBlockRows(t, ruled, members)
	if rows == 0 {
		t.Fatal("no rows generated")
	}
	if partial != 0 {
		t.Errorf("the detected block was partial on %d of %d generated rows", partial, rows)
	}

	basePartial, baseRows := partialBlockRows(t, unruled, members)
	if basePartial == 0 {
		t.Errorf("the unruled run is already coherent (%d of %d), so the round trip proves nothing",
			basePartial, baseRows)
	}
}

// blockMemberNames returns the members of the first co-missing candidate.
func blockMemberNames(t *testing.T, prof *synth.Profile) []string {
	t.Helper()
	for _, c := range prof.RuleCandidates {
		if c.Evidence != nil && c.Evidence.Detector == "co_missing" {
			return c.NullTogether
		}
	}
	t.Fatalf("no co-missing candidate; warnings = %v", prof.Warnings)
	return nil
}

// partialBlockRows counts the rows on which SOME but not all of the
// named fields are null.
func partialBlockRows(t *testing.T, data []byte, members []string) (partial, rows int) {
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
	values := map[string]float64{}
	nulls := map[string]bool{}
	for {
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		rows++
		n := 0
		for _, m := range members {
			if nulls[m] {
				n++
			}
		}
		if n != 0 && n != len(members) {
			partial++
		}
	}
	return partial, rows
}

// TestSuggestBlocks_IndependentFieldsSharingARateAreNotABlock is the
// discriminating criterion at the PUBLIC surface, on generated data
// rather than a hand-built fixture.
//
// solo1 and solo2 are drawn at the block's own 0.30 null rate and are
// independent of it and of each other. A rate-only detector folds all
// eight fields into one block; this asserts it does not, and asserts
// that the block it DOES find is the real one, so the test cannot be
// satisfied by finding nothing.
func TestSuggestBlocks_IndependentFieldsSharingARateAreNotABlock(t *testing.T) {
	prof, _ := blockFixtureCandidates(t)
	members := blockMemberNames(t, prof)
	got := strings.Join(members, ",")
	want := "gate,q1,q2,q3,q4,q5"
	if got != want {
		t.Errorf("block = %q, want %q", got, want)
	}
	for _, c := range prof.RuleCandidates {
		for _, m := range c.NullTogether {
			if m == "solo1" || m == "solo2" {
				t.Errorf("independent field %q drawn at the block's own null rate was emitted in a block (%v)",
					m, c.NullTogether)
			}
		}
	}
}

// TestSuggestBlocks_ComposeWithGatingCandidatesInOneFile is acceptance
// criterion 7 for the COMBINED file, plus the reason the two detectors'
// candidates are emitted in the order they are.
//
// THE BRIEF'S REASON FOR THAT ORDER IS WRONG AND THIS TEST IS WHERE IT
// WAS MEASURED. The expectation going in was that a null_together placed
// before a set_null over the same fields would be broken by it. For
// candidates AS DETECTED that cannot happen, and the argument is
// arithmetic rather than empirical: block members are admitted only when
// their null patterns are IDENTICAL, so they have identical conditional
// null rates at every level of every gate, so the gating detector
// classifies them identically — a gate takes a WHOLE block or none of
// it, never part of one. Both orders then produce "null iff gated or the
// first member drew null", and the third clause below asserts that
// equivalence rather than claiming a difference that is not there.
//
// The order is still chosen rather than incidental, and the reason is
// the file's entire purpose: it exists to be EDITED. A hand-narrowed
// gate — the analyst keeping two of a block's four targets, or adding a
// rule of their own — is exactly the case where the two orders differ,
// and gate-first is the one where the block REPAIRS the edit instead of
// being broken by it. The fourth clause measures that.
func TestSuggestBlocks_ComposeWithGatingCandidatesInOneFile(t *testing.T) {
	src := &synth.Spec{
		RowCount: 8000,
		Fields: []synth.FieldSpec{
			{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.70}},
		},
	}
	var targets []string
	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("q%d", i)
		targets = append(targets, name)
		src.Fields = append(src.Fields, synth.FieldSpec{
			Name: name, Type: "u4", Nullable: true, NullRate: 0,
			Distribution: synth.DistNormal,
			Params:       map[string]any{"mean": 4.0, "std": 1.5, "min": 1.0, "max": 7.0},
		})
	}
	src.Rules = []synth.RuleSpec{{When: "aware == 0", SetNull: targets}}
	data, _, err := synth.SynthBytes(src, synth.Options{Seed: 17})
	if err != nil {
		t.Fatalf("SynthBytes(source): %v", err)
	}
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{TopK: 8, IncludeStats: true, SuggestRules: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}

	// 1. The file carries BOTH kinds, gating first — which is the
	// PREFERENCE order, kept here because nothing forces a move: `aware`
	// is written by no candidate, so the block holds none of its gate's
	// source. The case where the constraint overrides the preference is
	// TestSuggestRules_EmittedOrderLeavesNoOrphanRows.
	firstGate, firstBlock := -1, -1
	for i, c := range prof.RuleCandidates {
		switch c.Evidence.Detector {
		case "gating":
			if firstGate < 0 {
				firstGate = i
			}
		case "co_missing":
			if firstBlock < 0 {
				firstBlock = i
			}
		}
	}
	if firstGate < 0 || firstBlock < 0 {
		t.Fatalf("combined file missing a kind (gate=%d block=%d); warnings = %v",
			firstGate, firstBlock, prof.Warnings)
	}
	if firstGate > firstBlock {
		t.Errorf("a co-missing block is declared at %d, ahead of the gating candidate at %d", firstBlock, firstGate)
	}

	// 2. The combined file, unmodified, generates a coherent block.
	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), prof.RuleCandidates, "/both.json"); err != nil {
		t.Fatalf("WriteRuleCandidates: %v", err)
	}
	load := func() *synth.Spec {
		t.Helper()
		spec, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 6000)
		if err := synth.ApplyRulesFile(cfg.Fs(), spec, "/both.json"); err != nil {
			t.Fatalf("the combined file was not consumed unmodified: %v", err)
		}
		return spec
	}
	partialFor := func(spec *synth.Spec, members []string) int {
		t.Helper()
		out, _, err := synth.SynthBytes(spec, synth.Options{Seed: 21})
		if err != nil {
			t.Fatalf("SynthBytes: %v", err)
		}
		p, rows := partialBlockRows(t, out, members)
		if rows == 0 {
			t.Fatal("no rows generated")
		}
		return p
	}
	if p := partialFor(load(), targets); p != 0 {
		t.Errorf("%d rows carry a partial block after both candidates applied", p)
	}

	// 3. As DETECTED the order is immaterial, and that is a property of
	//    the admission rule rather than a coincidence of this fixture.
	reversed := load()
	reversed.Rules = reorderBlocksFirst(reversed.Rules)
	if p := partialFor(reversed, targets); p != 0 {
		t.Errorf("reversing the order of DETECTED candidates broke the block on %d rows; "+
			"a gate takes a whole block or none of it, so it must not", p)
	}

	// 4. The asymmetry the chosen order exists for: a hand-narrowed gate
	//    over PART of a block. Gate-first, the block repairs it;
	//    block-first, the set_null breaks it on exactly the rows it
	//    gated.
	narrow := func(rules []synth.RuleSpec) []synth.RuleSpec {
		out := make([]synth.RuleSpec, len(rules))
		copy(out, rules)
		for i := range out {
			if out[i].Evidence != nil && out[i].Evidence.Detector == "gating" {
				out[i].SetNull = []string{"q1", "q2"}
			}
		}
		return out
	}
	edited := load()
	edited.Rules = narrow(edited.Rules)
	if p := partialFor(edited, targets); p != 0 {
		t.Errorf("gate-first: a hand-narrowed gate left %d partial block rows; the block must repair it", p)
	}
	editedRev := load()
	editedRev.Rules = reorderBlocksFirst(narrow(editedRev.Rules))
	if p := partialFor(editedRev, targets); p == 0 {
		t.Error("block-first: a hand-narrowed gate did not break the block, so the order is untested")
	}
}

// reorderBlocksFirst moves every co-missing candidate ahead of every
// other rule, preserving relative order within each group.
func reorderBlocksFirst(rules []synth.RuleSpec) []synth.RuleSpec {
	out := make([]synth.RuleSpec, 0, len(rules))
	for _, r := range rules {
		if r.Evidence != nil && r.Evidence.Detector == "co_missing" {
			out = append(out, r)
		}
	}
	for _, r := range rules {
		if r.Evidence == nil || r.Evidence.Detector != "co_missing" {
			out = append(out, r)
		}
	}
	return out
}

// TestSuggestBlocks_DocumentMovesOnlyItsWarnings extends E3-S1's
// boundary to the second detector: the co-missing findings — blocks,
// near misses and always-null columns — reach the caller on
// RuleCandidates and the profile document's `warnings`, and nowhere
// else.
func TestSuggestBlocks_DocumentMovesOnlyItsWarnings(t *testing.T) {
	data, _, err := synth.SynthBytes(blockSourceSpec(3000), synth.Options{Seed: 31})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	opts := synth.ProfileOptions{TopK: 8, IncludeStats: true, Seed: 1}
	plain, err := synth.ProfileBytes(data, opts)
	if err != nil {
		t.Fatalf("plain ProfileBytes: %v", err)
	}
	withDetection := opts
	withDetection.SuggestRules = true
	detected, err := synth.ProfileBytes(data, withDetection)
	if err != nil {
		t.Fatalf("detecting ProfileBytes: %v", err)
	}
	if len(detected.RuleCandidates) == 0 {
		t.Fatal("fixture must produce candidates for this test to mean anything")
	}

	plainDoc := marshalMap(t, plain)
	detectedDoc := marshalMap(t, detected)
	delete(plainDoc, "warnings")
	delete(detectedDoc, "warnings")
	if !bytes.Equal(remarshal(t, plainDoc), remarshal(t, detectedDoc)) {
		t.Error("the profile document moved beyond its warnings")
	}

	// And the candidates do not ride the document.
	raw, err := json.Marshal(detected)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "null_together") {
		t.Error("a co-missing candidate reached the profile document")
	}
}

// TestSuggestBlocks_AlwaysNullColumnIsNamedWithItsType is acceptance
// criterion 5 at the public surface, on the shape that motivated it: a
// column present in the schema and null on every row, for which
// generation today fabricates a distribution from a marginal computed
// over zero observations.
func TestSuggestBlocks_AlwaysNullColumnIsNamedWithItsType(t *testing.T) {
	src := blockSourceSpec(2000)
	src.Fields = append(src.Fields, synth.FieldSpec{
		Name: "lgbt", Type: "categorical_u8", Nullable: true, NullRate: 1,
		Distribution: synth.DistWeightedCategorical,
		Params:       map[string]any{"values": []any{"yes", "no"}, "weights": []any{1.0, 9.0}},
	})
	data, _, err := synth.SynthBytes(src, synth.Options{Seed: 5})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{TopK: 8, SuggestRules: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	var line string
	for _, w := range prof.Warnings {
		if strings.HasPrefix(w, `always-null column "lgbt"`) {
			line = w
		}
	}
	if line == "" {
		t.Fatalf("the always-null column was not reported; warnings = %v", prof.Warnings)
	}
	if !strings.Contains(line, "(categorical_u8)") {
		t.Errorf("the finding does not name the field's type: %q", line)
	}
	for _, c := range prof.RuleCandidates {
		for _, m := range append(append([]string{}, c.NullTogether...), c.SetNull...) {
			if m == "lgbt" {
				t.Errorf("the always-null column was forced into a rule: %+v", c)
			}
		}
	}
	groups := synth.GroupWarnings(prof.Warnings)
	var seen bool
	for _, g := range groups {
		if g.Kind == "always-null column" {
			seen = true
			if !g.Attention {
				t.Errorf("always-null grouped without attention")
			}
		}
	}
	if !seen {
		t.Errorf("always-null did not reach the terminal summary; groups = %+v", groups)
	}
}

// TestProfileAlwaysNull_ReproducedExactlyWithoutARule is the FU-09
// decision, asserted rather than asserted-about: an always-null column
// is REPRODUCED by generation, so the `set_null` candidate the detector
// declines to propose would change nothing.
//
// The warning this replaces claimed the opposite — that generation
// "fabricates a distribution" from a marginal computed over zero
// observations — and the claim was the whole case for proposing a rule.
// It is false: a column the profiler summarised nothing for reaches
// SpecFromProfile's default arm, which reconstructs a typed `constant`
// placeholder with null_rate 1.0, so nullableSampler nulls every row.
//
// All THREE row-value classes are covered in one case because the
// default arm is shared and each class reaches it through a different
// summariser being nil: a categorical (row value is a string), a numeric
// (float64) and a set_* (map[string]bool). A regression that restores
// the fabrication for one type only would otherwise pass.
//
// Two claims, both required, because either alone is trivially
// satisfiable: the column comes back null on EVERY row, and no rule
// candidate names it.
func TestProfileAlwaysNull_ReproducedExactlyWithoutARule(t *testing.T) {
	const rows = 2000
	src := &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "kept", Type: "u8", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 5.0, "std": 1.0, "min": 0.0, "max": 10.0}},
			{Name: "gone_cat", Type: "categorical_u8", Nullable: true, NullRate: 1,
				Distribution: synth.DistWeightedCategorical,
				Params:       map[string]any{"values": []any{"yes", "no"}, "weights": []any{1.0, 9.0}}},
			{Name: "gone_num", Type: "f64", Nullable: true, NullRate: 1,
				Distribution: synth.DistNormal,
				Params:       map[string]any{"mean": 1.0, "std": 1.0}},
			{Name: "gone_set", Type: "set_u8", Nullable: true, NullRate: 1,
				Distribution: synth.DistSetBernoulli,
				Params: map[string]any{
					"options":     []any{"tv", "radio"},
					"frequencies": []any{0.5, 0.5},
				}},
		},
	}
	data, _, err := synth.SynthBytes(src, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes source: %v", err)
	}
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{TopK: 8, SuggestRules: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}

	gone := []string{"gone_cat", "gone_num", "gone_set"}

	// (1) Every always-null column is reproduced as all-null, with no
	// rule applied at all.
	spec, _ := synth.SpecFromProfile(prof, rows)
	out, _, err := synth.SynthBytes(spec, synth.Options{Seed: 12})
	if err != nil {
		t.Fatalf("SynthBytes regenerated: %v", err)
	}
	if got := readRecordCount(t, out); got != rows {
		t.Fatalf("regenerated %d rows, want %d", got, rows)
	}
	for _, name := range gone {
		if got := readNullCountForField(t, out, name); got != rows {
			t.Errorf("%q came back null on %d of %d rows; an always-null column is not reproduced, "+
				"so the detector's decision not to propose a set_null candidate is wrong", name, got, rows)
		}
	}

	// (2) No candidate names one, and the finding says why.
	for _, c := range prof.RuleCandidates {
		for _, m := range append(append([]string{}, c.NullTogether...), c.SetNull...) {
			for _, name := range gone {
				if m == name {
					t.Errorf("always-null column %q was proposed as a rule: %+v", name, c)
				}
			}
		}
	}
	joined := strings.Join(prof.Warnings, "\n")
	for _, name := range gone {
		if !strings.Contains(joined, `always-null column "`+name+`"`) {
			t.Errorf("always-null column %q was not reported at all; warnings = %v", name, prof.Warnings)
		}
	}
	if strings.Contains(joined, "fabricates a distribution") {
		t.Error("the always-null finding still claims generation fabricates a distribution, " +
			"which this test measures to be false")
	}
	if !strings.Contains(joined, "redundant") {
		t.Error("the always-null finding does not say why no rule is proposed")
	}
}

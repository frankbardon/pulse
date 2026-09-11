package synth_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	stderrors "errors"
	"github.com/frankbardon/pulse/encoding"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

const suggestTargets = 6

// suggestSourceSpec builds a cohort that genuinely CARRIES a gating
// relationship, by generating it through the rule layer itself: `aware`
// is never null, and q1..q6 are nulled on exactly the rows where it is
// zero.
//
// Generating the source through a rule rather than hand-encoding rows is
// deliberate. The whole claim under test is a round trip — detection
// must recover a relationship that generation can then reproduce — and a
// fixture built by the same mechanism the round trip ends in is the one
// shape where "recovered" and "reproducible" mean the same thing.
func suggestSourceSpec(rows int) *synth.Spec {
	spec := &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.75}},
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west", "north"}, "weights": []any{3.0, 2.0, 2.0}}},
			// Null only ever through the rule, so the source's gate is
			// exact and detection has an unambiguous thing to find.
			{Name: "noise", Type: "f64", Nullable: true, NullRate: 0.30,
				Distribution: synth.DistNormal, Params: map[string]any{"mean": 10.0, "std": 3.0}},
		},
	}
	targets := make([]string, 0, suggestTargets)
	for i := 1; i <= suggestTargets; i++ {
		name := fmt.Sprintf("q%d", i)
		targets = append(targets, name)
		spec.Fields = append(spec.Fields, synth.FieldSpec{
			Name: name, Type: "u4", Nullable: true, NullRate: 0,
			Distribution: synth.DistNormal,
			Params:       map[string]any{"mean": 4.0, "std": 1.5, "min": 1.0, "max": 7.0},
		})
	}
	spec.Rules = []synth.RuleSpec{{When: "aware == 0", SetNull: targets}}
	return spec
}

// suggestFixtureProfile generates the source cohort, profiles it with
// detection on, and round-trips the profile through JSON — the shape a
// real caller holds, and the one that proves RuleCandidates is reachable
// in-process rather than off the document.
func suggestFixtureProfile(t *testing.T) (*synth.Profile, []byte) {
	t.Helper()
	data, _, err := synth.SynthBytes(suggestSourceSpec(6000), synth.Options{Seed: 77})
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

// TestSuggestRules_WrittenFileGeneratesUnmodified is acceptance
// criterion 1, and it is asserted BY GENERATION rather than by comparing
// shapes. A test that unmarshals the written file into []RuleSpec passes
// against a document whose predicates do not compile, whose targets name
// fields the derived spec does not declare, or whose `when` is never
// true — three silent shapes this package has shipped before.
//
// Both halves are here in ONE test because each is trivially satisfiable
// by abandoning the other: a rule nulling every row satisfies "the gate
// fires", and a rule that never fires satisfies "non-gated rows keep
// their inferred values".
func TestSuggestRules_WrittenFileGeneratesUnmodified(t *testing.T) {
	prof, _ := suggestFixtureProfile(t)

	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), prof.RuleCandidates, "/candidates.json"); err != nil {
		t.Fatalf("WriteRuleCandidates: %v", err)
	}

	// The profile a real caller holds has been through JSON, so the
	// candidates cannot be smuggled across in memory.
	decoded := roundTripProfile(t, prof)
	if len(decoded.RuleCandidates) != 0 {
		t.Fatalf("RuleCandidates survived the document; it must be json:\"-\"")
	}

	spec, _ := synth.SpecFromProfile(decoded, 4000)
	if err := synth.ApplyRulesFile(cfg.Fs(), spec, "/candidates.json"); err != nil {
		t.Fatalf("the written candidate file was not consumed unmodified: %v", err)
	}
	if len(spec.Rules) == 0 {
		t.Fatal("ApplyRulesFile loaded no rules")
	}

	ruled, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes(ruled): %v", err)
	}
	base, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 4000)
	unruled, _, err := synth.SynthBytes(base, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes(unruled): %v", err)
	}

	gated, gatedAllNull := gatedRowStats(t, ruled)
	if gated == 0 {
		t.Fatal("no gated rows generated; the assertion below would be vacuous")
	}
	if gatedAllNull != gated {
		t.Errorf("the detected rule fired on %d of %d gated rows, want all of them",
			gatedAllNull, gated)
	}

	// The negative control, in the same test: without the rules the
	// same seed does NOT produce the property, so the assertion above
	// is capable of failing.
	baseGated, baseAllNull := gatedRowStats(t, unruled)
	if baseGated == 0 {
		t.Fatal("unruled run produced no gated rows")
	}
	if baseAllNull == baseGated {
		t.Errorf("the unruled run is already coherent (%d of %d), so the round trip proves nothing",
			baseAllNull, baseGated)
	}
}

// gatedRowStats counts rows where aware == 0 and, of those, how many
// carry every target null.
func gatedRowStats(t *testing.T, data []byte) (gated, allNull int) {
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
		if values["aware"] != 0 {
			continue
		}
		gated++
		every := true
		for i := 1; i <= suggestTargets; i++ {
			if !nulls[fmt.Sprintf("q%d", i)] {
				every = false
				break
			}
		}
		if every {
			allNull++
		}
	}
	return gated, allNull
}

func roundTripProfile(t *testing.T, prof *synth.Profile) *synth.Profile {
	t.Helper()
	raw, err := json.Marshal(prof)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	var out synth.Profile
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	return &out
}

// TestSuggestRules_DocumentMovesOnlyItsWarnings is acceptance criterion
// 8 and the boundary around it, in one test: absent the flag the written
// document is byte-identical, and WITH the flag the only key that moves
// is `warnings` — no new section, and RuleCandidates is not on the wire.
func TestSuggestRules_DocumentMovesOnlyItsWarnings(t *testing.T) {
	data, _, err := synth.SynthBytes(suggestSourceSpec(3000), synth.Options{Seed: 77})
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
	if _, ok := detectedDoc["rule_candidates"]; ok {
		t.Error("detection added a section to the profile document")
	}
	delete(plainDoc, "warnings")
	delete(detectedDoc, "warnings")
	a := remarshal(t, plainDoc)
	b := remarshal(t, detectedDoc)
	if !bytes.Equal(a, b) {
		t.Errorf("the profile document moved beyond its warnings: %d vs %d bytes", len(a), len(b))
	}
}

func marshalMap(t *testing.T, prof *synth.Profile) map[string]any {
	t.Helper()
	raw, err := json.Marshal(prof)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func remarshal(t *testing.T, m map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	return raw
}

// TestRuleEvidence_IsInert is the gate on the one slot of RuleSpec that
// does nothing. A slot on a type whose other five members all execute is
// exactly where a later reader assumes execution, so the inertness needs
// its own test rather than resting on nobody having written the call.
//
// Three properties, each independently capable of failing:
//
//  1. a rule carrying evidence generates BYTE-IDENTICALLY to the same
//     rule without it, at the same seed;
//  2. a rule carrying ONLY evidence is still PULSE_SYNTH_RULE_EMPTY,
//     because evidence is not an action;
//  3. the key is `_evidence`, is absent when nil, and survives the round
//     trip the candidate file depends on.
func TestRuleEvidence_IsInert(t *testing.T) {
	base := suggestSourceSpec(500)
	plain, _, err := synth.SynthBytes(base, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes(plain): %v", err)
	}

	decorated := suggestSourceSpec(500)
	decorated.Rules[0].Evidence = &synth.RuleEvidence{
		Detector:    "gating",
		Note:        "nonsense an author typed by hand",
		GateField:   "nosuchfield",
		GatedLevels: []string{"nosuchlevel"},
		Targets:     []synth.RuleEvidenceTarget{{Field: "nosuchtarget", NullRate: 42}},
	}
	withEvidence, _, err := synth.SynthBytes(decorated, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes(with evidence): %v", err)
	}
	if !bytes.Equal(plain, withEvidence) {
		t.Errorf("evidence changed %d bytes of generated output; the slot must be inert",
			len(withEvidence))
	}

	evidenceOnly := suggestSourceSpec(100)
	evidenceOnly.Rules = []synth.RuleSpec{{Evidence: &synth.RuleEvidence{Detector: "gating"}}}
	raw, err := json.Marshal(evidenceOnly)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := synth.ParseSpec(raw); err == nil {
		t.Error("a rule carrying only evidence was accepted; evidence is not an action")
	} else {
		var ce *pulseerrors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != pulseerrors.PULSE_SYNTH_RULE_EMPTY {
			t.Errorf("evidence-only rule refused with %v, want PULSE_SYNTH_RULE_EMPTY", err)
		}
	}

	one, err := json.Marshal(decorated.Rules[0])
	if err != nil {
		t.Fatalf("marshal rule: %v", err)
	}
	if !strings.Contains(string(one), `"_evidence"`) {
		t.Errorf("evidence key is not spelled _evidence: %s", one)
	}
	bare, err := json.Marshal(base.Rules[0])
	if err != nil {
		t.Fatalf("marshal bare rule: %v", err)
	}
	if strings.Contains(string(bare), "_evidence") {
		t.Errorf("a rule with no evidence carries the key: %s", bare)
	}
	var back synth.RuleSpec
	if err := json.Unmarshal(one, &back); err != nil {
		t.Fatalf("unmarshal rule: %v", err)
	}
	if back.Evidence == nil || back.Evidence.GateField != "nosuchfield" {
		t.Errorf("evidence did not survive the round trip: %+v", back.Evidence)
	}
}

// TestWriteRuleCandidates_EmptyWritesAnEmptyArray pins the answer to
// "what did detection find" when the answer is nothing: `[]`, a legal
// rules document, rather than no file (which leaves a previous run's
// answer in place) or `null` (which ParseRules accepts but reads as
// absence rather than as an answer).
func TestWriteRuleCandidates_EmptyWritesAnEmptyArray(t *testing.T) {
	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), nil, "/none.json"); err != nil {
		t.Fatalf("WriteRuleCandidates(nil): %v", err)
	}
	raw, err := afero.ReadFile(cfg.Fs(), "/none.json")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Errorf("wrote %q, want []", strings.TrimSpace(string(raw)))
	}
	rules, err := synth.ParseRules(raw)
	if err != nil {
		t.Fatalf("ParseRules on the written file: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("parsed %d rules from an empty candidate file", len(rules))
	}
}

// TestWriteRuleCandidates_RefusalsNameThePath keeps the coded-error
// discipline `pulse errors lookup` depends on.
func TestWriteRuleCandidates_RefusalsNameThePath(t *testing.T) {
	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(nil, nil, "/x.json"); err == nil {
		t.Error("nil fs accepted")
	}
	if err := synth.WriteRuleCandidates(cfg.Fs(), nil, ""); err == nil {
		t.Error("empty path accepted")
	}
	ro := afero.NewReadOnlyFs(cfg.Fs())
	err := synth.WriteRuleCandidates(ro, nil, "/x.json")
	if err == nil {
		t.Fatal("read-only fs accepted")
	}
	var ce *pulseerrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != pulseerrors.DATA_FILE {
		t.Fatalf("write failure = %v, want DATA_FILE", err)
	}
	if ce.Details["path"] != "/x.json" {
		t.Errorf("details.path = %v, want the file", ce.Details["path"])
	}
}

// TestSuggestRules_NumericGatePredicateFiresOnEveryWireRow is the
// round() decision asserted BY GENERATION.
//
// E2-S4 measured this on the real cohort: a generated row holds the
// sampler's FLOAT and the file holds the stored integer, so a bare
// `familiarity == 1` selects only the draws that landed exactly on 1 —
// 1,843 rows where the file shows 2,739 — while round() reproduces the
// stored value (an integer field is written as floor(v + 0.5)) and
// selects exactly the rows a reader of the file would count.
//
// A detector emitting the bare form would therefore write a rule that
// VALIDATES, COMPILES, FIRES, and is silently wrong on a third of the
// rows it was meant to gate. This test is the only thing standing
// between that and a green suite, so it asserts the firing rather than
// the predicate's spelling.
func TestSuggestRules_NumericGatePredicateFiresOnEveryWireRow(t *testing.T) {
	src := &synth.Spec{
		RowCount: 6000,
		Fields: []synth.FieldSpec{
			{Name: "familiarity", Type: "u4", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 3.2, "std": 1.9, "min": 1.0, "max": 7.0}},
			{Name: "q1", Type: "u4", Nullable: true, NullRate: 0, Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 4.0, "std": 1.5, "min": 1.0, "max": 7.0}},
			{Name: "q2", Type: "u4", Nullable: true, NullRate: 0, Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 4.0, "std": 1.5, "min": 1.0, "max": 7.0}},
			{Name: "q3", Type: "u4", Nullable: true, NullRate: 0, Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 4.0, "std": 1.5, "min": 1.0, "max": 7.0}},
		},
		Rules: []synth.RuleSpec{
			{When: "round(familiarity) == 1", SetNull: []string{"q1", "q2", "q3"}},
		},
	}
	data, _, err := synth.SynthBytes(src, synth.Options{Seed: 5})
	if err != nil {
		t.Fatalf("SynthBytes(source): %v", err)
	}
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{TopK: 8, IncludeStats: true, SuggestRules: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	cand, ok := suggestCandidate(prof.RuleCandidates, "familiarity")
	if !ok {
		t.Fatalf("familiarity not proposed as a gate; warnings = %v", prof.Warnings)
	}

	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), []synth.RuleSpec{cand}, "/c.json"); err != nil {
		t.Fatalf("WriteRuleCandidates: %v", err)
	}
	spec, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 6000)
	if err := synth.ApplyRulesFile(cfg.Fs(), spec, "/c.json"); err != nil {
		t.Fatalf("ApplyRulesFile: %v", err)
	}
	out, _, err := synth.SynthBytes(spec, synth.Options{Seed: 99})
	if err != nil {
		t.Fatalf("SynthBytes(ruled): %v", err)
	}

	wire, fired := 0, 0
	r := bytes.NewReader(out)
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
		if values["familiarity"] != 1 {
			continue
		}
		wire++
		if nulls["q1"] && nulls["q2"] && nulls["q3"] {
			fired++
		}
	}
	if wire == 0 {
		t.Fatal("no generated row carries the gate level on the wire")
	}
	if fired != wire {
		t.Errorf("the detected gate fired on %d of %d rows the FILE shows at the gate level — "+
			"a bare `==` reads the pre-rounding float", fired, wire)
	}
}

func suggestCandidate(cands []synth.RuleSpec, gate string) (synth.RuleSpec, bool) {
	for _, c := range cands {
		if c.Evidence != nil && c.Evidence.GateField == gate {
			return c, true
		}
	}
	return synth.RuleSpec{}, false
}

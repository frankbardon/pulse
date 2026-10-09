package descriptor

import (
	stderrors "errors"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// tierOf classifies one ranked entry the way SkillsForIntent orders it.
func tierOf(md skills.Metadata) int {
	switch {
	case md.Kind == skills.KindReference:
		return intentSkillTierReference
	case md.Kind == "design":
		return intentSkillTierDesign
	}
	return intentSkillTierAtomic
}

// TestSkillsForIntent_Ranking pins the order for two analytic intents:
// the intents reference skill first, then design skills covering a
// serving operator (or its category) by name, then every serving
// operator's atomic skill, basic → intermediate → advanced, then name.
func TestSkillsForIntent_Ranking(t *testing.T) {
	snap := hidingSnapshot()
	for _, tc := range []struct {
		intent     string
		design     []string // must be present
		atomicHead []string // first atomic skills, in order
	}{
		{IntentCompareGroups, []string{"statistical-testing", "grouper-design"}, []string{"op-group-category", "op-group-date-ranges", "op-test-prop-z", "op-agg-ci-lower"}},
		{IntentRelationship, []string{"regression-modeling", "statistical-testing"}, []string{"op-mat-correlation", "op-mat-covariance"}},
	} {
		t.Run(tc.intent, func(t *testing.T) {
			got, err := snap.SkillsForIntent(tc.intent)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 || got[0].Name != skills.VirtualIntents {
				t.Fatalf("first skill = %v, want %s", skillNames(got), skills.VirtualIntents)
			}
			names := skillNames(got)
			for _, d := range tc.design {
				if !slices.Contains(names, d) {
					t.Errorf("design skill %s missing: %v", d, names)
				}
			}
			var atomic []string
			prevTier, prevLevel, prevName := -1, -1, ""
			for _, md := range got {
				tier, level := tierOf(md), 0
				if tier == intentSkillTierAtomic {
					atomic = append(atomic, md.Name)
					level = levelRank(builtinPurposes[md.Operator].Level)
					if !slices.Contains(builtinPurposes[md.Operator].Intents, tc.intent) {
						t.Errorf("%s: operator %s does not serve %s", md.Name, md.Operator, tc.intent)
					}
				}
				if tier < prevTier || tier == prevTier && (level < prevLevel || level == prevLevel && md.Name < prevName) {
					t.Errorf("%s out of order after %s", md.Name, prevName)
				}
				prevTier, prevLevel, prevName = tier, level, md.Name
			}
			if len(atomic) < len(tc.atomicHead) || !slices.Equal(atomic[:len(tc.atomicHead)], tc.atomicHead) {
				t.Errorf("atomic head = %v, want %v", atomic, tc.atomicHead)
			}
			// Every serving operator with an atomic skill is listed.
			for _, op := range snap.Ontology().OperatorsServing(tc.intent) {
				stem := "op-" + strings.ToLower(strings.ReplaceAll(op, "_", "-"))
				if _, ok := skills.Get(stem); ok && !slices.Contains(atomic, stem) {
					t.Errorf("serving operator %s's skill %s missing", op, stem)
				}
			}
		})
	}
}

// TestSkillsForIntent_NoServingOperator: an intent no operator serves
// answers its reference skill only.
func TestSkillsForIntent_NoServingOperator(t *testing.T) {
	got, err := hidingSnapshot().SkillsForIntent(IntentFlows)
	if err != nil || !slices.Equal(skillNames(got), []string{skills.VirtualIntents}) {
		t.Fatalf("flows = %v, err %v", skillNames(got), err)
	}
}

// TestSkillsForIntent_ProfilePruning: a hidden operator's atomic skill
// drops out, the list is a subset of the instance's Skills, and a
// pruned intent is refused like an unknown one through the shared
// resolver.
func TestSkillsForIntent_ProfilePruning(t *testing.T) {
	hidden := hidingSnapshot("TEST_ANOVA_F", "TEST_TUKEY_HSD")
	got, err := hidden.SkillsForIntent(IntentCompareGroups)
	if err != nil {
		t.Fatal(err)
	}
	names := skillNames(got)
	for _, gone := range []string{"op-test-anova-f", "op-test-tukey-hsd"} {
		if slices.Contains(names, gone) {
			t.Errorf("hidden skill %s listed", gone)
		}
	}
	if !slices.Contains(names, "op-test-t") {
		t.Errorf("visible op-test-t missing: %v", names)
	}
	visible := skillNames(hidden.Discovery().Skills())
	for _, n := range names {
		if !slices.Contains(visible, n) {
			t.Errorf("%s listed but not in the instance's Skills", n)
		}
	}

	ops := BaseOntology().OperatorsServing(IntentDrivers)
	pruned := hidingSnapshot(ops...)
	for _, s := range []struct {
		snap   *InstanceSnapshot
		intent string
	}{{hidingSnapshot(), "not_an_intent"}, {pruned, IntentDrivers}} {
		_, err := s.snap.SkillsForIntent(s.intent)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RECOMMEND_INTENT_UNKNOWN {
			t.Fatalf("%q: err = %v, want PULSE_RECOMMEND_INTENT_UNKNOWN", s.intent, err)
		}
		valid := ce.Details["valid"].([]string)
		if slices.Contains(valid, s.intent) || !slices.Contains(valid, IntentDescribe) {
			t.Errorf("%q: valid = %v", s.intent, valid)
		}
		// Same refusal as example search.
		_, eerr := s.snap.ExamplesSearch(examples.Query{Intent: s.intent})
		var ece *errors.CodedError
		if !stderrors.As(eerr, &ece) || !slices.Equal(ece.Details["valid"].([]string), valid) {
			t.Errorf("%q: example search refusal differs: %v", s.intent, eerr)
		}
	}
}

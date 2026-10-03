package descriptor

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/examples"
)

// fakeUnits is an injected unitStatusReader: U01 done, U50 open.
func fakeUnits(unit string) (string, bool) {
	s, ok := map[string]string{"U01": "done", "U50": "not-started"}[unit]
	return s, ok
}

// hasProblem reports whether any problem contains every fragment.
func hasProblem(problems []string, fragments ...string) bool {
	for _, p := range problems {
		all := true
		for _, f := range fragments {
			all = all && strings.Contains(p, f)
		}
		if all {
			return true
		}
	}
	return false
}

// TestGuidanceExemptions_Apply falsifies every ledger rule: an
// unexempted gap remains; an entry with no justification, no owner, a
// malformed or unknown owner, a duplicate key, a covered gap or a done
// owner is a problem; a permanent entry never goes stale on status.
func TestGuidanceExemptions_Apply(t *testing.T) {
	ok := guidanceExemption{Key: "A", Why: "justified", Owner: "U50"}
	cases := []struct {
		name      string
		gaps      []string
		ledger    []guidanceExemption
		remaining []string
		problem   []string // fragments of the one expected problem; nil = none
	}{
		{"exempted gap", []string{"A"}, []guidanceExemption{ok}, nil, nil},
		{"unexempted gap", []string{"A", "B"}, []guidanceExemption{ok}, []string{"B"}, nil},
		{"no justification", []string{"A"}, []guidanceExemption{{Key: "A", Why: " ", Owner: "U50"}}, nil, []string{"A", "no justification"}},
		{"no owner", []string{"A"}, []guidanceExemption{{Key: "A", Why: "w", Owner: ""}}, nil, []string{"A", "no owner unit"}},
		{"malformed owner", []string{"A"}, []guidanceExemption{{Key: "A", Why: "w", Owner: "someday"}}, nil, []string{"A", "not a roadmap unit ID"}},
		{"unknown owner unit", []string{"A"}, []guidanceExemption{{Key: "A", Why: "w", Owner: "U99"}}, nil, []string{"A", "U99", "no docs/roadmap/units doc"}},
		{"stale: owner done", []string{"A"}, []guidanceExemption{{Key: "A", Why: "w", Owner: "U01"}}, nil, []string{"A", "stale", "U01", "done"}},
		{"stale: gap covered", nil, []guidanceExemption{ok}, nil, []string{"A", "stale", "covered"}},
		{"duplicate key", []string{"A"}, []guidanceExemption{ok, ok}, nil, []string{"A", "listed twice"}},
		{"permanent ignores status", []string{"A"}, []guidanceExemption{{Key: "A", Why: "w", Owner: ownerPermanent}}, nil, nil},
		{"permanent still stale when covered", nil, []guidanceExemption{{Key: "A", Why: "w", Owner: ownerPermanent}}, nil, []string{"A", "stale", "covered"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rem, probs := applyExemptions("fixture", tc.gaps, tc.ledger, fakeUnits)
			if !slices.Equal(rem, tc.remaining) {
				t.Errorf("remaining = %v, want %v", rem, tc.remaining)
			}
			switch {
			case tc.problem == nil && len(probs) != 0:
				t.Errorf("want no problems, got %v", probs)
			case tc.problem != nil && (len(probs) != 1 || !hasProblem(probs, tc.problem...)):
				t.Errorf("want one problem naming %v, got %v", tc.problem, probs)
			}
		})
	}
}

// TestGuidanceExemptions_RoadmapStatusReader: the frontmatter reader
// resolves a fixture units directory, and a ledger entry owned by a unit
// whose doc says `status: done` is stale while an open owner is not.
func TestGuidanceExemptions_RoadmapStatusReader(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("U70-shipped.md", "---\nid: U70\nslug: shipped\nstatus: done\n---\n\n# U70\n\nstatus: not-started\n")
	write("U71-open.md", "---\nid: U71\nstatus: not-started\n---\n")
	write("U72-nofrontmatter.md", "# U72\n\nid: U72\nstatus: done\n")
	read := roadmapUnitStatusIn(dir)
	for unit, want := range map[string]string{"U70": "done", "U71": "not-started"} {
		if got, ok := read(unit); !ok || got != want {
			t.Errorf("%s status = %q (known %v), want %q", unit, got, ok, want)
		}
	}
	if _, ok := read("U72"); ok {
		t.Error("a doc without a leading frontmatter block resolved")
	}

	ledger := []guidanceExemption{{Key: "A", Why: "w", Owner: "U70"}, {Key: "B", Why: "w", Owner: "U71"}}
	_, probs := applyExemptions("fixture", []string{"A", "B"}, ledger, read)
	if len(probs) != 1 || !hasProblem(probs, "A", "stale", "U70") {
		t.Errorf("want exactly one stale-owner problem on A, got %v", probs)
	}

	// The live reader sees the real roadmap.
	if st, ok := roadmapUnitStatus()("U08"); !ok || st != "done" {
		t.Errorf("live roadmap: U08 status = %q (known %v), want done", st, ok)
	}
}

// TestGuidanceExemptions_GatesBite: each binding coverage gate, run
// against the REAL ledger, fails on a gap that is neither covered nor
// exempted, and flags the ledger entry of a gap that has been closed.
func TestGuidanceExemptions_GatesBite(t *testing.T) {
	live := roadmapUnitStatus()
	wantRemaining := func(t *testing.T, table string, gaps []string, ledger []guidanceExemption, gap string) {
		t.Helper()
		rem, _ := applyExemptions(table, gaps, ledger, live)
		if !slices.Contains(rem, gap) {
			t.Errorf("%s gate let unexempted gap %s through (remaining %v)", table, gap, rem)
		}
	}

	t.Run("purpose", func(t *testing.T) {
		reg := maps.Clone(builtinPurposes)
		delete(reg, "AGG_AVERAGE")
		wantRemaining(t, "purpose", purposeCoverageGaps(reg), purposeExemptions, "AGG_AVERAGE")
	})
	t.Run("interpretation", func(t *testing.T) {
		reg := maps.Clone(builtinInterpretations)
		reg["TEST_T"] = slices.DeleteFunc(slices.Clone(reg["TEST_T"]), func(in descriptor.Interpretation) bool {
			return in.Field == "p_value"
		})
		wantRemaining(t, "interpretation", interpretationCoverageGaps(reg), interpretationExemptions, "TEST_T:p_value")
	})
	t.Run("interpretation value path", func(t *testing.T) {
		// An unread needs-reading result is a gap the real ledger does
		// not excuse.
		reg := maps.Clone(builtinInterpretations)
		delete(reg, "AGG_SKEWNESS")
		wantRemaining(t, "interpretation", interpretationCoverageGaps(reg), interpretationExemptions, "AGG_SKEWNESS:value")

		// Reading the result closes the gap, so an entry for it goes stale
		// (owner permanent, so only the closed gap can make it stale).
		ledger := append(slices.Clone(interpretationExemptions),
			guidanceExemption{Key: "AGG_SKEWNESS:value", Why: "w", Owner: ownerPermanent})
		if _, probs := applyExemptions("interpretation", interpretationCoverageGaps(builtinInterpretations), ledger, live); !hasProblem(probs, "AGG_SKEWNESS:value", "stale") {
			t.Errorf("closed AGG_SKEWNESS value gap: want a stale entry, got %v", probs)
		}
	})
	t.Run("intent declarers", func(t *testing.T) {
		reg := map[string]descriptor.Purpose{"X": {Intents: []string{IntentDescribe}}}
		thin, _ := intentCoverageGaps(reg, nil)
		wantRemaining(t, "intent-declarers", thin, intentDeclarerExemptions, IntentDescribe)
	})
	t.Run("intent example", func(t *testing.T) {
		ledger := slices.DeleteFunc(slices.Clone(intentExampleExemptions), func(e guidanceExemption) bool {
			return e.Key == IntentSimulate
		})
		_, untagged := intentCoverageGaps(builtinPurposes, nil)
		wantRemaining(t, "intent-example", untagged, ledger, IntentSimulate)

		// Tagging an example closes the gap, so its entry goes stale.
		_, untagged = intentCoverageGaps(builtinPurposes, map[string][]string{"ex": {IntentSimulate}})
		if _, probs := applyExemptions("intent-example", untagged, intentExampleExemptions, live); !hasProblem(probs, IntentSimulate, "stale") {
			t.Errorf("closed simulate example gap: want a stale entry, got %v", probs)
		}
	})
	t.Run("glossary orphan", func(t *testing.T) {
		wantRemaining(t, "glossary-orphan", glossaryOrphans(nil), glossaryOrphanExemptions, "p-value")
	})
}

// TestGuidanceExemptions_Ledger: the lookup intent's exemptions and
// simulate's example exemption are permanent and justified, and nothing else is — a new permanent
// exemption is a deliberate edit of this test.
func TestGuidanceExemptions_Ledger(t *testing.T) {
	tables := map[string][]guidanceExemption{
		"purpose":          purposeExemptions,
		"interpretation":   interpretationExemptions,
		"intent-declarers": intentDeclarerExemptions,
		"intent-example":   intentExampleExemptions,
		"example-intent":   exampleIntentExemptions,
		"glossary-orphan":  glossaryOrphanExemptions,
	}
	permanent := map[string]bool{}
	for table, ledger := range tables {
		for _, e := range ledger {
			if e.Owner == ownerPermanent {
				permanent[table+"/"+e.Key] = true
				if strings.TrimSpace(e.Why) == "" {
					t.Errorf("%s permanent exemption %s has no justification", table, e.Key)
				}
			}
		}
	}
	want := map[string]bool{"intent-declarers/" + IntentLookup: true, "intent-example/" + IntentLookup: true, "intent-example/" + IntentSimulate: true}
	if !maps.Equal(permanent, want) {
		t.Errorf("permanent exemptions = %v, want exactly %v", slices.Sorted(maps.Keys(permanent)), slices.Sorted(maps.Keys(want)))
	}
}

// TestExampleIntentExemptions_StaleWhenTagged: a directory whose
// examples are all tagged has no gap, so its ledger entry is stale.
func TestExampleIntentExemptions_StaleWhenTagged(t *testing.T) {
	fx := []examples.ExampleSummary{{Name: "t1", Category: "tests"}}
	gaps := untaggedExampleCategories(fx, map[string][]string{"t1": {IntentDescribe}})
	ledger := []guidanceExemption{{Key: "tests", Owner: "U09", Why: "fixture entry for a directory that is now fully tagged"}}
	_, probs := applyExemptions("example-intent", gaps, ledger, roadmapUnitStatus())
	if !hasProblem(probs, "tests", "stale") {
		t.Errorf("tagged tests directory: want a stale entry, got %v", probs)
	}
}

package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/skills"
)

// TestSkillsCoverAllOperatorComponents is the non-skippable CI gate for
// per-operator component-key documentation. For every registered
// aggregator, grouper, and filterer, every key declared in the
// operator's ComponentSchema must appear in the target skill file.
//
// Routing:
//   - AGG_* → skills/aggregation-guide.md
//   - GROUP_* → skills/grouper-design.md
//   - FILTER_* → skills/aggregation-guide.md (filtering section)
//
// Universal-floor keys ({n, n_null} for aggregators, {total_n, n_null}
// for groupers, {n_in, n_out, n_null_input} for filterers) are
// documented inline by their respective floor sections — the test
// iterates every Keys[i] including the floor so a regression in the
// floor section trips the gate as well.
//
// Drift modes caught:
//   - A new operator landing with a new component key that the skill
//     does not yet document.
//   - A capability-row rename that loses skill coverage.
//   - A skill edit that removes a previously-documented key.
//
// The descriptor package is the natural home for this gate — skills/
// is a leaf the descriptor package imports for manifest assembly, so
// reaching from descriptor into skills.Get is acyclic. (The reverse
// would cycle.)
func TestSkillsCoverAllOperatorComponents(t *testing.T) {
	aggContent, ok := skills.Get("aggregation-guide")
	if !ok {
		t.Fatal("skills/aggregation-guide.md not found")
	}
	grouperContent, ok := skills.Get("grouper-design")
	if !ok {
		t.Fatal("skills/grouper-design.md not found")
	}

	t.Run("aggregators", func(t *testing.T) {
		for _, op := range aggregatorCapabilities() {
			op := op
			t.Run(op.Name, func(t *testing.T) {
				for _, key := range op.ComponentSchema.Keys {
					if !strings.Contains(aggContent, key.Name) {
						t.Errorf("aggregation-guide.md does not mention component key %q for %s (declare it under the operator's Components bullet)",
							key.Name, op.Name)
					}
				}
			})
		}
	})

	t.Run("groupers", func(t *testing.T) {
		for _, op := range grouperCapabilities() {
			op := op
			t.Run(op.Name, func(t *testing.T) {
				for _, key := range op.ComponentSchema.Keys {
					if !strings.Contains(grouperContent, key.Name) {
						t.Errorf("grouper-design.md does not mention component key %q for %s (declare it under the operator's Components bullet)",
							key.Name, op.Name)
					}
				}
			})
		}
	})

	// Filterers route to the filtering section of aggregation-guide.md
	// (per the Skill Pack routing table in CLAUDE.md). The universal
	// floor lives in the "Universal floor — `{n_in, n_out,
	// n_null_input}`" subsection; future per-filter specifics (e.g.
	// FILTER_RANGE n_below / n_above) extend that subsection inline.
	t.Run("filterers", func(t *testing.T) {
		for _, op := range filtererCapabilities() {
			op := op
			t.Run(op.Name, func(t *testing.T) {
				for _, key := range op.ComponentSchema.Keys {
					if !strings.Contains(aggContent, key.Name) {
						t.Errorf("aggregation-guide.md (filtering section) does not mention component key %q for %s (declare it under the filterer's Components bullet)",
							key.Name, op.Name)
					}
				}
			})
		}
	})
}

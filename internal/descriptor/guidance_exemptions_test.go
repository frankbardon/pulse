package descriptor

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ---- the guidance coverage exemption ledger --------------------------
//
// The coverage half of every guided-analysis gate is BINDING: a built-in
// without its Purpose, an inferential output without its Interpretation,
// an intent without three declaring operators or a tagged example, and a
// glossary term no Purpose links all fail — unless the gap is listed
// here. This file is the ONE place those exemptions live; a backfill
// closes a gap by declaring the guidance AND deleting its entry here (an
// entry whose gap is closed is stale and fails, so the two land together).
//
// Every entry carries a mandatory Why and an Owner: the roadmap unit
// (`docs/roadmap/units/<Owner>-*.md`) that will close the gap. When that
// unit's frontmatter flips to `status: done` the entry fails as stale —
// the owner shipped without closing it. Owner ownerPermanent marks a gap
// that is closed by design and never by a unit (today only the `lookup`
// intent, which routes to tooling, never an operator).

// ownerPermanent is the Owner of an exemption no roadmap unit will close.
const ownerPermanent = "permanent"

// guidanceExemption exempts one coverage gap, keyed per table.
type guidanceExemption struct {
	Key, Why, Owner string
}

// purposeExemptions — built-ins without a Purpose (TestSkillsCoverAllPurposes).
// Key: the operator name / TEST_* family / REG_* / OVERLAY_* kind / synth
// distribution kind, exactly as PurposeSurfaces lists it.
// Empty since the U09 backfill: every built-in declares its Purpose.
var purposeExemptions = []guidanceExemption{}

// interpretationExemptions — expected Interpretation outputs not yet
// declared (TestInterpretationCoversOutputs). Key: "<operator>:<field>",
// e.g. "TEST_T:p_value". Empty since the U09 backfill: every
// inferential output and every needs-reading descriptive operator's
// primary result (interpretation_reading.go) is read.
var interpretationExemptions = []guidanceExemption{}

// intentDeclarerExemptions — intents fewer than three built-in Purposes
// declare (TestPurposeQuestionsResolve). Key: the intent ID.
var intentDeclarerExemptions = []guidanceExemption{
	{Key: IntentLookup, Owner: ownerPermanent, Why: "Non-analytic intent that routes to the point-lookup tooling (pulse_lookup), never to an operator."},
	{Key: IntentFlows, Owner: "U28", Why: "Flow analysis needs the matrix overlays (stochastic matrices, steady states) U28 ships."},
	{Key: IntentMeasureConstruct, Owner: "U24", Why: "MAT_RELIABILITY and MAT_PCA declare it (two since U24 E3-S1); the 3-declarer floor still wants a third (U25's MAT_FACTOR, TODO #262, is the natural one)."},
}

// intentExampleExemptions — intents no example's _meta.intents tags
// (TestPurposeQuestionsResolve). Key: the intent ID.
var intentExampleExemptions = []guidanceExemption{
	{Key: IntentLookup, Owner: ownerPermanent, Why: "Non-analytic intent served by pulse_lookup; the example library holds request payloads, and a lookup is not one."},
	{Key: IntentFlows, Owner: "U28", Why: "No flow operator exists to exemplify until U28 ships the matrix overlays."},
	{Key: IntentSimulate, Owner: ownerPermanent, Why: "Synth specs are a separate surface (pulse synth); the raw internal/examples/synth/*.synth.json specs sit outside the embedded library and carry no _meta, so no library example can carry simulate."},
}

// exampleIntentExemptions — example directories holding at least one
// example with no _meta.intents (TestExamples_EveryExampleHasIntent).
// Key: the example category directory. Delete a directory's entry in the
// same change that tags its last untagged example.
var exampleIntentExemptions = []guidanceExemption{}

// glossaryOrphanExemptions — glossary terms no built-in Purpose links
// (TestGlossary_OrphanReport). Key: the term ID.
var glossaryOrphanExemptions = []guidanceExemption{
	// Matrix operators (correlation matrix, reliability, PCA).
	{Key: "factor", Owner: "U25", Why: "Defined as a latent factor (factor analysis), not a categorical grouping field, so GROUP_CATEGORY does not link it; the MAT_FACTOR operator (roadmap TODO #262, U25: factor analysis and rotation) does."},
	// Segmentation.
	{Key: "centroid", Owner: "U25", Why: "Written ahead of the segmentation operators U25 ships."},
	{Key: "distance", Owner: "U25", Why: "Written ahead of the segmentation operators U25 ships."},
	// Vector metrics.
	{Key: "similarity", Owner: "U27", Why: "Written ahead of the vector metrics U27 ships."},
	// Matrix overlays (raking, Markov flows).
	{Key: "raking", Owner: "U28", Why: "Written ahead of the raking overlay U28 ships."},
	{Key: "stochastic-matrix", Owner: "U28", Why: "Written ahead of the flow overlays U28 ships."},
	{Key: "steady-state", Owner: "U28", Why: "Written ahead of the flow overlays U28 ships."},
}

// exemptionTables is every ledger table above, keyed by its gate's
// table name. TestGuidanceExemptions_TablesComplete fails when a new
// `var ...Exemptions = []guidanceExemption` lands without a row here.
func exemptionTables() map[string][]guidanceExemption {
	return map[string][]guidanceExemption{
		"purpose":          purposeExemptions,
		"interpretation":   interpretationExemptions,
		"intent-declarers": intentDeclarerExemptions,
		"intent-example":   intentExampleExemptions,
		"example-intent":   exampleIntentExemptions,
		"glossary-orphan":  glossaryOrphanExemptions,
	}
}

// entriesOwnedBy returns "<table>/<key>" for every entry owned by unit,
// sorted.
func entriesOwnedBy(unit string, tables map[string][]guidanceExemption) []string {
	var out []string
	for table, ledger := range tables {
		for _, e := range ledger {
			if strings.TrimSpace(e.Owner) == unit {
				out = append(out, table+"/"+e.Key)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ---- applying a table ------------------------------------------------

// unitStatusReader returns a roadmap unit's frontmatter status, ok=false
// when no unit doc carries that ID. Injected so tests never depend on the
// live roadmap.
type unitStatusReader func(unit string) (status string, ok bool)

var ownerUnitPattern = regexp.MustCompile(`^U[0-9]+[a-z]?$`)

// applyExemptions drops exempted gaps from gaps and reports every ledger
// problem: an entry with no Why or no Owner, an Owner that is neither
// ownerPermanent nor a known unit, a non-permanent entry whose owner unit
// is done, a key listed twice, and an entry matching no current gap.
func applyExemptions(table string, gaps []string, ledger []guidanceExemption, status unitStatusReader) (remaining, problems []string) {
	entry := func(e guidanceExemption) string { return table + " exemption " + e.Key }
	byKey := map[string]bool{}
	for _, e := range ledger {
		if byKey[e.Key] {
			problems = append(problems, entry(e)+" is listed twice")
		}
		byKey[e.Key] = true
		if strings.TrimSpace(e.Why) == "" {
			problems = append(problems, entry(e)+" has no justification")
		}
		switch owner := strings.TrimSpace(e.Owner); {
		case owner == "":
			problems = append(problems, entry(e)+" has no owner unit")
		case owner == ownerPermanent:
		case !ownerUnitPattern.MatchString(owner):
			problems = append(problems, entry(e)+" owner "+owner+" is not a roadmap unit ID or "+ownerPermanent)
		default:
			st, ok := status(owner)
			if !ok {
				problems = append(problems, entry(e)+" owner "+owner+" has no docs/roadmap/units doc")
			} else if st == "done" {
				problems = append(problems, entry(e)+" is stale: owner "+owner+" is done without closing the gap")
			}
		}
	}
	open := map[string]bool{}
	for _, g := range gaps {
		open[g] = true
		if !byKey[g] {
			remaining = append(remaining, g)
		}
	}
	for _, e := range ledger {
		if !open[e.Key] {
			problems = append(problems, entry(e)+" is stale: the gap is covered, delete the entry")
		}
	}
	sort.Strings(remaining)
	return remaining, problems
}

// roadmapUnitStatus reads every docs/roadmap/units/*.md frontmatter `id`
// and `status` (repo-relative to this package) — the live reader the
// gates use. Test-only: production code never reads the roadmap.
func roadmapUnitStatus() unitStatusReader {
	return roadmapUnitStatusIn(filepath.Join("..", "..", "docs", "roadmap", "units"))
}

// roadmapUnitStatusIn is roadmapUnitStatus over any units directory.
func roadmapUnitStatusIn(dir string) unitStatusReader {
	st := map[string]string{}
	paths, _ := filepath.Glob(filepath.Join(dir, "U*.md"))
	for _, p := range paths {
		id, status := readUnitFrontmatter(p)
		if id != "" {
			st[id] = status
		}
	}
	return func(unit string) (string, bool) {
		s, ok := st[unit]
		return s, ok
	}
}

// readUnitFrontmatter returns the id and status of one unit doc's
// leading `---` frontmatter block.
func readUnitFrontmatter(path string) (id, status string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; sc.Scan(); i++ {
		line := strings.TrimSpace(sc.Text())
		if line == "---" {
			if i == 0 {
				continue
			}
			break
		}
		if i == 0 {
			return "", ""
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			switch strings.TrimSpace(k) {
			case "id":
				id = strings.TrimSpace(v)
			case "status":
				status = strings.TrimSpace(v)
			}
		}
	}
	return id, status
}

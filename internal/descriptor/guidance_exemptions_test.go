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
var purposeExemptions = func() []guidanceExemption {
	const why = "Descriptive built-in awaiting its Purpose in the U09 backfill."
	var out []guidanceExemption
	for _, names := range [][]string{
		// filterer
		{"FILTER_DATE_RANGES", "FILTER_EXCLUDE", "FILTER_EXPRESSION", "FILTER_FALSE",
			"FILTER_INCLUDE", "FILTER_NULL", "FILTER_RANGE", "FILTER_SET_CONTAINS_ALL",
			"FILTER_SET_CONTAINS_ANY", "FILTER_SET_CONTAINS_NONE", "FILTER_SET_EQUALS", "FILTER_TRUE"},
		// grouper
		{"GROUP_CATEGORY", "GROUP_DATE", "GROUP_DATE_RANGES", "GROUP_QUANTILE", "GROUP_RANGE",
			"GROUP_ROUNDED", "GROUP_SET_PER_ELEMENT", "GROUP_SET_VALUE"},
		// synth_distribution
		{"bernoulli", "constant", "discrete", "exponential", "lognormal", "mixture",
			"monotonic_from", "normal", "pareto", "poisson", "regex", "set_bernoulli", "uniform",
			"uniform_date", "weighted_categorical"},
	} {
		for _, n := range names {
			out = append(out, guidanceExemption{Key: n, Why: why, Owner: "U09"})
		}
	}
	return out
}()

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
	{Key: IntentMeasureConstruct, Owner: "U24", Why: "Construct measurement needs the reliability / PCA operators U24 ships."},
	{Key: IntentSegment, Owner: "U09", Why: "Declaring operators (groupers) gain Purposes in the U09 backfill."},
	{Key: IntentSimulate, Owner: "U09", Why: "Declaring operators (synth distributions) gain Purposes in the U09 backfill."},
}

// intentExampleExemptions — intents no example's _meta.intents tags
// (TestPurposeQuestionsResolve). Key: the intent ID.
var intentExampleExemptions = []guidanceExemption{
	{Key: IntentLookup, Owner: ownerPermanent, Why: "Non-analytic intent served by pulse_lookup; the example library holds request payloads, and a lookup is not one."},
	{Key: IntentFlows, Owner: "U28", Why: "No flow operator exists to exemplify until U28 ships the matrix overlays."},
	{Key: IntentMeasureConstruct, Owner: "U24", Why: "No construct-measurement operator exists to exemplify until U24 ships."},
	{Key: IntentBenchmark, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentChangeOverTime, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentCompareGroups, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentComposition, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentDataQuality, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentDescribe, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentDistributionShape, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentDrivers, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentPrepare, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentRelationship, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentSegment, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
	{Key: IntentSimulate, Owner: "U09", Why: "Examples gain _meta.intents tags in the U09 backfill."},
}

// glossaryOrphanExemptions — glossary terms no built-in Purpose links
// (TestGlossary_OrphanReport). Key: the term ID.
var glossaryOrphanExemptions = []guidanceExemption{
	// Descriptive terms a U09 Purpose may link (or the term is dropped).
	{Key: "covariance", Owner: "U09", Why: "Linked or dropped when the U09 descriptive Purposes land."},
	{Key: "factor", Owner: "U09", Why: "Linked or dropped when the U09 descriptive Purposes land."},
	{Key: "pairwise-deletion", Owner: "U09", Why: "Linked or dropped when the U09 descriptive Purposes land."},
	{Key: "statistical-significance", Owner: "U09", Why: "Linked or dropped when the U09 descriptive Purposes land."},
	{Key: "test-statistic", Owner: "U09", Why: "Linked or dropped when the U09 descriptive Purposes land."},
	// Matrix operators (reliability, PCA).
	{Key: "eigenvalue", Owner: "U24", Why: "Written ahead of the PCA operator U24 ships."},
	{Key: "loading", Owner: "U24", Why: "Written ahead of the PCA operator U24 ships."},
	{Key: "principal-component", Owner: "U24", Why: "Written ahead of the PCA operator U24 ships."},
	{Key: "reliability", Owner: "U24", Why: "Written ahead of the reliability operator U24 ships."},
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

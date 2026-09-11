package synth

import "fmt"

// This file is the firing counter for the rule pass: how many GENERATED
// rows each compiled rule actually applied to, and the warning raised
// for a rule that applied to none.
//
// # Why a rule that never fires is a fault rather than a curiosity
//
// A rule naming a mistyped field, an uncompilable predicate or a literal
// the target cannot hold is refused at spec parse (validateRules). A
// rule whose `when` is simply never TRUE is refused by nothing: it
// validates, it compiles, it applies to nothing, and the cohort
// generates cleanly with the structural fact the author wrote it for
// still absent. Nothing downstream can tell that cohort from one where
// the rule worked.
//
// That is the fourth instance of one failure class in this package, and
// in all four the output looked plausible: a fidelity report scoring
// conditional pairs generation never applied (v0.32.2), 85 of 105
// captured models silently unapplied and every packed_bool prevalence
// inverted (v0.33.0), and an emitted spec that parsed cleanly and then
// refused at generation (E2-S1). So the classification follows the
// package's standing rule rather than a judgement about severity: a
// requested step that did not happen needs ATTENTION, never the
// expected-outcome count — the exact inverse of the benign `default:`
// arm that cost 80% of --fit-models.
//
// The most common CAUSE is worth naming in the message rather than
// leaving to be rediscovered: the row holds the sampler's float and the
// wire holds round(f), so `when: "nps == 9"` over a numeric reconstructed
// as a continuous distribution fires only on the draws landing exactly
// on 9. E2-S1 measured that gate at 1,876 of 2,688 wire-value-1 rows
// without an int() normalisation and 3,815 of 3,815 with it.
//
// # What the count is over
//
// ACCEPTED rows — the rows that reached the file — never draws. generate()
// re-draws a whole row when a constraint rejects it, so a firing on a
// rejected attempt left no trace in the cohort, and a count including it
// cannot be divided by the row count to get a rate. That is the one
// arithmetic a reader will do with the number, so the counter commits
// per accepted row (see commitRow) rather than incrementing in apply.
//
// # Cost
//
// Two slices sized once at compile time, len(rules) each: a per-row
// scratch of which rules fired and the running totals. The pass still
// allocates nothing per row and consumes no RNG, so a spec's generated
// bytes are untouched by the counting.

// maxNeverFiredRuleWarnings caps how many individual never-fired rules
// are named before the rest collapse into one counted roll-up line.
//
// It matches maxThinLevelWarnings / maxThinResidualPairWarnings (20) for
// the same reason they match each other: these lines share one warning
// slice, and a reader who has learnt what a truncated listing looks like
// in one kind should not have to learn a second shape. The cap matters
// for the spec whose rules ALL gate on the same column — a `familiarity`
// block rewritten as one rule per target field is 50 rules that fire
// together or not at all, so the finding is one thing said fifty times
// and the listing is the second bound on it (the terminal summary's own
// 3-example cap is the third, and is a screen budget rather than a
// document one).
const maxNeverFiredRuleWarnings = 20

// ruleFiring is one compiled rule's firing report: the index that
// addresses it in Spec.Rules, the predicate that gated it, and the
// number of GENERATED rows it applied to.
//
// The count is exposed rather than only the zero case, because "fired on
// N rows" is the figure a calibration run wants and a figure recomputed
// later is a figure that drifts. It deliberately does NOT ride
// synth.Result: that struct is the `--json` payload and a new slot on it
// would move the envelope shape for every caller, for a diagnostic the
// warning channel already carries.
type ruleFiring struct {
	index int
	when  string
	rows  int
}

// noteFired records that rule i applied to the row currently being
// drawn. The row is not yet accepted; commitRow decides that.
func (a *ruleApplier) noteFired(i int) { a.rowFired[i] = true }

// commitRow folds the just-ACCEPTED row's firings into the totals.
// generate() calls it after the row has passed the constraint pass and
// been encoded; a rejected row is simply never committed and its
// firings are discarded when the next apply clears the scratch.
func (a *ruleApplier) commitRow() {
	if a == nil {
		return
	}
	for i, fired := range a.rowFired {
		if fired {
			a.firings[i]++
		}
	}
}

// firingCounts returns one report per compiled rule, in Spec.Rules
// order.
func (a *ruleApplier) firingCounts() []ruleFiring {
	if a == nil {
		return nil
	}
	out := make([]ruleFiring, 0, len(a.rules))
	for i := range a.rules {
		out = append(out, ruleFiring{
			index: a.rules[i].index,
			when:  a.rules[i].whenSrc,
			rows:  a.firings[i],
		})
	}
	return out
}

// neverFiredWarnings reports every compiled rule that applied to none of
// the `rows` generated rows, in declaration order, bounded by
// maxNeverFiredRuleWarnings with a counted roll-up for the rest.
//
// Declaration order rather than a severity order: every zero is equally
// zero, and the index is the author's handle back into the document, so
// the truncated tail is the tail of the file they are reading.
func (a *ruleApplier) neverFiredWarnings(rows int) []string {
	if a == nil {
		return nil
	}
	var never []ruleFiring
	for _, f := range a.firingCounts() {
		if f.rows == 0 {
			never = append(never, f)
		}
	}
	if len(never) == 0 {
		return nil
	}
	shown := never
	if len(shown) > maxNeverFiredRuleWarnings {
		shown = shown[:maxNeverFiredRuleWarnings]
	}
	out := make([]string, 0, len(shown)+1)
	for _, f := range shown {
		out = append(out, ruleNeverFiredWarning(f.index, f.when, rows))
	}
	if rest := len(never) - len(shown); rest > 0 {
		out = append(out, fmt.Sprintf(
			"+%d further rule(s) never fired; listing suppressed to keep the warning list readable — "+
				"their predicates are in the spec's \"rules\" array at the indices above %d",
			rest, shown[len(shown)-1].index))
	}
	return out
}

// ruleNeverFiredWarning names one rule that applied to nothing.
//
// It carries the index (a rule has no name of its own, and the index is
// the same handle validateRules' errors use), the predicate verbatim,
// and the row count the zero is out of — so the line is a statement
// about THIS run rather than an assertion a reader has to go and check.
func ruleNeverFiredWarning(idx int, when string, rows int) string {
	if when == "" {
		// Unreachable through generate(), which refuses RowCount <= 0: a
		// rule with no `when` applies to every row. Stated rather than
		// panicked because a hand-built applier can reach it.
		return fmt.Sprintf("rule %d never fired: it declares no when (every row) but %d row(s) were generated",
			idx, rows)
	}
	return fmt.Sprintf("rule %d never fired: when %q was true on 0 of %d generated row(s), "+
		"so the rule applied to nothing; note a numeric compares PRE-ROUNDING, "+
		"so normalise first (an earlier rule setting int(field)) or compare a range",
		idx, when, rows)
}

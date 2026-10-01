package synth

import (
	"fmt"
	"strings"
)

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
	// attempted is true when the rule selected at least one row on at
	// least one DRAW, whether or not that row survived the constraint
	// pass. Together with rows == 0 it is the only evidence that
	// separates "the predicate never matched anything" from "the
	// predicate matched and a constraint threw the rows away", and the
	// two have opposite remedies — see ruleNeverFiredWarning.
	attempted bool
	// reads is the declared fields the predicate reads, classified by
	// whether a comparison against each one is pre-rounding
	// (synth/rules_firing_cause.go). Empty for a rule with no `when`,
	// and for a predicate reading no declared field.
	reads []whenField
}

// noteFired records that rule i applied to the row currently being
// drawn. The row is not yet accepted; commitRow decides that.
//
// everFired is set here and NEVER cleared, which is the whole difference
// between the two slices: rowFired describes the row being drawn and
// firings counts the rows that reached the file, so a rule that fired
// only on constraint-rejected attempts leaves both at zero — correctly,
// because the file shows no trace of it. everFired is the memory of the
// attempt, and it exists so the never-fired warning can name the
// CONSTRAINT as the cause instead of guessing at the predicate.
func (a *ruleApplier) noteFired(i int) {
	a.rowFired[i] = true
	a.everFired[i] = true
}

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
			index:     a.rules[i].index,
			when:      a.rules[i].whenSrc,
			rows:      a.firings[i],
			attempted: a.everFired[i],
			reads:     a.rules[i].whenReads,
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
		out = append(out, ruleNeverFiredWarning(f, rows))
	}
	if rest := len(never) - len(shown); rest > 0 {
		out = append(out, fmt.Sprintf(
			"+%d further rule(s) never fired; listing suppressed to keep the warning list readable — "+
				"their predicates are in the spec's \"rules\" array at the indices above %d",
			rest, shown[len(shown)-1].index))
	}
	return out
}

// ruleNeverFiredWarning names one rule that applied to nothing, and
// names the cause that applies to THAT rule.
//
// It carries the index (a rule has no name of its own, and the index is
// the same handle validateRules' errors use), the predicate verbatim,
// and the row count the zero is out of — so the line is a statement
// about THIS run rather than an assertion a reader has to go and check.
//
// Three arms, in order of how directly the evidence supports them.
//
// # 1. The constraint arm
//
// The rule DID select rows; a constraint rejected every one of them and
// generate() re-drew. Detected, not guessed (ruleFiring.attempted), so
// the line says so outright: nothing about the predicate's arithmetic is
// wrong and the remedy is the constraint, not a normalisation. This arm
// exists because the single pre-rounding message sent this case to the
// wrong place entirely.
//
// # 2. The pre-rounding arm
//
// The predicate compares a field whose ROW value may differ from the
// value the FILE holds. The remedy it names is round(), not int(). An
// integer field is stored as floor(v+0.5) (writeFieldValueForField), so
// round(v) is the one expression that reproduces the value the FILE
// holds: the gate then selects exactly the rows a reader sees, and the
// stored column does not move. int(v) truncates, which widens the gate
// by moving VALUES down instead — measured on the motivating profile at
// 20,000 rows, a `familiarity <= 1` gate fires on 1,843 rows raw, 2,739
// behind round() with the column unchanged, and 3,824 behind int(),
// which gets there by dropping 1,085 respondents a point.
//
// # 3. The neutral arm
//
// No constraint evidence and no pre-rounding suspect. The line names the
// fields the predicate reads and how each is reconstructed, because that
// is the whole of what the spec can say, and it explicitly RULES OUT
// pre-rounding for any field that quantizes on write but draws exact
// values — a `discrete` integer column or a `bernoulli` packed_bool,
// where the row value already IS the stored value. Sending an author to
// normalise one of those is worse than saying nothing: the gate they
// would change is already exact.
func ruleNeverFiredWarning(f ruleFiring, rows int) string {
	if f.when == "" {
		// Unreachable through generate(), which refuses RowCount <= 0: a
		// rule with no `when` applies to every row. Stated rather than
		// panicked because a hand-built applier can reach it.
		return fmt.Sprintf("rule %d never fired: it declares no when (every row) but %d row(s) were generated",
			f.index, rows)
	}
	head := fmt.Sprintf("rule %d never fired: when %q was true on 0 of %d generated row(s)", f.index, f.when, rows)
	if f.attempted {
		return head + ", although it DID select rows that a constraint then rejected; " +
			"a rejected row is re-drawn and leaves no trace, so the rule reached nothing in the file — " +
			"the cause is the constraint, not the predicate: relax it, or widen the rule so it also " +
			"selects rows the constraints accept"
	}
	head += ", so the rule applied to nothing"
	var preRounded, exact []whenField
	for _, r := range f.reads {
		if r.preRounded {
			preRounded = append(preRounded, r)
			continue
		}
		if quantizingFieldTypes[r.typeName] {
			exact = append(exact, r)
		}
	}
	if len(preRounded) > 0 {
		return head + fmt.Sprintf("; it compares %s, whose row value is PRE-ROUNDING — the file holds "+
			"round(v) — so normalise first (an earlier rule setting round(field)) or compare a range",
			describeWhenFields(preRounded))
	}
	if len(f.reads) == 0 {
		return head + "; its predicate reads no declared field, so nothing in the generated data can satisfy it"
	}
	out := head + fmt.Sprintf("; it reads %s, and only the values those fields actually generate can match it — "+
		"check the predicate against each field's own reconstruction", describeWhenFields(f.reads))
	if len(exact) > 0 {
		out += fmt.Sprintf("; pre-rounding is NOT the cause for %s, whose row value already is the stored value",
			describeWhenFieldNames(exact))
	}
	return out
}

// maxNamedWhenFields bounds how many fields one never-fired line names.
// A predicate over a dozen columns would otherwise produce a line nobody
// reads, and the index in the message is the handle to the rest.
const maxNamedWhenFields = 4

// describeWhenFields renders fields as `"name" (type, distribution)`,
// which is exactly the two facts that decide whether a comparison
// against the field can match: what the schema declares and how
// generation reconstructs it.
func describeWhenFields(fs []whenField) string {
	parts := make([]string, 0, len(fs))
	for i, f := range fs {
		if i == maxNamedWhenFields {
			parts = append(parts, fmt.Sprintf("and %d more", len(fs)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%q (%s, %s)", f.name, f.typeName, f.dist))
	}
	return strings.Join(parts, ", ")
}

// describeWhenFieldNames renders bare quoted names, for the clause that
// has already stated the types.
func describeWhenFieldNames(fs []whenField) string {
	parts := make([]string, 0, len(fs))
	for i, f := range fs {
		if i == maxNamedWhenFields {
			parts = append(parts, fmt.Sprintf("and %d more", len(fs)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%q", f.name))
	}
	return strings.Join(parts, ", ")
}

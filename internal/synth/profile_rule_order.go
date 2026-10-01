package synth

import (
	"fmt"
	"sort"
	"strings"
)

// EMITTED ORDER IS APPLIED ORDER, so the detector has to CHOOSE one.
//
// The rule engine itself never reorders anything — RuleSpec's own doc is
// emphatic that rules apply in declaration order and are deliberately
// NOT topologically sorted, because a sort at APPLY time would make the
// applied order implicit and declaration order is the one ordering an
// author can read off the document. Nothing here changes that. This is
// the PROPOSAL side: three detectors each produce a list, they have to
// be concatenated in some order, and the order the analyst reads is the
// order the engine will run. Choosing it well is the detector's job.
//
// # The rule
//
// A candidate that WRITES a field another candidate READS is emitted
// BEFORE it. Within that constraint the detector order — gating, then
// co-missing blocks, then exact dependencies — is preserved as closely
// as possible: candidates are walked in detector order and a candidate a
// walked one depends on is HOISTED to just before it, never further.
//
// # Why it is not simply "gates, then blocks, then dependencies"
//
// That was the order through E3-S3, defended by an equivalence argument
// that is true and is about the wrong half of a rule. The argument: a
// block is admitted only when its members are null on exactly the same
// rows, so a gate classifies them identically and takes a WHOLE block or
// none of one — therefore, for candidates AS DETECTED, gate-then-block
// and block-then-gate produce the same nulls, and gate-first is the
// better choice because it is the order in which a block REPAIRS a
// set_null the analyst has narrowed by hand.
//
// That holds for a gate's TARGETS. It is false for a gate's SOURCE. When
// a block member is also the gate FIELD of a gating candidate, a block
// placed after that gate moves the gate's own INPUT after the gate has
// already read it: the gate fires on the drawn value, the block then
// nulls the field the gate was reading, and the target it decided to
// leave present is now a value on a row whose screener is absent. On the
// motivating cohort five gates sit in exactly that shape (`peopleAware`,
// `promotionAware`, `placementAware`, `productAware`, `priceAware` are
// all members of the 50-field block) and it left 8,693 of 20,000
// generated rows carrying an answer to a question the file says was
// never asked — the precise incoherence the rule layer exists to remove.
//
// The same shape reaches ACROSS detectors in the other direction: a
// dependency candidate's set_expr writes a field a gating candidate's
// `when` reads (`aware = round(familiarity) >= 2` against a gate on
// `aware`), and dependencies were emitted last.
//
// # What it costs
//
// A block hoisted ahead of a gate loses, FOR THAT GATE, the repair
// property the old order was chosen for: if the analyst narrows that
// gate's target list by hand and the hoisted block covers the removed
// targets, the block no longer follows the edit and no longer repairs
// it. That is the trade and it is not symmetric — the repair property
// protects an edit that may never be made, while the ordering fault
// corrupts every generated row unconditionally. Only the candidates that
// MUST move, move: on the motivating cohort the headline gate stays
// first and one block is hoisted ahead of the five gates that read it.
//
// # Cycles
//
// Two candidates can each write a field the other reads. No linear order
// satisfies both, so the edge that CLOSES the cycle during the walk is
// dropped and the pair is emitted in the order the surviving edge
// dictates — which can invert their detector order, because the
// surviving edge is a real constraint and the detector order is only a
// preference. One of the two then reads a value the other has not
// written yet, and that is REPORTED rather than resolved silently: an
// arbitrary choice here is indistinguishable from a correct one in the
// output, which is this package's standing failure mode. It does not
// arise on the motivating cohort.

// ruleCandidateOrder walks the concatenated candidate list in detector
// order and returns it reordered so that every writer precedes every
// reader of the same field. cands must already be in the preferred
// order (gating, co-missing, dependency); the walk perturbs it only
// where a dependency forces it.
func ruleCandidateOrder(cands []RuleSpec, warnings *[]string) []RuleSpec {
	if len(cands) < 2 {
		return cands
	}

	writes := make([]map[string]bool, len(cands))
	written := map[string]bool{}
	for i := range cands {
		writes[i] = ruleWrittenFields(cands[i])
		for f := range writes[i] {
			written[f] = true
		}
	}
	// Reads are only interesting when some OTHER candidate writes the
	// field: a predicate over a column no rule touches cannot be
	// invalidated by rule order. Intersecting against `written` is also
	// what keeps the identifier scan honest without a parser — `round`
	// and `isnull` are identifiers too, and they are not fields.
	reads := make([]map[string]bool, len(cands))
	for i := range cands {
		reads[i] = ruleReadFields(cands[i], written)
	}

	// preds[i] is every candidate that must be emitted before i.
	preds := make([][]int, len(cands))
	for i := range cands {
		for j := range cands {
			if i == j {
				continue
			}
			for f := range reads[i] {
				if writes[j][f] {
					preds[i] = append(preds[i], j)
					break
				}
			}
		}
	}

	const (
		unvisited = 0
		onStack   = 1
		emitted   = 2
	)
	state := make([]int, len(cands))
	out := make([]RuleSpec, 0, len(cands))
	var cycles []string

	var visit func(i int)
	visit = func(i int) {
		if state[i] != unvisited {
			return
		}
		state[i] = onStack
		for _, p := range preds[i] {
			switch state[p] {
			case onStack:
				// Mutual dependency. Drop this edge, keep the detector
				// order for the pair, and say so.
				cycles = append(cycles, ruleCyclePair(cands, p, i))
			case unvisited:
				visit(p)
			}
		}
		state[i] = emitted
		out = append(out, cands[i])
	}
	for i := range cands {
		visit(i)
	}
	if len(cycles) > 0 {
		sort.Strings(cycles)
		*warnings = append(*warnings, ruleOrderCycleWarning(cycles))
	}
	return out
}

// ruleWrittenFields is every field a rule can change: the four ACTION
// slots and nothing else. `when` is a predicate and writes nothing.
func ruleWrittenFields(r RuleSpec) map[string]bool {
	out := make(map[string]bool, len(r.SetNull)+len(r.NullTogether)+len(r.Set)+len(r.SetExpr))
	for _, f := range r.SetNull {
		out[f] = true
	}
	for _, f := range r.NullTogether {
		out[f] = true
	}
	for f := range r.Set {
		out[f] = true
	}
	for f := range r.SetExpr {
		out[f] = true
	}
	return out
}

// ruleReadFields is every field a rule's EXPRESSIONS look at, restricted
// to fields some candidate writes.
//
// The scan is an identifier tokeniser rather than an expr parse, and the
// restriction is what makes that exact enough: an identifier that is not
// a field written by another candidate cannot produce an edge, so the
// builtins (`round`, `isnull`) and any keyword drop out without being
// enumerated — a list that would go stale the first time the expression
// environment gains a function. A field name appearing inside a string
// literal would be a false edge; the detectors emit none, and a spurious
// edge costs an unnecessary hoist rather than a wrong answer.
//
// It deliberately does NOT read RuleEvidence.GateField /
// .SourceField. Those are correct for what the detectors emit today and
// would silently order nothing for a rule shape that arrives later with
// a predicate over two fields.
func ruleReadFields(r RuleSpec, written map[string]bool) map[string]bool {
	out := map[string]bool{}
	scan := func(src string) {
		for _, id := range exprIdentifiers(src) {
			if written[id] {
				out[id] = true
			}
		}
	}
	scan(r.When)
	for _, src := range r.SetExpr {
		scan(src)
	}
	return out
}

// exprIdentifiers splits an expression into Go-shaped identifiers.
func exprIdentifiers(src string) []string {
	if src == "" {
		return nil
	}
	var out []string
	start := -1
	for i := 0; i <= len(src); i++ {
		var c byte
		if i < len(src) {
			c = src[i]
		}
		isStart := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isPart := isStart || (c >= '0' && c <= '9')
		switch {
		case start < 0 && isStart:
			start = i
		case start >= 0 && !isPart:
			out = append(out, src[start:i])
			start = -1
		}
	}
	return out
}

// ruleCyclePair renders one dropped ordering edge for the warning.
func ruleCyclePair(cands []RuleSpec, a, b int) string {
	return ruleCandidateLabel(cands, a) + " <-> " + ruleCandidateLabel(cands, b)
}

// ruleCandidateLabel names a candidate the way the file shows it: its
// detector plus the field the reader most easily matches back.
func ruleCandidateLabel(cands []RuleSpec, i int) string {
	r := cands[i]
	det := "rule"
	if r.Evidence != nil && r.Evidence.Detector != "" {
		det = r.Evidence.Detector
	}
	switch {
	case r.Evidence != nil && r.Evidence.GateField != "":
		return det + "(" + r.Evidence.GateField + ")"
	case r.Evidence != nil && r.Evidence.SourceField != "":
		return det + "(" + r.Evidence.SourceField + ")"
	case len(r.NullTogether) > 0:
		return det + "(" + r.NullTogether[0] + ")"
	default:
		return det
	}
}

func ruleOrderCycleWarning(pairs []string) string {
	return fmt.Sprintf(
		"rule suggestion: %d candidate pair(s) each write a field the other reads, so no emitted order can let "+
			"both read the final value — the later ordering edge was dropped and the pair keeps its detector "+
			"order, which means one of the two reads a value the other has not written yet: %s",
		len(pairs), strings.Join(pairs, ", "))
}

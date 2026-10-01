package synth

import (
	"fmt"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// This file answers one question for resolveConflicts: which fields
// does a structural rule DETERMINE outright, so that nothing upstream
// should spend a stage producing a value the rule discards?
//
// # Why a rule claims at all
//
// The rule pass runs LAST in drawRow, after the model stage, and an
// unconditional `set` / `set_expr` overwrites its target on every row.
// Everything the model stage, the five conditional-pair stages and the
// copula did for that field is therefore thrown away — which is merely
// wasteful in the file, and actively misleading everywhere else. The
// fidelity report is the case that matters: BuildModelFidelity asks
// generation's own compiler which models ran (resolveConflicts ->
// buildModelDrawers) and refits each one against the generated
// partition, so a modelled field a rule overwrites produces a
// captured-versus-recovered coefficient delta for a value NOTHING KEPT,
// with no slot on the report saying so. That is the same silent-inertness
// class as v0.32.2 (scoring pairs generation never applied) and the
// E2-S5 predictor-kind default (85 of 105 captured models disabled with
// a plausible cohort either way): every number renders, and the only
// signal is that it describes something that did not happen.
//
// Pre-claiming at priority 0 removes the work AND the false report in
// one move, because the fidelity section is derived from the same
// arbitration.
//
// # What claims, and the three exclusions
//
// A claim is keyed on rules that SUPPLY a value and supply it on EVERY
// row from inputs the field's own generation does not provide. Three
// exclusions follow, and each of them is silent if it is got backwards:
//
//   - A rule carrying `when` writes only SOME rows, so claiming its
//     target would strip the model from every row the rule never
//     touches. The remaining rows would still carry a plausible value —
//     the field's bare marginal — and nothing would report the loss.
//     The restriction costs little in practice, because `set_expr`
//     collapses the motivating three-band case ("promoter is nps >= 9")
//     into ONE unconditional rule.
//
//   - `set_null` NEVER claims, at any conditionality. It removes a
//     value rather than supplying one, and the semantics of the pass
//     are `if gate then null else inferred` — the field's own model,
//     pair or correlation is exactly what produces the value the
//     non-gated rows keep. Claiming a set_null target would delete that
//     inference everywhere the rule did not gate.
//
//   - `null_together` supplies no value either; it copies one null
//     DECISION across a block. Same reasoning, same answer.
//
// # The self-referential set_expr, and why it is a fourth exclusion
//
// A `set_expr` whose expression READS ITS OWN TARGET does not determine
// the field — it TRANSFORMS whatever generation produced for it. The
// canonical instance is the normalisation idiom this package documents
// as the remedy for the pre-rounding gotcha:
//
//	{"set_expr": {"nps": "int(nps)"}}
//
// Claiming `nps` there would strip its model, its conditional pairs and
// its residual correlations and leave `int()` applied to a bare
// marginal draw — the documented FIX for one silent fault silently
// causing a larger one. So a self-reference is read as "this rule
// depends on the field's own generation", which is the same test the
// other three exclusions apply, and the field keeps its stage.
//
// The reference is detected on the PARSED expression rather than by
// substring (a rule over `nps` must not be excluded by a mention of
// `nps_reason`). Detection is conservative in the one direction that
// cannot lose structure: an expression that will not parse — unreachable
// on any path that generates, because validateRules refuses it at spec
// parse — is treated as a read, so the field keeps its model.

// ruleClaim is one (target, reason) pair a structural rule contributes
// to resolveConflicts' claim map. The desc is user-facing: it is
// interpolated into the "already claimed by %s" half of every conflict
// warning, so it reads as a REASON with the rule's index — a rule has
// no name of its own, and the index is the only handle back to the
// document (the same convention every PULSE_SYNTH_RULE_* error uses).
type ruleClaim struct {
	target claimTarget
	desc   string
}

// ruleClaims returns the pre-claims for rules, in declaration order
// with each rule's slots walked in a fixed order and each map's keys
// sorted. Never Go map order: the claim order decides which of two
// competing descriptions a warning names, and that must not depend on a
// map seed.
//
// See this file's header for the keying rule and its four exclusions.
func ruleClaims(rules []RuleSpec) []ruleClaim {
	if len(rules) == 0 {
		return nil
	}
	var out []ruleClaim
	for i, r := range rules {
		if r.When != "" {
			continue
		}
		for _, name := range sortedKeys(r.Set) {
			out = append(out, ruleClaim{
				target: claimTarget{field: name},
				desc:   ruleClaimDesc(i, "set"),
			})
		}
		for _, name := range sortedKeys(r.SetExpr) {
			if exprReadsIdentifier(r.SetExpr[name], name) {
				continue
			}
			out = append(out, ruleClaim{
				target: claimTarget{field: name},
				desc:   ruleClaimDesc(i, "set_expr"),
			})
		}
	}
	return out
}

// ruleClaimDesc is the single spelling of the claim reason. One
// function so the wording cannot drift between the two slots.
func ruleClaimDesc(idx int, slot string) string {
	return fmt.Sprintf("structural rule %d (%s)", idx, slot)
}

// exprReadsIdentifier reports whether src names ident anywhere in its
// syntax tree.
//
// Parsed rather than substring-matched, and parsed from the AUTHOR'S
// SOURCE rather than from a compiled program: a compiler is free to
// fold and rewrite, and the question here is what the author wrote.
// An unparseable expression answers true — see this file's header for
// why that is the conservative direction.
func exprReadsIdentifier(src, ident string) bool {
	tree, err := parser.Parse(src)
	if err != nil || tree == nil || tree.Node == nil {
		return true
	}
	probe := &identifierProbe{want: ident}
	ast.Walk(&tree.Node, probe)
	return probe.found
}

// identifierProbe is an ast.Visitor that records whether a given
// identifier appears. Function callees are identifiers too, so a field
// sharing a builtin's name (`int`) reads as a self-reference and simply
// does not claim — the conservative direction again.
type identifierProbe struct {
	want  string
	found bool
}

func (p *identifierProbe) Visit(node *ast.Node) {
	if id, ok := (*node).(*ast.IdentifierNode); ok && id.Value == p.want {
		p.found = true
	}
}

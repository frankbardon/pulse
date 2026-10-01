package synth

import (
	"math"
	"sort"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// This file answers the second question the firing counter raises. The
// counter (synth/rules_firing.go) knows THAT a rule applied to nothing;
// this file works out WHY, well enough for the warning to name the cause
// that actually applies to the rule in front of it rather than the one
// that was most common when the message was first written.
//
// # Why the single message was a defect, not an imprecision
//
// The original never-fired line always named the PRE-ROUNDING gotcha:
// the row holds the sampler's float while the file holds round(f), so
// `when: "nps == 9"` over a continuously-reconstructed integer column
// fires only on the draws landing exactly on 9. That was the right
// diagnosis for the shape that motivated the warning, and it is still
// the right diagnosis for an f32/f64 column, an integer column too wide
// for maxDiscreteLevels, and a hand-authored continuous distribution on
// an integer field.
//
// It stopped being right for everything else. Once a small integer
// column reconstructs as `discrete` and a packed_bool as `bernoulli`,
// the ROW VALUE FOR THOSE FIELDS ALREADY IS THE STORED VALUE — the
// staircase and the step emit exactly the levels the writer stores,
// through the field's own sampler, through a conditional pair and
// through the composed model draw alike. A message telling an author to
// normalise `round(nps)` when `nps` is a `discrete` u4 is not merely
// vague: it sends them to change a gate that is already exact, and the
// real cause (a level the support does not carry, a gate that is simply
// too narrow) goes unstated.
//
// # And a rule CAN fire without reaching the file
//
// The count is over ACCEPTED rows, deliberately (see rules_firing.go).
// A rule can therefore select rows on every attempt and still report
// zero, because a constraint rejected every one of them. The cause there
// is the CONSTRAINT and nothing about the predicate's arithmetic — so it
// gets its own arm, detected rather than guessed: noteFired records the
// attempt, commitRow records the acceptance, and a rule with attempts
// and no acceptances is exactly that case.
//
// # The bound
//
// This is a diagnostic, so it is allowed to be incomplete but not
// allowed to be wrong. Every arm below either reports something the
// compiled spec demonstrably says, or says nothing.

// whenField is one declared field a rule's `when` reads, with the two
// facts the diagnosis needs: what the schema declares it as, and how
// generation reconstructs it.
type whenField struct {
	name     string
	typeName string
	dist     string
	// preRounded is true when the value the ROW carries for this field
	// may differ from the value the FILE will hold, i.e. when the
	// pre-rounding gotcha applies to a comparison against it.
	preRounded bool
}

// quantizingFieldTypes is the set of field types whose wire value is
// DERIVED from the row value by rounding rather than stored as given.
//
// It is the set of writeFieldValueForField arms applying Floor(f+0.5)
// (u4, u8, u16, u32, u64, date) plus packed_bool, whose toBool rounds at
// 0.5. f32/f64 store the float they are given; decimal128 parses an
// exact literal; a categorical stores a dictionary ID and a set_* a
// bitmask, neither of which a numeric comparison reaches at all.
//
// This mirrors isIntegerQuantizedFieldType (synth/discrete.go) without
// reusing it, because the two answer different questions: that one asks
// which types are ELIGIBLE for the discrete histogram reconstruction and
// deliberately excludes date (it owns uniform_date) and packed_bool (it
// owns bernoulli), while this one asks which types round on the way to
// the file, which both of those do.
var quantizingFieldTypes = map[string]bool{
	"u4": true, "u8": true, "u16": true, "u32": true, "u64": true,
	"date": true, "packed_bool": true,
}

// exactValuedDistributions is the set of distributions whose draw is
// already the number the writer will store, so a comparison against the
// field reads the same value the file shows.
//
// Each entry is a claim about the sampler, and each is checkable:
//
//   - discrete draws from its own ascending support (newDiscreteSampler)
//     and a profile-derived support is the observed integer levels;
//   - bernoulli draws exactly 1 or 0;
//   - uniform_date draws startDays + an integer offset;
//   - poisson draws a count;
//   - monotonic_from draws start + k*step over integer params.
//
// The first three matter because the reconstruction picks them
// automatically; the last two are here because leaving them out would
// name pre-rounding for a field that cannot suffer from it.
//
// constant is ABSENT because a map keyed on the distribution NAME cannot
// answer for it — its value comes from the document and may be any float
// — so it is decided per FieldSpec instead; see
// distributionIsExactValued. The same claims also survive every OTHER
// writer that can reach the field — a conditional pair resolves a
// discrete target on its own staircase and a bernoulli target on its own
// step, and the composed model draw goes through quantileFor, which is
// the same staircase — so the exactness is a property of the field, not
// of which stage last touched it.
var exactValuedDistributions = map[string]bool{
	DistDiscrete:      true,
	DistBernoulli:     true,
	DistUniformDate:   true,
	DistPoisson:       true,
	DistMonotonicFrom: true,
}

// distributionIsExactValued answers exactValuedDistributions' question
// for one FIELD rather than for a distribution name, which is what the
// `constant` arm needs.
//
// A `constant` field's draw is the document's own literal, so whether it
// is the number the writer will store depends on the LITERAL and not on
// the distribution: {"value": 3} on a u8 is exact, {"value": 3.4} is not.
// The literal is coerced through constantRowValue — the same function
// the sampler itself uses, so the two cannot disagree about what the row
// will hold — and admitted when the result is a finite integral float.
// A literal the coercion refuses is not admitted: buildSampler will
// refuse the field on its own terms and a diagnostic must not claim
// anything about it.
//
// The non-scalar classes never reach here: quantizingFieldTypes is the
// gate above and it contains no categorical or set type.
func distributionIsExactValued(f FieldSpec) bool {
	if exactValuedDistributions[f.Distribution] {
		return true
	}
	if f.Distribution != DistConstant {
		return false
	}
	raw, ok := f.Params["value"]
	if !ok {
		return false
	}
	coerced, err := constantRowValue(f, raw)
	if err != nil {
		return false
	}
	n, isFloat := coerced.(float64)
	if !isFloat {
		return false
	}
	return !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n
}

// fieldIsPreRounded reports whether a comparison against this field in
// the rule at index idx reads a value that may differ from the one the
// file holds.
//
// Two independent sources of inexactness, and the order below is the
// contract:
//
//   - The SAMPLER. A field whose own distribution can draw a
//     non-integral value is pre-rounded, and that is what the warning
//     was written for.
//   - A `set_expr` REWRITE. A rule's expression result is an arbitrary
//     value, so a field some rule writes loses the sampler's exactness
//     claim — UNLESS every expression writing it is demonstrably
//     integral (rewriteIndex, exprIsIntegral).
//
// The integral case is not a nicety: `{"set_expr": {"nps": "round(nps)"}}`
// is the normalisation this package DOCUMENTS as the remedy for
// pre-rounding, and treating its target as pre-rounded meant the
// diagnostic told an author to apply a fix and then named the fix as the
// hazard. A demonstrably-integral rewrite in an EARLIER rule clears the
// sampler's inexactness too, because by the time this rule's predicate
// runs the field holds that rule's integral result — which is exactly
// what the message's own "normalise first (an earlier rule setting
// round(field))" advice describes.
//
// Ordering is deliberately asymmetric and conservative in the direction
// that cannot mislead: a rewrite that is NOT demonstrably integral marks
// the field whatever its position, because the diagnosis has no cheap
// way to know which rows a later `when` will reach.
func fieldIsPreRounded(f FieldSpec, rw rewriteIndex, idx int) bool {
	if !quantizingFieldTypes[f.Type] {
		return false
	}
	if rw.inexact[f.Name] {
		return true
	}
	if at, ok := rw.normalisedAt[f.Name]; ok && at < idx {
		return false
	}
	return !distributionIsExactValued(f)
}

// whenFieldsRead returns, in sorted order, the declared fields a rule's
// `when` reads, each classified by fieldIsPreRounded.
//
// Parsed from the AUTHOR'S SOURCE rather than from the compiled program,
// following exprReadsIdentifier: a compiler is free to fold and rewrite,
// and the question is what the author wrote. An unparseable source
// returns nothing — the diagnosis then falls back to the neutral arm,
// which is right, because a source that does not parse never compiled
// and never reached generation.
func whenFieldsRead(src string, byName map[string]FieldSpec, rw rewriteIndex, idx int) []whenField {
	if src == "" {
		return nil
	}
	tree, err := parser.Parse(src)
	if err != nil || tree == nil || tree.Node == nil {
		return nil
	}
	probe := &fieldReadProbe{declared: byName, seen: map[string]bool{}}
	ast.Walk(&tree.Node, probe)
	names := make([]string, 0, len(probe.seen))
	for name := range probe.seen {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]whenField, 0, len(names))
	for _, name := range names {
		f := byName[name]
		out = append(out, whenField{
			name:       name,
			typeName:   f.Type,
			dist:       f.Distribution,
			preRounded: fieldIsPreRounded(f, rw, idx),
		})
	}
	return out
}

// fieldReadProbe collects every declared field name an expression
// mentions, in either of the two spellings a rule predicate can use it:
// as a bare identifier (`nps == 9`) and as the string argument of the
// isnull builtin (`isnull("nps")`, which an author may also write as
// isnull(nps) — that spelling is an identifier and the first arm catches
// it).
//
// A function CALLEE is an identifier too, so a field sharing a builtin's
// name would be collected from a call that does not read it. That is the
// same false-positive exprReadsIdentifier accepts, and here it costs at
// most one extra field named in a diagnostic.
type fieldReadProbe struct {
	declared map[string]FieldSpec
	seen     map[string]bool
}

func (p *fieldReadProbe) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IdentifierNode:
		if _, ok := p.declared[n.Value]; ok {
			p.seen[n.Value] = true
		}
	case *ast.CallNode:
		callee, ok := n.Callee.(*ast.IdentifierNode)
		if !ok || callee.Value != isnullFuncName || len(n.Arguments) != 1 {
			return
		}
		lit, ok := n.Arguments[0].(*ast.StringNode)
		if !ok {
			return
		}
		if _, declared := p.declared[lit.Value]; declared {
			p.seen[lit.Value] = true
		}
	}
}

// rewriteIndex is what every rule's `set_expr` slots say about the
// fields they write, from the pre-rounding diagnosis' point of view.
// Built once per spec by ruleRewriteIndex; read only by
// fieldIsPreRounded.
type rewriteIndex struct {
	// inexact names fields written by at least one expression whose
	// result is not demonstrably integral. Position-insensitive: one
	// such write anywhere disqualifies the field.
	inexact map[string]bool
	// normalisedAt maps a field to the LOWEST rule index writing it with
	// a demonstrably-integral expression. A rule at a higher index reads
	// the normalised value.
	normalisedAt map[string]int
}

// ruleRewriteIndex classifies every `set_expr` target in the spec.
//
// The two maps are built in one pass in declaration order, so
// normalisedAt keeps the first (lowest) index for a field written more
// than once, and a field that is both integrally and inexactly written
// lands in BOTH — where fieldIsPreRounded's order gives inexact the
// verdict.
func ruleRewriteIndex(rules []RuleSpec) rewriteIndex {
	var rw rewriteIndex
	for i, r := range rules {
		for _, name := range sortedKeys(r.SetExpr) {
			if exprIsIntegral(r.SetExpr[name]) {
				if rw.normalisedAt == nil {
					rw.normalisedAt = map[string]int{}
				}
				if _, seen := rw.normalisedAt[name]; !seen {
					rw.normalisedAt[name] = i
				}
				continue
			}
			if rw.inexact == nil {
				rw.inexact = map[string]bool{}
			}
			rw.inexact[name] = true
		}
	}
	return rw
}

// integralCallees are the expr BUILTINS whose result is an integer
// VALUE, whatever its Go type: round and int are the two the
// normalisation idiom uses, floor and ceil are the same operation with a
// different tie rule. abs is deliberately absent — abs(-1.5) is 1.5.
//
// Matched against ast.BuiltinNode.Name, so a user-registered function
// sharing one of these names (an ast.CallNode) is not admitted.
//
// This is a diagnostic, so the set is allowed to be incomplete and is
// not allowed to be wrong: an expression outside it is simply treated as
// inexact, which is where every expression sat before.
var integralCallees = map[string]bool{
	"round": true, "int": true, "floor": true, "ceil": true,
}

// exprIsIntegral reports whether an expression's result is DEMONSTRABLY
// an integer value, from its source alone.
//
// Four shapes, each of which is a statement about the expression rather
// than about any row it will see:
//
//   - an integer literal;
//   - a bool literal, and any boolean-valued operator, because the
//     coercion matrix writes a bool to a numeric target as 1/0 — this
//     is what makes `--suggest-rules`' emitted band rules
//     ({"promoter": "round(nps) >= 9"}) exact;
//   - a call to one of integralCallees;
//   - a parenthesised/negated form of any of the above.
//
// Everything else answers false, including an arithmetic expression over
// integral operands (`round(nps) + 1` is integral in fact, and proving
// it needs a type lattice this diagnostic does not need). Parsed from
// the AUTHOR'S SOURCE for the reason exprReadsIdentifier is: a compiler
// is free to fold and rewrite, and the question is what the author
// wrote. An unparseable source — unreachable past validateRules —
// answers false, which is the direction that keeps the old advice.
func exprIsIntegral(src string) bool {
	tree, err := parser.Parse(src)
	if err != nil || tree == nil || tree.Node == nil {
		return false
	}
	return nodeIsIntegral(tree.Node)
}

func nodeIsIntegral(n ast.Node) bool {
	switch node := n.(type) {
	case *ast.IntegerNode, *ast.BoolNode:
		return true
	case *ast.UnaryNode:
		switch node.Operator {
		case "!", "not":
			return true
		case "-", "+":
			return nodeIsIntegral(node.Node)
		}
		return false
	case *ast.BinaryNode:
		switch node.Operator {
		case "==", "!=", "<", "<=", ">", ">=", "&&", "||", "and", "or",
			"in", "not in", "matches", "contains", "startsWith", "endsWith":
			return true
		}
		return false
	case *ast.BuiltinNode:
		// round / int / floor / ceil parse as BuiltinNode, not CallNode
		// — expr resolves its own builtins at parse time. A user
		// function with one of those names would be a CallNode and is
		// correctly NOT admitted: the diagnostic knows nothing about it.
		return integralCallees[node.Name]
	case *ast.ConditionalNode:
		return nodeIsIntegral(node.Exp1) && nodeIsIntegral(node.Exp2)
	}
	return false
}

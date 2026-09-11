package synth

import (
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
// constant is deliberately ABSENT: its value comes from the document and
// may be any float, so the claim would depend on the literal rather than
// on the distribution. The same three also survive every OTHER writer
// that can reach the field — a conditional pair resolves a discrete
// target on its own staircase and a bernoulli target on its own step,
// and the composed model draw goes through quantileFor, which is the
// same staircase — so the exactness is a property of the field, not of
// which stage last touched it.
var exactValuedDistributions = map[string]bool{
	DistDiscrete:      true,
	DistBernoulli:     true,
	DistUniformDate:   true,
	DistPoisson:       true,
	DistMonotonicFrom: true,
}

// fieldIsPreRounded reports whether a comparison against this field in a
// rule predicate reads a value that may differ from the one the file
// holds.
//
// rewritten names the fields some rule's `set_expr` writes. Those are
// treated as pre-rounded whatever their distribution says, because the
// expression's result is an arbitrary float and the exactness claim
// above is about the SAMPLER. That is the conservative direction: it
// keeps the pre-rounding advice for a field whose value the rules
// themselves may have moved off its support.
func fieldIsPreRounded(f FieldSpec, rewritten map[string]bool) bool {
	if !quantizingFieldTypes[f.Type] {
		return false
	}
	if rewritten[f.Name] {
		return true
	}
	return !exactValuedDistributions[f.Distribution]
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
func whenFieldsRead(src string, byName map[string]FieldSpec, rewritten map[string]bool) []whenField {
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
			preRounded: fieldIsPreRounded(f, rewritten),
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

// ruleSetExprTargets returns every field any rule's `set_expr` writes.
// Used only by fieldIsPreRounded; see there for why a rewritten field
// loses its exactness claim.
func ruleSetExprTargets(rules []RuleSpec) map[string]bool {
	var out map[string]bool
	for _, r := range rules {
		for name := range r.SetExpr {
			if out == nil {
				out = map[string]bool{}
			}
			out[name] = true
		}
	}
	return out
}

package synth

import (
	stderrors "errors"
	"fmt"
	"hash/fnv"
	"math"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
)

// uniform() and bernoulli(p) — random draws inside a RULE (#364, U52).
//
// # The stream: counter-based, one per draw site, off the main RNG
//
// A draw never touches the generation RNG. Every call site — one
// uniform() / bernoulli() call node in one rule slot's source — has its
// own key, and a draw is a pure function of
//
//	(seed, site key, accepted-row ordinal, attempt, call)
//
// hashed through splitmix64 (drawUniformAt). So:
//
//   - a spec that never calls a draw consumes exactly the RNG sequence it
//     consumed before the functions existed (byte-identical), and a spec
//     that ADDS a draw moves no other field's value — the main stream is
//     untouched;
//   - two sites never share a value, and reordering the evaluation of
//     unrelated sites (a short-circuited `when`, a rule that did not fire)
//     moves nothing, because no site reads a position in a shared stream.
//
// # Rejection: a rejected attempt's draws are DISCARDED, never replayed
//
// `attempt` counts the constraint-rejected attempts at the current
// accepted-row ordinal (0 on the first try, reset on acceptance). A
// rejected row is redrawn at the same ordinal with attempt+1, so its
// draws are fresh values — re-reading attempt 0's would reject the same
// draw forever — and the accepted row's draws are a function of the
// spec and seed alone. `call` counts this site's calls within the
// attempt (a site in a closure such as `all(...)` can run more than once),
// reset by the rule pass before every attempt.
//
// # Site keys
//
// A site's key is the FNV-1a 64 of a structural label — `rule/<index>/
// when/<k>` or `rule/<index>/set_expr/<field>/<k>`, k the call's position
// in the expression's parse walk — so a site keeps its stream when other
// rules or fields change, and moves only when its own rule moves index.
// drawStreamKey (seed) and drawSiteKey (label) are the reusable halves:
// another generation stage needing its own seeded draws (E2-S1) derives
// its seed key with a different domain constant and its own labels.
//
// # Where a draw may appear
//
// In a rule's `when` and `set_expr`. A constraint is an acceptance TEST,
// and a random one would make rejection itself random, so a draw there is
// refused (PULSE_SYNTH_RULE_DRAW_INVALID), as are wrong arguments and a
// literal p outside [0, 1]; a computed p outside [0, 1] (NaN included) is
// refused on the row that produced it. The --suggest-rules detectors never
// emit a draw. To reuse ONE draw in several rules (or filter on it), store
// it in an `"output": false` helper field and read the field.

const (
	uniformFuncName   = "uniform"
	bernoulliFuncName = "bernoulli"

	// The internal names the patcher rewrites a draw onto, carrying the
	// site index. The ':' cannot be lexed, so no author can call them.
	uniformDrawFuncName   = "uniform:draw"
	bernoulliDrawFuncName = "bernoulli:draw"

	// ruleDrawDomain separates the rule-draw seed key from any other
	// counter-based stream derived from the same seed.
	ruleDrawDomain uint64 = 0x52554C4544524157 // "RULEDRAW"
)

// drawFuncNames are the expression-visible draw functions.
var drawFuncNames = []string{uniformFuncName, bernoulliFuncName}

func isDrawFuncName(name string) bool {
	return name == uniformFuncName || name == bernoulliFuncName
}

// drawSite is one compiled draw call: its stream key and its call count
// within the current attempt.
type drawSite struct {
	key   uint64
	calls uint64
}

// splitmix64 is the SplitMix64 finalizer (Steele, Lea, Flood 2014):
// integer-only, so a draw is bit-identical on every architecture.
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

// drawStreamKey derives a counter-based stream family's seed key from the
// run seed and a domain constant.
func drawStreamKey(seed int64, domain uint64) uint64 {
	return splitmix64(uint64(seed) ^ domain)
}

// drawSiteKey is a site label's stable key.
func drawSiteKey(label string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(label))
	return h.Sum64()
}

// drawUniformAt is the stream's value at (ordinal, attempt, call): a
// uniform in [0, 1) from the top 53 bits of the chained hash. Exact — one
// integer-to-float conversion and a power-of-two scale, no rounding.
func drawUniformAt(seedKey, siteKey uint64, ordinal, attempt int, call uint64) float64 {
	h := splitmix64(seedKey ^ siteKey)
	h = splitmix64(h ^ uint64(ordinal))
	h = splitmix64(h ^ uint64(attempt))
	h = splitmix64(h ^ call)
	return float64(h>>11) * 0x1p-53
}

// drawCallShape checks a draw call's arguments: uniform() takes none,
// bernoulli one, and a LITERAL p must lie in [0, 1].
func drawCallShape(call *ast.CallNode, name string) *rowExprRefusal {
	refuse := func(format string, a ...any) *rowExprRefusal {
		return &rowExprRefusal{code: errors.PULSE_SYNTH_RULE_DRAW_INVALID, function: name, msg: fmt.Sprintf(format, a...)}
	}
	switch name {
	case uniformFuncName:
		if len(call.Arguments) != 0 {
			return refuse("uniform() takes no arguments, got %d; scale the draw instead (lo + (hi - lo) * uniform())", len(call.Arguments))
		}
	case bernoulliFuncName:
		if len(call.Arguments) != 1 {
			return refuse("bernoulli(p) takes one probability, got %d arguments", len(call.Arguments))
		}
		if p, ok := numberLiteral(call.Arguments[0]); ok && (math.IsNaN(p) || p < 0 || p > 1) {
			return refuse("bernoulli(p): p = %v is outside [0, 1]", p)
		}
	}
	return nil
}

// constraintExprStaticRefusal is rowExprStaticRefusal for a constraint:
// a draw call is refused first (draws belong to rules), then every other
// parse-settled refusal applies.
func constraintExprStaticRefusal(src string, declared map[string]bool, times map[string]rowTimeField) *rowExprRefusal {
	tree, err := parser.Parse(src)
	if err != nil || tree == nil || tree.Node == nil {
		return nil
	}
	probe := &drawCallProbe{declared: declared}
	ast.Walk(&tree.Node, probe)
	if probe.name != "" {
		return &rowExprRefusal{code: errors.PULSE_SYNTH_RULE_DRAW_INVALID, function: probe.name, msg: fmt.Sprintf(
			"calls %s(), and a constraint cannot draw: it is an acceptance test, and a random one makes rejection itself random — draw in a rule into an \"output\": false field and constrain the field",
			probe.name)}
	}
	return rowExprStaticRefusal(src, declared, times)
}

// drawCallProbe finds the first call to a draw function a declared field
// does not shadow.
type drawCallProbe struct {
	declared map[string]bool
	name     string
}

func (p *drawCallProbe) Visit(node *ast.Node) {
	if p.name != "" {
		return
	}
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	if id, ok := call.Callee.(*ast.IdentifierNode); ok && isDrawFuncName(id.Value) && !p.declared[id.Value] {
		p.name = id.Value
	}
}

// drawPatcher rewrites each well-formed draw call onto its internal name
// with the host's next site index as a trailing literal. A malformed call
// is left alone: the public stubs' signatures refuse it at the type check,
// and the walker has refused it first with its reason.
type drawPatcher struct {
	declared map[string]bool
	host     *compiledConstraints
}

func (p drawPatcher) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	if !ok || !isDrawFuncName(callee.Value) || p.declared[callee.Value] || drawCallShape(call, callee.Value) != nil {
		return
	}
	p.host.drawSites = append(p.host.drawSites, drawSite{})
	site := intLiteral(len(p.host.drawSites)-1, call)
	internal := uniformDrawFuncName
	args := []ast.Node{site}
	if callee.Value == bernoulliFuncName {
		internal = bernoulliDrawFuncName
		args = []ast.Node{call.Arguments[0], site}
	}
	// A NEW call node, not an in-place rename: the checker types the
	// tree before each patcher runs, and the public names are registered,
	// so the old callee already carries the public signature's type.
	fn := &ast.IdentifierNode{Value: internal}
	fn.SetLocation(callee.Location())
	patched := &ast.CallNode{Callee: fn, Arguments: args}
	patched.SetLocation(call.Location())
	ast.Patch(node, patched)
}

// drawFunctionOptions registers the draw functions on host: the public
// names for the type check (never run — the patcher rewrites every
// well-formed call) and the internal ones that draw.
func drawFunctionOptions(host *compiledConstraints) []expr.Option {
	return []expr.Option{
		expr.Function(uniformFuncName, drawUnpatched(uniformFuncName), new(func() float64)),
		expr.Function(bernoulliFuncName, drawUnpatched(bernoulliFuncName), new(func(float64) bool)),
		expr.Function(uniformDrawFuncName, host.uniformDrawBuiltin, new(func(int) float64)),
		expr.Function(bernoulliDrawFuncName, host.bernoulliDrawBuiltin, new(func(float64, int) bool)),
	}
}

func drawUnpatched(name string) func(params ...any) (any, error) {
	return func(...any) (any, error) {
		return nil, fmt.Errorf("%s(): call site was not compiled", name)
	}
}

// labelDrawSites keys the sites compiled since `from` under label, in
// registration (parse-walk) order.
func (c *compiledConstraints) labelDrawSites(from int, label string) {
	for k := from; k < len(c.drawSites); k++ {
		c.drawSites[k].key = drawSiteKey(fmt.Sprintf("%s/%d", label, k-from))
	}
}

// beginDrawAttempt resets every site's call count for a new attempt.
func (c *compiledConstraints) beginDrawAttempt() {
	for k := range c.drawSites {
		c.drawSites[k].calls = 0
	}
}

// drawAt is the next value of site idx's stream on this attempt.
func (c *compiledConstraints) drawAt(fn string, v any) (float64, error) {
	idx, ok := v.(int)
	if !ok || idx < 0 || idx >= len(c.drawSites) {
		return 0, fmt.Errorf("%s(): no compiled draw site", fn)
	}
	s := &c.drawSites[idx]
	u := drawUniformAt(c.drawSeed, s.key, c.ordinal, c.attempt, s.calls)
	s.calls++
	return u, nil
}

// uniformDrawBuiltin answers uniform(): a uniform draw in [0, 1).
func (c *compiledConstraints) uniformDrawBuiltin(params ...any) (any, error) {
	if len(params) != 1 {
		return nil, fmt.Errorf("%s expects no arguments, got %d", uniformFuncName, len(params)-1)
	}
	return c.drawAt(uniformFuncName, params[0])
}

// bernoulliDrawBuiltin answers bernoulli(p): true with probability p,
// as uniform() < p. A p outside [0, 1] is refused, coded, on this row.
func (c *compiledConstraints) bernoulliDrawBuiltin(params ...any) (any, error) {
	if len(params) != 2 {
		return nil, fmt.Errorf("%s expects 1 argument, got %d", bernoulliFuncName, len(params)-1)
	}
	p, err := numberParam(bernoulliFuncName, params[0])
	if err != nil {
		return nil, err
	}
	if math.IsNaN(p) || p < 0 || p > 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_RULE_DRAW_INVALID,
			fmt.Sprintf("bernoulli(p): p = %v is outside [0, 1]; clamp it (min(1, max(0, p)))", p),
			map[string]any{"function": bernoulliFuncName, "p": drawDetailValue(p), "row": c.ordinal})
	}
	u, err := c.drawAt(bernoulliFuncName, params[1])
	if err != nil {
		return nil, err
	}
	return u < p, nil
}

// drawDetailValue keeps a non-finite p readable in error details (JSON
// cannot carry NaN).
func drawDetailValue(p float64) any {
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return fmt.Sprint(p)
	}
	return p
}

// ruleDrawError lifts a draw's coded row-time refusal out of expr's
// wrapper, adding the rule's coordinates; nil for any other error.
func ruleDrawError(err error, idx int, slot, field, src string) error {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_SYNTH_RULE_DRAW_INVALID {
		return nil
	}
	details := map[string]any{errors.DetailSynthRule: idx, errors.DetailSynthRuleSlot: slot, "expr": src}
	for k, v := range ce.Details {
		details[k] = v
	}
	where := fmt.Sprintf("rule %d %s %q", idx, slot, src)
	if field != "" {
		details["field"] = field
		where = fmt.Sprintf("rule %d %s[%q] = %q", idx, slot, field, src)
	}
	return errors.NewCodedErrorWithDetails(ce.Code, fmt.Sprintf("%s: %s", where, ce.Message), details)
}

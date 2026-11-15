package guide

import (
	stderrors "errors"
	"slices"
	"strconv"
	"strings"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/exprreads"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/model/coefcall"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/internal/resultcall"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// explain_optimize.go describes a Compose batch's `optimize` blocks:
// request mode one step per block as it RUNS (each for_each block
// expanded by sweep.ExpandOptimize, the one Compose-level expansion),
// response mode one finding per ComposedResponse.Optimizations entry.
//
// A step's slot is the EXPANDED block's path (`optimize[i]`): the index
// the batch runs, the one every optimize refusal names (details.optimize,
// located by locatedSlot) and the one Optimizations[i] answers. A
// fanned-out block is preceded once by a `optimize[w].for_each` step at
// its written path stating the fan-out.

var roleOptimize = &role{"Optimize block", "optimize block", "optimize blocks"}

// optimizeGradients is what the Compose check says about each expanded
// block's gradient: known is false when no check ran, the batch was
// refused (the optimize rules may not have run to the advisory pass) or
// the instance suppresses the advisory; fallback maps a block index to
// the functions without a derivative rule
// (PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK details — the advisory
// predict raises from optblock.Checked.GradientFallbacks, the judgement
// the runtime's compile makes; never recomputed here).
type optimizeGradients struct {
	known    bool
	fallback map[int][]string
	// unresolved lists, per block index, the bound and start paths the
	// static check left to the runtime (UnresolvedScalars).
	unresolved map[int][]string
}

// optimizeGradientsOf reads the Compose check's envelope.
func (e *explainer) optimizeGradientsOf(env *descriptor.Envelope) optimizeGradients {
	g := optimizeGradients{fallback: map[int][]string{}, unresolved: map[int][]string{}}
	if env == nil {
		return g
	}
	res, ok := env.Data.(*descx.ComposeValidationResult)
	if !ok || res == nil {
		return g
	}
	for _, u := range res.UnresolvedScalars {
		if i, ok := optimizeIndex(u.Path); ok {
			g.unresolved[i] = append(g.unresolved[i], u.Path)
		}
	}
	code := string(errors.PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK)
	g.known = res.Optimize != nil && len(env.Errors) == 0 && !e.inst.AdvisorySuppressed(code)
	for _, a := range res.Advisories {
		if a.Code != code {
			continue
		}
		i, ok := a.Details["optimize"].(int)
		if !ok {
			continue
		}
		fns, _ := a.Details["functions"].([]string)
		g.fallback[i] = fns
	}
	return g
}

// optimizeIndex is the block index a path under `optimize[i].` names.
func optimizeIndex(path string) (int, bool) {
	rest, ok := strings.CutPrefix(path, "optimize[")
	if !ok {
		return 0, false
	}
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return 0, false
	}
	i, err := strconv.Atoi(rest[:end])
	return i, err == nil
}

// optimizeSteps describes c's optimize blocks as they run. A for_each
// that does not expand is a caveat naming its code, and the blocks are
// described as written.
func (e *explainer) optimizeSteps(c *types.ComposedRequest, g optimizeGradients) {
	if len(c.Optimize) == 0 {
		return
	}
	x, fans, err := sweep.ExpandOptimize(c, e.inst.SweepOptions(), nil)
	if err != nil {
		code := string(errors.PULSE_FOR_EACH_INVALID)
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			code = string(ce.Code)
		}
		e.caveat("A for_each on an optimize block does not expand (" + code + "), so the optimize blocks are described as written; pulse errors lookup " + code + " says how to fix it.")
		x, fans = c, nil
	}
	byPath := map[string]sweep.FanOut{}
	for _, fo := range fans {
		byPath[fo.Path] = fo
	}
	// written[i] is the block as written that expanded block i came from.
	var written []int
	for w := range c.Optimize {
		k := 1
		if fo, ok := byPath["optimize["+strconv.Itoa(w)+"]"]; ok && c.Optimize[w].ForEach != nil {
			k = fo.Count
		}
		for range k {
			written = append(written, w)
		}
	}
	told := map[int]bool{}
	for i := range x.Optimize {
		w := i
		if i < len(written) {
			w = written[i]
		}
		wpath := "optimize[" + strconv.Itoa(w) + "]"
		if fo, ok := byPath[wpath]; ok && !told[w] {
			told[w] = true
			e.plainStep(wpath+".for_each", nil, fanSentence(wpath, fo, roleOptimize))
		}
		e.optimizeStep(i, &x.Optimize[i], g)
	}
}

// optimizeStep is one block: what it optimizes over which variables and
// constraints, what it reads, how it is differentiated and, for a
// posterior block, over which draws.
func (e *explainer) optimizeStep(i int, spec *types.OptimizeSpec, g optimizeGradients) {
	e.counts[roleOptimize]++
	lead := roleOptimize.lead + " " + strconv.Itoa(i+1)
	if spec.Name != "" {
		lead += " " + tick(spec.Name)
	}
	verb := "optimizes"
	switch spec.Direction {
	case types.OptimizeMaximize:
		verb = "maximizes"
	case types.OptimizeMinimize:
		verb = "minimizes"
	}
	text := lead + " " + verb
	if spec.Objective != "" {
		text += " " + tick(spec.Objective)
	} else {
		text += " an objective it does not name"
	}
	var vars []string
	for _, v := range spec.Variables {
		s := tick(v.Name) + " between " + boundText(v.Lower) + " and " + boundText(v.Upper)
		if !v.Start.IsZero() {
			s += " (starting at " + boundText(v.Start) + ")"
		}
		vars = append(vars, s)
	}
	if len(vars) > 0 {
		text += " over " + andList(vars)
	}
	var cons []string
	for _, k := range spec.Constraints {
		switch k.Type {
		case types.OptimizeConstraintEq:
			cons = append(cons, tick(k.Expr)+" held at 0")
		case types.OptimizeConstraintIneq:
			cons = append(cons, tick(k.Expr)+" kept at or above 0")
		default:
			cons = append(cons, tick(k.Expr))
		}
	}
	if len(cons) > 0 {
		text += ", subject to " + andList(cons)
	}
	text += ", from " + count(spec.EffectiveStarts(), "start", "starts") + "."
	if reads := optimizeReads(spec); reads != "" {
		text += " " + reads
	}
	text += " " + gradientSentence(i, spec, g)
	if p := spec.Posterior; p != nil {
		text += " " + posteriorSentence(spec, p)
	}
	if n := len(g.unresolved[i]); n > 0 {
		text += " " + count(n, "bound or start reads", "bounds or starts read") + " a fit, a result or a request-bound function, so it is resolved when the batch runs, not here."
	}
	e.res.Steps = append(e.res.Steps, descriptor.ExplainStep{Slot: "optimize[" + strconv.Itoa(i) + "]", Text: text})
}

// boundText is a bound as written: its number, or its expression.
func boundText(b types.OptimizeBound) string {
	switch {
	case b.IsZero():
		return "an unset bound"
	case b.IsExpr():
		return tick(b.Expr())
	}
	return strconv.FormatFloat(b.Value(), 'g', -1, 64)
}

// optimizeReads names what a block reads beyond its variables: the
// batch params, the fits (coef(), response(), sweep_axis() with a
// literal fit), the slot results (result() with literal arguments) and
// the functions it calls — across its objective, constraints, bounds and
// starts. "" when it reads none.
func optimizeReads(spec *types.OptimizeSpec) string {
	srcs := []string{spec.Objective}
	for _, k := range spec.Constraints {
		srcs = append(srcs, k.Expr)
	}
	for _, v := range spec.Variables {
		for _, b := range []types.OptimizeBound{v.Lower, v.Upper, v.Start} {
			if b.IsExpr() {
				srcs = append(srcs, b.Expr())
			}
		}
	}
	var params, fits, results, funcs []string
	computed := map[string]bool{}
	add := func(list *[]string, s string) {
		if s != "" && !slices.Contains(*list, s) {
			*list = append(*list, s)
		}
	}
	for _, src := range srcs {
		if src == "" {
			continue
		}
		if r, err := exprreads.Read(src); err == nil {
			for _, p := range r.Params {
				add(&params, p)
			}
			for _, f := range r.Funcs {
				add(&funcs, f)
			}
		}
		for _, c := range coefcall.LiteralCalls(src) {
			add(&fits, c.Fit)
		}
		for _, f := range coefcall.LiteralFits(src, false) {
			add(&fits, f)
		}
		literal := map[string]int{
			model.CoefFunc:      len(coefcall.LiteralCalls(src)),
			model.ResponseFunc:  len(coefcall.LiteralResponseCalls(src)),
			model.SweepAxisFunc: len(coefcall.LiteralFits(src, false)) - len(coefcall.LiteralResponseCalls(src)),
		}
		for _, c := range resultcall.Calls(src) {
			if c.Literal {
				literal[resultcall.Func]++
				add(&results, tick(c.Aggregation)+" of the slot "+tick(c.Slot))
			}
		}
		for f, n := range readerCalls(src) {
			if literal[f] < n {
				computed[f] = true
			}
		}
	}
	literalOnly := map[string]bool{}
	for _, f := range []string{resultcall.Func, model.CoefFunc, model.ResponseFunc, model.SweepAxisFunc} {
		literalOnly[f] = !computed[f]
	}
	var parts []string
	if len(params) > 0 {
		parts = append(parts, "the params "+andList(ticks(params)))
	}
	if len(fits) > 0 {
		parts = append(parts, count(len(fits), "fit", "fits")+" "+andList(ticks(fits)))
	}
	if len(results) > 0 {
		parts = append(parts, "the result "+andList(results))
	}
	// A reader whose every call named its fit or result literally is
	// already named above; one with a computed argument stays listed.
	funcs = slices.DeleteFunc(funcs, func(f string) bool { return literalOnly[f] })
	if len(funcs) > 0 {
		parts = append(parts, "the functions "+andList(ticks(funcs)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "It reads " + andList(parts) + "."
}

// gradientSentence says how the solver differentiates the block: by
// finite differences when it asks for them, else analytically unless the
// Compose check reported a function without a derivative rule.
func gradientSentence(i int, spec *types.OptimizeSpec, g optimizeGradients) string {
	switch {
	case spec.EffectiveGradient() == types.OptimizeGradientFD:
		return "The solver differentiates it by finite differences, as its gradient fd asks."
	case len(g.fallback[i]) > 0:
		return "It asks for the analytic gradient, but " + andList(ticks(g.fallback[i])) +
			" has no derivative rule, so the solver differentiates the whole block by finite differences (" +
			string(errors.PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK) + ")."
	case g.known:
		return "The solver differentiates it analytically: every function it calls has a derivative rule."
	}
	return "It asks for the analytic gradient; whether every function it calls has a derivative rule is judged when the batch is checked."
}

// solvedGradientSentence is the result's runtime gradient echo (with a
// leading space; "" on a result without one): analytic, finite
// differences as asked, or finite differences because the functions in
// gradient_fallback have no derivative rule.
func solvedGradientSentence(r *types.OptimizeResult) string {
	switch {
	case r.Gradient == types.OptimizeGradientAnalytic:
		return " It solved with the analytic gradient."
	case r.Gradient == types.OptimizeGradientFD && len(r.GradientFallback) > 0:
		verb := " has"
		if len(r.GradientFallback) > 1 {
			verb = " have"
		}
		return " It solved by finite differences, because " + andList(ticks(r.GradientFallback)) +
			verb + " no derivative rule (" + string(errors.PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK) + ")."
	case r.Gradient == types.OptimizeGradientFD:
		return " It solved by finite differences, as its gradient fd asked."
	}
	return ""
}

// posteriorSentence names a posterior block's aggregation and the fit
// whose draws it reads.
func posteriorSentence(spec *types.OptimizeSpec, p *types.OptimizePosterior) string {
	what := "the mean of the objective"
	if p.EffectiveObjective() == types.OptimizePosteriorQuantile {
		what = "the " + strconv.FormatFloat(p.Q, 'g', -1, 64) + " quantile of the objective"
	}
	s := "It optimizes " + what + " across the posterior draws"
	if reads := optblock.PosteriorReads(spec); len(reads) == 1 {
		s += " of the fit " + tick(reads[0])
	}
	if p.Draws > 0 {
		s += ", evaluating " + count(p.Draws, "draw", "draws")
	}
	return s + ", and reports an interval per variable from re-solving on each draw."
}

// readOptimizations reads a ComposedResponse's Optimizations, one
// finding per block at `optimizations[i]`. It returns the blocks read.
func (e *explainer) readOptimizations(rr *responseReader, results []types.OptimizeResult) int {
	for i := range results {
		e.optimizeFinding(rr, i, &results[i])
	}
	return len(results)
}

// optimizeFinding is one block's result: descriptive (an optimum is no
// inference), not_computable when the block failed on its numbers.
func (e *explainer) optimizeFinding(rr *responseReader, i int, r *types.OptimizeResult) {
	slot := "optimizations[" + strconv.Itoa(i) + "]"
	subject := r.Name
	if subject == "" {
		subject = slot
	}
	lead := roleOptimize.lead + " " + tick(subject)
	f := descriptor.ExplainFinding{Slot: slot, Subject: subject, Verdict: descriptor.VerdictDescriptive, Numbers: map[string]*float64{}}
	if r.Error != nil {
		f.Verdict = descriptor.VerdictNotComputable
		e.addFinding(rr, f, lead+" reports no optimum: it failed with "+r.Error.Code+".")
		rr.later = append(rr.later, lead+" failed with "+r.Error.Code+"; pulse errors lookup "+r.Error.Code+" says how to fix it.")
		return
	}
	setNum(f.Numbers, "objective", r.Objective)
	setNum(f.Numbers, "max_violation", r.MaxViolation)
	setNum(f.Numbers, "iterations", float64(r.Iterations))
	setNum(f.Numbers, "starts", float64(r.Starts))
	setNum(f.Numbers, "starts_agreeing", float64(r.StartsAgreeing))
	verb := "reaches"
	switch r.Direction {
	case types.OptimizeMaximize:
		verb = "reaches its maximum"
	case types.OptimizeMinimize:
		verb = "reaches its minimum"
	}
	text := lead + " " + verb + " objective of " + fmtNum(r.Objective)
	var at []string
	for _, v := range r.Variables {
		setNum(f.Numbers, "variables."+v.Name+".value", v.Value)
		setNum(f.Numbers, "variables."+v.Name+".marginal", v.Marginal)
		s := tick(v.Name) + " = " + fmtNum(v.Value)
		if v.Active != "" {
			s += " (on its " + string(v.Active) + " bound)"
		}
		at = append(at, s)
		if v.Interval != nil {
			setNum(f.Numbers, "variables."+v.Name+".interval.lower", v.Interval.Lower)
			setNum(f.Numbers, "variables."+v.Name+".interval.upper", v.Interval.Upper)
		}
	}
	if len(at) > 0 {
		text += " at " + andList(at)
	}
	text += "."
	for k, c := range r.Constraints {
		setNum(f.Numbers, "constraints["+strconv.Itoa(k)+"].value", c.Value)
		setNum(f.Numbers, "constraints["+strconv.Itoa(k)+"].multiplier", c.Multiplier)
	}
	if len(r.Constraints) > 0 {
		text += " Its largest constraint violation is " + fmtNum(r.MaxViolation) + "."
	}
	if r.Converged {
		text += " The solver converged after " + count(r.Iterations, "iteration", "iterations")
	} else {
		text += " The solver did not converge"
		rr.later = append(rr.later, lead+" did not converge ("+r.Status+"), so its figures are the best point found, not a confirmed optimum.")
	}
	text += ", and " + strconv.Itoa(r.StartsAgreeing) + " of " + count(r.Starts, "start", "starts") + " reached that objective."
	text += solvedGradientSentence(r)
	if r.Converged && r.Starts > 1 && r.StartsAgreeing < r.Starts {
		rr.later = append(rr.later, lead+": only "+strconv.Itoa(r.StartsAgreeing)+" of "+count(r.Starts, "start", "starts")+
			" reached its best objective, so the objective may have several optima; more starts would test that.")
	}
	if p := r.Posterior; p != nil {
		setNum(f.Numbers, "posterior.draws", float64(p.Draws))
		setNum(f.Numbers, "posterior.draws_available", float64(p.DrawsAvailable))
		setNum(f.Numbers, "posterior.draws_resolved", float64(p.DrawsResolved))
		setNum(f.Numbers, "posterior.credible_level", p.CredibleLevel)
		what := "the posterior mean"
		if p.Objective == types.OptimizePosteriorQuantile {
			what = "the " + fmtNum(p.Q) + " posterior quantile"
		}
		text += " It optimizes " + what + " over " + strconv.Itoa(p.Draws) + " of " + count(p.DrawsAvailable, "draw", "draws") + " of the fit " + tick(p.Fit) + "."
		var ivs []string
		for _, v := range r.Variables {
			if v.Interval != nil {
				ivs = append(ivs, tick(v.Name)+" from "+fmtNum(v.Interval.Lower)+" to "+fmtNum(v.Interval.Upper))
			}
		}
		if len(ivs) > 0 {
			text += " Across " + count(p.DrawsResolved, "converged re-solve", "converged re-solves") + ", the best setting moves " +
				andList(ivs) + " (equal-tailed, at the " + fmtNum(p.CredibleLevel) + " level)."
		}
		if p.DrawsResolved < p.Draws {
			rr.later = append(rr.later, lead+": "+strconv.Itoa(p.Draws-p.DrawsResolved)+" of its per-draw re-solves did not converge, so its intervals rest on the rest.")
		}
	}
	e.addFinding(rr, f, text)
}

// readerCalls counts src's calls of each reader function (coef(),
// response(), sweep_axis(), result()), literal arguments or not; nil on
// a source that does not parse.
func readerCalls(src string) map[string]int {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	v := readerVisitor{}
	exprast.Walk(&tree.Node, v)
	return v
}

type readerVisitor map[string]int

func (v readerVisitor) Visit(node *exprast.Node) {
	n, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	if id, ok := n.Callee.(*exprast.IdentifierNode); ok {
		switch id.Value {
		case model.CoefFunc, model.ResponseFunc, model.SweepAxisFunc, resultcall.Func:
			v[id.Value]++
		}
	}
}

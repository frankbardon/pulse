// Package optblock holds the ONE rule set for a ComposedRequest
// `optimize` block (types.OptimizeSpec) short of solving it: the
// structural checks, the variable bounds resolved against the batch
// params, the objective and constraint compile against the block's
// environment, and the effective seed of its Latin-hypercube starts.
// Predict (internal/descriptor) and the runtime (internal/service) both
// call Check on the same block before any fit or slot runs, so the two
// refuse the same block with the same code, message and details; the
// runtime then solves what Compile returns.
//
// The environment an objective or constraint reads:
//
//   - every decision variable, a float64 identifier bound per iterate
//     (never a params constant: params fold at compile, a variable must
//     change between evaluations);
//   - the batch params as params.<name> (exprenv.Bound.Params, folded);
//   - coef(fit, term[, stat]) over the batch's `fits` — every
//     all-literal call judged statically by model.CheckCoef;
//   - result(slot, aggregation) — an ungrouped aggregation of a Compose
//     slot, run in the result pre-phase before any block (Env.Results;
//     ResultSlots names the slots);
//   - the expression built-ins (hill(), exp(), …), range_label(), the
//     embedder's ExprFunctions and, with a registered LookupTable,
//     lookup().
//
// No record field is in reach. A bound or start is a number or a scalar
// expression (internal/scalarslot — the one scalar-slot evaluator)
// resolved here: over literals and params at Check, so predict runs the
// range checks; one calling a request-bound function (coef(),
// sweep_axis(), response(), lookup(), an embedder function) is listed
// Unresolved by Check and resolved, then range-checked, by the
// runtime's Compile — unless Env.Fold is set and it reads only the
// block's own loaded `models` (folded at Check).
//
// No-execute leaf: it imports types, errors and the no-execute
// expression and model leaves only, never the engine
// (TestOptBlock_ImportBoundary).
package optblock

import (
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"

	"github.com/expr-lang/expr"
	exprbuiltin "github.com/expr-lang/expr/builtin"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/model/coefcall"
	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/internal/resultcall"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/internal/tablecall"
	"github.com/frankbardon/pulse/types"
)

// The PULSE_OPTIMIZE_SPEC_INVALID reasons.
const (
	ReasonNameMissing           = "name_missing"
	ReasonNameDuplicate         = "name_duplicate"
	ReasonVariablesMissing      = "variables_missing"
	ReasonObjectiveMissing      = "objective_missing"
	ReasonDirectionMissing      = "direction_missing"
	ReasonDirectionUnknown      = "direction_unknown"
	ReasonConstraintExprMissing = "constraint_expr_missing"
	ReasonConstraintTypeUnknown = "constraint_type_unknown"
	ReasonTooManyEqualities     = "too_many_equalities"
	ReasonStartsInvalid         = "starts_invalid"
	ReasonToleranceInvalid      = "tolerance_invalid"
	ReasonMaxIterationsInvalid  = "max_iterations_invalid"
)

// The PULSE_OPTIMIZE_SPEC_INVALID reasons of a block's `posterior`
// (field optimize[i].posterior…). A fit without its draws is reason
// draws_required (model.ReasonDrawsRequired), the reason a save, a
// holdout and a score of one share.
const (
	ReasonPosteriorObjectiveUnknown = "posterior_objective_unknown"
	ReasonPosteriorQInvalid         = "posterior_q_invalid"
	ReasonPosteriorDrawsInvalid     = "posterior_draws_invalid"
	ReasonPosteriorFitMissing       = "posterior_fit_missing"
	ReasonPosteriorFitsMultiple     = "posterior_fits_multiple"
	ReasonPosteriorFitNotBayes      = "posterior_fit_not_bayes"
)

// The PULSE_OPTIMIZE_VARIABLE_INVALID reasons (name_missing and
// name_duplicate are shared with the block's).
const (
	ReasonNameInvalid       = "name_invalid"
	ReasonNameReserved      = "name_reserved"
	ReasonBoundMissing      = "bound_missing"
	ReasonBoundInvalid      = "bound_invalid"
	ReasonBoundNotNumeric   = "bound_not_numeric"
	ReasonBoundNotFinite    = "bound_not_finite"
	ReasonBoundsCrossed     = "bounds_crossed"
	ReasonStartOutOfBounds  = "start_out_of_bounds"
	ReasonUnknownIdentifier = "unknown_identifier"
	ReasonCompileError      = "compile_error"
	ReasonNotNumeric        = "not_numeric"
)

// Env is what a block's expressions may call beyond the decision
// variables and the expr-lang built-ins.
type Env struct {
	// Params are the batch's normalized params (the ComposedRequest's
	// top-level `params` through exprenv.NormalizeParams).
	Params exprenv.Params
	// Fits are the names coef() resolves: the batch's `fits`
	// (model.CatalogFromFits).
	Fits model.Catalog
	// Extensions are the embedder's ExprFunction names; Lookup reports a
	// registered LookupTable (lookup() is declared).
	Extensions []string
	Lookup     bool
	// Tables are the instance's named tables the literal lookup() /
	// range_label() calls are judged against (internal/tablecall —
	// the evaluating builtins' codes); nil judges none.
	Tables tablecall.Catalog
	// Outer is the namespace a block's `models` entry may not rebind:
	// the instance's registered populations and the batch's fits
	// (model.WithinCatalog). Nil checks the entries against each other
	// only.
	Outer model.Catalog
	// Load reads and decodes a block's `models` files (model.LoadFiles
	// plus the instance's loaded-type checks): predict's and the
	// runtime's loaders run the same rules. Nil refuses any file as
	// unreadable.
	Load Loader
	// Results are the Compose slots result(slot, aggregation) reads
	// (resultcall.CatalogOf over the batch's effective slots): every
	// literal call is judged against them; nil puts result() out of
	// scope (PULSE_RESULT_INVALID out_of_scope).
	Results *resultcall.Catalog
	// Loaded, when set, are the files an earlier Check already read,
	// index-aligned with the blocks (nil where a block loads none):
	// Compile reuses them instead of reading again, so the runtime
	// solves exactly what it validated.
	Loaded []*model.Loaded
	// Fold, on a declaration-only Check (CheckAll), folds a bound or
	// start whose only request-bound reads are coef() / sweep_axis() /
	// response() of the block's own `models` entries: their figures are
	// known before anything runs, so the value is the runtime's and its
	// range checks run here (scalarslot.Env.Loaded over the Set the
	// runtime binds for the block, the bound its Declared()). A read of
	// a batch fit stays Unresolved. Predict and the runtime's pre-fit
	// check both set it, so a folded refusal fires before any fit on
	// both sides. Compile ignores it.
	Fold bool

	// loaded is the block's loaded-model Set a folding Check reads
	// (nil: fold none).
	loaded *model.Set
}

// Loader reads and decodes one block's `models` (refs passed
// model.CheckRefs).
type Loader func(refs model.Refs) (*model.Loaded, *errors.CodedError)

// Compiled is one block ready to solve: the variables in block order
// with their resolved bounds and start (NaN where the block sets none),
// and the objective and constraints compiled once. Evaluate a program
// with expr.Run(prog, Compiled.Vars(x)).
type Compiled struct {
	Names        []string
	Lower, Upper []float64
	Start        []float64
	Objective    *vm.Program
	Constraints  []*vm.Program
	// Refs and Loaded are the block's `models` entries and their
	// decoded files (zero / nil when it loads none): the runtime binds
	// them above the batch's fits for coef().
	Refs   model.Refs
	Loaded *model.Loaded
	// Unresolved are the bounds and starts a declaration-only Check
	// could not resolve (NaN in Lower / Upper / Start), in block order;
	// always nil from Compile, which resolves every one.
	Unresolved []Unresolved
	// PosteriorFit is a `posterior` block's draw carrier: the one fit
	// its objective and constraints read (checkPosteriorFit); "" without
	// `posterior`.
	PosteriorFit string
}

// Unresolved is one bound or start whose expression calls a
// request-bound function, so its value — and its bounds_crossed /
// start_out_of_bounds / finiteness checks — waits for the runtime.
type Unresolved struct {
	// Field is the optimize-rooted path (optimize[i].variables[j].upper).
	Field string
	// Expression is the bound as written.
	Expression string
	// Reads are the request-bound functions it calls, sorted.
	Reads []string
}

// Vars is the per-iterate expression env binding each decision variable
// to x (block order).
func (c *Compiled) Vars(x []float64) map[string]any {
	env := make(map[string]any, len(c.Names))
	for i, n := range c.Names {
		env[n] = x[i]
	}
	return env
}

// Check judges every block in order and returns the first refusal (nil
// when every block passes): Compile against env with every request-bound
// function declared but never run.
func Check(specs []types.OptimizeSpec, env Env) *errors.CodedError {
	_, cerr := CheckLoaded(specs, env)
	return cerr
}

// CheckLoaded is Check that also returns each block's decoded `models`
// files, index-aligned with specs (nil where a block loads none; nil
// throughout on a refusal) — what Env.Loaded hands a later Compile.
func CheckLoaded(specs []types.OptimizeSpec, env Env) ([]*model.Loaded, *errors.CodedError) {
	c, cerr := CheckAll(specs, env)
	if cerr != nil {
		return nil, cerr
	}
	return c.Loaded, nil
}

// Checked is CheckAll's view of blocks that pass: each block's decoded
// `models` files (index-aligned, nil where a block loads none) and every
// bound or start left Unresolved, in block order.
type Checked struct {
	Loaded     []*model.Loaded
	Unresolved []Unresolved
}

// CheckAll is CheckLoaded that also returns the bounds and starts the
// declaration-only compile could not resolve — what predict reports as
// not statically resolved.
func CheckAll(specs []types.OptimizeSpec, env Env) (*Checked, *errors.CodedError) {
	if cerr := checkNames(specs); cerr != nil {
		return nil, cerr
	}
	out := &Checked{Loaded: make([]*model.Loaded, len(specs))}
	for i := range specs {
		c, cerr := compile(&specs[i], i, env, placeholderBound(env), false)
		if cerr != nil {
			return nil, cerr
		}
		out.Loaded[i] = c.Loaded
		out.Unresolved = append(out.Unresolved, c.Unresolved...)
	}
	return out, nil
}

// placeholderBound declares env's request-bound functions with
// exprenv.Placeholder: the typed declaration a runtime compile binds,
// never run.
func placeholderBound(env Env) exprenv.Bound {
	return env.scalar().Declared()
}

// scalar is env as a scalar slot's environment (its Fits the block's,
// models included, once Compile has set them).
func (env Env) scalar() scalarslot.Env {
	return scalarslot.Env{Params: env.Params, Fits: env.Fits, Tables: env.Tables, Extensions: env.Extensions, Lookup: env.Lookup, Results: env.Results, Loaded: env.loaded}
}

// checkNames is the block-name rule across the list: every block named,
// no name twice.
func checkNames(specs []types.OptimizeSpec) *errors.CodedError {
	seen := map[string]bool{}
	for i := range specs {
		name := specs[i].Name
		if name == "" {
			return specInvalid(i, "", "optimize["+strconv.Itoa(i)+"].name", ReasonNameMissing,
				"has no name; every optimize block needs a name unique among the blocks", nil)
		}
		if seen[name] {
			return specInvalid(i, name, "optimize["+strconv.Itoa(i)+"].name", ReasonNameDuplicate,
				"repeats the name "+strconv.Quote(name)+" an earlier block already took", map[string]any{"value": name})
		}
		seen[name] = true
	}
	return nil
}

// Compile is one block's rules in order — its structure, then each
// variable (name, bounds, start), then the objective and each
// constraint (params, identifiers, compile, result type, coef()) — and
// on success the block ready to solve. i is the block's index (every
// refusal's details.optimize); bound carries the request-bound
// implementations (its Params are replaced by env.Params): the runtime
// passes its bound models, and every bound and start is evaluated
// against them, then range-checked. The block name list rule (missing /
// duplicate names) and the declaration-only judgement are Check's.
func Compile(spec *types.OptimizeSpec, i int, env Env, bound exprenv.Bound) (*Compiled, *errors.CodedError) {
	return compile(spec, i, env, bound, true)
}

// compile is Compile; run false is Check's declaration-only judgement,
// which leaves a bound calling a request-bound function Unresolved.
func compile(spec *types.OptimizeSpec, i int, env Env, bound exprenv.Bound, run bool) (*Compiled, *errors.CodedError) {
	at := "optimize[" + strconv.Itoa(i) + "]"
	if cerr := checkSpec(spec, i, at); cerr != nil {
		return nil, cerr
	}
	out := &Compiled{
		Names: make([]string, len(spec.Variables)),
		Lower: make([]float64, len(spec.Variables)),
		Upper: make([]float64, len(spec.Variables)),
		Start: make([]float64, len(spec.Variables)),
	}
	fits, cerr := loadModels(spec, i, at, env, out)
	if cerr != nil {
		return nil, cerr
	}
	env.Fits = fits
	if !run && env.Fold && out.Loaded != nil {
		// The Set the runtime binds the block's files into
		// (Service.runComposeOptimize: WithLoaded over the block's
		// models), without the batch's fits — a loaded name's figures do
		// not depend on them. A bind fault folds nothing (the runtime
		// refuses it as it binds). The bound must be the folding
		// Declared() over it: a placeholder would run on a foldable read.
		if set, err := (*model.Set)(nil).WithLoaded(model.Refs{Models: spec.Models}, out.Loaded); err == nil {
			env.loaded = set
			bound = env.scalar().Declared()
		}
	}
	reserved := reservedNames(env)
	seen := map[string]bool{}
	for j := range spec.Variables {
		v := &spec.Variables[j]
		vat := at + ".variables[" + strconv.Itoa(j) + "]"
		if cerr := checkVariableName(spec, i, vat, v.Name, reserved, seen); cerr != nil {
			return nil, cerr
		}
		seen[v.Name] = true
		r := boundResolver{spec: spec, i: i, variable: v.Name, env: env, bound: bound, run: run, out: out}
		lo, loOK, cerr := r.resolve(vat+".lower", v.Lower, true)
		if cerr != nil {
			return nil, cerr
		}
		hi, hiOK, cerr := r.resolve(vat+".upper", v.Upper, true)
		if cerr != nil {
			return nil, cerr
		}
		if loOK && hiOK && lo > hi {
			return nil, variableInvalid(i, spec.Name, vat, v.Name, ReasonBoundsCrossed,
				"has lower bound "+fmtFloat(lo)+" above its upper bound "+fmtFloat(hi), map[string]any{"lower": lo, "upper": hi})
		}
		start := math.NaN()
		if !v.Start.IsZero() {
			var startOK bool
			start, startOK, cerr = r.resolve(vat+".start", v.Start, false)
			if cerr != nil {
				return nil, cerr
			}
			if loOK && hiOK && startOK && (start < lo || start > hi) {
				return nil, variableInvalid(i, spec.Name, vat+".start", v.Name, ReasonStartOutOfBounds,
					"starts at "+fmtFloat(start)+", outside its bounds ["+fmtFloat(lo)+", "+fmtFloat(hi)+"]",
					map[string]any{"value": start, "lower": lo, "upper": hi})
			}
		}
		out.Names[j], out.Lower[j], out.Upper[j], out.Start[j] = v.Name, lo, hi, start
	}
	bound.Params = env.Params
	opts := compileOptions(out.Names, bound)
	prog, cerr := compileExpr(spec, i, at+".objective", spec.Objective, out.Names, env, opts)
	if cerr != nil {
		return nil, cerr
	}
	out.Objective = prog
	for k := range spec.Constraints {
		prog, cerr := compileExpr(spec, i, at+".constraints["+strconv.Itoa(k)+"].expr", spec.Constraints[k].Expr, out.Names, env, opts)
		if cerr != nil {
			return nil, cerr
		}
		out.Constraints = append(out.Constraints, prog)
	}
	if spec.Posterior != nil {
		fit, cerr := checkPosteriorFit(spec, i, at, env.Fits)
		if cerr != nil {
			return nil, cerr
		}
		out.PosteriorFit = fit
	}
	return out, nil
}

// PosteriorReads are the fits a block's objective and constraints read
// by a literal fit argument — every all-literal coef() call's and every
// literal-fit response() call's — each once, in first-read order
// (objective first, then each constraint).
func PosteriorReads(spec *types.OptimizeSpec) []string {
	var out []string
	add := func(name string) {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	srcs := []string{spec.Objective}
	for _, c := range spec.Constraints {
		srcs = append(srcs, c.Expr)
	}
	for _, src := range srcs {
		for _, c := range coefcall.LiteralCalls(src) {
			add(c.Fit)
		}
		for _, c := range coefcall.LiteralResponseCalls(src) {
			add(c.Fit)
		}
	}
	return out
}

// checkPosteriorFit is a `posterior` block's draw-carrier rule, judged
// once its expressions compiled (every literal read resolved in fits):
// the objective and constraints read exactly one fit
// (posterior_fit_missing / posterior_fits_multiple), a REG_MMM_BAYES
// fit (posterior_fit_not_bayes) carrying its draws (draws_required).
// It returns that fit. Predict's catalog judges a Compose fit by its
// request's params.return_draws and a loaded file by its outputs, as
// the runtime's bound set does.
func checkPosteriorFit(spec *types.OptimizeSpec, i int, at string, fits model.Catalog) (string, *errors.CodedError) {
	field := at + ".posterior"
	reads := PosteriorReads(spec)
	switch {
	case len(reads) == 0:
		return "", specInvalid(i, spec.Name, field, ReasonPosteriorFitMissing,
			"sets posterior, but its objective and constraints read no fit; read a REG_MMM_BAYES fit through coef() or response() with its name as a literal", nil)
	case len(reads) > 1:
		return "", specInvalid(i, spec.Name, field, ReasonPosteriorFitsMultiple,
			"sets posterior, but its objective and constraints read "+strconv.Itoa(len(reads))+" fits; a posterior block reads the draws of exactly one REG_MMM_BAYES fit",
			map[string]any{"fits": slices.Clone(reads)})
	}
	fit := reads[0]
	var sig model.Signature
	if fits != nil {
		sig, _ = fits.Signature(fit)
	}
	if sig.Type != types.REG_MMM_BAYES {
		typ := string(sig.Type)
		if sig.EffectiveKind() != types.FitKindRegression {
			typ = string(sig.EffectiveKind())
		}
		return "", specInvalid(i, spec.Name, field, ReasonPosteriorFitNotBayes,
			"sets posterior over fit "+strconv.Quote(fit)+", a "+typ+"; only a REG_MMM_BAYES fit has posterior draws",
			map[string]any{"fit": fit, "type": typ})
	}
	if sig.DrawsMissing {
		return "", specInvalid(i, spec.Name, field, model.ReasonDrawsRequired,
			"sets posterior over fit "+strconv.Quote(fit)+", which carries no posterior draws; fit it with params.return_draws true (a saved file keeps them)",
			map[string]any{"fit": fit})
	}
	return fit, nil
}

// checkPosterior is a `posterior` block's own structural rule: a known
// objective, q in (0, 1) for quantile only, draws >= 0.
func checkPosterior(spec *types.OptimizeSpec, i int, at string) *errors.CodedError {
	p := spec.Posterior
	if p == nil {
		return nil
	}
	name, pat := spec.Name, at+".posterior"
	obj := p.EffectiveObjective()
	if !slices.Contains(types.AllOptimizePosteriorObjectives(), obj) {
		return specInvalid(i, name, pat+".objective", ReasonPosteriorObjectiveUnknown,
			"has unknown posterior objective "+strconv.Quote(string(obj))+"; set mean or quantile",
			map[string]any{"value": string(obj), "valid": posteriorObjectiveNames()})
	}
	switch {
	case obj == types.OptimizePosteriorQuantile && !(p.Q > 0 && p.Q < 1):
		return specInvalid(i, name, pat+".q", ReasonPosteriorQInvalid,
			"sets posterior objective quantile with q "+fmtFloat(p.Q)+"; set q in (0, 1)",
			map[string]any{"value": p.Q})
	case obj != types.OptimizePosteriorQuantile && p.Q != 0:
		return specInvalid(i, name, pat+".q", ReasonPosteriorQInvalid,
			"sets posterior q "+fmtFloat(p.Q)+" with objective "+string(obj)+"; q is read only by objective quantile",
			map[string]any{"value": p.Q})
	}
	if p.Draws < 0 {
		return specInvalid(i, name, pat+".draws", ReasonPosteriorDrawsInvalid,
			"sets posterior draws "+strconv.Itoa(p.Draws)+"; set 0 (every draw) or more",
			map[string]any{"value": p.Draws})
	}
	return nil
}

func posteriorObjectiveNames() []string {
	var out []string
	for _, o := range types.AllOptimizePosteriorObjectives() {
		out = append(out, string(o))
	}
	return out
}

// loadModels is the block's `models` rule — model.CheckRefs against
// env.Outer, then the files read (env.Loaded[i] when an earlier Check
// read them, else env.Load) — and the catalog its coef() calls resolve
// against: the batch's fits plus the loaded models. A refusal keeps its
// own code and gains details.optimize / details.field.
func loadModels(spec *types.OptimizeSpec, i int, at string, env Env, out *Compiled) (model.Catalog, *errors.CodedError) {
	if len(spec.Models) == 0 {
		return env.Fits, nil
	}
	// The block's inline models read the batch's params.
	refs := model.Refs{Models: spec.Models, Params: types.Params(env.Params)}
	if cerr := model.CheckRefs(refs, env.Outer); cerr != nil {
		return nil, locate(cerr, i, spec.Name, at+".models")
	}
	var loaded *model.Loaded
	switch {
	case i < len(env.Loaded) && env.Loaded[i] != nil:
		loaded = env.Loaded[i]
	case env.Load != nil:
		var cerr *errors.CodedError
		if loaded, cerr = env.Load(refs); cerr != nil {
			return nil, locate(cerr, i, spec.Name, at+".models")
		}
	default:
		var cerr *errors.CodedError
		if loaded, cerr = model.LoadFiles(refs, nil, scalarslot.InlineNumber); cerr != nil {
			return nil, locate(cerr, i, spec.Name, at+".models")
		}
	}
	out.Refs, out.Loaded = refs, loaded
	return model.CatalogWithLoaded(env.Fits, refs, loaded), nil
}

// checkSpec is the block's structural rule, in field order.
func checkSpec(spec *types.OptimizeSpec, i int, at string) *errors.CodedError {
	name := spec.Name
	if len(spec.Variables) == 0 {
		return specInvalid(i, name, at+".variables", ReasonVariablesMissing, "declares no decision variable; give it at least one", nil)
	}
	if spec.Objective == "" {
		return specInvalid(i, name, at+".objective", ReasonObjectiveMissing, "has an empty objective; give it the expression to optimise", nil)
	}
	switch {
	case spec.Direction == "":
		return specInvalid(i, name, at+".direction", ReasonDirectionMissing, "sets no direction; set maximize or minimize",
			map[string]any{"valid": directionNames()})
	case !slices.Contains(types.AllOptimizeDirections(), spec.Direction):
		return specInvalid(i, name, at+".direction", ReasonDirectionUnknown, "has unknown direction "+strconv.Quote(string(spec.Direction))+"; set maximize or minimize",
			map[string]any{"value": string(spec.Direction), "valid": directionNames()})
	}
	eqs := 0
	for k, c := range spec.Constraints {
		cat := at + ".constraints[" + strconv.Itoa(k) + "]"
		if c.Expr == "" {
			return specInvalid(i, name, cat+".expr", ReasonConstraintExprMissing, "has a constraint with an empty expression", nil)
		}
		if !slices.Contains(types.AllOptimizeConstraintTypes(), c.Type) {
			return specInvalid(i, name, cat+".type", ReasonConstraintTypeUnknown, "has a constraint of type "+strconv.Quote(string(c.Type))+"; set eq (expr = 0) or ineq (expr >= 0)",
				map[string]any{"value": string(c.Type), "valid": constraintTypeNames()})
		}
		if c.Type == types.OptimizeConstraintEq {
			eqs++
		}
	}
	if eqs > len(spec.Variables) {
		return specInvalid(i, name, at+".constraints", ReasonTooManyEqualities,
			"has "+strconv.Itoa(eqs)+" eq constraints over "+strconv.Itoa(len(spec.Variables))+" variables; there may be no more eq constraints than variables",
			map[string]any{"value": eqs})
	}
	if spec.Starts != nil && (*spec.Starts < 1 || *spec.Starts > types.OptimizeMaxStarts) {
		return specInvalid(i, name, at+".starts", ReasonStartsInvalid,
			"sets starts "+strconv.Itoa(*spec.Starts)+"; set 1 to "+strconv.Itoa(types.OptimizeMaxStarts)+", or drop it for "+strconv.Itoa(types.OptimizeDefaultStarts),
			map[string]any{"value": *spec.Starts})
	}
	if t := spec.Tolerance; t < 0 || math.IsNaN(t) || math.IsInf(t, 0) {
		return specInvalid(i, name, at+".tolerance", ReasonToleranceInvalid, "sets tolerance "+fmtFloat(t)+"; set a finite number >= 0 (0 is the default 1e-6)",
			map[string]any{"value": t})
	}
	if spec.MaxIterations < 0 {
		return specInvalid(i, name, at+".max_iterations", ReasonMaxIterationsInvalid,
			"sets max_iterations "+strconv.Itoa(spec.MaxIterations)+"; set 0 (the default 100) or more",
			map[string]any{"value": spec.MaxIterations})
	}
	return checkPosterior(spec, i, at)
}

// identRe is a decision variable name.
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// checkVariableName is one variable's name rule.
func checkVariableName(spec *types.OptimizeSpec, i int, vat, name string, reserved, seen map[string]bool) *errors.CodedError {
	switch {
	case name == "":
		return variableInvalid(i, spec.Name, vat+".name", "", ReasonNameMissing, "has no name", nil)
	case !identRe.MatchString(name):
		return variableInvalid(i, spec.Name, vat+".name", name, ReasonNameInvalid,
			"is named "+strconv.Quote(name)+", which is not an identifier ([A-Za-z_][A-Za-z0-9_]*)", nil)
	case reserved[name] || !declarable(name):
		return variableInvalid(i, spec.Name, vat+".name", name, ReasonNameReserved,
			"is named "+strconv.Quote(name)+", which the expression language reserves (params, a function name or a keyword)", nil)
	case seen[name]:
		return variableInvalid(i, spec.Name, vat+".name", name, ReasonNameDuplicate,
			"repeats the name "+strconv.Quote(name)+" an earlier variable of the block took", nil)
	}
	return nil
}

// reservedNames are the identifiers a decision variable may not take:
// params, $env, every expression function in reach (the reserved
// built-ins, coef, sweep_axis, response, result, lookup, range_label, the set
// helpers, the embedder's
// functions)
// and expr-lang's own built-ins.
func reservedNames(env Env) map[string]bool {
	out := map[string]bool{exprenv.ParamsName: true, exprenv.CoefFunc: true, exprenv.SweepAxisFunc: true, exprenv.ResponseFunc: true, exprenv.LookupFunc: true, exprenv.RangeLabelFunc: true, exprenv.ResultFunc: true}
	for _, n := range exprenv.Names() {
		out[n] = true
	}
	for _, n := range exprenv.SetHelperNames() {
		out[n] = true
	}
	for _, n := range env.Extensions {
		out[n] = true
	}
	for _, n := range exprbuiltin.Names {
		out[n] = true
	}
	return out
}

// declarable reports whether name compiles as a float64 identifier —
// false for an expr-lang keyword (true, nil, in, not, let, …).
func declarable(name string) bool {
	prog, err := expr.Compile(name, expr.Env(map[string]any{name: float64(0)}))
	if err != nil {
		return false
	}
	t := prog.Node().Type()
	return t != nil && t.Kind() == reflect.Float64
}

// boundResolver resolves one variable's bounds and start.
type boundResolver struct {
	spec     *types.OptimizeSpec
	i        int
	variable string
	env      Env
	bound    exprenv.Bound
	run      bool
	out      *Compiled
}

// resolve resolves one bound or start: a finite literal, or a scalar
// expression (scalarslot.Eval over the block's params, fits, models,
// tables and embedder functions) evaluated to a finite number. ok is
// false — the value NaN, the field recorded Unresolved — when a
// declaration-only check meets an expression calling a request-bound
// function. required refuses an absent bound (bound_missing); an absent
// start is NaN, ok false.
func (r *boundResolver) resolve(field string, b types.OptimizeBound, required bool) (float64, bool, *errors.CodedError) {
	if b.IsZero() {
		if required {
			return 0, false, variableInvalid(r.i, r.spec.Name, field, r.variable, ReasonBoundMissing,
				"has no "+lastKey(field)+" bound; set a number or an expression such as \"params.<name>\"", nil)
		}
		return math.NaN(), false, nil
	}
	if !b.IsExpr() {
		v := b.Value()
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false, r.refuse(field, b, scalarslot.NotFinite(v))
		}
		return v, true, nil
	}
	val, refusal := scalarslot.Eval(b.Expr(), scalarslot.Real, r.env.scalar(), r.bound, r.run)
	if refusal != nil {
		return 0, false, r.refuse(field, b, refusal)
	}
	if !val.Resolved {
		r.out.Unresolved = append(r.out.Unresolved, Unresolved{Field: field, Expression: b.Expr(), Reads: val.Reads})
		return math.NaN(), false, nil
	}
	return val.Number, true, nil
}

// refuse locates a scalar-slot refusal at the bound: a shared code
// (PULSE_PARAMS_*, PULSE_MODEL_*, a table code, a failed fit's own code)
// keeps it, located; a slot-neutral reason becomes
// PULSE_OPTIMIZE_VARIABLE_INVALID reason bound_<reason> with
// details.expression (and details.param for a bare params reference).
func (r *boundResolver) refuse(field string, b types.OptimizeBound, refusal *scalarslot.Refusal) *errors.CodedError {
	if refusal.Coded != nil {
		return locate(refusal.Coded, r.i, r.spec.Name, field)
	}
	extra := make(map[string]any, len(refusal.Details)+2)
	for k, v := range refusal.Details {
		extra[k] = v
	}
	what := "has a " + lastKey(field) + " that " + refusal.What
	if b.IsExpr() {
		extra["expression"] = b.Expr()
		what = "has " + lastKey(field) + " " + strconv.Quote(b.Expr()) + ", which " + refusal.What
	}
	if p := b.Param(); p != "" {
		extra["param"] = p
	}
	return variableInvalid(r.i, r.spec.Name, field, r.variable, "bound_"+refusal.Reason, what, extra)
}

// compileOptions are the block's compile options: the decision
// variables declared float64, then the shared env with the request-bound
// declarations (exprenv.Bound.Options — the params fold first).
func compileOptions(names []string, bound exprenv.Bound) []expr.Option {
	decl := make(map[string]any, len(names))
	for _, n := range names {
		decl[n] = float64(0)
	}
	return append([]expr.Option{expr.Env(decl)}, bound.Options()...)
}

// compileExpr is one objective / constraint expression's rules: the
// params read, every free identifier a variable (or the params fold),
// the compile, a numeric result, then every all-literal coef() call
// against the fits and every literal lookup() / range_label() call
// against the tables.
func compileExpr(spec *types.OptimizeSpec, i int, field, src string, names []string, env Env, opts []expr.Option) (*vm.Program, *errors.CodedError) {
	if cerr := exprenv.ParamsRefusal(src, env.Params, false); cerr != nil {
		return nil, locate(cerr, i, spec.Name, field)
	}
	if refs, _, parsed := exprident.Free(src); parsed {
		folds := exprenv.FoldsParams(src)
		for _, ref := range refs {
			if slices.Contains(names, ref) || (folds && ref == exprenv.ParamsName) {
				continue
			}
			return nil, expressionInvalid(i, spec.Name, field, src, ReasonUnknownIdentifier,
				"reads "+strconv.Quote(ref)+", which is neither a decision variable of the block nor params.<name>",
				map[string]any{"identifier": ref, "valid": slices.Clone(names)})
		}
	}
	if cerr := resultcall.Refusal(src, env.Results); cerr != nil {
		return nil, locate(cerr, i, spec.Name, field)
	}
	prog, err := expr.Compile(src, opts...)
	if err != nil {
		reason := exprenv.CompileReason(err)
		return nil, expressionInvalid(i, spec.Name, field, src, ReasonCompileError,
			"does not compile: "+reason, map[string]any{"compile_error": reason})
	}
	if t := prog.Node().Type(); t != nil && !numericKind(t.Kind()) {
		return nil, expressionInvalid(i, spec.Name, field, src, ReasonNotNumeric,
			"yields "+t.String()+", not a number", map[string]any{"result_type": t.String()})
	}
	if cerr := coefcall.StaticRefusal(src, env.Fits); cerr != nil {
		return nil, locate(cerr, i, spec.Name, field)
	}
	if cerr := tablecall.Refusal(prog.Node(), env.Tables); cerr != nil {
		return nil, locate(cerr, i, spec.Name, field)
	}
	return prog, nil
}

// numericKind reports whether an expression result of kind k can be a
// number: a numeric kind, or an untyped (interface) result judged per
// evaluation.
func numericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Float64, reflect.Float32, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Interface:
		return true
	}
	return false
}

// ResultSlots are the indices of the Compose slots the blocks' literal
// result() calls read — in a bound, a start, the objective or a
// constraint — sorted and unique: the slots the result pre-phase runs
// before any block (resultcall.Slots). Nil when no block reads one.
func ResultSlots(specs []types.OptimizeSpec, cat *resultcall.Catalog) []int {
	var srcs []string
	for i := range specs {
		spec := &specs[i]
		for _, v := range spec.Variables {
			for _, b := range []types.OptimizeBound{v.Lower, v.Upper, v.Start} {
				if b.IsExpr() {
					srcs = append(srcs, b.Expr())
				}
			}
		}
		srcs = append(srcs, spec.Objective)
		for _, c := range spec.Constraints {
			srcs = append(srcs, c.Expr)
		}
	}
	return resultcall.Slots(srcs, cat)
}

// EffectiveSeed is the seed a block's Latin-hypercube starts are drawn
// with: spec.RNGSeed when non-zero, else regresample.DeriveSeed — the
// one rng_seed 0 derivation every seeded draw shares — over the block's
// wire form with rng_seed excluded (never the clock). It reads the spec
// only; predict and the runtime report the same seed.
func EffectiveSeed(spec *types.OptimizeSpec) int64 {
	if spec == nil {
		return 0
	}
	if spec.RNGSeed != 0 {
		return spec.RNGSeed
	}
	unseeded := *spec
	unseeded.RNGSeed = 0
	body, err := json.Marshal(&unseeded)
	if err != nil {
		body = nil
	}
	return regresample.DeriveSeed(body, 0, nil)
}

// directionNames / constraintTypeNames are the valid values as strings.
func directionNames() []string {
	var out []string
	for _, d := range types.AllOptimizeDirections() {
		out = append(out, string(d))
	}
	return out
}

func constraintTypeNames() []string {
	var out []string
	for _, c := range types.AllOptimizeConstraintTypes() {
		out = append(out, string(c))
	}
	return out
}

// lastKey is the final key of an optimize-rooted path.
func lastKey(field string) string {
	for k := len(field) - 1; k >= 0; k-- {
		if field[k] == '.' {
			return field[k+1:]
		}
	}
	return field
}

func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// blockLabel names block i in a message.
func blockLabel(i int, name string) string {
	if name == "" {
		return "optimize block " + strconv.Itoa(i)
	}
	return "optimize block " + strconv.Quote(name)
}

// baseDetails are every refusal's location keys.
func baseDetails(i int, field, reason string, extra map[string]any) map[string]any {
	d := map[string]any{"optimize": i, "field": field, "reason": reason}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

func specInvalid(i int, name, field, reason, what string, extra map[string]any) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_OPTIMIZE_SPEC_INVALID,
		blockLabel(i, name)+" "+what, baseDetails(i, field, reason, extra))
}

func variableInvalid(i int, name, field, variable, reason, what string, extra map[string]any) *errors.CodedError {
	d := baseDetails(i, field, reason, extra)
	d["variable"] = variable
	subject := "variable"
	if variable != "" {
		subject = "variable " + strconv.Quote(variable)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OPTIMIZE_VARIABLE_INVALID,
		blockLabel(i, name)+": "+subject+" "+what, d)
}

func expressionInvalid(i int, name, field, src, reason, what string, extra map[string]any) *errors.CodedError {
	d := baseDetails(i, field, reason, extra)
	d["expression"] = src
	return errors.NewCodedErrorWithDetails(errors.PULSE_OPTIMIZE_EXPRESSION_INVALID,
		blockLabel(i, name)+": "+lastKey(field)+" expression "+strconv.Quote(src)+" "+what, d)
}

// locate is a shared refusal (PULSE_PARAMS_UNDECLARED, a PULSE_MODEL_*
// coef() refusal) with the block's location keys added: its own code,
// message and details kept.
func locate(cerr *errors.CodedError, i int, name, field string) *errors.CodedError {
	d := make(map[string]any, len(cerr.Details)+2)
	for k, v := range cerr.Details {
		d[k] = v
	}
	d["optimize"] = i
	d["field"] = field
	out := errors.NewCodedErrorWithDetails(cerr.Code, blockLabel(i, name)+": "+cerr.Message, d)
	out.Cause = cerr.Cause
	return out
}

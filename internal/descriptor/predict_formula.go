package descriptor

import (
	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// formulaExpressionMissing is newFormulaAttribute's refusal text
// (internal/processing/attribute.go), repeated here because predict
// cannot import the engine; TestFormulaAttribute_ExpressionSlotPredictParity
// pins the two to the byte.
const formulaExpressionMissing = "formula attribute requires an expression"

// formulaExpressionRefusal re-derives ATTR_FORMULA's constructor
// refusal: the expression is read from the attribute's `expression`
// slot ONLY — an empty slot is PROCESSING_CONFIG, also when the text
// sits in params.expression, which nothing reads. Before, predict
// passed such a request and only the runtime refused it. A slot whose
// type the instance hides routes to "" and is skipped, exactly like a
// never-registered name. Nil when every formula carries an expression.
func formulaExpressionRefusal(req *types.Request, opts *PredictOptions) *errors.CodedError {
	if req == nil {
		return nil
	}
	for _, attr := range req.Attributes {
		if attr != nil && opRoute(opts.Instance, attr.Type) == types.ATTR_FORMULA && attr.Expression == "" {
			return errors.NewCodedError(errors.PROCESSING_CONFIG, formulaExpressionMissing)
		}
	}
	return nil
}

// validateFormulaExpressions is formulaExpressionRefusal on the request
// root (Predict). PredictCompose and PredictChain call the refusal per
// slot / stage, after its field references and weights — the order the
// runtime builds the attribute in.
func validateFormulaExpressions(env *descriptor.Envelope, req *types.Request, opts *PredictOptions) {
	if ce := formulaExpressionRefusal(req, opts); ce != nil {
		addCodedError(env, ce)
	}
}

// The expression compile (U45 E2-S3).
//
// The field-reference walk compiles every ATTR_FORMULA,
// FILTER_EXPRESSION (a Request filterer, a facet's, a fitted vector
// attribute's params.fit_on) and total() argument with expr-lang's full
// type check, exactly as the engine's build does, and records a failure
// as PULSE_FORMULA_COMPILE_ERROR through the engine's own constructor
// (exprenv.CompileError, the exprenv.Site* nouns), so code, message and
// details agree to the byte. The walk is the runtime's entry check too,
// so a bad expression is refused before any record on both sides.
//
// The env is the engine's, declared without it:
//
//   - every schema field typed as a decoded record surfaces it
//     (exprenv.Prototype);
//   - the named vectors the expression reads, []float64
//     (vectors.ExprBindings, exprenv.VectorType);
//   - every name an earlier slot derives — a feature output, an
//     earlier attribute's label — float64 (exprenv.DerivedType), except
//     a derived column the dictionary-inheritance view types
//     (prevstate.KeySchema: a previous-state output), declared as its
//     state field binds (exprenv.DerivedDecl — a categorical is a
//     string), the engine's declaration from the same view. The
//     engine meets these only on the first record (a deferred compile)
//     and types them from it; predict types them from the producing
//     slot. A name no slot produces stays undeclared and fails as the
//     unknown name the first-record compile meets. Once an extension
//     feature opened the column set (w.open), or the named vectors did
//     not resolve, such a name is unknowable here and the expression is
//     not compiled;
//   - the calendar guard over the schema (exprenv.CalendarGuard): a
//     bare datetime field as a day helper's day, or a literal unknown
//     date_trunc unit, is refused exactly as the engine refuses it;
//   - the request-bound functions through exprenv.Bound — coef with its
//     typed overloads, the embedder's ExprFunctions, lookup when a
//     LookupTable is registered — and, for a total-bearing formula,
//     total() and its patcher (totalcall.Options), each bound to a
//     placeholder that never runs.
//
// The ref() patcher is not run: it only appends a constant plan and
// swaps an aligned argument of an untyped built-in, which changes no
// compile verdict. An expression the walk already refused (a total()
// site / nesting / arity refusal, a coef() refusal) is not compiled,
// as the engine's build stops at that refusal first.

// exprCompileScope is the walk's compile state, fixed per request.
type exprCompileScope struct {
	schema *encoding.Schema
	// keys is the request's dictionary-inheritance view over schema
	// (prevstate.KeySchema; schema itself when no slot contributes): a
	// derived column it declares is typed as its source field.
	keys *encoding.Schema
	// vecs are the request's named vectors, resolved over schema;
	// vecNames their names, and vecsFailed set when Resolve refused
	// (its refusal is reported elsewhere): a name among vecNames is then
	// unknowable here.
	vecs       []vectors.Resolved
	vecNames   map[string]bool
	vecsFailed bool
	bound      exprenv.Bound
}

// initCompile fixes the walk's compile scope for req over schema.
func (w *fieldRefWalk) initCompile(req *types.Request, schema *encoding.Schema) {
	c := &w.compile
	c.schema = schema
	c.keys = prevstate.KeySchema(req.Attributes, schema)
	if len(req.Vectors) > 0 {
		c.vecNames = make(map[string]bool, len(req.Vectors))
		for _, v := range req.Vectors {
			c.vecNames[v.Name] = true
		}
	}
	vecs, err := vectors.ResolveRequest(req, schema)
	c.vecs, c.vecsFailed = vecs, err != nil
	c.bound = exprenv.Bound{Coef: exprenv.Placeholder}
	if w.snap != nil {
		for _, fn := range w.snap.ExprFunctions {
			c.bound.Extensions = append(c.bound.Extensions, exprenv.NamedFunc{Name: fn.Name, Fn: exprenv.Placeholder})
		}
		if len(w.snap.LookupTables) > 0 {
			c.bound.Lookup = exprenv.Placeholder
		}
	}
}

// checkCompile records src's compile refusal, formula marking an
// ATTR_FORMULA expression (total() admitted) and a filter otherwise. It
// stands down for an empty expression (formulaExpressionRefusal's, and
// the filterer's own, refusal) and when the walk recorded a refusal for
// src since before.
func (w *fieldRefWalk) checkCompile(src string, formula bool, before int) {
	if src == "" || len(w.out) > before {
		return
	}
	base := w.compile.bound.Options()
	if !formula {
		if ce := w.compileRefusal(src, exprenv.SiteFilter, base); ce != nil {
			w.out = append(w.out, ce)
		}
		return
	}
	opts := base
	if totalcall.Contains(src) {
		keys := totalcall.Args(src)
		for _, key := range keys {
			if ce := w.compileRefusal(key, exprenv.SiteTotalArgument, base); ce != nil {
				w.out = append(w.out, ce)
				return
			}
		}
		opts = append(w.compile.bound.Options(), totalcall.Options(totalcall.Index(keys), exprenv.Placeholder)...)
	}
	if ce := w.compileRefusal(src, exprenv.SiteFormula, opts); ce != nil {
		w.out = append(w.out, ce)
	}
}

// compileRefusal compiles src at site against the walk's current column
// set: nil when it compiles, or when a name it reads is unknowable here.
func (w *fieldRefWalk) compileRefusal(src, site string, opts []expr.Option) *errors.CodedError {
	c := &w.compile
	decl := exprenv.Prototype(c.schema)
	refs, _, parsed := exprident.Free(src)
	isField := func(name string) bool { _, ok := decl[name]; return ok }
	for _, v := range vectors.ExprBindings(refs, c.vecs, isField) {
		decl[v.Name] = exprenv.VectorType
	}
	if parsed {
		for _, name := range refs {
			if _, ok := decl[name]; ok {
				continue
			}
			switch {
			case w.cols[name], w.preFilter[name]:
				decl[name] = exprenv.DerivedDecl(c.keys, name)
			case w.open, c.vecsFailed && c.vecNames[name]:
				return nil
			}
		}
	}
	all := make([]expr.Option, 0, 3+len(opts))
	all = append(all, expr.Env(decl), exprenv.CalendarGuard(c.schema))
	prog, err := expr.Compile(src, append(all, opts...)...)
	if err != nil {
		return exprenv.CompileError(site, src, err)
	}
	return exprenv.ResultRefusal(site, src, prog)
}

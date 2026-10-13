package processing

import (
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"
	exprtypes "github.com/expr-lang/expr/types"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprbuiltins"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/vectors"
)

// Compile-once expr-lang programs for FILTER_EXPRESSION and ATTR_FORMULA.
//
// Both operators used to call expr.Compile on EVERY row, against that
// row's Record.AllValues(). That cost ~7-24 µs a row (compile work grows
// with the env width) and had a correctness bug: a null field is absent
// from AllValues, so any row where a referenced field was null failed to
// COMPILE ("unknown name") and aborted the whole request.
//
// An exprProgram compiles once per build, against a PROTOTYPE env
// declared from the schema: every schema field, typed exactly as
// AllValues surfaces it (exprValueType — float64 for numeric/date/bool
// columns, string for a dictionary-bearing categorical, []string for a
// dictionary-bearing set, Decimal128 / uint64 / SetMask otherwise). Type
// checking therefore sees the same types a per-row compile saw on a row
// where every field was present, so a request that compiled per row
// compiles here, to the same program, and one that failed to compile
// fails with the same code and message — now at build, before any row.
//
// Names outside the schema (a feature output, an earlier attribute's
// label) are unknowable at build. When the expression names one, the
// compile is DEFERRED to the first record, against the prototype plus
// that record's own values — exactly the env today's first-row compile
// used — and then cached. Either way the program compiles once.
//
// Null semantics. A null (or absent) field is simply absent from the
// run-time env, and expr-lang's map env reads an absent key as nil. A
// row whose referenced fields are all present runs the TYPED program —
// the per-row compile's program, byte-identical results. A row where a
// referenced field is null runs an UNTYPED twin (every env name declared
// `any`, compiled lazily on the first such row), so nil follows
// expr-lang's own nil rules uniformly across field types: `x == nil` is
// true, `x != nil` false, `x == v` false, `x != v` true, `x in [...]`
// false, `x ?? d` yields d; an ordering comparison, arithmetic or a
// function that rejects nil raises an evaluation error. The typed
// program would specialise `cat == "a"` on a string column to a string
// compare that cannot take nil, so without the twin `!=` on a null
// categorical and `!=` on a null number would disagree. What an
// evaluation error on a null row MEANS is the operator's call:
// FILTER_EXPRESSION treats it as UNKNOWN and drops the row (SQL WHERE
// semantics); ATTR_FORMULA has no null output and raises it.
//
// Concurrency: a compiled *vm.Program is immutable and expr.Run builds
// a fresh VM per call, so one program is shared by every goroutine that
// holds the FilterFunc / attribute. The deferred and untyped compiles
// are guarded by mu and published through atomics.

// exprCompiles counts expr.Compile calls made by exprProgram —
// process-wide, monotonic. The deterministic compile-count gates read
// deltas of it; ExprCompileCount exposes it for cross-package tests and
// diagnosis.
var exprCompiles atomic.Int64

// ExprCompileCount returns the number of expr-lang compiles made by
// FILTER_EXPRESSION and ATTR_FORMULA since process start. Diagnostic
// only: a request compiles each expression once per build (plus once
// more, lazily, when a row with a null referenced field first appears),
// independent of the row count.
func ExprCompileCount() int64 { return exprCompiles.Load() }

// exprProgram is one expression's compile-once state.
type exprProgram struct {
	src  string
	what string // "filter" | "formula" — the historical message noun
	opts []expr.Option

	// decl is the typed env declaration of the build (schema prototype,
	// or prototype + first record on the deferred path). Frozen once the
	// typed program is published.
	decl exprtypes.Map
	// inputs are the expression's free identifiers that name an env
	// value (not a callee, a let binding or $env); a row where one is
	// absent from the env is a null-input row. Settled with the typed
	// program.
	inputs []string
	// refs are every free identifier the AST names (callees and let
	// bindings excluded), used to decide at build whether the schema
	// alone can declare them.
	refs []string
	// wholeEnv is set when the expression reads $env: it then needs the
	// record's whole AllValues map, not just the named fields.
	wholeEnv bool
	// vecs are the request's named vectors (Request.Vectors) the
	// expression references, each bound per row as a []float64 (nil:
	// none — the binding path then costs nothing). frames recycles the
	// per-row member buffers.
	vecs   []exprVectorBinding
	frames sync.Pool
	// refPatch rewrites the expression's ref() calls on every compile
	// (nil: it calls none).
	refPatch *refPatcher

	typed   atomic.Pointer[vm.Program]
	untyped atomic.Pointer[vm.Program]

	mu         sync.Mutex
	compileErr error // cached deferred compile failure
	untypedErr error // cached untyped compile failure (not expected)
	untypedSet bool
}

// newExprProgram builds an expression's program against schema. When
// every name the expression references is a schema field or one of the
// request's named vectors (vecs, resolved by internal/vectors; nil:
// none), or the expression does not parse, it compiles now and returns
// any compile error; otherwise the compile is deferred to the first
// record.
//
// refTables are the instance's reference-vector tables (nil: none). An
// expression calling ref() is judged against them and the named vectors
// at build (refStaticRefusal — predict's rules) and every compile
// patches its ref() calls (refPatcher).
func newExprProgram(src, what string, schema *encoding.Schema, vecs []vectors.Resolved, refTables vectors.ReferenceTables, opts []expr.Option) (*exprProgram, error) {
	if err := vectorStaticRefusal(src, what); err != nil {
		return nil, err
	}
	p := &exprProgram{src: src, what: what, opts: opts, decl: exprPrototype(schema)}
	if callees, _ := exprident.Callees(src); slices.Contains(callees, exprbuiltins.RefFunc) {
		scope := refScope(src, refTables, vecs, func(name string) bool {
			// A native vector field is a vector here, not a shadowing field.
			return schema != nil && schema.Field(name) != nil && !vectors.IsNativeVector(name, schema)
		})
		if err := refStaticRefusal(src, scope); err != nil {
			return nil, err
		}
		p.refPatch = &refPatcher{scope: scope}
	}
	refs, wholeEnv, parsed := exprFreeIdentifiers(src)
	p.refs, p.wholeEnv = refs, wholeEnv
	if err := p.bindVectors(vecs); err != nil {
		return nil, err
	}
	deferred := false
	if parsed {
		for _, name := range refs {
			if _, ok := p.decl[name]; !ok {
				deferred = true
				break
			}
		}
	}
	if deferred {
		return p, nil
	}
	prog, err := p.compile(p.decl)
	if err != nil {
		return nil, p.compileError(err)
	}
	p.settleInputs()
	p.typed.Store(prog)
	return p, nil
}

// compile runs expr.Compile against decl with the extension options.
func (p *exprProgram) compile(decl exprtypes.Map) (*vm.Program, error) {
	exprCompiles.Add(1)
	opts := make([]expr.Option, 0, 2+len(p.opts))
	opts = append(opts, expr.Env(decl))
	opts = append(opts, p.opts...)
	if p.refPatch != nil {
		opts = append(opts, expr.Patch(p.refPatch))
	}
	return expr.Compile(p.src, opts...)
}

func (p *exprProgram) compileError(err error) error {
	return errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
		fmt.Sprintf("compiling %s expression: %s", p.what, p.src))
}

func (p *exprProgram) evalError(err error) error {
	if r := exprRefusal(err); r != nil {
		// A coef() refusal, an unequal-length vector pair or an
		// invalid vnormalize mode is the request's, under its own code.
		return r
	}
	return errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
		fmt.Sprintf("evaluating %s expression: %s", p.what, p.src))
}

// settleInputs keeps the referenced names the declaration binds — the
// named vectors excepted: a vector is always bound (a null member is a
// NaN element), so it never makes a row a null-input row.
func (p *exprProgram) settleInputs() {
	for _, name := range p.refs {
		if _, ok := p.decl[name]; ok && !p.isVector(name) {
			p.inputs = append(p.inputs, name)
		}
	}
}

// exprVectorBinding is one named vector an expression reads: its
// identifier and its member-read plan in axis order (memberFill — the
// shared fill, so a native vector field reads like its scalar twin).
type exprVectorBinding struct {
	name string
	fill memberFill
}

// exprVectorFrame is one row's member buffers, index-aligned with
// exprProgram.vecs. Frames are recycled through exprProgram.frames, so
// each worker reuses its buffers row after row; that is safe because no
// built-in retains or returns its argument (internal/vecmath returns a
// NEW slice) and a vector as the expression's result is refused.
//
// boxed holds each buffer already converted to an interface value: the
// slice header never changes (only its elements are rewritten), so the
// env reuses the box and a row binds without allocating.
type exprVectorFrame struct {
	bufs  [][]float64
	boxed []any
}

var exprVectorType = exprtypes.TypeOf([]float64(nil))

// bindVectors declares every named vector the expression references
// as a []float64 identifier: a Request.Vectors entry (no schema field
// shadows one: a vector named like a field is refused earlier,
// PULSE_VECTOR_NAME_COLLISION), or a native vector field named directly
// — bound through the shared fill, so a null vector reads all-NaN like
// its all-null scalar twin, never nil and never a null-input row. An
// unreferenced vector binds nothing. A vector that is the whole
// expression is refused here, at build: a result is a scalar.
func (p *exprProgram) bindVectors(vecs []vectors.Resolved) error {
	if len(vecs) == 0 || len(p.refs) == 0 {
		return nil
	}
	for _, name := range p.refs {
		v, ok := vectors.Find(vecs, name)
		if !ok {
			continue
		}
		if _, field := p.decl[name]; field && v.Native == nil {
			continue
		}
		p.vecs = append(p.vecs, exprVectorBinding{name: name, fill: newMemberFill(v)})
		p.decl[name] = exprVectorType
	}
	if len(p.vecs) == 0 {
		return nil
	}
	if tree, err := exprparser.Parse(p.src); err == nil {
		if id, ok := tree.Node.(*exprast.IdentifierNode); ok && p.isVector(id.Value) {
			return vectorResultError(p.what)
		}
	}
	p.frames.New = func() any {
		f := &exprVectorFrame{bufs: make([][]float64, len(p.vecs)), boxed: make([]any, len(p.vecs))}
		for i := range p.vecs {
			f.bufs[i] = make([]float64, p.vecs[i].fill.Len())
			f.boxed[i] = f.bufs[i]
		}
		return f
	}
	return nil
}

// isVector reports whether name is a bound named vector.
func (p *exprProgram) isVector(name string) bool {
	for i := range p.vecs {
		if p.vecs[i].name == name {
			return true
		}
	}
	return false
}

// fillVectors binds every named vector into env from r through the
// shared fill (memberFill): a null (or absent) member reads NaN, so a
// row whose members are all null — a null native vector included —
// binds an all-NaN vector, never nil. The returned frame goes back
// through releaseVectors once the evaluation is done.
func (p *exprProgram) fillVectors(env map[string]any, r *Record) *exprVectorFrame {
	f := p.frames.Get().(*exprVectorFrame)
	for i := range p.vecs {
		b := &p.vecs[i]
		b.fill.fill(r, f.bufs[i], nil)
		env[b.name] = f.boxed[i]
	}
	return f
}

func (p *exprProgram) releaseVectors(f *exprVectorFrame) {
	if f != nil {
		p.frames.Put(f)
	}
}

// program returns the typed program, compiling it on the first record
// when the build deferred it.
func (p *exprProgram) program(env map[string]any) (*vm.Program, error) {
	if prog := p.typed.Load(); prog != nil {
		return prog, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if prog := p.typed.Load(); prog != nil {
		return prog, nil
	}
	if p.compileErr != nil {
		return nil, p.compileErr
	}
	decl := make(exprtypes.Map, len(p.decl)+len(env))
	for k, v := range env {
		decl[k] = exprtypes.TypeOf(v)
	}
	for k, t := range p.decl { // schema fields keep their declared type
		decl[k] = t
	}
	prog, err := p.compile(decl)
	if err != nil {
		p.compileErr = p.compileError(err)
		return nil, p.compileErr
	}
	p.decl = decl
	p.settleInputs()
	p.typed.Store(prog)
	return prog, nil
}

// untypedProgram returns the null-row twin: the same expression over
// the same env names, every one declared `any`.
func (p *exprProgram) untypedProgram() (*vm.Program, error) {
	if prog := p.untyped.Load(); prog != nil {
		return prog, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.untypedSet {
		return p.untyped.Load(), p.untypedErr
	}
	decl := make(exprtypes.Map, len(p.decl))
	for k := range p.decl {
		decl[k] = exprtypes.Any
	}
	prog, err := p.compile(decl)
	p.untypedSet = true
	if err != nil {
		p.untypedErr = p.compileError(err)
		return nil, p.untypedErr
	}
	p.untyped.Store(prog)
	return prog, nil
}

// exprEnvPool recycles the per-row env maps of runRecord. An env never
// outlives its expr.Run: the program cannot return the map itself (an
// expression that names $env takes the AllValues path instead).
var exprEnvPool = sync.Pool{New: func() any { return make(map[string]any, 8) }}

// runRecord evaluates the expression against one record. The env holds
// only the fields the expression reads (Record.exprValue — the values
// AllValues would give them), so a row costs O(referenced fields), not
// O(schema width). An expression that reads $env, or whose compile is
// still deferred, takes the whole AllValues map, as does a record that
// already carries it.
func (p *exprProgram) runRecord(r *Record) (out any, nullInput bool, err error) {
	if p.typed.Load() == nil || p.wholeEnv || r.allValuesCache != nil {
		// Deferred compile, $env, or a record whose AllValues map is
		// already built (a buffered record another expression read).
		if len(p.vecs) == 0 {
			return p.run(r.AllValues())
		}
		// The AllValues map is the record's shared cache: bind the
		// vectors into a pooled copy, never into the cache.
		env := exprEnvPool.Get().(map[string]any)
		for k, v := range r.AllValues() {
			env[k] = v
		}
		f := p.fillVectors(env, r)
		out, nullInput, err = p.run(env)
		p.releaseVectors(f)
		clear(env)
		exprEnvPool.Put(env)
		return out, nullInput, err
	}
	env := exprEnvPool.Get().(map[string]any)
	for _, name := range p.inputs {
		if v, ok := r.exprValue(name); ok {
			env[name] = v
		} else {
			nullInput = true
		}
	}
	var f *exprVectorFrame
	if len(p.vecs) > 0 {
		f = p.fillVectors(env, r)
	}
	out, err = p.eval(env, nullInput)
	p.releaseVectors(f)
	clear(env)
	exprEnvPool.Put(env)
	return out, nullInput, err
}

// run evaluates the expression against a whole env. nullInput reports
// whether a referenced field was null / absent on this row; err is
// already coded (compile or evaluation).
func (p *exprProgram) run(env map[string]any) (out any, nullInput bool, err error) {
	if _, err := p.program(env); err != nil {
		return nil, false, err
	}
	for _, name := range p.inputs {
		if _, ok := env[name]; !ok {
			nullInput = true
			break
		}
	}
	out, err = p.eval(env, nullInput)
	return out, nullInput, err
}

// eval runs the typed program, or its untyped twin on a null-input row.
func (p *exprProgram) eval(env map[string]any, nullInput bool) (any, error) {
	prog := p.typed.Load()
	if nullInput {
		// The twin is strictly more permissive than the typed program,
		// so its compile cannot fail where the typed one passed; if it
		// ever did, the typed program runs rather than an operator
		// reading a compile failure as a null verdict.
		if untyped, err := p.untypedProgram(); err == nil {
			prog = untyped
		}
	}
	out, err := expr.Run(prog, env)
	if err != nil {
		return nil, p.evalError(err)
	}
	return out, nil
}

// exprPrototype declares every schema field typed as Record.AllValues
// surfaces it. A duplicated name keeps its first occurrence, as
// Schema.Field does.
func exprPrototype(schema *encoding.Schema) exprtypes.Map {
	decl := exprtypes.Map{}
	if schema == nil {
		return decl
	}
	for i := range schema.Fields {
		f := &schema.Fields[i]
		if _, dup := decl[f.Name]; dup {
			continue
		}
		decl[f.Name] = exprtypes.TypeOf(reflect.Zero(exprValueType(f)).Interface())
	}
	return decl
}

var (
	exprFloat64Type    = reflect.TypeOf(float64(0))
	exprStringType     = reflect.TypeOf("")
	exprStringsType    = reflect.TypeOf([]string(nil))
	exprUint64Type     = reflect.TypeOf(uint64(0))
	exprSetMaskType    = reflect.TypeOf(encoding.SetMask{})
	exprDecimal128Type = reflect.TypeOf(encoding.Decimal128{})
	exprFloat64sType   = reflect.TypeOf([]float64(nil))
)

// exprValueType is the Go type Record.AllValues gives field f's value:
// a dictionary-bearing categorical resolves to its label, a
// dictionary-bearing set to its label slice, a decimal / dictionary-less
// set rides its wide value, a vector its []float64 elements, everything
// else is the float64 slot. Pinned
// against every decode path by TestExprValueType_MatchesAllValues.
func exprValueType(f *encoding.Field) reflect.Type {
	switch {
	case f.Type.IsCategorical():
		if f.Dictionary != nil {
			return exprStringType
		}
	case f.Type.IsSet():
		if f.Dictionary != nil {
			return exprStringsType
		}
		if f.Type.IsWideSet() {
			return exprSetMaskType
		}
		return exprUint64Type
	case f.Type == encoding.FieldTypeDecimal128:
		return exprDecimal128Type
	case f.Type.IsVector():
		return exprFloat64sType
	}
	return exprFloat64Type
}

// exprFreeIdentifiers returns the names an expression reads from its
// env (internal/exprident.Free — the one walk the vector-reference count
// and the predict layer also read): every identifier that is not a
// callee, a let binding or $env, plus `$env.name` / `$env["name"]`
// members; usesEnv reports any use of $env. parsed=false when the
// expression does not parse (the compile then reports the syntax error).
func exprFreeIdentifiers(src string) (names []string, usesEnv, parsed bool) {
	return exprident.Free(src)
}

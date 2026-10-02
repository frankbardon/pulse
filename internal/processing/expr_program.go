package processing

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"
	exprtypes "github.com/expr-lang/expr/types"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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

	typed   atomic.Pointer[vm.Program]
	untyped atomic.Pointer[vm.Program]

	mu         sync.Mutex
	compileErr error // cached deferred compile failure
	untypedErr error // cached untyped compile failure (not expected)
	untypedSet bool
}

// newExprProgram builds an expression's program against schema. When
// every name the expression references is a schema field (or the
// expression does not parse), it compiles now and returns any compile
// error; otherwise the compile is deferred to the first record.
func newExprProgram(src, what string, schema *encoding.Schema, opts []expr.Option) (*exprProgram, error) {
	p := &exprProgram{src: src, what: what, opts: opts, decl: exprPrototype(schema)}
	refs, wholeEnv, parsed := exprFreeIdentifiers(src)
	p.refs, p.wholeEnv = refs, wholeEnv
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
	opts := make([]expr.Option, 0, 1+len(p.opts))
	opts = append(opts, expr.Env(decl))
	opts = append(opts, p.opts...)
	return expr.Compile(p.src, opts...)
}

func (p *exprProgram) compileError(err error) error {
	return errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
		fmt.Sprintf("compiling %s expression: %s", p.what, p.src))
}

func (p *exprProgram) evalError(err error) error {
	return errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
		fmt.Sprintf("evaluating %s expression: %s", p.what, p.src))
}

// settleInputs keeps the referenced names the declaration binds.
func (p *exprProgram) settleInputs() {
	for _, name := range p.refs {
		if _, ok := p.decl[name]; ok {
			p.inputs = append(p.inputs, name)
		}
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
		return p.run(r.AllValues())
	}
	env := exprEnvPool.Get().(map[string]any)
	for _, name := range p.inputs {
		if v, ok := r.exprValue(name); ok {
			env[name] = v
		} else {
			nullInput = true
		}
	}
	out, err = p.eval(env, nullInput)
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
)

// exprValueType is the Go type Record.AllValues gives field f's value:
// a dictionary-bearing categorical resolves to its label, a
// dictionary-bearing set to its label slice, a decimal / dictionary-less
// set rides its wide value, everything else is the float64 slot. Pinned
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
	}
	return exprFloat64Type
}

// exprFreeIdentifiers returns the names an expression reads from its
// env: every identifier that is not a callee, a let binding or $env,
// plus `$env.name` / `$env["name"]` members; usesEnv reports any use of
// $env. parsed=false when the expression does not parse (the compile then
// reports the syntax error).
func exprFreeIdentifiers(src string) (names []string, usesEnv, parsed bool) {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil, false, false
	}
	v := &exprFreeIdentVisitor{skip: map[*exprast.IdentifierNode]bool{}, lets: map[string]bool{}, seen: map[string]bool{}}
	// Walk is post-order: a node's children are visited before it, so
	// mark callees / let names / $env members in a first pass.
	exprast.Walk(&tree.Node, &exprFreeIdentMarker{v: v})
	exprast.Walk(&tree.Node, v)
	return v.names, v.usesEnv, true
}

type exprFreeIdentMarker struct{ v *exprFreeIdentVisitor }

func (m *exprFreeIdentMarker) Visit(node *exprast.Node) {
	switch n := (*node).(type) {
	case *exprast.CallNode:
		if id, ok := n.Callee.(*exprast.IdentifierNode); ok {
			m.v.skip[id] = true
		}
	case *exprast.VariableDeclaratorNode:
		m.v.lets[n.Name] = true
	case *exprast.MemberNode:
		if id, ok := n.Node.(*exprast.IdentifierNode); ok && id.Value == "$env" {
			if s, ok := n.Property.(*exprast.StringNode); ok {
				m.v.add(s.Value)
			}
		}
	}
}

type exprFreeIdentVisitor struct {
	usesEnv bool
	skip    map[*exprast.IdentifierNode]bool
	lets    map[string]bool
	seen    map[string]bool
	names   []string
}

func (v *exprFreeIdentVisitor) add(name string) {
	if !v.seen[name] {
		v.seen[name] = true
		v.names = append(v.names, name)
	}
}

func (v *exprFreeIdentVisitor) Visit(node *exprast.Node) {
	id, ok := (*node).(*exprast.IdentifierNode)
	if !ok || v.skip[id] || v.lets[id.Value] {
		return
	}
	if id.Value == "$env" {
		v.usesEnv = true
		return
	}
	v.add(id.Value)
}

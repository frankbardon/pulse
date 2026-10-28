package descriptor

// ExprReads is what one expr-lang expression reads, from a parse alone
// (no compile, no cohort, no request): the data dependencies an editor,
// a linter or an agent needs to wire a formula, a filter or a holdout to
// the columns and request parameters it names, before any run.
//
// The reading is the engine's own (the walk predict and the runtime bind
// an expression's env from), so a name listed here is a name the engine
// would look up, and a name it skips is one the engine never binds:
//
//   - Vars are the free identifiers in first-use order: a column, a
//     derived label, a vector, or (in an optimize block) a decision
//     variable — which one is the caller's context. String literals,
//     keywords (true, false, nil, in, not, ...), let-bound names, closure
//     pointers (#, .field), callees and $env itself are not vars. A
//     member chain reads its root (`a.b.c` reads `a`); `$env.x` and
//     `$env["x"]` read `x`. `params` is a var only when used other than
//     as `params.<name>` / `params["<name>"]` (bare, a dynamic index, a
//     callee): then it reads a column named params.
//   - Params are the request-parameter names read as `params.<name>` or
//     `params["<name>"]`, first-use order. A `let params = ...` binding
//     shadows the namespace: none of its uses is a parameter read. The
//     reading assumes `params` is not a column of the request; where it
//     is, every such read is refused PULSE_PARAMS_FIELD_COLLISION.
//   - Funcs are the functions called by name (`f(...)`), in walk order
//     (an inner call before the call enclosing it) without repeats.
//     Stock expr-lang built-ins (len, abs, filter, ...) parse as
//     operators, not calls, and are not listed.
//   - UsesEnv reports any use of $env. A dynamic `$env[expr]` may read
//     any column, so when it is set Vars is a lower bound.
//
// Every slice is non-nil (an empty JSON array, never null).
type ExprReads struct {
	Vars    []string `json:"vars"`
	Params  []string `json:"params"`
	Funcs   []string `json:"funcs"`
	UsesEnv bool     `json:"uses_env,omitempty"`
}

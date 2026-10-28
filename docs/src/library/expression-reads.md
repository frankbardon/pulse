# Expression Read Sets

**Audience:** Go embedders building editors, linters or agents that need
an expression's data dependencies before running it.

`pulse.ExpressionReads(src)` parses one expr-lang expression and returns
what it reads, as a `descriptor.ExprReads`. It needs no instance, cohort
or request, and it never compiles or runs the expression.

```go
reads, err := pulse.ExpressionReads(`wave == params.wave and lookup("t", id) > 0`)
// reads.Vars   == []string{"wave", "id"}
// reads.Params == []string{"wave"}
// reads.Funcs  == []string{"lookup"}
```

| Field | JSON | Holds |
|---|---|---|
| `Vars` | `vars` | free names in first-use order: columns, derived labels, vectors (in an `optimize` block, decision variables) |
| `Params` | `params` | request parameters read as `params.<name>` or `params["<name>"]`, first-use order |
| `Funcs` | `funcs` | functions called by name, inner call first, no repeats |
| `UsesEnv` | `uses_env` | any use of `$env`; a dynamic `$env[expr]` may read any column, so `Vars` is then a lower bound |

Every slice is non-nil.

## Scoping

The reading is the same walk the engine binds an expression's env from,
so a listed name is one the engine would look up:

- String literals and keywords (`true`, `false`, `nil`, `in`, `not`, ...)
  are not reads.
- `let` bindings are not reads; the names their values read are.
- Closure pointers (`#`, `.field` inside `map` / `filter` / `all`, ...)
  are not reads; the collection and any outer name are.
- A member chain reads its root: `a.b.c` reads `a`.
- `$env.x` and `$env["x"]` read `x`.
- Callees are not vars. Stock built-ins (`len`, `abs`, `filter`, ...) are
  operators, not calls, and are not listed in `Funcs`.
- `params.x` is a parameter read, not a column read. `params` itself is
  a var only when used another way (bare, `params[k]` with a non-literal
  key, a callee), which reads a column named `params`. A
  `let params = ...` binding shadows the namespace: none of its uses is
  a parameter read. The reading assumes `params` is not a column; where
  it is, the engine refuses every parameter read
  (`PULSE_PARAMS_FIELD_COLLISION`).

## Errors

A source that does not parse returns an empty read set and a
`*errors.CodedError` with code `PULSE_FORMULA_COMPILE_ERROR`, details
`compile_error` (the parser's one-line reason) and `expression`. A
request template's `{{token}}` is not expression syntax: render the
template first.

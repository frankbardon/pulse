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

## Request read maps

`(*pulse.Pulse).RequestReads(doc, root)` applies the same reading to a
whole request document and returns a `descriptor.RequestReads`: for
every component slot that reads data, where it sits and what it reads.
Like predict without a cohort, it needs no cohort and runs nothing. The
document need not validate.

`doc` is the raw JSON of a Request, ComposedRequest, ChainRequest or
FacetRequest. `root` names which (`descriptor.ReadsRootRequest`,
`ReadsRootComposed`, `ReadsRootChain`, `ReadsRootFacet`); `""` detects it
from the top-level keys: `stages` is a chain; `requests`, `sweep`,
`banner`, `fits` or `optimize` is a compose; `fields` is a facet;
anything else is a request.

```go
reads, err := p.RequestReads([]byte(`{
  "params": {"wave": 2},
  "filterers": [{"type": "FILTER_EXPRESSION", "expression": "wave == params.wave"}],
  "attributes": [{"type": "ATTR_FORMULA", "label": "period", "expression": "wave == 2 ? 2 : 1"}],
  "vectors": [{"name": "items", "fields": ["q1", "q2", "q3"]}],
  "matrices": [{"type": "MAT_SEM", "vector": "items", "params": {"model": "f =~ q1 + q2 + q3", "group": "period"}}]
}`), "")
// reads.Root == "request"; reads.Slots (path | key -> columns / params):
//   attributes[0] | expression   -> [wave] / []
//   filterers[0]  | expression   -> [wave] / [wave]
//   matrices[0]   | params.group -> [period] / []
//   matrices[0]   | vector       -> [items] / []
//   vectors[0]    | fields       -> [q1 q2 q3] / []
```

Each `descriptor.SlotReads`:

| Field | JSON | Holds |
|---|---|---|
| `Path` | `path` | the component holding the slot, in the document's JSON shape: `filterers[0]`, `matrices[0]`, `requests[2].filterers[0]`, `stages[1].request.groups[0]`, `optimize[0]`; `""` for a slot on the document root |
| `Key` | `key` | the dotted key of the value inside the component: `expression`, `field`, `fields`, `params.group`, `weight.field`, `covariance.cluster` |
| `Request` | `request` | the request whose `params` the slot's `params.<name>` reads resolve against (`""` the root: a Request's, Compose batch's, chain's or facet's own `params`); a Compose or chain sub-request inherits the root's `params` beneath its own |
| `Columns` | `columns` | the column-namespace names read, first-read order, no repeats |
| `Params` | `params` | the request parameters read |
| `Variables` | `variables` | an optimize objective's or constraint's names: decision variables, never columns |
| `Funcs` | `funcs` | an expression slot's called functions |
| `UsesEnv` | `uses_env` | an expression slot uses `$env`; `Columns` is then a lower bound |
| `Error` | `error` | the slot's own failure (`code`, `message`, `details`), with `Columns` and `Params` empty |

Slots are sorted by `Path` (array indices numerically), then `Key`. A
list slot (`fields`, `predictors`, `partition_by`, a window's or test's
`order_by`, a join's `on`) is one entry under the list's key.

A column name is resolved by the caller. It can be a cohort field, a
label an earlier step computes (a feature, an attribute, a window or an
aggregation), or a vector name (a `vector` slot, or an expression that
names one). In the example, `period` is the column the `ATTR_FORMULA`
computes. An inline `fields` entry is listed as written, glob entries
included. A vector's `pattern` is not expanded.

### What is walked

- **Expression slots**: Pulse's own expression-slot list. That is the
  filterer and attribute `expression`, a regression `holdout`, an
  `OVERLAY_FORMULA` `params.formula`, a feature's `params.fit_on`, and
  an optimize block's `objective` and constraint `expr`. Each is read
  as `ExpressionReads` reads it.
- **Typed slots**: the request roots' column slots: `field`, `fields`,
  `vector`, `target`, `predictors`, `split_by`, `rows` / `cols`,
  `subject_field`, `order_field`, `order_by`, `partition_by`, `weight`,
  `tz_field`, `time_zone_field`, a regression's `unpenalized`,
  `bounds.nonnegative`, `jackknife_by` and `covariance`, `sort`,
  `labels`, a join's `on` (left side), a vector's `fields`, the
  crosstab and banner groupers and cells, and a facet's `fields` and
  `additive_fields`.
- **Operator params**: every params entry the instance manifest marks
  `names_column`, e.g. MAT_SEM `params.group` or AGG_RATIO
  `params.numerator_field`. MAT_PARTIAL_CORRELATION `control: "all"`
  is a keyword, not a column.
- **Nested components**: an attribute's `params.fit_on` filterer list,
  and MAT_DISTANCE's `params.between` grouper.
- **Extension operators**: the registration's `FieldInputs` hook,
  reported under key `params`. Without a hook, an extension contributes
  only its typed slots. So does an operator type the instance does not
  know.
- **Roots**: Compose `requests`, `fits[].request`, `sweep.request`,
  `banner.request`, banner sections and groups, `optimize` blocks
  (their `"params.<name>"` variable bounds are params reads), Compose
  overlays; chain `stages[].request` and chain overlays; facet
  filterers, labels and fields.

### Errors

One bad slot never fails the map. An expression that does not parse
gives that slot `PULSE_FORMULA_COMPILE_ERROR`. A value of the wrong JSON
shape (a number for a field name, say) gives `SERVICE_VALIDATION` with
`details.path` and `details.key`. Only a document that is not a JSON
object, or an unknown `root`, returns an error for the whole call
(`SERVICE_VALIDATION`).

Render request templates (`$var`, `{{token}}`, `$when`) first. An
unrendered `{{token}}` in an expression slot is that slot's
`PULSE_FORMULA_COMPILE_ERROR`; anywhere else it reads as a literal name.

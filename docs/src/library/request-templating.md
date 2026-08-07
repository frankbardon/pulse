# Request Templating

**Audience:** Go embedders and operators who want a request *shape*
fixed once — reviewed, stored, version-controlled — and callers who
supply only the parts that legitimately vary.

A **template** is a JSON document at rest that renders into a
**validated, typed Pulse request**. The analytical shape (which
aggregations, which groupers, which filters, which cohort) is
authored once; a caller passes a small map of variables and gets back
a `*types.Request` — or one of the four other request roots — ready
to hand to `Process`.

Rendering is **execution-free**. It never opens a cohort, never reads
a record, and never touches the filesystem beyond the template file
itself.

> **Not `expr-lang`.** Template variables and Pulse's expression
> runtime are different axes that happen to both use braces. `$var`,
> `{{name}}` and `$when` are *request-authoring* parameters,
> substituted **before** the JSON is decoded into a request.
> `ATTR_FORMULA` and `FILTER_EXPRESSION` are `expr-lang` expressions
> over *row fields*, evaluated **during** execution. They do not
> interoperate: a formula cannot see a template variable, and a
> formula string sitting in a template body is just text as far as
> the renderer is concerned.

## Why the wrapper

A template file is not a bare request with holes punched in it. It is
an explicit four-part wrapper:

```json
{
  "name": "finance/revenue",
  "description": "Revenue bucketed by size, optionally segmented",
  "target": "request",
  "variables": [],
  "body": {}
}
```

| Key | Required | Why it exists |
|---|---|---|
| `target` | yes | Names the request root the rendered body strict-decodes into. It **cannot be inferred** — the five roots overlap enough in shape that guessing would silently pick the wrong one. |
| `variables` | no | Declares every parameter a caller may (or must) supply. Declaration-first is what lets a UI build a form, or an agent build a prompt, from the template alone — without rendering it first to discover what it wants. |
| `body` | yes | The parameterised request, markers still in place. Deliberately **not runnable as-is**: it becomes a request only once rendered. |
| `name` | no | Optional, and the file path is authoritative. A `name` that disagrees with the derived path name is rejected rather than silently renaming the author's template. |
| `description` | no | Human-facing prose, surfaced in listings. |

Unknown top-level keys are **rejected**. A typo'd `"varaibles"` that
silently yielded zero variables is precisely the class of silent
failure this feature exists to eliminate.

`target` is one of five lowercase values, each selecting a public
request root:

| `target` | Decodes into | Execute with |
|---|---|---|
| `request` | `types.Request` | `Process`, `Predict`, `ProcessStream` |
| `composed` | `types.ComposedRequest` | `Compose`, `ComposeParallel` |
| `chain` | `types.ChainRequest` | `ProcessChain` |
| `facet` | `types.FacetRequest` | `FacetSchema` |
| `sample` | `types.SampleRequest` | `SampleWithRequest` |

## A worked example

Put this at `<root>/finance/revenue.json`, which names it
`finance/revenue`:

```json
{
  "target": "request",
  "variables": [
    {"name": "metric", "type": "field", "required": true},
    {"name": "bucket", "type": "integer", "default": 10},
    {"name": "segs", "type": "list", "items": "string"}
  ],
  "body": {
    "label": "total {{metric}}",
    "cohort": {"filename": "sales.pulse"},
    "aggregations": [
      {"type": "AGG_SUM", "field": {"$var": "metric"}}
    ],
    "groups": [
      {"type": "GROUP_RANGE", "field": {"$var": "metric"}, "interval": {"$var": "bucket"}},
      {"$when": "segs", "type": "GROUP_CATEGORY", "field": "region", "include": {"$var": "segs"}}
    ]
  }
}
```

Point an engine at the directory and render it:

```go
p, err := pulse.New(pulse.Options{
    DataDir:      "/var/data/pulse",
    TemplateDirs: []string{"/etc/pulse/templates"},
})
if err != nil {
    log.Fatal(err)
}

req, err := p.RenderTemplateRequest("finance/revenue", map[string]any{
    "metric": "revenue",
})
if err != nil {
    log.Fatal(err)
}

resp, err := p.Process(ctx, req)
```

The rendered request is:

```json
{
  "label": "total revenue",
  "cohort": {"filename": "sales.pulse"},
  "aggregations": [{"type": "AGG_SUM", "field": "revenue"}],
  "groups": [{"type": "GROUP_RANGE", "field": "revenue", "interval": 10}]
}
```

Three things happened. `metric` was substituted into both a string
(`"total {{metric}}"`) and a slot (`{"$var": "metric"}`). `bucket`
fell back to its declared default and spliced in as the **number**
`10`, not the string `"10"`. And the `segs`-guarded grouper — no
value supplied, no default — was dropped entirely, with the array
compacted so no `null` hole was left behind.

## Substitution — three forms, and nothing else

There is no expression language in a template body. A body is a
request with holes in it, not a program. The whole grammar is three
forms.

### Slot markers — `{"$var": "name"}`

An object is a marker **if and only if** `$var` is its single key and
its value is a non-empty string. The whole object is replaced by the
resolved value, and the substitution is **type-preserving**:

```json
{"interval": {"$var": "bucket"}}
```

renders to `{"interval": 10}` — never `{"interval": "10"}`. This is
what lets a marker fill a numeric, boolean, array or object slot.

`{"$var": "x", "other": 1}` is not a marker; it is literal data and
passes through untouched. A lone `$var` whose value is *not* a
non-empty string is rejected at validation rather than passed through
as data.

Substituted values are spliced as data and are **not re-walked**. A
caller who supplies the literal string `"{{metric}}"` gets that string
verbatim in the rendered request, not a second round of interpolation.

### String sugar — `"{{name}}"`

Inside a **string value**, `{{name}}` interpolates the variable's
natural text: strings verbatim, numbers as their exact literal,
booleans as `true`/`false`, dates as their ISO string. Whitespace is
tolerated — `{{ name }}` works. `{{{{` is the escape for a literal
`{{`.

Two types have no natural text form: a `list` and a `period` inside a
string are `PULSE_TEMPLATE_VAR_TYPE`. Splice them with a marker
instead.

Object **keys** are not interpolated. `{{` appearing in a key is
rejected outright at validation, so this is never a silent no-op.

### Optional blocks — `{"$when": "name", ...}`

A block carrying `$when` survives if and only if the named variable
resolved. The `$when` key is stripped either way, so it never reaches
the decoder.

**`$when` is presence, not truthiness.** It drops only when the
variable was neither supplied nor defaulted. A caller who supplies
`""`, `[]`, `0`, or `false` has supplied a value, and the block
**stays** — a legitimate zero has to be templatable.

Guards evaluate **before** the walk descends, so an unresolved marker
inside a dropped block never raises. That ordering is the entire point
of the guard: it is how an author says "this slot is optional".

A dropped object is removed from its parent array with the slice
compacted (a `null` hole would decode to a nil operator slot), or its
key is removed from its parent object. `$when` on the root body is an
error.

A surviving *unguarded* marker naming a variable that resolved to
nothing is `PULSE_TEMPLATE_UNRESOLVED`. A marker never silently
vanishes — optionality is spelled `$when`, always.

## The nine variable types

| Type | Accepts | Extra declaration slot | Notes |
|---|---|---|---|
| `string` | any JSON string | — | |
| `number` | any JSON number | — | integral or fractional |
| `integer` | a JSON number with no fractional part | — | `1` and `1.0` qualify; `1.5` does not |
| `boolean` | a JSON bool | — | |
| `field` | a JSON string naming a cohort field | — | shape-identical to `string`; a distinct type so cohort-bound field-name constraining can be layered on later without a wire change |
| `enum` | a string in the declared set | `values` (required) | membership is exact and **case-sensitive** |
| `list` | a JSON array | `items` (required) | `items` must be a **scalar** type — lists do not nest |
| `date` | a string parsing as `YYYY-MM-DD` | — | a strict subset of `encoding.DateFormats`; `03/04/2024` is ambiguous and rejected |
| `period` | an object carrying exactly one of `ranges` or `table` | — | mirrors the `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES` `Params` shape |

The scalar types — `string`, `number`, `integer`, `boolean`, `field`,
`date` — are the six legal `items` values. The three non-scalars are
excluded for distinct reasons: `list` would nest and a nested list has
no element-type slot to declare; `period` is a JSON object rather than
a scalar; `enum` is scalar on the wire but draws its membership set
from the variable-level `values` slot, which a list declaration cannot
express per-element unambiguously.

Every declaration also carries:

- **`required`** — the variable must resolve at render.
  `required` together with a `default` is legal and not a
  contradiction: the default resolves the variable, so a required
  variable carrying a default can never go missing.
- **`default`** — the value used when the caller supplies none, held
  as raw JSON so integer fidelity survives to the render walk. An
  explicit JSON `null` means "no default", identical to omitting it.
- **`description`** — optional prose, so a caller can build a form or
  a prompt from the declaration alone.

Unknown keys inside a `period` object, and inside each
`{label, start, end}` range, are rejected — a `"startt"` typo must not
silently become an open bound.

## Layered template directories

`Options.TemplateDirs []string` is the ordered list of directory
roots. When it is empty, `PULSE_TEMPLATES_DIR` applies instead, split
on `os.PathListSeparator` (`:` on Unix, `;` on Windows) — the same
PATH-style shape, so a deployment can layer a site-override directory
ahead of a shipped default without a second variable. The
programmatic option wins outright: when `TemplateDirs` is non-empty
the environment is never consulted.

Every `*.json` file beneath a root is a template. Its **name** is its
path relative to **its own root**, minus the `.json` extension,
forward-slash separated regardless of host OS:

```text
/etc/pulse/templates/finance/revenue.json   →   finance/revenue
```

Subdirectories namespace for free, and the root's own location never
leaks into the name.

**Roots are a precedence list and the first root wins.** The same name
under a later root is *shadowed*, not rejected — that is what makes an
override layer possible. The losing entries are not discarded: their
source paths are recorded on the winner's `Summary.Shadows`, so "why
is my override not taking effect?" is answerable from
`ListTemplates()` alone. A shadowed entry deliberately gets no summary
of its own; a listing whose entries cannot all be fetched would be a
trap.

Root edge cases:

| Root | Behaviour |
|---|---|
| blank / whitespace-only | **skipped** — an empty element in a PATH-style list is not a fault |
| does not exist | **skipped** — an absent optional override layer is not a fault |
| exists but is a **regular file** | **error**, naming the path — a root pointed at a file is a genuine misconfiguration |
| exists and is a directory | walked |

A file whose derived name would be empty (`.json`, `finance/.json`) is
an error naming the path at startup, not a silent skip.

**Filesystem faults use `DATA_FILE`**, not the `PULSE_TEMPLATE_*`
family. That split is deliberate: `PULSE_TEMPLATE_*` describes a fault
in a *document* or a *render*, while an unreadable directory, an
unreadable file, or a root that is a regular file is an ordinary
filesystem problem and reads better under the code every other
filesystem fault in Pulse uses.

Template directories are process **configuration**, not cohort data,
so they are read through the `os` package rather than `afero` — the
same sanctioned exception the label-table and range-table loaders use.

## The hot-reload lifecycle

Templates reload without a restart. A lookup whose cached snapshot has
aged past the store's rescan interval (one second, a package constant
rather than an `Option` — a dial nobody can set better than the store
can is a permanent public surface bought for nothing) re-walks the
roots first. A file dropped into a scanned directory becomes
renderable, a changed file starts serving its new content, and a
deleted one stops resolving.

A rescan is a directory walk plus one stat per candidate. A file whose
size *and* modification time both match the copy already parsed is
carried over rather than re-read, so rescanning an unchanged directory
costs syscalls and no JSON parsing.

`ReloadTemplates()` forces that walk immediately. It is an escape
hatch, not the mechanism — reach for it when you need determinism
rather than eventual visibility: a deployment step that writes a
template and must render it on the next line, or a test that would
otherwise have to sleep.

### What a malformed file does depends on when it breaks

This is the load-bearing policy, and the phase split is intentional:

| Phase | A malformed template file | Why |
|---|---|---|
| At `pulse.New()` | **hard-fails startup**, naming the offending path | At boot, a broken document is a deploy error the operator must see immediately. |
| After startup, parsed successfully before | **keeps serving its last-good parse**; marked `Broken` in `ListTemplates()` with the fault in `Error` | A partially-written file is the *normal transient state* when a human edits in place. Killing the catalog over a half-finished save is worse than briefly-stale content. |
| After startup, never parsed successfully | Listed with `Broken: true` and an **empty `Target`**; `GetTemplate` / `RenderTemplate` return `PULSE_TEMPLATE_INVALID` naming the path | There is no last-good copy to fall back to. The entry is listed to be **seen**, not fetched. |

Two consequences follow, and both are deliberate:

**`ReloadTemplates()` returns `nil` for a broken file.** An error there
would tell a caller its whole catalog failed over one half-written
editor save, masking every otherwise-healthy template. Brokenness is
observable through the listing, not through the reload return value.
An unreadable file degrades the same way — it is also one file.

**A root that is not a directory is still a hard error from
`ReloadTemplates()`.** That is a misconfiguration rather than a
transient edit, so it must not be swallowed. A failed walk leaves the
previously loaded templates entirely in place.

Repairing a file clears its broken state on the next rescan.
`Summary.Broken` and `Summary.Error` are both `omitempty`, so a
healthy listing marshals byte-identically to the shape that predates
per-file degradation.

One scoping note: a broken file in a *lower-precedence* root that is
shadowed by a healthy override is tracked internally but not surfaced
in `ListTemplates()`. The entry an operator can actually reach is
fine.

## The facade

Five methods, all on `*pulse.Pulse`:

```go
summaries := p.ListTemplates()                        // []template.Summary
tmpl, err := p.GetTemplate("finance/revenue")         // *template.Template
rendered, err := p.RenderTemplate(name, vars)         // *template.Rendered — all five targets
req, err := p.RenderTemplateRequest(name, vars)       // *types.Request — the 95% path
err = p.ReloadTemplates()                             // force a rescan now
```

`ListTemplates` is sorted by name and always returns a **non-nil**
slice (possibly empty) so it marshals as `[]` rather than `null`. An
engine with no template directories configured lists nothing; that is
an ordinary deployment, not a fault, though the other three lookup
methods will answer `PULSE_TEMPLATE_NOT_FOUND`.

`RenderTemplate` is the general form. Exactly one of `Rendered`'s
typed pointers is populated, selected by `Rendered.Target` — read that
pointer (or `Rendered.Typed()`) and hand it to the matching execution
method. There are deliberately no per-execution-mode convenience
wrappers: N execution modes would mean N wrappers to keep in sync
forever, for no capability gain.

`Rendered.JSON` is the rendered body before decode, retained alongside
the typed value on purpose. **Re-marshaling the typed request would
not reproduce it** — every request struct is dense with `omitempty`,
so any slot that rendered to an explicit zero would silently vanish
from a round trip. If you echo, log, or diff a render, use
`Rendered.JSON`.

The returned template from `GetTemplate` is the engine's own copy and
must be treated as read-only. Rendering never mutates it.

## The error family

Nine codes, all in the `PULSE` domain. Every one carries the template
name under the `template` detail key; the variable-scoped members also
carry `variable`. `pulse errors lookup <CODE>` is authoritative for
the message and fixups.

The organising rule is **provenance, not detection time**:

- A bad **declared default** is an *author* error →
  `PULSE_TEMPLATE_INVALID`, always. Default checking is fully semantic
  at declaration time — enum membership, date parseability, the
  `period` ranges-XOR-table rule — which is what keeps the
  fail-fast-at-`pulse.New()` promise real. A template with an
  unparseable date default must not lie in wait until someone renders
  it.
- A bad **caller-supplied value** is a *caller* error →
  `PULSE_TEMPLATE_VAR_TYPE` / `PULSE_TEMPLATE_VAR_ENUM`.

The same split governs variable *names*. A `$var`, `{{name}}` or
`$when` naming an **undeclared** variable is an author error caught at
validation time — a typo'd `{"$var": "metrc"}` fails at `pulse.New()`,
not on first render. A **declared but unresolved** reference is a
render-time `PULSE_TEMPLATE_UNRESOLVED`.

| Code | Raised when | What the author or caller should do |
|---|---|---|
| `PULSE_TEMPLATE_NOT_FOUND` | the requested name is not registered — including every name on an engine with no template directories | Check the derived name (path relative to its own root, minus `.json`); check the roots are configured and reachable. |
| `PULSE_TEMPLATE_INVALID` | a document failed declaration validation: malformed JSON, unknown wrapper key, non-object body, duplicate variable name, uninterpretable declaration, bad declared default, marker naming an undeclared variable, or `name` disagreeing with the path | Fix the document. This is always an **author** fault, and at startup it is fatal. |
| `PULSE_TEMPLATE_TARGET_UNKNOWN` | `target` is absent or not one of the five roots — **and also** when `RenderTemplateRequest` is called on a template declaring some other (perfectly valid) target | Two meanings, so read the details. `expected_target` present means the target is fine and the *method* is the mismatch: call `RenderTemplate` and read the pointer named in the message. |
| `PULSE_TEMPLATE_VAR_MISSING` | a `required` variable resolved to nothing — no supplied value, no default | Supply the variable, or give the declaration a default. |
| `PULSE_TEMPLATE_VAR_UNKNOWN` | the caller supplied a variable the template does not declare | Fix the caller's key. Unknown variables are **rejected, never ignored** — silently dropping one produces a request that looks parameterised but is not. |
| `PULSE_TEMPLATE_VAR_TYPE` | a supplied value does not match the declared type — including a `list` or `period` used inside a `{{}}` string, which has no text form | Correct the supplied JSON type, or splice with a `{"$var": ...}` marker instead of string sugar. |
| `PULSE_TEMPLATE_VAR_ENUM` | a supplied value is not a member of an `enum`'s `values` set | Membership is exact and case-sensitive; the details carry the permitted set. |
| `PULSE_TEMPLATE_UNRESOLVED` | an unguarded marker survived substitution — declared, but nothing resolved | Supply the variable, give it a default, or wrap the block in `$when`. Rendering hard-fails rather than emit a request carrying a literal marker string. |
| `PULSE_TEMPLATE_RENDER_INVALID` | substitution succeeded but the rendered JSON failed **strict decode** into the target request type | Usually an unknown field, or a substituted value landing in a slot whose type it does not fit. |

Strict decode is harsher than the rest of Pulse, and that is worth
knowing before it bites: a body copied out of an `examples/` file
**with its `_meta` block still attached** will fail
`PULSE_TEMPLATE_RENDER_INVALID`. The message names the offending field
and calls out the `_meta` case explicitly.

## What rendering does not do

**Render never opens a cohort.** A template that renders is well-formed
against the request *shape* — nothing more. Field existence, type
compatibility, operator applicability and streamability are all
[`Predict`](../cli/api-predict.md)'s job, and stay there. A render
that succeeds and a `Predict` that fails is the expected division of
labour, not a bug.

There is also **no CLI surface** for templating. It is a library and
embedding feature: `Options.TemplateDirs` /
`PULSE_TEMPLATES_DIR` plus the five facade methods.

## See also

- [`pulse.New` & Options](options.md) — the rest of the construction
  surface.
- [Go API Overview](overview.md) — the facade methods a rendered
  request is handed to.
- [`pulse api predict`](../cli/api-predict.md) — the cohort-aware
  validation a render deliberately leaves undone.
- `skills/request-templating.md` — the dense, agent-facing version of
  this chapter, reachable at runtime through `skills.Get`.

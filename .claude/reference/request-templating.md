# Request templating — the `template/` document model

Relocated verbatim from CLAUDE.md (section `## Request templating`). CLAUDE.md keeps the one-paragraph identity inline — what the surface is, the `template/` import ceiling, the five facade methods, and the fact that this is a **library/embedding surface only: no CLI leaf, no MCP tool**. Everything below is the long form it points at.

Load it before changing the request-template document model (`template.Template` / `Variable` / `Summary` wrapper keys), the variable type set (`template.AllVarTypes`), the target set (`template.AllTargets`), or the substitution syntax (`$var` / `{{}}` / `$when`) — CLAUDE.md's Update Demand table names this file as a required companion for exactly that trigger.

`format_version` does NOT move for anything described here: `template.Template` / `Variable` / `Summary` / `Rendered` live in `template/`, not `types/`, so they are unreachable from `descriptor.BuildPayloadSchema` and `descriptor/testdata/payload-schema.json` is untouched.

## Contents

Paragraph order below is the file's order; each entry is that paragraph's bold lead-in.

1. Not expr-lang.
2. File wrapper.
3. Substitution — three forms, no expression language.
4. Nine variable types
5. Directory precedence.
6. Hot-reload lifecycle — phase split is the contract. (carries the three-row phase table)
7. Errors — nine `PULSE_TEMPLATE_*` codes, chosen by provenance not detection time.
8. Render never opens a cohort.
9. `format_version` stays `"1.1"`.

## The contract

**Not expr-lang.** `$var` / `{{}}` / `$when` are *request-authoring* parameters substituted **before** decode; `ATTR_FORMULA` / `FILTER_EXPRESSION` are expr-lang over *row fields* at execution time. No interop by design — a formula cannot see a template variable, and a formula string in a body is inert text to the renderer.

**File wrapper.** `{"name"?, "description"?, "target", "variables"[], "body"}`; **unknown top-level keys rejected** (a typo'd `"varaibles"` yielding zero variables is the silent failure this feature exists to kill). `target` ∈ `request | composed | chain | facet | sample` (lowercase) selects the strict-decode root — one of the five `types` request roots — required, never inferred. `name` derives from the file path (path relative to its own root, minus `.json`, forward-slash separated); a `name` key disagreeing with the path is rejected. `body` is a non-empty object, deliberately **not runnable as-is**.

**Substitution — three forms, no expression language.** (1) Slot marker `{"$var":"bucket"}` — a marker **iff** `$var` is the only key and its value a non-empty string; replaced whole and **type-preserving** (`{"interval":{"$var":"bucket"}}` → `{"interval":10}`, never `"10"`). `{"$var":"x","other":1}` is literal data; substituted values are spliced as data and never re-walked. (2) String sugar `"{{name}}"` — inside a **string value** only, `{{ name }}` tolerated, `{{{{` escapes a literal `{{`; `list`/`period` have no text form → `PULSE_TEMPLATE_VAR_TYPE`; `{{` in an object **key** is rejected at validation. (3) Guard `{"$when":"segs",…}` — block survives iff the variable resolved; key stripped either way. **Presence, not truthiness**: supplied `""`/`[]`/`0`/`false` KEEPS the block; it drops only when unsupplied AND undefaulted. Guards evaluate **before** descent, so markers inside a dropped block never raise. Array drops compact the slice (a `null` hole would decode to a nil operator slot); root-level `$when` errors.

**Nine variable types** (`template.AllVarTypes()`): `string`, `number`, `integer` (`1.0` yes, `1.5` no), `boolean`, `field` (a string today; cohort binding can layer on later without a wire change), `enum` (+`values`, exact + case-sensitive), `list` (+`items`, scalar element types only — lists do not nest), `date` (only `YYYY-MM-DD`, a strict subset of `encoding.DateFormats`; `03/04/2024` is ambiguous), `period` (exactly one of `ranges` XOR `table`, mirroring the `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES` `Params` shape; unknown keys in it or in a `{label,start,end}` range rejected). The six scalars (`string`/`number`/`integer`/`boolean`/`field`/`date`) are the legal `items` values. Per-declaration slots: `required` (legal together with `default` — the default resolves it, so it can never go missing), `default` (raw JSON, so integer fidelity survives; explicit `null` = no default), `description`.

**Directory precedence.** `Options.TemplateDirs []string`, else `PULSE_TEMPLATES_DIR` split on `os.PathListSeparator` — the programmatic option wins outright and suppresses the env var entirely. Roots are an **ordered precedence list; first root wins**. A same-named template under a later root is **shadowed, not rejected**, and the losers land on the winner's `Summary.Shadows` rather than being discarded (shadowed entries get no summary of their own — a listing whose entries cannot all be fetched would be a trap). Blank root → skipped; missing root → skipped; root that exists but is a **regular file** → error naming the path. **Filesystem faults are `DATA_FILE`**, deliberately outside the `PULSE_TEMPLATE_*` family. Config-dir loading goes through `os`, not afero — same sanctioned exception as `label_loader.go` / `range_loader.go`.

**Hot-reload lifecycle — phase split is the contract.** A lookup whose snapshot has aged past the store's 1s rescan interval (package constant, deliberately not an `Option`) re-walks the roots; a file is re-parsed only when size or mtime moved. `ReloadTemplates()` forces the walk now — the deterministic path for a deploy step that writes then renders.

| Phase | Malformed file does | Why |
|---|---|---|
| At `pulse.New()` | **hard-fails startup**, path named | a broken document at boot is a deploy error the operator must see immediately |
| After startup, parsed before | **serves its last-good parse**; `ListTemplates` marks `Summary.Broken` + `.Error` | a half-written file is the normal transient state of in-place editing; killing the catalog is worse than slightly stale content |
| After startup, never parsed | listed to be SEEN (empty `Target`), not fetchable; `GetTemplate`/`RenderTemplate` → `PULSE_TEMPLATE_INVALID` naming the path | no last-good to fall back to |

`ReloadTemplates()` returns **nil** for a broken file — an error there would mask every otherwise-healthy template. A root that is not a directory **is** still a hard error (misconfiguration, not a transient edit) and a failed walk leaves the previous index entirely in place. Repair clears the state on the next rescan. `Summary.Broken`/`.Error` are both `omitempty`, so healthy listings stay byte-identical to the pre-E3 wire shape.

**Errors — nine `PULSE_TEMPLATE_*` codes, chosen by provenance not detection time.** A bad **declared default** is an author error → `_INVALID` (checked semantically at declaration: enum membership, date parse, period XOR — that is what keeps the fail-fast-at-`pulse.New()` promise real); a bad **caller value** → `_VAR_TYPE` / `_VAR_ENUM`. Same split on names: `$var`/`{{}}`/`$when` naming an **undeclared** variable is an author error caught at validation (`_INVALID`); a **declared but unresolved** reference is render-time `_UNRESOLVED`. Full family: `_NOT_FOUND`, `_INVALID`, `_TARGET_UNKNOWN`, `_VAR_MISSING`, `_VAR_UNKNOWN`, `_VAR_TYPE`, `_VAR_ENUM`, `_UNRESOLVED`, `_RENDER_INVALID`. **`_TARGET_UNKNOWN` currently carries two meanings** — an absent/unrecognised `target`, AND `RenderTemplateRequest` called on a template declaring a different (valid) target; the second case also carries `expected_target` in details. Details keys: `errors.DetailTemplate` (`"template"`) + `errors.DetailVariable` (`"variable"`); `pulse errors lookup CODE` is authoritative.

**Render never opens a cohort.** A rendering template is well-formed against the request *shape* only; field existence, type compatibility, operator applicability and streamability stay `Predict`'s job. Strict decode is harsher than the rest of Pulse — a body pasted from an `examples/` file with its `_meta` block attached fails `_RENDER_INVALID`. `Rendered.JSON` is retained alongside the typed value on purpose: re-marshaling the typed request would NOT reproduce it, because the request structs are dense with `omitempty` and any slot that rendered to an explicit zero would vanish on a round trip — echo `Rendered.JSON`, never a re-marshal.

**`format_version` stays `"1.1"`.** Nothing payload-reachable changed: `Options.TemplateDirs` is not a payload type, and `template.Template` / `Variable` / `Summary` / `Rendered` live in `template/`, not `types/`, so they are not reachable from `descriptor.BuildPayloadSchema`. `descriptor/testdata/payload-schema.json` is untouched by templating; only the manifest golden moved, and only for the nine new error codes.

Env var: `PULSE_TEMPLATES_DIR` — see "Build / Env". Detail: `skills/request-templating.md` + `docs/src/library/request-templating.md`.

# Guided analysis — Explain: request mode

Per-topic slice of the guided-analysis reference (Explain: input, output, steps, predict, profiles and prose). Index (rules + section map): `.claude/reference/guided-analysis.md` — load it first, then only the slice(s) your change touches.

## Explain

- `Pulse.Explain(ctx, descriptor.ExplainRequest{Request | Composed | Chain | Facet | Sample | Response, Detail?})` says in plain words what a request will do (REQUEST mode) or what a response found (RESPONSE mode, Compose / Chain / Facet results included, see Result roots) — both fill the same result type.
- Types live in `descriptor/explain.go`; logic in the NO-EXECUTE `internal/guide/explain.go` (`TestGuideNoExecutionImports`); the facade (`explain.go`) only wires `guide.Checks`.

### Input

- Exactly one request root, keyed by the request-template target spellings (`request`, `composed`, `chain`, `facet`, `sample`); none or several, or a `detail` other than `terse` (default) / `full`, is `SERVICE_VALIDATION` — checked before any cohort opens (`guide.ValidateExplainRequest`).

### Output

- Result type `ExplainResult`: `{mode, root, detail, summary, valid?, steps[]?, findings[], defaults_applied[]?, advisories[]?, refusals[]?, warnings[]?, sentences[]?, glossary_refs[]?, follow_ups[]?, caveats[]?}`.
- `findings` is always an array (empty in request mode); a finding is `{subject, operator?, verdict, strength_band?, convention?, numbers{}, multiplicity?{method, family, m}}` with the closed `Verdicts()` enum and `numbers` values `*float64` (an undefined figure is `null`).
- Terse = `summary` (one sentence) + the structured parts (`steps`, `findings`, `defaults_applied`, `advisories`, `refusals`, `caveats`); `full` adds `sentences` (summary, step texts, advisory messages), `glossary_refs` (the named operators' `Purpose.Glossary` terms the PRUNED ontology keeps) and `follow_ups` (their declared `FollowUps` through the same `keepAlternatives` Recommend uses), and appends each test / post-test / regression's `Assumptions` to `caveats` as `"OP: <assumption>"`.
- **Caveats stay in terse** — they say what was not checked and what predict refuses, which a reader must not lose (the many-tests caveat rides the same slot).

### Steps

- One per slot, in the order the engine applies them: joins, filterers, features, attributes, windows, groups, aggregations, crosstab, tests, post_tests, regressions, matrices, overlays, then `weight` and `multiplicity` sentences.
- `slot` is the wire path, prefixed by the slot's place in its root (`requests[1].aggregations[0]`, `stages[0].request.groups[0]`); a Compose root adds its own `overlays[k]` / `multiplicity`, a chain its whole-chain overlays, a facet its field list, filters, additive fields, top-K, quantiles, histogram and overlays, a sample its row cap and label bindings.
- A step's `text` is a template over the slot's structure (`"Aggregation 1 runs AGG_SUM on `score`."`) plus the operator's scrubbed `Purpose.Plain`; the summary counts steps by role (`"This request reads `c.pulse` and runs 1 grouping and 2 aggregations."`).
- **Item fan-out.** Feature, attribute and window steps describe the items that RUN: each request is expanded with the one `descx.ExpandRequestItems` (no derived-column limit — predict's check reports that), so a `for_each` item yields one step per combination, each keeping the WRITTEN slot (`windows[1]`) and naming its column (`, adding the column `x_ad30``), preceded once by a plain `<slot>.for_each` step stating the fan-out (count, axes, grid / zip); the summary counts expanded items.
- **Optimize blocks (`internal/guide/explain_optimize.go`).** After the slots, one step per Compose `optimize` block as it RUNS — expanded by `sweep.ExpandOptimize`, `slot` the EXPANDED `optimize[i]` (the index every optimize refusal's `details.optimize` and `optimizations[i]` use; `locatedSlot` maps `details.optimize` to it), a fanned block preceded once by `optimize[w].for_each` at its written path — naming objective, direction, variables + bounds (+ start), constraints, starts, reads (params via `internal/exprreads`; literal fits via `coefcall`, literal `result()` via `resultcall`), a posterior block's draws (`optblock.PosteriorReads`), the bounds left to the runtime (`unresolved_scalars`) and the gradient mode: `fd` as declared; else finite differences when the Compose check raised `PULSE_ADVISORY_OPTIMIZE_GRADIENT_FALLBACK` for the block (its `functions`, never recomputed); analytic only when the check ran, the batch passed and the advisory is not suppressed — otherwise "judged when the batch is checked".
- `ValidateComposeWithOptions` already runs the optimize rules at the runtime's point (`OptimizeCheck`), so Explain's verdict is predict's (`TestExplain_ComposeOptimizeMatchesPredictAndRuntime`).
- Response mode reads `ComposedResponse.Optimizations` one finding each at `optimizations[i]` (descriptive; `not_computable` for a failed block; numbers `objective`, `max_violation`, `iterations`, `starts`, `starts_agreeing`, `variables.<v>.{value,marginal,interval.lower,interval.upper}`, `constraints[k].{value,multiplier}`, `posterior.*`) with caveats for non-convergence, disagreeing starts, unconverged per-draw re-solves and a failure code (`TestExplainOptimize_*`).
- **Post-fit entries (`internal/guide/explain_postfit.go`).** After the optimize blocks, one step per Compose `post_fit` entry at `post_fit[i]`: operator (role `Post-fit entry`, its registration's `Purpose.Plain`), label, fits read, params (a scalar with its value, a list with its length) and the phase; a type the instance does not register is a plain step + caveat, never an `operator`.
- `locatedSlot` maps `details.post_fit` and a params refusal's `details.path` under `post_fit[i].` to that slot.
- Response mode reads `ComposedResponse.PostFit` one descriptive finding each at `post_fit[i]` (numbers `scalars.<name>`, `tables.<name>.rows`; a failed entry `not_computable` + a caveat naming its code) (`TestExplainPostFit_*`, `TestAddingPostFitRecipe_ExampleRuns`).
- A `for_each` that does not expand is a caveat naming the code and its items are described as written (`internal/guide/explain_for_each.go`; `TestForEach_Explain`).
- Recommend needs no change: its drafts never carry `for_each` and a drafted window fans out as written (`TestForEach_Recommend`).

### Predict

- A root naming a cohort is predicted IN-PROCESS with the facade's `predictOptions` (instance snapshot, defaults, suppression, the cohort's sidecar facts): `Request` → `descx.Predict`; `Composed` → `ValidateComposeWithOptions` with `SidecarLoader` wired (batch errors + every slot's advisories, prefixed) plus a per-slot `Predict` for each slot's inferred defaults; `Chain` → `ValidateChainWithOptions` plus a stage-0 `Predict` for its defaults (later stages' defaults resolve against the previous stage's output at run time — a caveat says so; a chain carries no advisories, as in predict); `Facet` → `ValidateFacetWithOptions`; `Sample` is never predicted.
- A single-file cohort is an open handle read for header + schema only (`openGuideCohort`, shared with bound Recommend; `TestExplain_FacadeReadsNoRecord`).
- An inferred operator fills its untyped step (`defaulted: true`, `"AGG_SUM is inferred for `score` from its type."`) and is listed in `defaults_applied` (predict's `DefaultApplied`, `path` prefixed `Requests/<i>` or `Stages/0/Request`).
- Predict's verdict is `valid` (nil when nothing was checked — no cohort, or a sample — with a leading "No cohort is named…" caveat); each predict error lands verbatim in `refusals` (`details.slot` names a Compose slot) and as a caveat naming ONLY its code — predict's message is engine prose, not an Explain template, and would not pass the prose lint.
- Each predict WARNING lands verbatim in `warnings` (the same `details.slot` rule, no caveat), once: a Compose slot's warning that the Compose check already reports located `details.request` is not repeated from the slot's own predict.
- A cohort that cannot be opened is `DATA_FILE`, a malformed header / schema `ENCODING_INVALID`.

### Profiles + prose

- A step naming a hidden operator gets no `operator` and says the instance does not offer it (plus a caveat); every summary, step text, advisory message and caveat passes `ProseScrub.Text`; glossary refs and follow-ups come from the pruned ontology (`TestExplain_ProfileHidesOperators`, root `TestExplain_FacadeProfiled`).
- Every template passes `LintGuidanceText` (`TestExplain_ProseLint` over all five roots × both details; the binding golden-wide `TestExplainProseLint` below).
- Not an observed operation (`.claude/reference/observability.md`, Not operations).

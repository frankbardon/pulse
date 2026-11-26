# Execution modes — Parallel Compose, Compose banner, Compose base and item fan-out

Per-topic slice of the execution-modes reference. Index (Mode index + section map): `.claude/reference/execution-modes.md` — load it first, then only the slice(s) your engine work touches. Compose fits and the result pre-phase are in `execution-modes/compose.md`.

## Parallel Compose

`pulse.ComposeParallel`, `pulse api compose --parallel N`. Bounded worker pool over `ComposedRequest`, order-preserving by slot index. `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}` (FailFast defaults true).

- **Overlay fold after the slot barrier.** The orchestrator calls `s.applyComposeOverlays` (`internal/service/compose_overlay.go` → `processing.ApplyComposeOverlaysWithRequests`) once every slot has produced a finalised `*Response`, before returning. It reads `*Response` objects only and leaves per-slot `Responses[i].Overlays` untouched. All COMPOSE kinds are buffered; the streamable subset is empty by construction.
- **Failures skip the fold.** With `FailFast=true` any slot failure returns the first observed error without invoking the overlay hook; `FailFast=false` routes slot failures through `SERVICE_INTERNAL`, which also skips it. Empty / nil `req.Overlays` short-circuits with no allocation, so overlay-free Compose JSON is byte-identical to a request that omits the slot.
- **Dispatch.** Per-kind handler tables live in `internal/processing/overlay_compose_dispatch.go`; an unknown kind is refused before dispatch, and `applyComposeStub` is reached only by test stub kinds.
- **Request-shape gates need the authored request.** The `OVERLAY_PROP_Z_PANEL` within-prefix slab gate turns on each slot's declared `Crosstab.Rows` grouper types, which a materialised `MatrixPayload` does not carry. So the service passes per-slot requests and the `*ExtensionRegistry` into `ApplyComposeOverlaysWithRequests`, and the gate runs per spec inside the dispatch loop (after reference/target resolution, before the key-set and schema walk — predict's position), declining whenever an earlier refusal is due so the cap fires first. An embedder calling the bare `ApplyComposeOverlays` gets no request-shape gate.
- **Response shaping never runs inside the service.** The service hands the facade the label- and defaults-resolved slot clones; `Pulse.Compose` / `ComposeParallel` apply each slot's `return` plan plus `ComposedRequest.Return` via `returnshape.ApplyComposed` AFTER the overlay and multiplicity folds, so every layer reads unshaped slots and serial ≡ parallel. What a slot skips COMPUTING is decided per slot with the whole batch in hand: a slot a Compose overlay names keeps the Components its overlays read (`Service.composeSlotVetoes`; the floor kinds `OVERLAY_PROP_Z_CELL` / `_PANEL` / `OVERLAY_CHISQ_VS_REF` keep `components.crosstab` only over a weighted slot). See the Compute plan paragraph of `.claude/reference/response-components.md` and the `update-demand/payload-and-return.md` (`Request.Return` row).
- **Slot labels.** `applyComposeLabelDefaults` synthesises `request_<index+1>` for an empty `Request.Label`; an unresolved reference / target label is `PULSE_OVERLAY_REFERENCE_UNKNOWN` / `PULSE_OVERLAY_TARGET_UNKNOWN`. See `skills/compose-requests.md`.

## Compose banner

`ComposedRequest.Banner` (`types.BannerSpec`, `capability:compose_banner`, depending on `capability:compose` + `capability:crosstab`) is Compose sugar, not an engine mode. `internal/banner.ExpandCompose` (shared with `PredictCompose`) runs the sweep expansion, then appends one crosstab slot per section × block (Total — a no-column crosstab — first, then each group). Each slot is a deep copy of the base request with:

- the section's filterers appended, and its rows / `cell` / `cells` replacing the base crosstab's, plus the group's columns;
- the base `labels` scoped to the bindings the slot's rows / columns group by (`scopeLabels`; a binding grouped by no slot stays everywhere);
- on group slots only, the section's own overlays, which REPLACE the banner-level ones (else the banner overlays).

Every slot runs as an ordinary slot (N scans of the cohort; `ComposeParallel` overlaps them), under the rank veto so the stitch reads it unshaped. After the multiplicity barrier fold, `applyComposeBanner` (`internal/service/compose_banner.go`) builds `ComposedResponse.Banner` on both orchestrators. Contract: `.claude/reference/response-components.md` (Opt-out and the Compose surface); skill `skills/compose-banner.md`.

## Compose base

`ComposedRequest.Base` (`capability:compose_base`, depending on `capability:compose`) is Compose sugar, not an engine mode: a `types.Request` partial merged copy-on-write into every EXPLICIT slot by `banner.MergeBase` (`internal/banner/base.go`), called first inside `banner.ExpandCompose`. Serial Compose, `ComposeParallel` and `PredictCompose` therefore judge the same merged slots, and everything downstream (slot gate, params inheritance, sweep and banner expansion, the `result()` catalog, item `for_each`) sees the base items as the slot's own. Never applied to the sweep body, the banner slots or a fit request; no per-slot opt-out.

Merge rules are walked by reflection over `types.Request`, so a new list slot inherits them with no edit:

- `label` is never merged; a set base `label` is `PULSE_COMPOSE_BASE_COLLISION` reason `base_label`.
- A map (`params`) merges key-wise, the slot's key winning — precedence slot > base > top-level `params`.
- A list carries the base items first, then the slot's own. A base item whose `label` / `name` / `as` equals one of the slot's own in the SAME list is reason `label_collision` (details `request`, `slot`, `list`, `label`); unlabelled items are not compared (a duplicate default label falls to that list's own duplicate rule).
- Any other slot takes the base's value only when the slot leaves it zero.
- Each slot gets its own JSON deep copy of the base items, so a slot's in-place rewrites (smart defaults, item fan-out) never reach a sibling or the caller; the input is never mutated.

The effective request carries no `base`, so `NormalizedRequest` and predict's echo show the merged slots; predict's `result.request` keeps the batch as written. A set `base` on an instance hiding `capability:compose_base` is an unknown field before the merge; a base item a profile hides is refused on the merged slot, located `details.request`. Explain merges it too, and the MCP `return` default lands on the base instead of each slot. Contract row: `update-demand/compose-expansion.md`; skill `skills/compose-requests.md`.

## Item fan-out (`for_each`)

A `for_each {axes, mode}` on a window, feature, attribute, aggregation or matrix item is expanded by ONE function, `sweep.ExpandItems` (import-fenced `internal/sweep`), wrapped with the `MaxDerivedColumns` then `MaxExpandedItems` preflight by `descx.ExpandRequestItems`. Every request host calls it right after the slot gate and before anything else reads the item lists: `Service.process` (so Process, streaming, every Compose slot and chain stage 0), `Service.materialize` (before its gates, so every expanded item becomes a column), every chain stage (located `details.stage`), and predict. To find a new host, grep `ExpandRequestItems`.

- **In place.** The runtime rewrites the item lists IN PLACE, as smart defaults do, so the echo shows the expanded lists and a second run is a no-op; predict expands a clone.
- **Order.** (1) An aggregation `for_each` where no fan-out runs (a crosstab `cell` / `cells` / `margin_aggregations` entry, so a banner cell too) is refused first, reason `position_unsupported` — never silently ignored. (2) Structural and reference checks over every for_each item, in expansion order features, attributes, windows, then aggregations and matrices (so an aggregation may read a `{{ch}}_lag` column a window fan-out adds). (3) Limits — `MaxDerivedColumns` over plain + expanded column items, `MaxExpandedItems` over the aggregation and matrix items the fan-outs ADD (plain ones never counted). (4) Rendering (grid: first axis slowest; zip: lockstep) and a strict decode per item.
- Each rendered matrix needs its own `name`; a repeat falls to the matrix duplicate-name refusal. The `result()` catalog sees each slot's expanded aggregation labels, and a `base` item's `for_each` expands per slot after the merge.
- **Inside a compose sweep body** the sweep is the OUTER loop: `sweep.Expand` renders the body per combination, passing every placeholder that names an enclosing for_each axis through untouched, and each slot then fans its items out in place, so the slot count (and predict's `expanded_count`) is unchanged. Item and sweep axes never cross-reference: an item axis named like a sweep axis is `PULSE_FOR_EACH_INVALID` `axis_shadowed` (`details.axis`, `sweep_axis`).

**Optimize blocks** fan out at the Compose level, not per slot: `sweep.ExpandOptimize`, called in `banner.ExpandCompose` right after `MergeBase` (so before the `MaxComposeSlots` preflight, `OptimizeRefusal`, the `result()` catalog and its pre-phase — serial, parallel and predict alike). It runs the same axis and reference rules, requires an axis in `name` (`name_without_axis`), checks `MaxExpandedItems` over the blocks it adds, and renders each combination with a strict decode. A typed field takes a whole `{"$var"}` value (`OptimizeSpec.UnmarshalJSON` keeps such a block as written for the render; a marker without a for_each stays a decode error); a marker standing for a whole list or a list entry is `PULSE_FOR_EACH_INVALID` `marker_position` (a free-form `inline` body admits one anywhere). The echo carries the expanded blocks; predict's `Request` the block as written.

**Never inside an expression.** An `{{axis}}` token in an `internal/exprslots` expression slot is refused by the reference scan, before the limit and any rendering: `PULSE_FOR_EACH_EXPR_SPLICE`, or `PULSE_SWEEP_EXPR_SPLICE` for a compose sweep's request or overlays; details `field`, `placeholder`, `slot`. The slot list also covers an embedder extension's `Expression`-flagged params (slot `extension_param`; `.claude/reference/architecture/extension-surface.md`). Both ride the one expansion, so predict refuses identically (`TestExprSplice_PredictRuntimeParity`); the template-side twin is `PULSE_TEMPLATE_EXPR_SPLICE` (`.claude/reference/request-templating.md`).

**Hash.** `Request.Hash` hashes the request as written (`for_each` is `omitempty`, so a for_each-free request hashes byte-identically); the hash of the expanded request equals its hand-expanded twin's.

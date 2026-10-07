# Response shaping — ask for what you need

**Status:** U17 landed (selection at serialization); U18 pending (computation skipping, MCP default, size estimates) · **Target:** v1.0.0

> **Amended at U17 (landed).** Shipped as designed except where listed here; the unit doc's Landed deviations carry the full record. `include` without a preset is an allowlist over an empty base. `minimal` is the primary result of each slot (data and warnings alone would drop test statistics and matrices); `standard` keeps metadata and drops components. Selection runs at serialization in two layers (a Go-value prune, then a planned wire encoder), so precision is wire-only and counts stay exact. `data[*].<column>` is validated against the request's output columns. Codes: `PULSE_RETURN_PATH_UNKNOWN`, `PULSE_RETURN_INVALID`, and the buffered-only warning `PULSE_RETURN_PATH_UNMATCHED`. The `returned {preset, digest, precision?}` marker is stamped only for a non-identity plan. `DisableComponents` is a shorthand only beside a `return` layer; `EchoRequest` is not folded in. `ComposedRequest.Return` (overlays only) joined per-slot and per-stage `return`. Feature profiles DO carry a `return` section (see feature-profiles 01). Still open for U18: skip computation, MCP `standard` default, predict size estimates, `PULSE_RETURN_PATH_UNMATCHED` on streams.

## The problem (from the planning review)

Pulse deliberately returns *a lot* per response, for completeness:
- `Response.Data`, `Components` (per-aggregator, grouper and filterer counters, plus crosstab cell components);
- `Metadata`, `Warnings`;
- overlay layers with summaries;
- test details, regression diagnostics;
- the planned `MatrixResult` auxiliaries, and so on.

That suits an analyst's audit trail and can be heavy for an agent's context window. The decision was *not* to cap output on the developer's behalf. Instead, the question is whether developers can **say what they need and leave the rest unsent**.

The existing opt-outs show the pattern already exists in pieces: `DisableComponents` (engine and per-request), `EchoRequest` (opt-in), `--no-components`. What's missing is one general mechanism instead of one-off switches.

## Options considered

| Option | Idea | Verdict |
|---|---|---|
| A. More boolean switches | `DisableOverlaySummaries`, `DisableDetails`, … | ✘ grows forever; each switch is its own contract |
| B. Named presets | `detail: "minimal" \| "standard" \| "full"` | ✔ great default ergonomics, but too coarse alone |
| C. Field selection (include / exclude paths) | `select: ["data", "tests[*].p_value"]`, in the style of Google's FieldMask or a GraphQL selection | ✔ precise; one mechanism for every slot, present and future |
| D. Server-side JSON post-filtering only | compute everything, strip at serialization | ✘ saves tokens but not compute; a fine fallback, not the design |

**Recommendation: B + C, compiled into a computation plan.** Presets are named selections, and selection is the primitive. Crucially, the selection is applied **before execution where possible**: unrequested components are not accumulated, unrequested overlays not folded, unrequested auxiliaries not computed. "Unsent" then also means "never computed", which is the projected-decode philosophy Pulse already applies to fields.

## Shape

```jsonc
{
  "return": {
    "preset": "standard",                      // minimal | standard | full (default: full = today)
    "include": ["components.run", "tests[*].details.effect_size"],
    "exclude": ["data[*].debug_*", "overlays[*].payload.matrix"]
  }
}
```

- **Paths** follow the JSON names of the typed `Response` (the same names the payload JSON Schema publishes), with `[*]` for arrays and `*` as a suffix glob on map keys. That keeps them self-describing: an agent can read valid paths from `pulse schema`.
- **Resolution:** `preset` → `include` adds → `exclude` removes. Exclude wins on conflicts. Every path is validated against the schema at predict time, and an unknown path is a coded error (`PULSE_RETURN_PATH_UNKNOWN`), never silently ignored.
- **Presets** are defined once in `descriptor/` and listed in the manifest with their expanded paths, so they are fully transparent:

  | Preset | Contains |
  |---|---|
  | `full` | everything (today's output; default) |
  | `standard` | data, test/regression headline numbers, overlay values, warnings; no `Components`, no per-cell auxiliaries, no echoed request |
  | `minimal` | data and warnings/errors only |

- **Defaults:**
  - **Library:** `Options.DefaultReturn` sets an instance default. With nothing set, output is `full` and byte-identical to today, honouring "everything enabled by default".
  - **MCP (decided): the default is `standard`.** This applies to `pulse mcp`, `mcpserve` and `gosdk.Register` (via `gosdk.Config.DefaultReturn`, default `standard`). Override with `pulse mcp --return full`, the config field, or the feature-profile file's `return` section.
  - A request's own `return` always overrides the instance default, so an agent can ask for `full` on one call.
  - This is a visible change to MCP tool output relative to today. Release notes call it out, and MCP goldens are regenerated once, at this change.
- **Absorbing the old switches.** `DisableComponents` / `--no-components` and `EchoRequest` remain, as documented shorthands for `exclude: ["components"]` / `include: ["request"]`. No break.

## Where data volume actually comes from

Field selection controls *which* parts come back. Two more knobs, both optional and both developer-chosen, address *how much* of a part comes back without Pulse imposing caps:

- **`data` columns:** `include: ["data[*].region", "data[*].avg_sat"]` keeps only named output columns.
- **Precision:** `return.precision` sets significant digits for floats, e.g. `4`. It is often the single largest token saving for agents, and it was already proposed for matrices (vector-matrix 02, R5). It is generalized here, and the default is unlimited.

Row counts stay uncapped by design. Pulse returns all rows unless the developer filters, groups or sorts differently.

## Contract notes

- **Additive.** `Request.Return` is `omitempty`, so `format_version` stays `"1.1"`. Excluded fields are **absent** (as today with `omitempty`), never null-filled, so a consumer can't confuse "excluded" with "zero".
- **The response says what it did:** the envelope's `request` echo (when on) shows the resolved return plan; otherwise an additive `returned: {preset, digest}` marker lets caches and consumers tell shaped responses apart.
- **Streaming:** the selection applies per emitted row or chunk, and excluded mergeable components are not accumulated.
- **Compose / ProcessChain:** `return` applies to each slot or stage, plus the top-level overlays.
- **Feature profiles:** paths owned by hidden features don't exist in the instance's schema, so selecting them is `PULSE_RETURN_PATH_UNKNOWN`, the same as any nonexistent path. That is consistent with invisibility.
- **Predict:** reports an estimated response size per top-level section, with and without the selection. That tells an agent ahead of time whether `standard` is enough.

## Gates

- **`TestReturnFullIsIdentity`:** no `return`, or `preset: full`, produces byte-identical output to today.
- **`TestReturnSkipsComputation`:** excluded components and overlays are provably not computed (instrumented counters), not merely stripped.
- **`TestReturnPathsMatchSchema`:** every preset path resolves in the payload JSON Schema.

## Deliverables

- [ ] `Request.Return {preset, include, exclude, precision}`; `Options.DefaultReturn` (library default `full`); `gosdk.Config.DefaultReturn` + `pulse mcp --return` (MCP default `standard`)
- [ ] Path grammar over the response schema; predict-time validation; `PULSE_RETURN_PATH_UNKNOWN`
- [ ] Presets `full` / `standard` / `minimal` in `descriptor/`, listed in the manifest
- [ ] Selection compiled into the execution plan (skip components, overlays and auxiliaries not requested)
- [ ] Float precision control
- [ ] Old switches documented as shorthands; `returned` marker
- [ ] Predict per-section size estimates
- [ ] Identity, skip-computation and schema-path gates; topical skill `response-shaping.md`

## Decisions

- **MCP default:** `standard` on every MCP surface; the library default stays `full`. Agents can still request `full` per call.

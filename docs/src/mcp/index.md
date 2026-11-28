# MCP Integration

**Audience:** operators wiring Pulse into an MCP-aware AI client (Claude Desktop, Claude Code, Cursor, Zed, custom hosts), and embedders who want to expose Pulse to an LLM agent.

This page is the human-facing guide: what the server does, how to wire it up, what the LLM sees, and how to debug a misbehaving session. Agent-facing guidance ships inside the binary as the `session-bootstrap` skill — fetch it via `pulse_skills_get` (or `pulse skills show session-bootstrap`).

## What `pulse mcp` is

`pulse mcp` runs the Pulse library as a Model Context Protocol (MCP) server. The host (Claude Desktop, Claude Code, etc.) launches it as a subprocess, speaks JSON-RPC over its stdio streams, and shuts it down on session close. The LLM sees Pulse as a set of **tools** (callable functions), **resources** (browseable URIs), and **prompts** (canned slash commands).

```
┌─────────────┐  stdio JSON-RPC  ┌────────────┐  Go calls  ┌─────────────┐
│  AI client  │ ───────────────→ │ pulse mcp  │ ─────────→ │ pulse.Pulse │
│   (host)    │ ←─────────────── │ (this bin) │ ←───────── │  (library)  │
└─────────────┘                  └────────────┘            └─────────────┘
                                       │
                                       └── stderr ─→ host log pane
```

The server is a thin translator. Every tool wraps a public method on `pulse.Pulse`; the same code path powers the CLI.

## Quickstart

```bash
# 1. Build and place on PATH
make build && cp ./bin/pulse /usr/local/bin/

# 2. Pick a data directory
mkdir -p /var/data/pulse

# 3. Wire into your host (see below) and restart it

# 4. From the LLM session, call:
#    pulse_manifest      → cache once
#    pulse_inspect       → open a cohort
#    pulse_predict       → validate a request
#    pulse_process       → execute
```

## Wiring into a host

### Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```jsonc
{
  "mcpServers": {
    "pulse": {
      "command": "/usr/local/bin/pulse",
      "args": ["mcp"],
      "env": {
        "PULSE_DATA_DIR": "/var/data/pulse"
      }
    }
  }
}
```

Restart Claude Desktop. Pulse tools appear in the tool picker.

### Claude Code

```bash
claude mcp add pulse --env PULSE_DATA_DIR=/var/data/pulse -- pulse mcp
```

Or by hand in a project's `.mcp.json`:

```jsonc
{
  "mcpServers": {
    "pulse": {
      "command": "/usr/local/bin/pulse",
      "args":    ["mcp"],
      "env":     { "PULSE_DATA_DIR": "/var/data/pulse" }
    }
  }
}
```

### Cursor / Zed / generic stdio hosts

Any host that speaks the MCP stdio transport can launch `pulse mcp` the same way — provide the binary path, the `mcp` argument, and the `PULSE_DATA_DIR` env var.

## What the LLM sees

### Tool surface

The manifest's `mcp_tools` (`pulse --json | jq '.data.mcp_tools'`) is the
authoritative list for an instance; a [feature profile](../library/feature-profiles.md)
mounts only what it enables. Each tool's full description is served with
`tools/list` (source: `internal/mcp/toolmeta/meta.go`), and each has a
`tool-<name>` skill (`tool-predict`, `tool-import`, …).

| Job | Tools |
|---|---|
| Discover | `pulse_manifest` (**call first**, cache per session), `pulse_skills_list`, `pulse_skills_get`, `pulse_examples_search`, `pulse_examples_get`, `pulse_errors_lookup` (per-code Message + Fixups) |
| Look at a cohort | `pulse_cohorts_list` (every cohort in the data directory with its record count and fields; call it first when you do not know which cohort holds the data), `pulse_inspect` (header + schema, never records; also binds schemas, below), `pulse_sample` (up to N rows), `pulse_facet` (distinct values of one field), `pulse_facet_schema` (multi-field summary: counts, nulls, stats, histograms) |
| Author and run | `pulse_recommend` (an intent ID to draft requests), `pulse_predict` (validate without running), `pulse_process`, `pulse_compose` (several requests, same or different cohorts, one round trip), `pulse_process_chain` (each stage reads the previous stage's rows), `pulse_explain` (plain-words reading of a request or result), `pulse_lookup` (rows by key from a prebuilt sidecar index) |
| Bring data in | `pulse_import` (a tabular file, SPSS included, to a managed handle; `.pulse` passes through), `pulse_imports_list` (managed imports only), `pulse_drop` |
| Write a new cohort | `pulse_dedup` (parent groups), `pulse_materialize` (derived columns as fields), `pulse_rake` (survey weights) |
| Name things | `pulse_label_tables`, `pulse_label_resolve` (display name to raw key), `pulse_range_tables`, `pulse_lookup_tables` |

### Resources

| URI scheme | Yields |
|---|---|
| `pulse://<path>` | One resource per `.pulse` file under the data directory. Read returns `descriptor.InspectResult` JSON (header + schema only — no record bytes). The per-file entries are produced by a startup scan the operator can disable (`pulse mcp --no-cohort-scan`); a read by URI resolves either way. |
| `pulse://schema` | The instance's payload JSON Schema (draft 2020-12), the same document `pulse schema` prints. |
| `pulse-skill://<name>` | One per skill — every embedded skill plus the two rendered from Go registries, `glossary` and `intents`. Read returns the markdown body. |

Resources are registered once at server start: a file added afterwards is not listed until the server restarts, but reads by URI through the template. Reading a cohort resource reads only header bytes.

### Prompts

| Name | Args | Returns |
|---|---|---|
| `pulse-bootstrap` | none | A short instructions block telling the assistant what to call (and in what order) before authoring any request, and where the authoritative references live. Inject at session start. |
| `pulse-author-request` | `question` | Classifies the question against the intents (label + example phrasings) and hands it to the matching mounted `pulse-<intent>` prompt, or — for `prepare` and `lookup` — straight to the mounted route tool (`pulse_import` / `pulse_dedup`, `pulse_lookup`; `simulate` has no MCP tool). It lists only what this server mounts. Otherwise a guided tool-call sequence: manifest → examples search → inspect → predict → process. |
| `pulse-describe` | `cohort`, `measure` | The guided intent workflow for `describe` (below). |
| `pulse-compare-groups` | `cohort`, `outcome`, `group` | The guided intent workflow for `compare_groups` (below). |
| `pulse-relationship` | `cohort`, `measures`, `categories` | The guided intent workflow for `relationship` (below). |
| `pulse-drivers` | `cohort`, `outcome`, `predictors` | The guided intent workflow for `drivers` (below). |
| `pulse-change-over-time` | `cohort`, `time`, `measure` | The guided intent workflow for `change_over_time` (below). |
| `pulse-composition` | `cohort`, `category`, `by` | The guided intent workflow for `composition` (below). |
| `pulse-benchmark` | `cohort`, `measure`, `group` | The guided intent workflow for `benchmark` (below). |
| `pulse-distribution-shape` | `cohort`, `measure` | The guided intent workflow for `distribution_shape` (below). |
| `pulse-segment` | `cohort`, `measures` | The guided intent workflow for `segment` (below). |
| `pulse-measure-construct` | `cohort`, `items` | The guided intent workflow for `measure_construct` (below). |
| `pulse-flows` | `cohort`, `from`, `to` | The guided intent workflow for `flows` (below). |
| `pulse-data-quality` | `cohort`, `fields` | The guided intent workflow for `data_quality` (below). |

**Intent prompts.** One `pulse-<intent>` prompt per analytic intent (the tooling intents `prepare`, `simulate` and `lookup` route to tools and get none), generated from the intent registry. Every argument is optional: `cohort` plus one field hint per role of the intent's shapes, comma-separated field names. All share one script: `pulse_inspect` the cohort → `pulse_recommend {intent, fields}` (the role arguments become `fields` hints) → `pulse_explain` the draft request and confirm it with the user → `pulse_process` → `pulse_explain` the response. Under a feature profile each prompt is its own `mcp_extra:prompt_<intent>` feature (it needs `capability:recommend`, `capability:explain` and `capability:process`), and it is not mounted when every operator serving its intent is hidden.

Hosts that surface prompts as slash commands let users trigger these directly.

## Recommended session flow

The default sequence for nearly every user request:

1. **`pulse_manifest`** once at session start. No arguments. Cache the payload — it is deterministic for a binary version and carries every fact needed to author a valid request.
2. **`pulse_import`** when the user hands the LLM a raw tabular file (CSV, TSV, NDJSON, JSON array, Parquet, Arrow, Excel, SPSS). Returns a managed handle that subsequent tools address as if it were a `.pulse`. Skip when the cohort already exists as a managed handle or `.pulse` file in `PULSE_DATA_DIR`.
3. **`pulse_inspect`** on the handle (or path). Reads header + schema only — no record bytes — and binds the action tools to the cohort's fields (see below; `pulse_import` binds too).
4. **`pulse_predict`** with the authored request. Validates against the schema, surfaces applied defaults, streamability, and any structural errors / warnings. Each error code carries Fixup metadata so the LLM can repair the request without another round trip.
5. **`pulse_process`** to execute. Use `pulse_compose` to batch several requests in one round trip.

Cheaper probes are available without going through `pulse_process`:

- **`pulse_sample`** for a row preview.
- **`pulse_facet`** for distinct values of a single field.
- **`pulse_examples_search`** / **`pulse_examples_get`** to crib a runnable request template from the embedded library.

## Managed imports + TTL

`pulse_import` lets the LLM hand the server any tabular file and address it from then on as if it were a `.pulse`.

- **Convertible formats** (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss) are imported into `$PULSE_DATA_DIR/imports/<handle>.pulse` with a sidecar `<handle>.pulse.meta.json` carrying `imported_at`, `expires_at`, `ttl_seconds`, source path, source format, and row count — plus `source_tz` / `column_source_tz` / `source_tz_field` / `dst_policy` when the import read naive datetimes in a source zone (the `pulse_import` fields of the same names; omitted for a UTC import), and `zone_field` when it declared the cohort's default per-row zone field. `result.managed=true`.
- **Pulse passthroughs** (`.pulse` extension) under `PULSE_DATA_DIR` are not copied — the server returns the relative path verbatim with `managed=false`. A `.pulse` outside `PULSE_DATA_DIR` is copied into the managed pool.

**Source path resolution.** Relative `source` paths resolve against `PULSE_DATA_DIR`. Absolute paths read from the host filesystem through a separate "source fs."

**Import jail.** Absolute source paths are confined to a single directory tree (the *jail root*). Default: the working directory the MCP server was launched from. Paths that escape the jail (including `..`) return `PULSE_IMPORT_SOURCE_FORBIDDEN`. Override via `pulse.Options.ImportSourceJailRoot` when embedding.

**Sliding TTL.** Default lifetime is `7d` (overridable via `PULSE_IMPORT_TTL`, or per-import via the `ttl` field — accepts Go duration like `"24h"`, day form like `"7d"`, or `"pin"` for never-expire). Every later read of the handle (inspect, predict, process, sample, facet, …) slides `expires_at` forward. The pool self-sweeps on every `pulse_import` call — no daemon required. Inspect with `pulse_imports_list`; evict manually with `pulse_drop`.

## Schema-bound enums

After a successful `pulse_inspect` or `pulse_import`, the server re-registers the action tools (`pulse_process`, `pulse_predict`, `pulse_compose`, `pulse_process_chain`, `pulse_sample`, `pulse_facet`, `pulse_facet_schema`) with JSON Schemas that embed enum constraints on field-name parameters. The LLM picks field names from a typed list rather than free-texting and discovering on predict that the name was wrong.

What gets constrained on bound `pulse_process` / `pulse_predict` / `pulse_compose` schemas:

| Path | Enum |
|---|---|
| `aggregations[].field` | All cohort field names, narrowed on the `AGG_SET_*` types to the cohort's set fields (an `allOf` `if type ∈ {…} then {field ∈ sets}` branch; `false` on a cohort with none); the same on a crosstab's `cell` / `cells` / `margin_aggregations` |
| `aggregations[].type` | Full aggregator catalogue (`AGG_*`) |
| `attributes[].field` | The fields the attribute `type` accepts (manifest `accepts_types`): the enum is the union over the instance's attribute types, and an `allOf` of `if type ∈ {…} then {narrower enum}` branches narrows it per type — numeric for `ATTR_ZSCORE` / `TSCORE` / `NORMALIZED` / `PERCENTILE`, a categorical or `packed_bool` state for `ATTR_PREVIOUS_STATE`, a categorical or unsigned integer for `ATTR_CODE_IN`, a date or datetime for `ATTR_DATE_PART`, a set for `ATTR_SET_*`, an extension attribute's declared `Accepts`; an attribute that reads no field of its own keeps the numeric class |
| `attributes[].subject_field`, `attributes[].order_field` | All cohort field names, narrowed on `ATTR_PREVIOUS_STATE` to any scalar but a set (subject) and a date or datetime (order) |
| `attributes[].type` | Full attribute catalogue (`ATTR_*`) |
| `filterers[].field` | All cohort field names, narrowed on the `FILTER_SET_*` types to the cohort's set fields (same branch) |
| `filterers[].type` | Full filterer catalogue (`FILTER_*`) |
| `groups[].field` | All cohort field names, narrowed on `GROUP_SET_VALUE` / `GROUP_SET_PER_ELEMENT` to the cohort's set fields (same branch; also a crosstab's `rows` / `columns`) |
| `groups[].type` | Full grouper catalogue (`GROUP_*`) |
| `windows[].field`, `windows[].partition_by[]` | All cohort field names |
| `windows[].order_by[].field` | Numeric and date fields |
| `windows[].type` | Full window catalogue (`WIN_*`) |
| `tests[].field`, `tests[].field2` | Numeric fields only |
| `tests[].split_by` / `rows` / `cols` / `subject_field` | All cohort field names |
| `tests[].type` | Full test catalogue (`TEST_*`) |
| `attributes[].target`, `attributes[].predictors[]` | Numeric fields only (regression attributes) |
| `regressions[].target`, `regressions[].predictors[]` | Numeric fields only |
| `regressions[].type` | Full regression catalogue (`REG_*`) |
| `matrices[].type` | Full matrix catalogue (`MAT_*`) |
| `joins[].on[].left_field` | All cohort field names (left side only) |
| `joins[].on[].right_field` | The join targets' field names when `pulse_inspect` named `joins`; free string otherwise |
| `pulse_facet` `field` arg | All cohort field names |

Beyond the enums, a bound schema carries every key the payload schema declares on the request roots it describes (`Request`, `ComposedRequest`, `ChainRequest`, `FacetRequest`, and `pulse_predict`'s alternative roots), nested slot objects included, and no key the instance's payload schema lacks. The only deliberate omissions are a test's `multiplicity.alpha` (the engine refuses it: a test reads its own `alpha`), a facet overlay's `weight` (facets are never weighted) and the `labels` slot on an instance offering no label table. `TestBindForInstance_PayloadSchemaParity` holds the two in step, profile-free and under feature profiles.

**Items that fan out (`for_each`).** On a `features[]`, `attributes[]`, `windows[]`, `aggregations[]` or `matrices[]` item that carries a `for_each`, every enum-bound string above (the operator `type`, `field`, `partition_by[]`, `order_by[].field`, a regression attribute's `target` / `predictors[]`, the window `frame.mode`, a weight field) also accepts an axis placeholder: the property is `{"type": "string", "anyOf": [{"enum": [...]}, {"pattern": "\\{\\{\\s*[A-Za-z_][A-Za-z0-9_]*\\s*\\}\\}"}]}`, and the item carries `"if": {"required": ["for_each"]}` with an `else` that forbids the placeholder form at the same sites — so an item without `for_each` keeps the strict enum (a typo is still rejected) and a `{"field": "{{ch}}_spend", "for_each": …}` validates. The `else` forbids only a placeholder that is NOT an enum member (`{"not": {"type": "string", "pattern": …, "not": {"enum": [...]}}}`), so a cohort field literally named `{{x}}` stays nameable without `for_each`. The per-type `attributes[]` branches relax alike. Plain draft 2020-12 keywords, pinned against a real validator by `TestBind_ForEach*` and `TestBind_AttributeFields*` (`internal/mcp`; bound admits ≡ the accepted-types table predict and the runtime read, for every built-in attribute type × field type). An instance hiding `capability:for_each` binds neither the key nor the placeholder branch. A reader looking for a property's enum finds it at `enum` or, on these items, as the first `anyOf` branch.

**Join targets.** `pulse_inspect` takes an optional `joins: [{right, as}]` naming the cohorts the session's requests will join. Each target that opens widens the bound `pulse_process` / `pulse_predict` / `pulse_compose` / `pulse_process_chain` field enums with its joined names (`as` + right field, the names predict resolves against the joined schema; type rules kept, so a joined categorical is still no `ATTR_ZSCORE` field though an `ATTR_CODE_IN` one) and binds `joins[].on[].right_field` to its own fields; `left_field` stays left-only, and `pulse_sample` / `pulse_facet` / `pulse_facet_schema` stay on the inspected cohort. Inspect's output is unchanged, a target that does not open widens nothing, and an instance hiding `capability:joins` ignores the targets. The bound `as` is the one named here: a request joining with another prefix or another cohort is still judged by `pulse_predict`. Pinned by `TestBind_JoinTarget*` (`internal/mcp`) and `TestRegister_BindOnInspectJoinTargets` (`mcp/gosdk`).

**Trigger and lifecycle.** Binding fires on a successful `pulse_inspect` or `pulse_import`. The server replaces the tools by name (`AddTool`), and the go-sdk server emits `notifications/tools/list_changed`; the host picks up the bound schemas on its next list. The server holds one tool set, so on stdio (one process, one session) the bound tools are exactly that session's.

**Limitations.**
- *Multi-file sessions:* the latest inspect wins. Track multiple cohorts client-side.
- *Per-element type ↔ field correlation is partial:* the per-type `allOf` branches cover the `attributes[]` types and the set operators (`AGG_SET_*`, `FILTER_SET_*`, `GROUP_SET_*`, pinned by `TestBindSetFieldTypes`); any other aggregator, filterer or grouper keeps the cohort-wide enum ("if `aggregations[i].type == AGG_SUM` then `field` must be numeric" is not expressed). Operator–type compatibility lives in the `type` property description; strict validation remains `pulse_predict`'s job.
- *Field names, not values:* the enums constrain field names only; a `values` list, a set's labels or a categorical's levels are never narrowed to the field's dictionary — read them with `pulse_facet`.
- *Transport support:* an embedder serving one server to several HTTP sessions shares one tool set, so the latest bind applies to every session; one server per session (the go-sdk `getServer` factory) keeps them apart. The manifest's `accepts_types` table stays authoritative either way.
- *Empty enums omitted:* when the cohort has zero fields in a category (e.g. no geo fields), the enum is omitted entirely rather than emitted as `[]`.

Disable binding entirely with `--bind-on-open=false`.

## Lean responses by default

`pulse_process`, `pulse_predict`, every `pulse_compose` slot and every
`pulse_process_chain` stage shape a request WITHOUT its own `return`
block by the `standard` preset — the primary result plus metadata,
tests, crosstab and overlays, never `components` — and stamp
`returned {preset: "standard", digest}`. Precedence: request `return` >
host (`pulse mcp --return`, `gosdk.Config.DefaultReturn`,
`mcpserve.Options.DefaultReturn`) > instance default
(`pulse.Options.DefaultReturn`, else the feature profile's `return`) >
`standard`. An agent that needs everything sends
`"return": {"preset": "full"}`; an operator makes `full` the server
default with `pulse mcp --return full`. The library default is `full`. Contract: [Response shaping](../library/response-shaping.md).

## Configuration

| Env var | Purpose | Default |
|---|---|---|
| `PULSE_DATA_DIR` | Cohort base directory. Required. | (none — server fails to start without it) |
| `PULSE_IMPORTS_DIR` | Subdirectory for managed-import handles. | `imports` |
| `PULSE_IMPORT_TTL` | Default TTL for managed handles. Accepts Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin`. | `7d` |

Embedders set the same per instance through `pulse.Options` (`DataDir`, `FS`, `ImportsDir`, `ImportTTL`, `ImportSourceFS`, `ImportSourceJailRoot`) and serve it with `mcpserve` or `gosdk.Register` ([Serving MCP](../library/mcp-serving.md)).

## Transport caveats

Protocol revisions, negotiation and the upgrade policy: [Protocol Versions](protocol.md).

- **Stdio.** The only transport `pulse mcp` serves. Schema binding is exact on its single session (see Limitations). Stdout is the JSON-RPC channel; stderr is the log channel — never write structured output to stdout outside the protocol.
- **Streamable HTTP.** Not exposed by the `mcp` CLI leaf. The go-sdk server supports it; embedders build their own `go-sdk` server and call `gosdk.Register(server, p, cfg)` (or use `mcpserve.Serve`), then serve over the transport of their choice. Bind-on-inspect has no per-session override on a shared HTTP server (see Limitations).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `data directory required: set PULSE_DATA_DIR or pass --data-dir` | Neither env var nor flag set | Pass `PULSE_DATA_DIR` in the host's `env` block, or `--data-dir` in `args` |
| Tools don't appear in the host UI after editing config | Host caches tool list | Restart the host fully (not just the conversation) |
| `pulse_import` returns `PULSE_IMPORT_SOURCE_FORBIDDEN` for an absolute path | Path escapes the import jail (default = server's working dir) | Either move the file under the jail, launch the server from a higher-level directory, or set `pulse.Options.ImportSourceJailRoot` when embedding |
| `pulse_inspect` succeeds but bound enums never fire | Binding is off (`--bind-on-open=false`, `BindOnInspect: false`), or the host ignores `notifications/tools/list_changed` | Turn binding on, or rely on `pulse_predict` for validation; the manifest's `accepts_types` lists give the LLM the same information |
| Tool calls hang | Host wrote non-protocol bytes to the server's stdin, or server wrote non-protocol bytes to stdout | Check server stderr; restart the session. `pulse mcp` itself only writes a one-line startup notice to stderr at boot |

To see what the server registers without launching the host:

```bash
pulse --json | jq '.data.mcp_tools[]'
pulse --json | jq '.data.skills[]'
```

## Skill cross-reference for LLM agents

If you are writing a system prompt for an LLM agent that uses Pulse, point it at these skills rather than at this site:

| LLM task | Skill |
|---|---|
| MCP wiring, tool surface, schema binding | `session-bootstrap` |
| Author a `Process` request | `request-envelope`, `aggregation-design` |
| Compose multiple sub-requests in one call | `compose-requests` |
| Iterate on a request with `pulse_predict` | `tool-predict` |
| Look up an error code or warning | `tool-errors-lookup` |
| Pick an aggregator / filterer | `aggregation-design` |
| Pick an attribute (z-score, percentile, formula, ...) | `attribute-composition` |
| Design a grouper | `grouper-design` |
| Use a window operator (`WIN_*`) | `window-design` |
| Use a feature engineer (`FEAT_*`) | `feature-engineering` |
| Run a statistical test (tier-1 or tier-2) | `statistical-testing` |
| Fit a regression (OLS, GLM, Bayesian) | `regression-modeling` |
| Generate synthetic data | `synthetic-data` |
| Gate, mask or derive a synthetic field (`rules[]`) | `synth-structural-rules` |
| Understand a cohort's schema layout | `cohort-schema-design` |
| Import a tabular source into `.pulse` | `tool-import` |
| Pick an export format | `pulse --json` (`Export` capability block) |
| Work with `decimal128` (currency, precise arithmetic) | `financial-cohorts` |
| Get started end-to-end (LLM walkthrough) | `session-bootstrap` |

The agent should call `pulse_skills_list` once at session start, then `pulse_skills_get` on demand. The served text is authoritative.

## Related

- [`mcp` (CLI leaf)](../cli/mcp.md) — flag reference and exit codes for the server binary
- [Deployment](../ops/deployment.md) — production hardening notes
- [Troubleshooting](../ops/troubleshooting.md) — non-MCP failure modes

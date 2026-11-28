# pulse api compose

**Audience:** CLI users executing a batch of related requests in one
call.

`pulse api compose` runs multiple [`types.Request`](https://github.com/frankbardon/pulse/blob/main/types/types.go)
entries against one or more cohorts. The whole batch is one
`ComposedRequest`; the engine can run the entries sequentially or in
parallel against a bounded worker pool.

> **LLM agents using MCP:** see the `pulse_compose` MCP tool and the
> `compose-requests` skill.

## Synopsis

```
pulse api compose --request FILE [--json] [--stream [--stream-fits]]
                                  [--parallel N] [--no-fail-fast]
                                  [--no-defaults] [--no-components]
                                  [--echo-request]
```

## Flags

Each flag's type, default and help text is in the
[Command & flag reference](reference.md#pulse-api-compose).
`--no-defaults`, `--no-components` and `--echo-request` mean what they
mean on [`api process`](api-process.md#flags), applied to every slot; an
echoed `sweep` or `banner` shows the expanded slots that ran.

## Request file shape

```json
{
  "requests": [
    { "cohort": {"filename": "sales.pulse"}, "aggregations": [...] },
    { "cohort": {"filename": "sales.pulse"}, "groups":       [...] },
    { "cohort": {"filename": "ops.pulse"},   "filterers":    [...] }
  ]
}
```

Each `requests[i]` is a full `types.Request`. Slots are independent —
they may target different cohorts, use different operators, etc.

When the cohort is a shard archive, each request can target the whole
archive (fan-out across every shard) or one shard via the
`archive.pulse#shard.pulse` anchor — Compose preserves slot order
regardless:

```json
{
  "requests": [
    {"cohort": {"filename": "Q1_2019.pulse"},                "aggregations": [...]},
    {"cohort": {"filename": "Q1_2019.pulse#20190101.pulse"}, "aggregations": [...]},
    {"cohort": {"filename": "wave_2018.pulse"},              "aggregations": [...]}
  ]
}
```

## Output ordering

Responses come back **in input order**, regardless of `--parallel`.
A worker that finishes early waits its turn before emitting. So
`data.responses[i]` always corresponds to `request.requests[i]`.

## Parallel mode

`--parallel N`:

- `1` (default) — sequential `Compose`, equivalent to running each
  request through `pulse api process` in a loop.
- `0` — `runtime.GOMAXPROCS` workers.
- `>1` — exactly N workers.

Workers share Pulse's read-only registries; per-request stateful
operators are constructed fresh. See [Parallel
Compose](../library/parallel-compose.md) for full mechanics.

## Failure semantics

With `--no-fail-fast` unset (the default, fail-fast on; the library's
zero `ComposeOptions` behaves the same):

- The first failing request cancels in-flight siblings.
- The command exits 1 with the first error.

With `--no-fail-fast` (the library's `ComposeOptions.CollectErrors`):

- Every request runs to its own completion (or per-request timeout).
- Errors aggregate into a single `SERVICE_INTERNAL` error whose
  `details.failed_indices` lists the slot indices that failed.
  When a sweep-bound fit's sweep slots fail, no other slot runs and
  `details.skipped_indices` lists the slots that never ran.
- No slots are returned, not even the ones that succeeded: the
  command exits 1 and the aggregate is the only result (the
  envelope's `errors` under `--json`).

## Output

### `--json`

```json
{
  "format_version": "1.1",
  "data": {
    "responses": [ /* one Response per slot, in input order */ ],
    "overlays":  [ /* one OverlayLayer per ComposedRequest.overlays spec; omitted when no Compose overlays */ ]
  },
  "errors": [],
  "warnings": []
}
```

`data` is a `ComposedResponse`. Each `data.responses[i]` is a full
`Response` carrying its slot's final `label` (the caller's, or the
synthesized `request_<i+1>`; kept by every `return` preset), with
`components` as on [`api process`](api-process.md#--json).
`data.overlays[i]` carries the post-fold Compose-host overlay layer plus
its per-layer `warnings[]` (`{code, message, details}`); it is omitted
when the batch has no Compose overlays.

Every slot's run-time warnings (`data.responses[i].warnings`) and the
batch's own (`data.warnings`) are also written to the envelope's
top-level `warnings`, a slot's entry located by `details.request`;
overlay-layer warnings stay on their layer.

Response shaping passes through: each slot's `return` shapes
`data.responses[i]`, and a top-level `return` (`overlays…` paths only)
shapes `data.overlays` after the fold and adds `data.returned` (see the
[Return slot](../contract/payload-schema.md#compose-and-chain)).

### `--stream`

```json
{"index": 0, "row": { ... }}
{"index": 0, "row": { ... }}
{"index": 1, "row": { ... }}
```

The `index` field identifies which slot's request produced each row.
Each `row` follows its slot's `return`: the selected columns only, at
that slot's `precision`.

After the rows come the terminal lines, each only when it applies, in
this order: `{"warnings": [...]}` (the batch warned), `{"sweep_seed": N}`
(a `random` / `lhs` sweep), `{"ranking": [...]}` (a ranked sweep;
`rank.profile` adds `ranking_profile`, an adaptive `de` / `bo` sweep
adds `sweep_trace`), `{"banner": {...}}`, `{"optimizations": [...]}`,
`{"comparisons": [...]}`. With `--stream-fits`, a batch that declares
`fits` ends with one more line, `{"fits": {...}}`: the buffered `fits`
object, keyed by fit name, written last. The library writes the same stream with
`pulse.WriteComposeStream` (`ComposeStreamOptions.TerminalFits`).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | All requests succeeded (warnings allowed) |
| 1 | One or more requests failed (fail-fast: first error; aggregated: any failure) — in the envelope's `errors` under `--json` |
| 2 | Usage error (unknown flag, malformed value, missing required flag); no envelope |

See [Exit status](flags.md#exit-status): under `--json` a fatal error is the envelope's `errors` (exit 1); warnings alone exit 0.

## Examples

### Sequential batch

```bash
pulse api compose --request batch.json --json
```

### Parallel with 4 workers, aggregated errors

```bash
pulse api compose --request batch.json --parallel 4 --no-fail-fast --json
```

### Stream a parallel batch into a downstream consumer

```bash
pulse api compose --request batch.json --parallel 4 --stream | \
    jq -c 'select(.index == 2)'
```

## Related

- [`pulse api process`](api-process.md) — single-request leaf
- [Library: Parallel Compose](../library/parallel-compose.md) — Go-side
  equivalents
- `skills/compose-requests.md` (LLM) — request composition patterns

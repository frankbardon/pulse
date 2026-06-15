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
pulse api compose --request FILE [--json] [--stream]
                                  [--parallel N] [--no-fail-fast]
                                  [--no-defaults] [--echo-request]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--request`       | `-r` | string | (required) | Composed-request JSON path |
| `--json`          |      | bool   | false      | Wrap output in the standard envelope |
| `--stream`        |      | bool   | false      | Stream rows as NDJSON; each line is `{"index": N, "row": {...}}` |
| `--parallel`      |      | int    | 1          | Worker count; 0 = `GOMAXPROCS`, 1 = sequential |
| `--no-fail-fast`  |      | bool   | false      | Aggregate errors across slots instead of cancelling on first failure (parallel mode only) |
| `--no-defaults`   |      | bool   | false      | Disable smart operator-type inference |
| `--echo-request`  |      | bool   | false      | Include the normalized `ComposedRequest` on `envelope.request`; each slot reflects its post-defaults form. Ignored under `--stream` |

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
`responses[i]` always corresponds to `request.requests[i]`.

## Parallel mode

`--parallel N`:

- `1` (default) — sequential `Compose`, equivalent to running each
  request through `pulse api process` in a loop.
- `0` — `runtime.GOMAXPROCS` workers.
- `>1` — exactly N workers.

Workers share Pulse's read-only registries; per-request stateful
operators are constructed fresh. See [Parallel
Compose](../library/parallel-compose.md) for full mechanics.

## FailFast semantics

With `--no-fail-fast` unset (the default, fail-fast on):

- The first failing request cancels in-flight siblings.
- The command exits non-zero with the first error.

With `--no-fail-fast`:

- Every request runs to its own completion (or per-request timeout).
- Errors aggregate into a single `SERVICE_INTERNAL` error whose
  `details.failed_indices` lists the slot indices that failed.
- Successful slots populate the response array; failed slots are
  `null`.

## Output

### `--json`

```json
{
  "format_version": "1.0",
  "data": [ /* response per slot, in input order */ ],
  "errors": [],
  "warnings": []
}
```

Each entry in `data[]` is a full `Response`, so per-slot
`data[i].components` follows the same additive contract as
[`api process`](api-process.md#-json).

### `--stream`

```json
{"index": 0, "row": { ... }}
{"index": 0, "row": { ... }}
{"index": 1, "row": { ... }}
```

The `index` field identifies which slot's request produced each row.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | All requests succeeded |
| 1 | One or more requests failed (fail-fast: first error; aggregated: any failure) |

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

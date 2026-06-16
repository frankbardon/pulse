# pulse manifest

**Audience:** CLI users (and orchestration agents) discovering Pulse's
self-description — what commands exist, which aggregators are
registered, which field types are supported, and what skills the
binary ships with.

The manifest is the bare-`pulse` invocation with `--json`. It is
deterministic and process-wide: it never depends on cohort data or
the filesystem.

> **LLM agents using MCP:** the manifest is also available via the
> `pulse_manifest` MCP tool. Agents typically call this once per
> session and cache the result.

## Synopsis

```
pulse --json [--slim]
```

(There is no `pulse manifest` subcommand — the manifest is the root
command's `--json` output.)

## Flags

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--json` | bool | false | Emit the manifest as a JSON envelope |
| `--slim` | bool | false | Drop prose descriptions from the manifest payload (smaller for size-sensitive clients) |

## Manifest shape

From [`descriptor/manifest.go`](https://github.com/frankbardon/pulse/blob/main/descriptor/manifest.go):

```json
{
  "format_version": "1.1",
  "data": {
    "commands":   [ /* every CLI leaf with a usage line */ ],
    "operators":  [ /* every aggregator / attribute / filterer / grouper / window / feature */ ],
    "tests":      [ /* every tier-1 statistical test */ ],
    "post_tests": [ /* every tier-2 post-test variant */ ],
    "distributions": [ /* every synth distribution kind */ ],
    "errors":     [ /* every registered error code with a description */ ],
    "mcp_tools":  [ /* every MCP tool name + description */ ],
    "field_types":[ /* every .pulse field type */ ],
    "skills":     [ /* every embedded skill with metadata */ ]
  },
  "errors":   [],
  "warnings": []
}
```

Every list is sorted deterministically (alphabetical or category +
alphabetical). The same Pulse binary always emits the same manifest
bytes (modulo `--slim`).

## Determinism gates

Several CI tests enforce manifest completeness — see [Testing
Conventions](../contributing/testing.md). Notably:

- `TestManifestOperatorsComplete` — every registered operator appears
  in the manifest.
- `TestManifestTestsComplete` / `TestManifestPostTestsComplete` —
  every registered statistical test appears.
- `TestManifestDistributionsComplete`, `TestManifestErrorCodesComplete`,
  `TestManifestMCPToolsComplete` — same for distributions, error
  codes, and MCP tools.
- `TestManifestStreamableMatchesTypes` — every operator's
  `streamable` flag mirrors the per-type method.

## When to use the manifest

| Use case | Reach for |
|---|---|
| Discover what's available | `pulse --json` |
| Confirm a specific operator's params and emit type | `jq '.data.operators[] | select(.name == "AGG_FOO")'` |
| List embedded skills with their `applies_to` | `jq '.data.skills[]'` |
| Generate documentation or client stubs | Parse the full manifest once at boot |
| Quick "is this name a real operator?" | `pulse --json --slim | jq '.data.operators[].name'` |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Always (the manifest is in-memory, deterministic, never errors) |

## Examples

### Print the manifest

```bash
pulse --json | jq '.data | keys'
```

### Slim variant for embedding in an agent's system prompt

```bash
pulse --json --slim > manifest.slim.json
```

### List every aggregator with its emitted type

```bash
pulse --json | jq '.data.operators[] | select(.category == "aggregation") | {name, emits_type}'
```

### Confirm a feature operator's parameters

```bash
pulse --json | jq '.data.operators[] | select(.name == "FEAT_BUCKETIZE")'
```

## Related

- [How LLMs Use Pulse](../mcp/index.md) — the manifest is one of the
  agent discovery primitives
- [Library: pulse.Manifest](../library/overview.md) — Go counterpart
- [Internals: Architecture](../internals/architecture.md) — why the
  manifest cannot import `service/` or `processing/`

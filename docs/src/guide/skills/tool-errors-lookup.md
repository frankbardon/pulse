```yaml
name: tool-errors-lookup
kind: tool
description: Look up Pulse error code metadata — message and fixup hints.
type: reference
applies_to: mcp
```

## When to use

CALL WHEN A TOOL RETURNS AN ERROR CODE YOU NEED EXPLAINED.
Also to triage a known code or enumerate every code in one domain. The manifest carries only the slim code-name list — fetch detail here on demand to keep session context lean.

## Input

- `code` (string, optional): exact code identifier (e.g. `PULSE_FIELD_TYPE_UNSUPPORTED`). One result on a hit, none on a miss.
- `domain` (string, optional): domain prefix (`PULSE`, `ENCODING`, `PROCESSING`, `SERVICE`, `DATA`, `CLI`). Case-insensitive; enumerates every code in that domain.
- `query` (string, optional): case-insensitive substring over messages, fixup hints and code names, ranked in that order (ties alphabetical).

## Output

`{results: [...]}` (no envelope), each `{code, domain, message, fixups: [{action, hint}]}`; a code with no mechanical repair carries `fixup_not_applicable: true` and no `fixups`. `results: []` when nothing matches (never null). Several axes set → their intersection.

## Gotchas

- At least one of `code` / `domain` / `query` MUST be set; all empty is the coded tool error `SERVICE_VALIDATION`.
- Every code carries at least one fixup or `fixup_not_applicable: true`.
- An intersection keeps the order of the first axis set, taken in the order `code`, `domain` (alphabetical), `query` (ranked).

## See

- `pulse_manifest` `error_codes` (names only) and `error_domains`.
- [`session-bootstrap`](session-bootstrap.md) — error-handling role in the workflow.
- [`tool-manifest`](tool-manifest.md) — companion catalog for domain prefixes.

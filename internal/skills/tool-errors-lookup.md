---
name: tool-errors-lookup
kind: tool
description: Look up Pulse error code metadata — message and fixup hints.
type: reference
applies_to: mcp
---

## When to use

After `pulse_process` / `pulse_predict` returns a coded error and you need the prose explanation, or when triaging a known error code, or to enumerate every code in one domain. The manifest carries only the slim code-name list — fetch detail here on demand to keep session context lean.

## Input

- `code` (string, optional): exact code identifier (e.g. `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`). Returns 1-element array on hit, empty on miss.
- `domain` (string, optional): domain prefix (`PULSE`, `ENCODING`, `PROCESSING`, `SERVICE`, `DATA`, `CLI`). Case-insensitive; enumerates every code in that domain.
- `query` (string, optional): case-insensitive substring across descriptions and fixup hints; ranks message hits above fixup hits.

## Output

`descriptor.Envelope` wrapping `[]perr.LookupResult` — each entry: `Code`, `Domain`, `Message`, `Fixups[]` (Title + Body). Empty array when nothing matches (never null). Multiple axes set → intersection of candidate sets.

## Gotchas

- At least one of `code` / `domain` / `query` MUST be set; all empty → error `specify at least one of code, domain, query`.
- Every code carries at least one `Fixup` template OR `FixupNotApplicable: true`. Enforced by `TestCodesHaveFixups`.
- Intersection preserves the first-supplied axis's ordering (Search ranking, ByDomain alphabetical, or single-code).

## See

- Error-code list in `pulse_manifest` (`error_codes` slim slice).
- `session-bootstrap` — error-handling role in the workflow.
- `tool-manifest` — companion catalog for domain prefixes.

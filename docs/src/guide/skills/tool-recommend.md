```yaml
name: tool-recommend
kind: tool
description: Turn a question kind (an intent ID) into ranked draft requests — placeholder skeletons, or predict-checked drafts bound to a cohort's fields.
type: reference
applies_to: mcp
```

## When to use

CALL WHEN YOU KNOW THE KIND OF QUESTION BUT NOT THE REQUEST.
Get ranked drafts instead of JSON built from memory. CLI twin: `pulse recommend --intent ID [--cohort C] [--field F …] [--level L] [--limit N] [--json]`.

## Input

- `intent` (string, required): an ID from the [`intents`](intents.md) skill. Unknown or empty → `PULSE_RECOMMEND_INTENT_UNKNOWN`; `details.valid` lists every ID.
- `cohort` (string): a `.pulse` path. Set → drafts BOUND to its fields, each predict-checked. Omit → UNBOUND skeletons.
- `fields` (array, cohort only): hints; each pins the first role of the intent it fits. Not a field, or fits no role → `SERVICE_VALIDATION`.
- `level` (`basic` / `intermediate` / `advanced`): rank that level first. Default: simplest first.
- `limit` (int): cap, default 10.

## Output

`intent`, `bound`, `shapes` (the roles a request's fields fill), `recommendations[]`, `routes_to`, `truncated`, `candidates_considered`. Each recommendation: `operator`, `category`, `level`, `why`, `bound`, `request` (the draft), `placeholders`, `needs` (`{param, why}`), `advisories` (predict's), `alternatives`, `follow_ups`.

## Gotchas

- **Unbound drafts never run as-is.** Every `"<placeholder>"` string (named after its wire key, listed in `placeholders`) must be replaced; then `pulse_predict` it.
- **`bound: false` on a cohort draft** means it `needs` a value only you can choose — a success value, a reference mean, a model family. Fill each `needs[].param` before running.
- `bound: true` drafts passed predict; still read their `advisories`.
- Empty `recommendations` + `routes_to` is not an error: tooling answers it, or nothing serves it yet.
- Reads header, schema and sidecar only. Unreadable cohort → `DATA_FILE`.
- Ranking: hints, fully bound, level, fewest advisories.

## See

- [`intents`](intents.md) — every intent ID and its field shapes.
- [`tool-predict`](tool-predict.md) — re-check a draft after filling placeholders.
- [`session-bootstrap`](session-bootstrap.md) — where recommend sits in a session.

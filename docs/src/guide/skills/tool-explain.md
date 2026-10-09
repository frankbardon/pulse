```yaml
name: tool-explain
kind: tool
description: Say in plain words what a request will do, or what a result found — closed verdicts, conventional strength bands and caveats.
type: reference
applies_to: mcp
```

## When to use

Before running a request, to confirm what it does; after, to read the result without re-deriving verdicts from p-values. CLI twin: `pulse explain --request F | --response F [--request F] [--root R] [--detail full] [--json]`.

## Input

Exactly ONE root, as an object:

- Request mode: `request` (a process request body), `composed`, `chain`, `facet` or `sample`. Never run; a root naming a cohort is predict-checked.
- Response mode: `response`, `composed_response`, `chain_response` or `facet_result`, exactly as Pulse returned it, plus its own request beside it (`request` / `composed` / `chain` / `facet`).
- `detail`: `terse` (default) or `full`.

## Output

`mode`, `root`, `summary` (one sentence), `findings[]` (`slot`, `subject`, `operator`, `verdict`, `strength_band` + `convention`, `numbers`), `caveats`. Request mode adds `valid`, `steps[]`, `defaults_applied`, `advisories`, `refusals`. `full` adds `sentences`, `glossary_refs`, `follow_ups`.

## Gotchas

- **Relay `caveats`; never drop them** — uncorrected p-values, alpha assumed for a regression, a partial reading.
- A `no_evidence_of_*` verdict is not evidence of none; say "no evidence of a difference", never "no difference".
- Pass results unedited: a `null` figure is undefined and reads `not_computable`.
- Without the request companion an aggregation is described by count only.
- Zero or several roots, or an unknown `detail` → `SERVICE_VALIDATION`; unreadable cohort → `DATA_FILE`.

## See

- [`glossary`](glossary.md) — terms in `glossary_refs`.
- [`tool-predict`](tool-predict.md) — the check request mode runs.
- [`tool-recommend`](tool-recommend.md) — drafts to explain before running.

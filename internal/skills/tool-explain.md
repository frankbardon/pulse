---
name: tool-explain
kind: tool
description: Say in plain words what a request will do, or what a result found — closed verdicts, conventional strength bands and caveats.
type: reference
applies_to: mcp
---

## When to use

CALL TO SAY IN PLAIN WORDS WHAT A REQUEST WILL DO, OR WHAT A RESULT FOUND — instead of reading p-values yourself.
Or how an operator is validated.
CLI twin: `pulse explain OPERATOR | --request F | --response F [--request F] [--root R] [--detail full] [--json]`.

## Input

Exactly ONE root, as an object:

- Request mode: `request` (a process request body), `composed`, `chain`, `facet` or `sample`. Never run; a root naming a cohort is predict-checked.
- Response mode: `response`, `composed_response`, `chain_response` or `facet_result`, exactly as Pulse returned it, plus its own request beside it (`request` / `composed` / `chain` / `facet`).
- Operator mode: `operator` (an operator name), alone.
- `detail`: `terse` (default) or `full`.

## Output

`mode`, `root`, `summary` (one sentence), `findings[]` (`slot`, `subject`, `operator`, `verdict`, `strength_band` + `convention`, `numbers`), `caveats`. Request mode adds `valid`, `steps[]`, `defaults_applied`, `advisories`, `refusals`, `warnings` (predict's coded warnings, each once; `details.slot` names a Compose slot). `full` adds `sentences`, `glossary_refs`, `follow_ups`. Operator mode adds `validation`: `references[]` (`against`, `tolerance`, `tests`), `gap`, `weight_refusal`, `certification[]` (NIST digits).

## Gotchas

- **Relay `caveats`; never drop them** — uncorrected p-values, an assumed alpha, a partial reading.
- A `no_evidence_of_*` verdict is not evidence of none; say "no evidence of a difference", never "no difference".
- Pass results unedited: a `null` figure is undefined and reads `not_computable`.
- Without the request companion an aggregation is described by count only.
- A `composed` root is judged exactly as `pulse_predict` judges it: its `fits` and each slot's `models` bind every slot; `details.slot` names `requests[i]` or `fits[k]`.
- A selection regression (and a Compose regression fit, read at `fits.<name>`) is named by the predictors it KEPT; its sentence names the dropped candidates.
- Relay `validation.gap`; no `validation` means no record, not unchecked.
- Zero or several roots, an operator beside a root or one this instance does not offer, or an unknown `detail` → `SERVICE_VALIDATION`; unreadable cohort → `DATA_FILE`.

## See

- `glossary` — terms in `glossary_refs`.
- `tool-predict` — the check request mode runs.
- `tool-recommend` — drafts to explain before running.

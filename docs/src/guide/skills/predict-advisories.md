```yaml
name: predict-advisories
description: Predict advisories — coded, non-blocking notes in a predict result that the chosen analysis may not fit the data (a two-group test over many groups, many uncorrected p-values); how they differ from warnings, what each code means, what it suggests, and how an embedder suppresses one.
kind: design
type: guide
applies_to: predict
covers: [advisories, PULSE_ADVISORY, analysis fit, suppress advisories]
```

# Predict advisories

Predict answers "will this run" (errors, warnings). An **advisory** answers a different question: "does this analysis fit the data". `pulse_predict` / `pulse api predict --json` carry them in `data.advisories`, one `{code, message, details}` per finding, omitted when none fires.

- **Not a warning.** Advisories ride their own slot, never the envelope `warnings`. `--strict` never turns one into an error and `valid` never changes.
- **Never changes execution.** The request runs exactly as written; acting on an advisory is the caller's choice.
- **Authoritative metadata only.** A rule reads the request, the schema and its dictionaries, and figures predict already computed. No field-name or value-shape guessing, so a rule that cannot see the data stays silent.
- **`details.suggested`** is a replacement operator name or a request patch to merge — never a rewritten request — and only ever names something this instance offers. `pulse_errors_lookup CODE` returns the fixup text.

## Codes

| Code | Fires when | `details` |
|---|---|---|
| `PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS` | a two-sample t-test's `split_by` is a categorical field whose dictionary holds more than two entries (exactly two never fires) | `slot`, `operator`, `field`, `split_by`, `groups`, `suggested` (the many-group ANOVA; absent when none is offered) |
| `PULSE_ADVISORY_MANY_TESTS` | `p_values.uncorrected` ≥ `threshold` (10) and no `multiplicity` block anywhere — request, slot or instance default | `total`, `uncorrected`, `basis`, `threshold`, `suggested: {"multiplicity": {"method": "holm"}}` |

The many-tests message says "at least N" when `p_values.basis` is `lower_bound` and "an estimated N" when it is `dictionary`; any `multiplicity` block — `method: none` included — silences it.

The two-group rule reads the dictionary, not the data: it fires even when a filter leaves only two groups.

## Suppression

Embedders set `Options.SuppressAdvisories` (a list of codes) for questions their product answers itself. `pulse.New` refuses an unknown code with `PULSE_SUPPRESS_ADVISORY_UNKNOWN`. There is no per-request or CLI knob.

## See

[`tool-predict`](tool-predict.md) · [`multiplicity-correction`](multiplicity-correction.md) · [`statistical-testing`](statistical-testing.md) · `pulse_errors_lookup` for `PULSE_ADVISORY_*`.

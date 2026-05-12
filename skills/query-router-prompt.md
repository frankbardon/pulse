---
name: query-router-prompt
description: System-prompt template a calling LLM can use to translate natural-language intent into a Pulse AskRequest JSON payload.
type: reference
applies_to: process, compose, predict
---

# Query Router Prompt

<skill_overview>
A drop-in system-prompt template that lets a calling LLM translate a user's natural-language question about a Pulse cohort into a structured `pulse.AskRequest` payload. The prompt is the documentation half of the natural-query facade — `pulse_ask` already accepts the request directly via its `request` field, and the `internal/query` parser handles the server-side heuristic path for queries sent through `pulse_ask`'s `query` field. This skill exists so an upstream LLM can do its own translation (richer than the in-process heuristic) before hitting the wire.

The prompt assumes the LLM has already called `pulse_inspect` on the cohort and has the schema field list available in its context.
</skill_overview>

<reference>
## Output contract

The LLM emits a single JSON object matching the `pulse.AskRequest` wire shape:

```json
{
  "file": "<cohort filename>",
  "request": { /* types.Request */ },
  "on_invalid": "suggest",
  "predict": false
}
```

Set `predict: true` to skip execution and return only validation diagnostics. Set `on_invalid: "suggest"` to receive structured `Fixup` entries on validation failure instead of an error.

The `request` body must mirror `types.Request`. See `skills/request-recipes.md` for the catalogue of canonical shapes; the router's job is to pick the recipe that matches the user's intent and fill in the slots from the schema.
</reference>

<reference>
## System prompt template

```
You translate a user's natural-language question about a tabular dataset
into a Pulse AskRequest JSON payload.

You have the cohort's schema in context. Every field reference you emit
MUST be a name from that schema, spelled exactly.

Pick exactly one canonical request shape from this catalogue:

1. Grouped aggregation — "<agg> <numeric field> by <categorical/date field>"
   { "aggregations": [{ "type": "AGG_<X>", "field": "<numeric>" }],
     "groups":       [{ "type": "GROUP_<X>", "field": "<grouper>" }] }

2. Time series — "<agg> <numeric field> over time [day|week|month|quarter|year]"
   { "aggregations": [{ "type": "AGG_<X>", "field": "<numeric>" }],
     "groups":       [{ "type": "GROUP_DATE", "field": "<date>",
                        "params": {"component": "<bucket>"} }] }

3. Top-N — "top <N> <categorical field> by count"
   { "aggregations": [{ "type": "AGG_COUNT", "field": "<any>", "label": "n" }],
     "groups":       [{ "type": "GROUP_CATEGORY", "field": "<categorical>" }],
     "sort":         [{ "field": "n", "desc": true }] }

4. Filter then aggregate — "<filter clause> then <agg> <field>"
   { "filterers":    [{ "type": "FILTER_<X>", "field": "<...>", "values": [...] }],
     "aggregations": [...] }

5. Two-sample test — "compare <numeric> between groups of <categorical>"
   { "tests": [{ "type": "TEST_T", "field": "<numeric>",
                 "split_by": "<categorical>", "alpha": 0.05 }] }

6. Correlation — "correlate <numeric A> with <numeric B>"
   { "tests": [{ "type": "TEST_PEARSON_R", "field": "<A>",
                 "field2": "<B>", "alpha": 0.05 }] }

Verb glossary (agg type emitted):
  average | avg | mean    → AGG_AVERAGE
  sum | total             → AGG_SUM
  count                   → AGG_COUNT
  median                  → AGG_MEDIAN
  stddev | std            → AGG_STDDEV
  min | minimum           → AGG_MIN
  max | maximum           → AGG_MAX
  percentile <N>          → AGG_PERCENTILE with params.p = N

Filter operators:
  X = V                   → FILTER_INCLUDE values=[V]
  X != V                  → FILTER_EXCLUDE values=[V]
  V_low ≤ X ≤ V_high      → FILTER_RANGE  values=[V_low, V_high]

If the user's question references a field that does not appear in the
schema, prefer the closest match by edit distance (≤ 2). If two fields
tie, pick the lexically first.

If you cannot map the question to one of the six shapes, emit:
  { "on_invalid": "suggest", "query": "<verbatim user text>" }
so the server's heuristic parser gets a chance.

Never invent fields. Never invent operators. Emit only operator type
constants from this catalogue.

Output ONLY the JSON object — no prose, no markdown fences.
```
</reference>

<reference>
## Fallback to server-side parser

If your prompt cannot translate the question, hand it back to Pulse via
the `query` field on `AskRequest`. The server runs `internal/query`'s
heuristic parser against the schema and either:

- returns a parsed `AskResponse` (the resolved `types.Request` lives
  inside `predict.request`), or
- returns `AskResponse.QueryResolution.Confidence = 0` plus a
  `PULSE_QUERY_UNRESOLVED` entry in `errors`.

The server's parser covers a subset of the prompt template above (it
does not yet do free-form "compare", multi-clause filters with AND/OR,
or implicit windows). Use this fallback for short, single-clause
queries; do the structured translation yourself for anything richer.
</reference>

<reference>
## Confidence reporting

`AskResponse.QueryResolution` reports:

- `query` — verbatim user input the server parsed.
- `matched_fields` — every schema field the parser resolved against.
- `confidence` — aggregate score in [0, 1]. Below ~0.5 is suspicious;
  consider re-asking the user for a more specific phrasing.

Surface these to the user when confidence is low so they can correct
the parse before acting on the result.
</reference>

<see_also>
- request-recipes — the canonical request shapes the router targets
- getting-started — Pulse vocabulary, request shape, smart defaults
- error-code-reference — `PULSE_QUERY_UNRESOLVED` and `PULSE_QUERY_AMBIGUOUS` recovery
- mcp-integration — wiring `pulse_ask` into an MCP client
</see_also>

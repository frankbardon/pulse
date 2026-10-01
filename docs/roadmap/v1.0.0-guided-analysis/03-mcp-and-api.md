# 03 — MCP & API: from need to request

The docs in 02 help a human browse. This document turns the same metadata into **callable** help: for a developer in Go, for a CLI user, and above all for an LLM agent that has to turn "are regions equally satisfied?" into a valid request in a few round-trips.

Per Pulse's library-first rule, each capability is a **facade method first**, with a thin CLI leaf and MCP tool on top. All of it is deterministic and contains no model.

---

## M1. `pulse.Recommend` — intent + cohort → ranked runnable drafts (Committed)

```go
recs, err := p.Recommend(ctx, pulse.RecommendRequest{
    Intent:  "compare_groups",            // from the taxonomy; required
    Cohort:  "survey_2026q3.pulse",       // optional but strongly recommended
    Fields:  []string{"satisfaction", "region"}, // optional hints
    Level:   "basic",                     // optional: prefer simpler methods
})
```

**What it does:**
1. Loads the cohort **schema only**, the same as predict: no records, no-execute.
2. Matches the intent's shape descriptors against the schema. Given "one outcome plus one grouping", it finds numeric candidates for the outcome and categorical candidates for the grouping, and uses `Fields` hints when given.
3. For each matching operator, builds a **draft request** bound to real field names.
4. Runs `Predict` on each draft and drops anything invalid. Predict advisories (M3) are attached.
5. Ranks the drafts: hinted fields first, then by `Level` (simplest suitable), then by fewest advisories.

**Output** (one entry per recommendation):

```jsonc
{
  "operator": "TEST_ANOVA_F",
  "why": "Compares the average of 'satisfaction' (f32) across the 5 values of 'region'.",
  "request": { /* complete, predict-validated Request */ },
  "advisories": [ { "code": "PULSE_ADVISORY_UNEQUAL_SPREAD_RISK", "message": "..."} ],
  "alternatives": [ { "operator": "TEST_KRUSKAL_WALLIS", "when": "if satisfaction is skewed" } ],
  "follow_up": [ { "operator": "TEST_TUKEY_HSD", "why": "to see which regions differ" } ]
}
```

**Cohort-free mode (decided: supported).** With no `Cohort`, Recommend skips steps 1–2 and 4 and returns *unbound* recommendations: operator, `why`, the field shapes it needs ("one numeric outcome + one categorical grouping"), alternatives, follow-ups, and a request skeleton with `<placeholder>` field names. These are marked `bound: false` and are never predict-validated, because there is nothing to validate against. Uses: early exploration ("what could I do with survey data?"), the docs generator (decision trees and guide cards come from the same call), and agents planning before they pick a cohort. Supplying a cohort later upgrades the same recommendation to a bound, validated draft.

**What it explicitly does *not* do:** parse English. The agent maps "are regions equally satisfied?" to `compare_groups` using the intents list (M4), which is a trivial step for an LLM. Pulse then does the part LLMs get wrong: picking a valid operator for the actual field types and producing a request that validates.

**Surfaces:** `pulse recommend --intent compare_groups --cohort X [--field a --field b] --json`, and the MCP tool `pulse_recommend`. Following the Update Demand, the new tool needs `skills/tool-recommend.md` and `mcp/toolmeta` metadata, and the CLI leaf needs a `flags.md` row.

---

## M2. `pulse.Explain` — request or response → plain language (Committed)

There are two modes behind one method.

**Explain a request** ("what will this do?"):

> "Keeps rows where `country = US`. Groups them by `region` (5 groups). For each region, computes the **average** of `satisfaction`. Then tests whether those averages differ across regions (**one-way ANOVA**). [Glossary: one-way ANOVA]"

This is built from the request's slots plus each operator's `Purpose.Plain`. It is useful in code review ("what does this saved template actually do?"), for templated requests (explain the rendered request), and for an agent confirming intent with its user before running anything expensive.

**Explain a response** ("what did we learn?"):

> "Average satisfaction **differs by region** (p = 0.003, below the 0.05 threshold). The effect is **small**: region accounts for about 4% of the variation (η² = 0.04, Cohen's convention). To see which regions differ from each other, run `TEST_TUKEY_HSD`. 312 rows were excluded because `satisfaction` was missing."

This is built from `Interpretation` rules, the shared p-value rules, the Components floor (the null counts surface as "excluded rows", a silent-bias warning most readers miss), and `Purpose` follow-ups.

**Properties:**
- **Templated and deterministic.** Golden-tested per operator.
- **Structured output.** Alongside the prose, Explain returns `{sentences[], findings[{subject, verdict, strength_band, numbers}], glossary_refs[], follow_ups[]}`, so an agent can re-word it without re-deriving any of it.
- **Never overstates.** A non-significant result reads "no evidence of a difference", never "no difference". Bands always name their convention. Multiple-comparison context is mentioned whenever more than one inferential result is present.
- **Surfaces:** `pulse explain --request file.json` and `pulse explain --response out.json`; MCP tool `pulse_explain`.

**Stretch: an inline `Response.Interpretation` — opt-in only (decided).** `Request.Explain: true` (CLI `--explain`) attaches the structured findings to the response, saving one MCP round-trip. **It is off by default everywhere:** the library, the CLI and MCP never turn it on implicitly, MCP prompts do not set it, and a response without it is byte-identical to today's. It is an additive `omitempty` slot, so `format_version` stays `"1.1"`, but it does trigger the Response-slot row of the Update Demand. When on, it carries `findings[]` only (no prose sentences) unless `explain_detail: "full"` is set.

**Terse by default.** `pulse_explain` returns `findings[]` plus a one-sentence summary by default; full sentences, glossary refs and caveats come with `detail: "full"`. The agent pays for prose only when it needs prose.

---

## M3. Predict advisories (Committed)

Predict already validates a request. Add **advisories**: plain-language, non-blocking notes about *fit for purpose*, derived from `Purpose.Assumptions` and the schema. They are distinct from warnings, because they are about the analysis choice, not the data or the engine.

| Advisory | Trigger (schema / request only) |
|---|---|
| `PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS` | `TEST_T` / `TEST_WELCH` grouped by a categorical with > 2 dictionary entries → suggests ANOVA |
| `PULSE_ADVISORY_MANY_TESTS` | > N inferential results in one request with no adjustment overlay → suggests `OVERLAY_CORR_PVALUE` / Holm |
| `PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC` | a numeric-coded categorical (e.g. SPSS-imported region codes with value labels) used in `AGG_AVERAGE` |
| `PULSE_ADVISORY_ORDINAL_PARAMETRIC` | parametric test on a small-range integer scale (u4/u8 with ≤ 7 distinct labels) → mentions the rank-based alternative |
| `PULSE_ADVISORY_COSINE_ON_SCALE` | raw cosine on a `kind: scale` vector (vector-matrix doc 07) |
| `PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED` | cohort has an SPSS weight variable / a field named like a weight, and the request uses none |

**Rules:**
- Advisories never change execution.
- They ride `PredictResult.Advisories` and the `pulse_predict` output, and `Recommend` attaches them too.
- They are suppressible per code (`Options.SuppressAdvisories`).
- They are coded like errors, so each needs `codeMetadata` with Message and Fixup. The fixup is the suggested alternative request.
- They are header-only: predict's no-execute contract holds, because every trigger above needs only schema, dictionaries and the request.

---

## M4. MCP: intents and prompts (Committed)

**Intents in the manifest.** `pulse_manifest` gains `intents[]`: `{id, plain, sounds_like[], shapes[]}`. An agent fetches the manifest once per session, so it then has everything needed to classify a user's question. This adds no tool.

**MCP prompts.** Pulse currently exposes MCP *tools* and *resources*. The MCP spec also has **prompts**: named, parameterised workflows a client can surface to users (as slash-commands in many clients). Add one prompt per intent:

```
pulse-compare-groups(cohort, outcome?, groups?)
pulse-find-relationships(cohort, fields?)
pulse-check-scale(cohort, items?)
pulse-check-data-quality(cohort)
...
```

Each prompt is a short, scripted workflow for the agent:
1. `pulse_inspect` the cohort.
2. `pulse_recommend` with the intent.
3. Confirm the draft with the user, using `pulse_explain` on the request.
4. `pulse_process`.
5. `pulse_explain` the response.

Prompts are generated from the intent registry, gated like tools (`TestSkillsCoverAllMCPPrompts`), and mounted in `mcp/gosdk` beside the tools. This is the most direct answer to "translate need into Pulse requests", because the workflow is shipped *by Pulse*, not reinvented by every harness.

---

## M5. Search and token optimizations (Committed / Stretch)

| Item | Tier | Change |
|---|---|---|
| `pulse_examples_search {intent}` | C | filter by intent; `query` also searches `_meta.question` and `interpretation` |
| Synonym table | C | a deterministic map from plain words to intents and operators ("differ", "vs", "compare" → `compare_groups`; "move together", "linked" → `relationship`; "SPSS RELIABILITY" → `MAT_RELIABILITY`), from `Purpose.KnownAs` plus a curated list; used by examples search and `pulse_skills_list` |
| `pulse_skills_list {intent}` | C | returns the intent's question guide skill + atomic skills, ranked by `Level` |
| Intent-scoped manifest | S | `pulse_manifest {intent: "compare_groups"}` returns only the operators, params and examples relevant to that intent, roughly a tenth of the full manifest's tokens |
| Tool descriptions lead with purpose | C | every MCP tool description's first sentence says when to call it (the `## When to use` section already exists in tool skills; move its first line into `mcp/toolmeta` descriptions, which is what clients actually show the model) |
| `pulse-skill://glossary` resource | C | one fetch gives the whole glossary |
| "Did you mean" on errors | S | when a request fails validation in a way a different operator would satisfy (e.g. `TEST_T` given a 4-level grouping), the coded error's `details.suggested_operator` names it; the fixup template already exists as a mechanism |

---

## M6. Library ergonomics for developers (Stretch)

- **`pulse.Describe(operator)`** returns the `Purpose` and `Interpretation` structs for any operator, so Go callers can build their own UI help without parsing skills.
- **GoDoc.** Generated `// Purpose:` comment blocks on the exported type constants (`types.AggSum`, …), so IDE hover shows the plain-language line. Generated from metadata, with a gate that it is current.
- **`pulse operators --intent compare_groups`**: a human-readable CLI listing for terminal users (a thin leaf over `Describe`).

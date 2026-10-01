# Documentation audit — before v1.0.0

**Status:** proposal · **Target:** after all feature units, before the v1.0.0 release candidate

## Goal

Pulse should go live with the **most helpful, current and comprehensive documentation we can produce**. v1.0.0 is when most new users and embedders arrive, and it is when documentation errors become expensive: they turn into support questions, wrong analyses, and confused agents.

The audit is a deliberate, final pass over **every surface a human or an agent reads**. It is backed by automated checks, so the result stays true after release.

## What "good" means (the audit criteria)

Every document, skill, help text and description is checked against six criteria:

| Criterion | Question | Example failure |
|---|---|---|
| **Accurate** | Does it match v1.0.0 behaviour exactly? | a flag renamed in U02's package moves; an outdated default |
| **Current** | Does it mention anything removed, renamed or pre-1.0? | `WelfordTriple`, `OVERLAY_CORR_PVALUE`, `MCP version 1.0.0` |
| **Complete** | Is every public feature documented somewhere a user will find it? | a facade method with no docs page; an env var only in code |
| **Helpful** | Does it start from the reader's task, with a working example? | reference prose with no "when would I use this" |
| **Consistent** | Same term for the same thing everywhere (per the glossary)? | "cohort" vs "dataset" vs "file" for the same concept |
| **Runnable** | Does every example request, snippet and command actually run? | a JSON request in a guide that fails predict |

## Surfaces in scope

| Surface | Audience | Notes |
|---|---|---|
| mdBook site (`docs/src/**`), incl. the generated Analysis Guide | humans | structure (`SUMMARY.md`) reviewed as well as pages |
| `README.md`, `CONTRIBUTING.md`, `SECURITY.md`, `STABILITY.md` | humans | README is the front door. It needs v1 install from GitHub Releases |
| GoDoc for every public package (the U02 list) | Go developers | package docs, exported identifiers, `Example*` functions |
| CLI `--help` for every leaf | admins | must match `docs/src/cli/flags.md` |
| Skill pack (atomic + topical) | agents | budgets met; progressive disclosure intact; fences correct |
| MCP tool descriptions, prompts, resource descriptions | agents | first sentence = when to call |
| Manifest descriptions, `Purpose` / `Interpretation`, glossary | agents + humans | plain language; conventions named |
| Error code messages and fixups (`pulse errors lookup`) | everyone | every fixup actionable |
| Examples library (`examples/**`, `_meta`) | agents + humans | runnable; story fields present |
| `CLAUDE.md` + `.claude/reference/**` | contributors and coding agents | accurate long form; size budget |

**Not in scope:** the roadmap documents themselves (`docs/roadmap/**`). They are planning artefacts and are marked done or post-1.0, not polished.

## Method

### 1. Inventory and coverage matrix
Build one table listing every **public thing**, with where it is documented:
- facade methods and public package identifiers (from U02);
- CLI leaves and flags;
- MCP tools, prompts and resources;
- operators;
- field types;
- error codes;
- env vars;
- `Options` fields;
- request and response slots.

A blank cell is a gap. Most columns can be **generated** from the manifest, the payload schema, `go doc` and the CLI tree, so the matrix is itself a repeatable check.

### 2. Automated checks (kept after release)
| Check | Catches |
|---|---|
| Link checker on the built mdBook site and on Markdown in the repo | broken internal/external links, renamed anchors |
| Runnable-snippet test: every fenced `json` request and `go` snippet tagged as runnable in `docs/src` is executed (requests through predict + process against fixture cohorts; Go snippets compiled) | examples that silently rotted |
| CLI help ↔ `flags.md` parity | flags documented but not mounted, or vice versa (extends the two-way `TestSkillsCoverAllCliLeaves` idea to flags) |
| GoDoc `Example*` functions for the public facade (run by `go test`) | API examples that don't compile or don't produce the shown output |
| `TestSkillTokenBudget` switched from soft (log) to hard (fail) | the transitional over-budget skills noted in `skill-pack.md` |
| Removed-name scan: a denylist of removed or pre-1.0 identifiers that must not appear in any served text | stale names surviving in prose |

### 3. Human and agent passes
- **Accuracy and currency pass:** a section-by-section read against the coverage matrix, recording findings in a log (`docs-audit-findings.md` on the audit branch) with severity: wrong > missing > stale > unclear > polish.
- **Getting started for v1:** rewrite the path from zero to a first answer: install from GitHub Releases → first cohort → first analysis → first MCP session. Add a single **embedder guide** covering profiles, limits, observability and response shaping, which are new in v1 and currently spread across pages.
- **Terminology pass:** align with the glossary; purge pre-1.0 and removed names.
- **Fresh-reader review:** a developer without a statistics background follows Getting Started and two question guides, and records where they got stuck.
- **Agent task evaluation:** a fixed set of ~20 plain-language tasks (one or two per intent) is given to an agent that only has the MCP server, the manifest and the skills. Success means a valid request whose result answers the question. Each failure traces to a documentation fix (skill, prompt, tool description or Purpose text), and the evaluation is rerun until it passes. The task set stays in the repo as a regression check for future releases.

### 4. Sign-off
Every finding in the log is fixed, or explicitly deferred with a reason and an issue link. The maintainer signs off in the audit PR.

## Why last, and why a separate unit

Every feature unit already updates its own docs (Update Demand), so this audit should find integration gaps rather than missing pages. Those gaps are things no single unit owns: an overview page that predates three features, a Getting Started that never mentions profiles, inconsistent terminology across themes, an agent that can't find the right prompt. They are only visible once everything has landed. Running the audit before the release candidate means the rc ships with the final docs, and downstream validation exercises them too.

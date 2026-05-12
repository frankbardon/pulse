# How LLMs Use Pulse

This chapter is a **pointer**, not a guide. The human-facing site you are reading
is for operators, embedders, and contributors. LLM-facing guidance ships inside
the Pulse binary as an embedded skill pack and is delivered over the Model
Context Protocol.

## Two surfaces, one engine

| Surface | Audience | How it is delivered |
|---|---|---|
| This mdBook site | Humans — operators, embedders, contributors | Static HTML at <https://frankbardon.github.io/pulse/> |
| Embedded skill pack | LLM agents | `pulse mcp` server, loaded with `pulse_skills_list` then `pulse_skills_get` |

The skill pack speaks MCP voice: tool calls, JSON request payloads, error
codes, no CLI invocations. This site speaks CLI voice and Go-library voice.

## Task → skill cross-reference

If you are writing a system prompt for an LLM agent that uses Pulse, point it at
these skills rather than at this site:

| LLM task | Skill name |
|---|---|
| Author a `Process` / `Compose` request | `request-recipes` |
| Compose multiple sub-requests in one call | `request-recipes` |
| Look up an error code or warning | `error-code-reference` |
| Pick an aggregator | `aggregation-guide` |
| Pick an attribute (z-score, percentile, formula, ...) | `attribute-composition` |
| Design a grouper | `grouper-design` |
| Use a window operator (`WIN_*`) | `window-operations` |
| Use a feature engineer (`FEAT_*`) | `feature-engineering` |
| Run a statistical test | `statistical-testing` |
| Generate synthetic data | `synthetic-data` |
| Inspect or predict a `.pulse` file | `schema-inspection` |
| Understand a cohort's schema layout | `cohort-schema-design` |
| Pick an export format | `export-format-selection` |
| Wire Pulse into an MCP client | `mcp-integration` |
| Get started end-to-end (LLM walkthrough) | `getting-started` |

## How an agent discovers the skill pack

At session start, an LLM connected to a Pulse MCP server should call
`pulse_skills_list` once to enumerate the catalog, then `pulse_skills_get` on
demand for the skills relevant to its current task. The agent should treat the
returned text as authoritative guidance; this site does not duplicate that
content and may lag.

For wiring details (Claude Desktop, Claude Code, generic MCP clients), see
[`mcp` (CLI leaf)](../cli/mcp.md) on the human side and the `mcp-integration`
skill on the LLM side.

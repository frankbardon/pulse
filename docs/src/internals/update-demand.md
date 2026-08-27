# The Update Demand

> **Source of truth:** this chapter is mirrored from the "Update Demand"
> section of [`CLAUDE.md`](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md).
> Both files are kept in lock-step; `CLAUDE.md` is authoritative if they ever
> diverge (a `TestUpdateDemandTableCovers` CI gate enforces table coverage
> against the registries).

Any change to Pulse code, configuration, file format, or public surface MUST update the corresponding skill file(s) and `CLAUDE.md` in the same PR. This is not a courtesy. It is a non-skippable CI failure if any of the trigger conditions below is met without the corresponding doc update.

## Trigger → required update

| If you change... | You MUST also update... | Enforced by |
|---|---|---|
| A registered aggregator | `skills/aggregation-design.md` (add or update the section for that aggregator) | `TestSkillsCoverAllComponents` |
| A registered attribute | `skills/attribute-composition.md` | `TestSkillsCoverAllComponents` |
| A registered filterer | `skills/aggregation-design.md` (filtering section) | `TestSkillsCoverAllComponents` |
| A registered grouper | `skills/grouper-design.md` | `TestSkillsCoverAllComponents` |
| A registered window operator | `skills/window-design.md` | `TestSkillsCoverAllWindowTypes` |
| An error code (added/removed/renamed) | `errors/fixup_metadata.go` (`codeMetadata` Message + Fixups; surfaced per-code via `pulse_errors_lookup` / `pulse errors lookup CODE`) | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete` |
| A CLI leaf (added/removed/flag added) | the command index in `docs/src/cli/flags.md` + `skills/session-bootstrap.md` if the leaf carries a user-facing flag | `TestSkillsCoverAllCliLeaves` (checks the leaf path is NAMED under `skills/` or `docs/src/`; it cannot check that the prose is adequate) |
| A `--json` envelope or `format_version` | `CLAUDE.md` "Output Format Contract" | `TestClaudeMdMentionsFormatVersion` |
| A `.pulse` file format change (header layout, new field type) | `CLAUDE.md` "Code Conventions" + `skills/cohort-schema-design.md` | `TestClaudeMdMentionsFormatVersion`, `TestSkillsCoverAllFieldTypes` |
| A new non-skippable CI gate | `CLAUDE.md` (gate listed by name in the relevant section) | `TestClaudeMdMentionsAllNonSkippableGates` |
| A new architectural decision | `CLAUDE.md` (relevant section) + PRD if applicable | reviewer enforcement |
| An environment variable | `CLAUDE.md` "Build / Dev / Test Workflow" + `skills/session-bootstrap.md` | `TestClaudeMdMentionsAllEnvVars` |
| A registered MCP tool (added/removed) | `docs/src/internals/adding-mcp-tool.md` + `mcp/toolmeta/meta.go` (name + description) | `TestSkillsCoverAllMCPTools`, `TestManifestMCPToolsComplete` |
| A new MCP action tool with field-name parameters | `mcp/bind.go` (add a per-tool JSON Schema builder + entry in `Bind`) + `docs/src/internals/adding-mcp-tool.md` (Schema-bound enums section) | `TestMCPSchemaBinding_RemovesInvalidFields`, `TestMCPSchemaBinding_AllFieldsInFiltererEnum`, `TestMCPSchemaBinding_SampleAndFacetFieldEnum`, `TestMCPSchemaBinding_DedupAndSort`, `TestMCPSchemaBinding_NilSchema` |
| A registered feature operator | `skills/feature-engineering.md` (operator catalog) + capability declaration in `descriptor/capabilities_features.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered synth distribution kind | `skills/synthetic-data.md` (Supported distributions) + capability declaration in `descriptor/capabilities_distributions.go` | `TestSkillsCoverAllSynthDistributions`, `TestManifestDistributionsComplete` |
| A registered statistical test (`TEST_*`) | `skills/statistical-testing.md` (Operator catalog) + `types/streamability.go` + `types/streamability_test.go` + capability declaration in `descriptor/capabilities_tests.go` | `TestStreamability_TestsKnown`, `TestManifestTestsComplete` |
| A registered tier-2 post-test variant | Capability declaration in `descriptor/capabilities_tests.go` (`postTestCapabilities`) | `TestManifestPostTestsComplete` |
| A registered aggregator/attribute/filterer/grouper/window capability metadata | Capability declaration in `descriptor/capabilities_<category>.go` (params, accepts_types, emits_type, streamable_hint) | `TestManifestOperatorsComplete` |
| A new error code | Description row in `descriptor/capabilities_errors.go` (`errorMetaTable`) | `TestManifestErrorCodesComplete` |
| An error code's fixup template | Entry in `errors/fixup_metadata.go` (`codeMetadata`) — surfaced per-code via `pulse_errors_lookup` / `pulse errors lookup CODE` | `TestCodesHaveFixups` |
| A new operator's streaming capability | `types/streamability.go` (case for the new type) + table in `types/streamability_test.go` | `TestRegistryStreamabilityMatchesTypes`, `TestStreamability_*Known`, `TestManifestStreamableMatchesTypes` |
| The default operator table | `CLAUDE.md` "Code Conventions → Smart defaults" + `skills/session-bootstrap.md` ("Smart defaults" section) | `TestDefaults_Applied` + reviewer enforcement |

**The Update Demand applies recursively to itself:** when a new trigger row is added (e.g., a new component category, a new contract), this table MUST be updated in the same PR. `TestUpdateDemandTableCovers` (non-skippable) parses this table and asserts every registered component category and contract type has a row.

If you find yourself wanting to defer the doc/skill update to "a follow-up PR," stop. The follow-up PR will not happen, and the next Claude Code session will read a stale CLAUDE.md and produce wrong code. Update in the same PR or do not merge.

# U32 documentation review

This is the committed record of the documentation work that comes before the v1.0.0 release. It covers three units: [U32](../units/U32-docs-audit.md) (docs-audit, "U32a", PR #459), [U32b](../units/U32b-docs-content.md) (docs-content, PR #460) and [U32c](../units/U32c-docs-validation.md) (docs-validation). It records how each unit checked the docs, what they found, and what happened to every finding. It ends with the maintainer sign-off (#167).

- **Scope:** every surface a reader or an agent reads. That is the mdBook site, the repo docs (`README.md`, `CONTRIBUTING.md`, `SECURITY.md`), `CLAUDE.md`, `.claude/reference/`, the skill pack, the example library, MCP tool / prompt / resource text, manifest descriptions, Purpose / Interpretation / glossary, error messages and fixups, CLI help and public GoDoc.
- **Not in scope:** statistical correctness of the formulas. That is covered by the statistics reviews ([U08](U08-statistics-review.md), [U12](U12-weighting-review.md), [U24](U24-matrix-operators-review.md), [U25](U25-multivariate-review.md), [U30](U30-flows-review.md)) and their sign-off (#206).
- **Status:** every finding is fixed, or is deferred with a reason and a TODO id. No row is open. The owner signs off below.

## Method

### U32a: the checks (docs-audit)

U32a built the machinery that the other two units used, and made each check a binding gate:

- the documentation inventory and coverage matrix (#159);
- CI doc checks: link checker, runnable snippets, CLI help ↔ `flags.md` parity, GoDoc `Example*` functions and the removed-name scan (#160);
- the hard `TestSkillTokenBudget` (#161);
- `make doc-checks`.

All of these gates are listed in `docs/src/contributing/testing.md` (Structural gates) and `docs/src/contributing/doc-checks.md`. U32a did no reading pass. Its findings are its build-time calls and deferrals, kept in its planning decisions log.

### U32b: the reading pass (docs-content)

U32b read every surface in full against the code. It ran 25 reading slices (stories E5 to E8, plus rows carried over from E1–E4). Each slice used scripted checks:

- every named test, path, identifier, code and CLI command resolves;
- the served text has no history or roadmap prose;
- terms match a term sheet.

On top of the scripts, each slice did a close read against the source, the manifest (`pulse --json`) and `--help`. Every example request was run against freshly built fixtures, and every synth spec through `pulse synth`. The slices logged 976 rows, each as `| surface | file | finding | disposition |`. The disposition was fixed, to-file or no-change.

### U32c: tested on the docs' consumers (docs-validation)

- **Agent evaluation.** `internal/eval` holds 20 tasks that cover every intent, a reference request per task, assertion-based answer checks and an intent-coverage gate. The reference requests run in CI. The hand procedure is [Agent Evaluation](../../src/contributing/agent-eval.md): one fresh Sonnet subagent per task, allowed only the pulse MCP tools, given a client-neutral prompt, with self-correction allowed. A task passes when its final request passes the checker (`go run ./internal/tools/evalscore internal/eval/runs/<run>`). Every failure was classified (doc surface, tool description, Purpose, code gap, harness, model slip) and fixed in the surface the agent should have read. The whole set was rerun until green.
- **Fresh reader (proxy).** A Sonnet subagent got only the built mdBook pages and the `pulse` binary, in a scratch directory with no repo. It worked Getting Started, `guide/questions/compare_groups` and `guide/questions/change_over_time` literally. Each ambiguity, wrong statement or stuck point was logged and fixed, and the reader ran again on the fixed build.
- **Carried cleanups and owner items:**
  - #775: `explain-and-skills.md` restructured.
  - #800: the "byte-identical" convention stated once.
  - #801: public GoDoc rewritten to the current contract.
  - The `CLAUDE.md` manifest wording.
  - #799: facet `params.field` refused `PULSE_OVERLAY_FIELD_UNKNOWN`.
  - #768: private vulnerability reporting.
  - The Parquet u4 marker test.

## Findings

### U32a (docs-audit)

| Finding / deferral | Disposition |
|---|---|
| A null crosstab axis grouper reached an unguarded path (`PROCESSING_INTERNAL`) | fixed in U32a (E1-S2): refused `PROCESSING_CONFIG` (`internal/crosstabaxis`) |
| Null entries in Request list slots were refused inconsistently; `labels: [null]` kept `SERVICE_VALIDATION` | fixed in U32a (E1-S4, E1-S5): one nil-entry gate, `PROCESSING_CONFIG {slot, index}`, `labels` aligned (owner call) |
| The template store bypassed afero (`os.MkdirTemp` in its GoDoc example) | fixed in U32a (E3-S4): the store reads `Options.FS` |
| The other directory loaders (label, lookup, range table, populations) still read through `os` | **#756**, U33 (open) |
| `sites` per-case-table attribution (the rest of #453) | #757, delivered in U32b |
| Tag-taxonomy table drifted from `CanonicalTags` | #758, delivered in U32b |
| `TestCliHelpMatchesFlagsDoc` checks names only across the whole tree | #759, delivered in U32b |
| Word-level capability prose under a hidden feature | #760, delivered in U32b |
| Removed-name scan skips MCP prompt bodies and `ExportReference` | #761, delivered in U32b |
| Example names the removed `ApplyOverlays` | #762, delivered in U32b |
| Twelve generated skill-page JSON blocks lack `cohort=` / `norun` | #763, delivered in U32b |
| `TestSkillSeeExampleTagsResolve` redundant | #764, delivered in U32b |
| Coverage matrix mdBook column near-vacuous for some row kinds | #765, delivered in U32b |
| Removed-name scan misses the `pio.<Name>` alias | #766, delivered in U32b |
| Removed-name content purge (the content half of #165) | moved to U32b and delivered there |
| Book search index past mdBook's 10 MB warning | no-change by owner decision (2026-11-25): the generated guide and coverage page are excluded from search, `heading-split-level = 2`, and the `docs` CI step fails on any mdBook warning |

The follow-ups #739–#755 were carried into `TODO.md` in the U32a PR as roadmap bookkeeping. They came from earlier units, not from the audit: #739–#749 belong to U33 and #750–#755 to the post-v1 backlog.

### U32b (docs-content)

Of the 976 rows, 737 are fixed, 150 are no-change (accurate as written, or out of slice and owned by another slice) and 43 are to-file. 21 rows mix dispositions: 16 fixed with a to-file part, 3 fixed / no-change, 2 no-change with a to-file part. The remaining 25 record a scripted check's result or a verification ("empty", "verified", "kept") rather than a disposition. So 61 rows carry a to-file part.

**Fixed rows, by surface.** Every fix landed in U32b's PR, in the commit of the slice that found it:

| Surface | Slices | Rows read | What the fixes were |
|---|---|---|---|
| Repo docs, term sheet | E5-S1 | 27 | README commands run and the Go snippet compiled, then corrected; the term sheet the later slices applied |
| mdBook: getting started, concepts, format, MCP, ops, examples; doc-check infrastructure | E1–E4, E5-S2 | 48 | Getting Started and the embedder pages rewritten (#164); `pulse --json` in place of `pulse manifest --json`; format pages (header, field widths, records, schema-block framing, dictionaries) matched to `encoding/`; the MCP tool table and CLI path rules corrected; consumer names removed |
| mdBook: CLI | E5-S3 | 27 | flags, usage strings and samples matched to `--help`; `DescribeFlag` renders true bool defaults |
| mdBook: library | E5-S4, E5-S5 | 73 | every API claim checked against the public-API golden and GoDoc; option defaults (`FailFast`, `CoalesceWindow`, `Suffix`) corrected; history prose cut |
| mdBook: contributing, internals, contract | E5-S6, E5-S7, E5-S7b | 106 | every named test and path resolves; recipes cross-checked against `make sites`; `extension-points.md` and `payload-schema.md` traced claim by claim |
| `.claude/reference/` | E6-S1 to E6-S5 | 116 | dead test names and paths removed; stale referents fixed; the `alternts` typo key corrected to `alternates`; no ceiling raised |
| Topical skills | E7-S1 to E7-S3 | 190 | codes, reasons, keys and commands checked against the code; Go names replaced by wire keys; history prose cut |
| Atomic skills (`op-*`, `type-*`, `tool-*`) | E7-S4, E7-S5 | 128 | each skill matched to its manifest entry and implementation; wire placement of params; refusal codes corrected |
| Example library | E8-S1, E8-S2 | 105 | every request run; descriptions matched to real output; degenerate examples moved to fixtures where they say something (factor score, share / lift, population facets) |
| MCP text, manifest descriptions, Purpose / Interpretation, glossary | E8-S3, E8-S3b | 97 | served strings read line by line; Go names, PRD and history prose removed from served text |
| Error messages and fixups | E8-S4 | 26 | Message and every Fixup checked against the emit sites; wire keys in refusal prose; `CLI_ERROR` and `PULSE_SHARD_AGGREGATE_DRIFT` registered (#771) |
| Terminology close-out | E8-S5 | 33 | term-sheet sweep across every surface; the "byte-identical" decision (finished by #800) |

**Every to-file row.** Some to-file rows were fixed later in the same effort, and this table records those as fixed. Each one was re-checked against the code at E6-S1 of U32c (2026-11-27).

| Slice | Finding | Disposition |
|---|---|---|
| E5-S1 | GitHub private vulnerability reporting disabled; `SECURITY.md`'s only channel fails | **#768**, done in U32c (E5-S2): enabled 2026-11-26; U33 checks `enabled: true` before every tag |
| E5-S2 | `PROCESSING_REGRESSION_REGULARIZED_SELECTION` emitted but unregistered | fixed in U32b (E5-G1); the sibling codes were registered under #771 (E8-S4) with `TestEmittedCodeLiteralsRegistered` |
| E5-S2 | `FieldType.ByteSize` GoDoc says bit-packed types share bytes | fixed in U32b (GoDoc states one byte each) |
| E5-S2 | `--bind-on-open` usage omits `pulse_import` | fixed in U32b |
| E5-S3 | `cohort inspect` text mode omits the default zone field | **#769**, U33 |
| E5-S3 | No cohort completion for `cohort zone set` / `clear` | **#770**, U33 |
| E5-S3 | `DescribeFlag` drops a true bool default | fixed in U32b |
| E5-S5 | Stale `Options` GoDoc (`FeatureProfile`, `DefaultReturn`, `DisableComponents`) | fixed in U32b |
| E5-S5 | `ComposeOptions.FailFast` GoDoc claims default `true` | GoDoc fixed in U32b; the default itself is **#772**, U33 (owner decision) |
| E5-S5 | `WatchOptions` GoDoc; no way to watch every file | GoDoc fixed in U32b; watching every file is **#773**, U33 |
| E4-S1 | `make sites` has no I/O-path mode | **#767**, U33 |
| E5-S6 | `make sites` has no field-type, error-code or MCP-tool target | **#774**, U33 |
| E5-S6 | `DescImport` history prose ("SPSS is now writable too") | fixed in U32b (E8-S3) |
| E5-S7 | `pulse shard` text errors print a non-catalogue label; `DATA_FILE` vs `SERVICE_RESOURCE` | fixed in U32b (E8-S4, #771): labels became `CLI_ERROR`; an unopenable cohort is `SERVICE_RESOURCE` on every surface |
| E5-S7 | Stale comments in `schema.go` / `params_allowlist.go` | fixed in U32b |
| E5-S7 | `pulse mcp` help history prose | fixed in U32b |
| E5-S7b | `standard` return preset's regression key list is stale | **#777**, U33 |
| E5-S7b | Extension filterer `FieldInputs` receives no params | **#778**, U33 |
| E6-S1 | No test drives `--no-components` | fixed in U32b (`internal/cli/api_no_components_test.go`) |
| E6-S1 | `make sites slot response` misses `ComposedResponse` fields | fixed in U32b (`internal/tools/sites` resolves them) |
| E6-S3 | No Parquet `pulse:type=u4` marker test | fixed (`TestParquet_U4PinRoundTrips`); U32c (E5-S2) confirmed it, falsified it and added no duplicate |
| E6-S4 | `explain-and-skills.md` hard to scan | **#775**, done in U32c (E4-S1) |
| E6-S5 | `PULSE_TEMPLATES_DIR` loader does not skip sidecars | **#776**, U33 |
| E6-S5 | `tool-skills-get` names the wrong atomic-skill stem form | fixed in U32b |
| E7-S1, E7-S4 | `AGG_RATIO` requires and type-checks an unread `field`; `FieldFilter: "any"` overstates | **#779**, U33 |
| E7-S1 | Formula arity fault: predict and runtime codes differ | **#780**, U33 |
| E7-S1 | Manifest `join.limitations` carries "v1:" and follow-up prose | fixed in U32b |
| E7-S2 | `REG_BAYES_LINEAR` margins refusal omits `REG_ORDINAL` | fixed in U32b (E8-S4) |
| E7-S2, E7-S3 | Served version prose ("pre-alpha.35", "pre-Components", "numeric only in v1") | fixed in U32b |
| E7-S4, E7-S5 | Predict passes requests the run refuses (`AGG_DISTINCT_SUM`, `AGG_CI_*` bootstrap, `ATTR_SET_HAS`, `FILTER_NULL`, `FILTER_TRUE` / `FALSE`, `FILTER_RANGE`, `FILTER_SET_*`, `*_vs_baseline` position); 7 rows | skills fixed in U32b; the predict gap is **#781**, U33 |
| E7-S4 | `WIN_FORMULA` reads a Welford cell only with Go casing | **#782**, U33 |
| E7-S5 | Overlay descriptions Go-named, with history prose | fixed in U32b (E8-S3, E8-S5); `ref_kinds` is **#794** |
| E7-S5 | Overlay, test and regression refusal text uses Go names | fixed in U32b (E8-S4); the facet code is **#799**, done in U32c (E5-S1) |
| E7-S5 | Manifest `tests[].requires` lists Go names | **#783**, U33 (wire shape, owner decision) |
| E7-S5 | `FEAT_*` `emits_type` disagrees with the output | **#784**, U33 |
| E7-S5 | `pulse_facet` renders non-categorical values as floats | **#785**, U33 (the skill documents current behaviour) |
| E7-S5 | `pulse_inspect` flattens a coded error to text | fixed in U32b (`envelopeEntryError`) |
| E7-S5 | Inferred `set_*` rungs drop rows instead of promoting | **#786**, U33 |
| E7-S5 | Lookup tools' not-found answers are uncoded | fixed in U32b (`errNotFound`, `SERVICE_VALIDATION`) |
| E7-S5 | `PredictResult.DefaultsApplied` GoDoc misdescribes the echo | fixed in U32b |
| E8-S1 | `attributes/14_factor_score` battery does not converge | fixed in U32b (moved to `battery.pulse`) |
| E8-S1 | `REG_MMM_BAYES` diagnostics in a named fit name `""` and miscount | **#787**, U33 |
| E8-S1 | `TestExamples_RunEndToEnd` accepts degenerate results | fixed in U32b (the `_meta.degenerate_result` gate in `internal/integration/examples_test.go`) |
| E8-S1 | Date-part hour / weekday keys sort as strings | **#788**, U33 |
| E8-S2 | Facet population cohort ignores `data_dir` | **#789**, U33 (examples fixed) |
| E8-S2 | `WIN_SHARE` / `WIN_LIFT` / `WIN_FORMULA` read a decimal sum as null | **#790**, U33 (examples moved) |
| E8-S2 | `OVERLAY_CHISQ_VS_REF` between two samples overstates | **#791**, U33 (example and skill fixed) |
| E8-S2 | No CLI route embeds overlays in an export | **#792**, U33 |
| E8-G1 | A crosstab `AGG_COUNT` cell with no `field` returns zeros silently | **#793**, U33 |
| E8-S3 | Manifest overlay `ref_kinds` are Go names | **#794**, U33 |
| E8-S3 | Predict `defaults_applied[].path` uses Go names | **#795**, U33 |
| E8-S4 | `SERVICE_REGISTRY` / `CLI_COMMAND` registered, never emitted | **#796**, U33 |
| E8-S4 | `Fixup.Path` spellings mixed | **#797**, U33 |
| E8-S4 | SPSS cardinality threshold internal-only | **#798**, U33 (the hint no longer offers it) |
| E8-S4 | Facet unknown `params.field` code differs from every other host | **#799**, done in U32c (E5-S1) |
| E8-S5 | "byte-identical" leaves its referent implicit | **#800**, done in U32c (E4-S2) |
| E8-S5 | Public GoDoc still says "today" / "future work" | **#801**, done in U32c (E4-S3) |

The U32b log also carried two out-of-slice follow-ups:

- the `*_vs_margin` count-cell wording and a `chisq-vs-ref` two-sample caution;
- `ComposedRequest.*` Go names in the `compose-*` skill descriptions.

Both are fixed in the current skills.

### U32c (docs-validation)

There are 64 rows: 54 fixed, 8 no-change and 2 filed (#802, #803). The table records the owner-action row (#768) as fixed.

**Eval infrastructure and harness**

| # | Surface | Finding | Disposition |
|---|---|---|---|
| C-01 | MCP surface | No MCP tool runs synth, so an MCP-only agent can author a spec but not run it; the eval scores it through a `synth` pseudo-tool | **filed #802** (U33, owner decision). Reason: whether a synth tool joins the MCP surface is a contract call; the skills already say synth is CLI-only |
| C-02 | `internal/eval/check.go` | Exists-semantics assertions can pass by coincidence on a loose scope | no-change: tasks pin `key` / `metric` / `in_row`; documented in the package |
| C-03 | `internal/eval/eval_test.go` | Answer-key leak guard admits `internal/tools/` (evalscore) | no-change: the one intended importer |
| C-04 | brief | `buildExampleFixtures` is test-only, so the eval has its own builder (`eval.NewEnv`) on the same CSVs and schemas | no-change |
| C-05 | agent-eval procedure | The import task needs `intake.csv` copied into the data dir | fixed |

**Carried cleanups and owner items (E4, E5)**

| # | Surface | Finding | Disposition |
|---|---|---|---|
| C-06 | `guided-analysis/explain-and-skills.md` | #775: split into three slices with headed bullets; a word-multiset diff shows no rule lost | fixed (`6b6c95109`) |
| C-07 | `update-demand.md`, `docs/src/library/overview.md` | #800: the convention stated once per audience | fixed (`59f03b6d9`) |
| C-08 | execution-modes and byte-layout slices | #800: 11 ambiguous referents named; the rest left clear | fixed (`59f03b6d9`) |
| C-09 | `docs/src/library/compose-sweeps.md` | #800: the non-adaptive sweep comparison named | fixed |
| C-10 | `CLAUDE.md` | `pulse manifest --json` is not a command | fixed: `pulse --json` at both mentions |
| C-11 | public GoDoc | #801: about 100 comments rewritten to the current contract; the scripted grep returns 0 hits | fixed (`2d7aea701`) |
| C-12 | `types/overlay.go` etc. | Remaining "Forward-compat:" labels describe current omitempty contracts | no-change |
| C-13 | facet overlay | #799: unknown `params.field` is `PULSE_OVERLAY_FIELD_UNKNOWN` at predict and runtime | fixed (`593f6a6c1`) |
| C-14 | Parquet tests | `TestParquet_U4PinRoundTrips` already existed; falsified; no duplicate | no-change |
| C-15 | U33 release gates | No repo release checklist; the `enabled: true` check went into U33 "Gates & tests" | fixed (`93e735300`) |
| C-16 | GitHub repo settings | Private vulnerability reporting disabled (#768) | fixed: the owner enabled it on 2026-11-26 (`enabled: true`); #768 ticked (`74e893814`) |

**Agent eval run r1 and its fixes (E2-S1, E2-S2)**

| # | Surface | Finding | Disposition |
|---|---|---|---|
| C-17 | MCP tool surface | Code gap, 3 failures plus friction in about 15 tasks: no tool lists the cohorts in the data dir | fixed: `Pulse.ListCohorts`, `pulse_cohorts_list`, `pulse cohorts` (owner choice; `e2e996090`) |
| C-18 | eval procedure, evalscore | Harness, 1 failure: a parallel import task contaminated the shared data dir | fixed: the import task runs last and alone, then the data dir is reset |
| C-19 | `DescManifest` and the intents skill | Tool description, 1 failure: the agent scoped to `describe` and missed `distribution_shape` | fixed: the descriptions point at the intents skill and `details.valid` |
| C-20 | served examples | Friction in 9 tasks: the served `data_dir: ".data"` does not resolve on a server | fixed: served bodies drop it |
| C-21 | `op-filter-range` Params | `values` listed as if under `params` | fixed: every op skill says where each key goes on the wire (`793d0f685`) |
| C-22 | slim MCP manifest | Unscoped payloads overflow clients; no param prose in the slim manifest | fixed: scoping and skill-fetch guidance in the descriptions |
| C-23 | `synthetic-data` skill | Not saying up front that synth is CLI-only; no CLI flags | fixed (the MCP-tool question is C-01) |
| C-24 | `pulse_import` result | `"format":"pulse"` for a CSV source | fixed: additive `source_format` |
| C-25 | examples search | A plain-word query found nothing | fixed by a description edit (the missing synonym tier is C-33) |
| C-26 | `DescProcess` (r2) | Tool description, 1 failure: shares worked out by hand | fixed: the share sentence (`222625dff`) |
| C-27 | `ListCohorts` | Delegated design calls (recursive sorted walk, `managed` flag, coded per-file errors, `Dir` validation, core-surface binding) | fixed (`e2e996090`) |
| C-28 | `internal/examples/served.go` | Served bodies drop `data_dir`; stored files unchanged | fixed |
| C-29 | `pulse_import` | Additive `source_format` | fixed |
| C-30 | describe intent | Left as is: its text claims spread, not shape | no-change |
| C-31 | eval r3 answer key | Counts by region are a correct answer to the composition task (owner call, 2026-11-27) | fixed: the assertions accept the south / west counts on `data` rows |
| C-32 | harness (r4) | Agents started with about 584K tokens of context; one was killed; the owner ruled r3 the green run | no-change: r4 kept as a record |
| C-33 | `internal/examples/search.go` | Search has no plain-word synonym tier | **filed #803** (U33). Reason: a new match tier with its own tests, outside the eval's fix set; the failing query already matches |
| C-34 | unscoped `pulse_skills_list` | Still overflows client limits; agents retry with a scope | no-change: the description warns |

**Proxy reader run 1 and its fixes (E3-S1, E3-S2)**

| # | Page · section | Finding | Disposition |
|---|---|---|---|
| C-35 | installation · Verify | No sample `pulse --version` output | fixed |
| C-36 | installation · data directory | Does not say why the next pages run from the data dir | fixed |
| C-37 | first-cohort · 2 | "edit sales.schema.json" with no example edit | fixed |
| C-38 | first-cohort · 2–3 | No gloss of cohort, dictionary, low-cardinality, type names | fixed |
| C-39 | first-analysis · 1 | Unexplained `cohort=` fence tag | fixed |
| C-40 | first-analysis · 3 | **wrong:** says every envelope has `request` | fixed |
| C-41 | first-analysis · 3 | `data.components` and raw floats unexplained | fixed |
| C-42 | first-analysis · 2 | Warning advice not linked to the schema edit | fixed |
| C-43 | first-analysis · 4 | CSV number formatting unexplained | fixed |
| C-44 | cli-tour | `pulse cohorts`, explain and recommend missing; stage names unlinked | fixed |
| C-45 | first-mcp-session · 1 | No expected tool count; `sleep 2` unexplained | fixed |
| C-46 | first-mcp-session · 2–3 | **stuck:** needs an MCP client; no shell-only `tools/call` | fixed |
| C-47 | compare_groups · worked example | **stuck:** no page says how to get `experiment.pulse` | fixed: `pulse examples data` (`9081e673e`) and a "To run the example yourself" step |
| C-48 | compare_groups · pick | Jargon; first operator not named; effect size before its bands | fixed |
| C-49 | change_over_time · worked example | **stuck:** same as C-47 | fixed (as C-47) |
| C-50 | change_over_time · worked example | Before / after paired t with no date field; "a quarter" not derivable | fixed |
| C-51 | change_over_time · command line | `explain --response FILE` steps unclear | fixed |
| C-52 | `pulse examples data` | Leaf and library shape (`WriteExampleData`, `--out`, `--overwrite`) | fixed (`9081e673e`) |
| C-53 | `example_data.go` overwrite | Refuse-before-write (`SERVICE_VALIDATION`, `target_exists`); `--overwrite` removes only fixture files | fixed |
| C-54 | `example_data.go` scope | Writes what `build.sh` writes, held to it by `TestExamplesData_MatchesBuildScript` | fixed |
| C-55 | `fixturedata.go` embed | The cohort list is the embedded schemas, held to `build.sh` by `TestFixtureCohorts_MatchBuildScript` | fixed |
| C-56 | `features.go` binding | Bound to the core `examples` surface; no new feature row | fixed |
| C-57 | `docs/src/examples/library.md` | CLI resolves cohorts from the working directory: `cd DIR` recipe | fixed (`90be63914`) |

**Proxy reader run 2 (E3-S2)**

| # | Page · section | Finding | Disposition |
|---|---|---|---|
| C-58 | guide/questions · result JSON | Cosmetic: exponent form differs from the reader's `jq` reprint | fixed: a note under every result block with an exponent (`5349ed368`) |
| C-59 | guide/questions · "To run the example yourself" | Not saying to run `examples data` outside `PULSE_DATA_DIR` | fixed (`5349ed368`) |

**Spec check (E6-S1)**

| # | Surface | Finding | Disposition |
|---|---|---|---|
| C-60 | `internal/eval/check.go` | E1-S1 falsification not recorded | fixed: reverting one `inRange` bound fails `TestEvalReferences` |
| C-61 | `internal/eval/check.go` | E1-S2 per-kind falsification not recorded | fixed: one line reverted per assertion kind, each fails `TestCheck_*` or `TestEvalReferences` |
| C-62 | `internal/descriptor/overlay_facet.go` | E5-S1 predict-side falsification not recorded | fixed: reverting the code fails the predict tests and the root parity test |
| C-63 | `internal/service/facet_overlay.go` | E5-S1 runtime-side falsification not recorded | fixed: reverting the code fails `TestFacetOverlay_ParamsFieldRefusalsParity` |
| C-64 | eval answer key | Loose assertions: composition's bare share values, flows' unscoped cell, segment tasks checking only the row count | fixed: shares scoped to `crosstab`, flows to the crosstab cells or a bronze→silver row, band averages and cluster sizes checked; `TestEvalCompositionAnswers`. `simulate_test_scores` stays a schema check: no assertion kind reads a written cohort's values |

## Eval and reader results

### Agent evaluation

The runs are committed under `internal/eval/runs/`. Each holds the final request and a short transcript summary for every task.

| Run | Score | Failures | Classification and fix |
|---|---|---|---|
| `2026-11-26-r1` | 15/20 | `benchmark_income_target`, `composition_region_share`, `prepare_income_zscore`, `quality_missing_income`, `distribution_income_skew` | code gap (cohort listing, C-17), harness contamination (C-18), tool description (C-19) |
| `2026-11-27-r2` | 19/20 as run; 20/20 rescored | `composition_region_share` (as run) | tool description (C-26); passes under the widened key (C-31) |
| `2026-11-27-r3` | 19/20 as run; **20/20** rescored | `composition_region_share` (as run) | answer key widened (C-31); **the green run** |
| `2026-11-27-r4` | 17/20 | broken harness: oversized starting context, one agent killed, import task not run | not a measurement; kept as a record (C-32) |

- **r3 is the green run.** It scored 19/20 as run: `composition_region_share` answered with counts by region. It scored 20/20 after the owner widened the composition key (C-31). Under that key r2 also rescores 20/20, but r3 is the green run by owner ruling: it is the first full run after the last served-text change (`222625dff`), and r2 predates it. Both still score 20/20 under the spec check's tighter keys (C-64).
- **The r3 `simulate_test_scores` submission was assembled by the orchestrator.** The agent named the synth operators and params but ran no synth, since no MCP tool runs one (#802). The orchestrator built the spec from what the agent named and chose the seed (42). The procedure now forbids this: a task with no MCP tool is scored only on a spec the agent wrote itself ([Agent Evaluation](../../src/contributing/agent-eval.md)).

The CI reference test and the intent-coverage gate run the 20 reference requests on every build. U33 reruns the evaluation on the release build before the rc and `v1.0.0` tags (U33, Gates & tests).

### Fresh reader (proxy)

- **Run 1:** 17 rows, 3 of them stuck points. Two pages could not obtain `experiment.pulse`, and the MCP session page needed a client. All 17 were fixed. The new `pulse examples data` leaf writes every fixture cohort, and the pages were corrected (C-35 to C-57).
- **Run 2:** completed Getting Started, Compare groups and Change over time with no stuck point. Its 2 minor rows were fixed (C-58, C-59).

## Open items handed on

These items are filed under [U33](../units/U33-v1-release.md):

- #756, #767, #769, #770, #772–#774, #776–#798: from U32a and U32b;
- #802 and #803: from U32c.

The items that need an owner decision are #772, #783, #788 and #802.

## Maintainer sign-off (#167)

**Proxy-reader caveat.** The fresh reader was an AI subagent (Sonnet), not a human. It stood in for a human newcomer developer without a statistics background. It got only the built mdBook pages and the `pulse` binary, and it worked the pages literally. A model reader resolves some ambiguities a person would trip on, and trips on some a person would not. Treat "no stuck point" as evidence that the pages are complete and runnable, not as proof that they are easy for a human. The agent evaluation has the same limit: one model family, one run per task. The interview planned an owner spot-check of the reader log, and the owner records it on the sign-off line.

By signing, the maintainer confirms that:

- every finding in this record is fixed, or deferred with a reason and a TODO id;
- the deferrals filed under U33 are accepted as v1.0.0 work;
- the proxy-reader caveat above is accepted.

Signed off on 2026-11-27 by Frank Bardon Jr. (maintainer). Reader-log spot-check: approved with this review, 2026-11-27.

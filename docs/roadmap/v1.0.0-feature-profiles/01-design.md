# 01 — Feature Profiles: design & phasing

## Core rules

1. **No profile = the default feature set = everything**, including features added in future releases. Byte-identical to today.
2. **A profile is a complete, closed allowlist.** It declares every feature the instance offers. It never subtracts from "everything", and it never grows when Pulse is upgraded.
3. **Hidden is invisible.** Every *instance* surface is produced as if hidden features were never registered. There is no disabled error, disabled list, banner or hint. The one deliberate exception is the unprofiled admin CLI (decision 6), which is not an MCP or embedder surface. **Nothing about a hidden feature may reach MCP context by any path** (decision 7).
4. **Instance-wide.** One profile per `*Pulse`, fixed at `pulse.New`. There is no request-, context- or tenant-level variation.
5. **Availability, not behaviour.** A profile decides which features exist. Per-request output choices remain per-request, e.g. the guided-analysis `Request.Explain` opt-in. If the instance's profile doesn't include `explain`, that request field doesn't exist for the instance either: it is absent from its payload schema and refused like any unknown field.
6. **Profiles never weaken the build.** Coverage gates, goldens and the Update Demand still apply to the full registry.

---

## P1. What a feature is

Every feature has a **stable name**, a **kind** and a **`Since` version**. All three are declared once in the registries that already exist.

| Kind | Name examples | Owns |
|---|---|---|
| `capability` | `process`, `compose`, `process_chain`, `facet`, `stream`, `watch`, `joins`, `crosstab`, `lookup`, `templates`, `synth`, `import`, `export`, `shard_write`, `index_build`, `dedup`, `widen`, `recommend`, `explain` | facade methods, CLI leaves, MCP tools, request slots |
| `operator` | `AGG_SUM`, `TEST_ANOVA_F`, `OVERLAY_CORRESPONDENCE`, `MAT_PCA`, `REG_GLM`, extension names | registry entries, enum values, atomic skills, examples, fenced prose |
| `io_format` | `csv`, `parquet`, `spss`, … | import/export adapters, CLI subcommands |
| `mcp_extra` | `cohort_resources` (the `pulse://` enumeration), each MCP prompt | MCP-only surfaces with no facade method |

**Always-present core.** Self-description is not optional, because an instance must be able to describe itself. So these are not listable features and are present in every profile: the manifest, payload schema, skills list/get, examples search/get, errors lookup, inspect and predict. They all *render the instance's view*, so they reveal nothing hidden.

**Not features.** Field types (a cohort's schema is data, and refusing a type would make valid files unreadable), and behaviour switches (`DisableDefaults`, `DisableComponents`, `DisableProjection`, `DisableCohortScan`). The switches keep their own defaults and may also be set in the profile file for convenience (P3).

**`Since` metadata.** Each registration carries the Pulse release that introduced it (`Since: "1.0.0"`). For everything that exists at the profiles release, `Since` is `"1.0.0"`. A gate (`TestFeaturesHaveSince`) requires the field, and the Update Demand gains the row "a new feature → its `Since` version".

---

## P2. Profile shape

```jsonc
{
  "profile": "survey-self-serve",          // embedder's own label; never shown to users
  "written_with": "1.0.0",                 // informational: the Pulse version `profile init` ran on
  "features": [                            // exact names only — what is listed is exactly what exists
    "capability:process", "capability:crosstab", "capability:facet",
    "AGG_AVERAGE", "AGG_COUNT", "AGG_FREQUENCY", "AGG_SUM",
    "GROUP_CATEGORY", "GROUP_DATE", "FILTER_INCLUDE", "FILTER_RANGE",
    "TEST_CHISQ", "TEST_T", "TEST_WELCH",
    "OVERLAY_SHARE_OF_ROW", "OVERLAY_INDEX_VS_MARGIN", "OVERLAY_STD_RESIDUAL",
    "io_format:csv", "io_format:spss"
  ],
  "behaviour": { "disable_projection": false }   // optional; omitted = engine defaults
}
```

**Exact names only (recommended).** A profile lists every feature by its exact name. Patterns such as `TEST_*` are not accepted in the file. A pattern would quietly match tests added in later releases, and the alternative fix (pinning patterns to a baseline version) proved hard to reason about. The list is longer, but it means exactly what it says. Embedders never write it by hand: `pulse profile init` (P5) writes the full current list, and the embedder deletes lines. `written_with` is informational only and feeds `pulse profile diff`.

*Alternative considered (not recommended): pinned patterns.* Patterns and categories (`AGG_*`, `capability:*`) are allowed, but expand only over features whose `Since ≤ baseline`. A profile written against 1.0.0 that says `TEST_*` keeps exactly the 1.0.0 tests forever, and a test added in 1.2.0 stays invisible. To adopt new features, the embedder either raises `baseline` (and gets new matches deliberately) or names them explicitly. This satisfies decision 2 while keeping profiles readable.

**Validation at `pulse.New`.** These are embedder-facing config errors, never visible to the instance's users:

| Problem | Result |
|---|---|
| Unknown feature name, or a name with `Since > ` the running Pulse version | `PULSE_PROFILE_FEATURE_UNKNOWN`. This is a typo guard, and catches a profile written for a newer Pulse |
| A pattern instead of an exact name | `PULSE_PROFILE_FEATURE_UNKNOWN` (patterns are not feature names) |
| An enabled feature whose dependency is missing | `PULSE_PROFILE_DEPENDENCY`, naming both. The dependency graph is declared in `descriptor/dependencies.go`, and `TestProfileDependenciesComplete` checks it against the registry |
| An operator enabled without the capability that hosts it (e.g. an overlay kind without `crosstab`, `facet`, `compose` or windowed `process`) | `PULSE_PROFILE_DEPENDENCY` |

**Removed features.** If a later Pulse release removes or renames a feature, a profile naming it fails with `PULSE_PROFILE_FEATURE_UNKNOWN` until it is edited. This is explicit, and matches the existing rule that a typo'd table must not silently become a table that isn't there.

---

## P3. Configuration sources

| Source | Form | Notes |
|---|---|---|
| `pulse.Options.Profile` | Go struct | highest precedence (the existing "Options always overrides" rule) |
| `pulse.Options.ProfileFile` | path to the JSON above | read through the instance's `afero.Fs` |
| `PULSE_PROFILE` | path to a profile file | lowest precedence; read by `pulse.New` and therefore by `pulse mcp`; triggers the Update Demand env-var row |
| `pulse mcp --profile <file>` | flag on the MCP leaf only | the CLI itself is **not profiled** (decision 6): every other leaf always shows and runs the full surface |

Pulse ships **no built-in presets** as live profiles. Under decision 2, a preset that Pulse updated in a later release would grow an embedder's feature set without their consent. Instead, Pulse ships **example profile files** (in `examples/profiles/`). Each is an exact-name list as of the release it shipped in, and Pulse never edits a published example in place. Embedders copy one and own it from then on.

---

## P4. How each surface behaves for an instance with a profile

The rule for every row is that **output equals what an imaginary Pulse build containing only the profile's features would produce.**

- **Manifest.**
  - Built from an `InstanceSnapshot`, which merges `ExtensionsSnapshot` and the resolved feature set.
  - Components, tests, overlays, regressions, synth distributions, commands, MCP tools, capability blocks, intents, error codes and skills list only enabled features.
  - It has no profile section that lists or counts hidden things.
  - **Caching.** Response caches need to tell instances apart, so the manifest carries a `feature_set_digest` *on every instance, including the default*. Because it is always present, it reveals nothing about whether a profile exists.
- **Request handling.**
  - Names resolve against the instance's feature set only, so a hidden name follows the path of an unknown name.
  - One resolution step, at the request-validation choke point used by every entry point (Process, Compose, ProcessChain, Facet, ProcessStream, Watch, FilterToFile, template render, Recommend drafts), returns the existing unknown-type error.
  - Hidden request slots (e.g. `crosstab` when the capability is hidden) are refused exactly as an unknown JSON field is today under strict decode.
- **Payload schema.** `p.PayloadSchema()` is instance-scoped, and `pulse schema` and `pulse://schema` serve it. The schema `$id` follows `format_version`, and `feature_set_digest` is always present in `$comment`.
- **Skills and examples:** discovery shrinks, direct reads stay (P4a).

- **Errors.** Codes owned only by hidden features are absent from the errors list and `pulse errors lookup`. Codes shared with enabled features remain.
- **MCP.**
  - Only enabled tools, prompts and resources are registered.
  - Tool input schemas carry the instance's enums.
  - The `pulse-bootstrap` prompt text is rendered from the instance's view, and the prompt does not mention profiles.
- **CLI.** Not profiled (decision 6). The CLI is an admin tool and always shows the full surface. The one exception is `pulse mcp --profile`, which configures the MCP server's instance.
- **Smart defaults.** A default whose target operator is hidden doesn't apply. The request behaves exactly as it would for a field type with no default rule.
- **Guided analysis.**
  - Recommend, Explain, `NotFor` alternatives, follow-ups, intents (an intent with no enabled operator disappears), question guides, decision trees and the glossary are all rendered from the instance's view.
  - When every alternative is hidden, the alternatives line is simply absent.
  - Glossary terms used only by hidden features are absent.
- **Reference export.** `pulse docs export` / `p.ExportReference(fs, dir)` renders the catalog, guides, glossary and decision trees for this instance. This is how "documents react to the configuration" for an embedder's own published docs. The public Pulse site documents the default (full) set.

---

## P4a. Skills: a shrinking ontology with progressive disclosure

Decision 7 sets the rule: **the ontology shrinks to the instance's features, and every path that can put text into an agent's context honours it** — listing, search, cross-links, recommendations *and* fetch by exact name. Topical skills remain available, always served rendered for the instance. A hidden feature's own skill and examples do not exist for the instance.

### The ontology
The **ontology** is the graph that discovery walks:

```
intent (compare_groups)  →  operators (TEST_T, TEST_WELCH, …)  →  atomic skills (op-test-t, …)
                                    ↘ examples (tagged)   ↘ glossary terms   ↘ NotFor / follow-up edges
topical skills  →  intents / operators they route to
```

Most of it already exists as metadata: skill frontmatter (`operator:`, `category:`), example `_meta.operators`, and, from the guided-analysis theme, `Purpose.Intents`, `NotFor`, follow-ups and glossary links. A profile **prunes the graph once at `pulse.New`**: every node owned by a hidden feature is removed, along with every edge into it.

### Which surfaces walk the pruned graph
| Surface | Behaviour |
|---|---|
| `pulse_skills_list`, `pulse-skill://` resource list | enabled nodes only |
| `pulse_examples_search` | enabled nodes only |
| Manifest `skills`, `intents`, operator lists | enabled nodes only |
| `## See` lines and cross-links in served skills | edges to pruned nodes removed at render |
| Recommend, Explain, `NotFor`, follow-ups, glossary | pruned graph only |
| `pulse_skills_get` / `pulse_examples_get` / `pulse-skill://<name>` **by exact name** | a hidden feature's atomic skill or example returns the same "not found" as a name that never existed. A topical skill is returned **rendered for the instance** (hidden spans stripped), or "not found" if nothing enabled remains in it |

So there is no path, guessed name or otherwise, by which a hidden feature's documentation enters an agent's context, which would only confuse the agent about what it can run. The library's embedded skill FS stays complete, because the full pack is a build artifact; filtering happens in the instance-scoped `List` / `Get` that every MCP and facade path calls. A gate (`TestSkillsCoverProfileGet`) asserts that for each example profile, fetching every hidden skill and example by exact name is byte-identical to fetching a never-existing name, and that no served topical skill contains a hidden name.

### Writing topical skills so they don't over-expose: progressive disclosure
Stripping text from skills at render time works, but it is brittle when it is the *only* defence. The proposed fix is to write topical skills in layers, so most of them need no stripping at all:

1. **Topical skills teach the decision, not the catalogue.** They describe concepts, trade-offs and criteria, e.g. "use a rank-based test when the data is skewed or ordinal". They do **not** enumerate every operator family. Where they would have listed operators, they route through the ontology: "the instance's options for this are listed under intent `compare_groups` (`pulse_skills_list {intent}` / `pulse_recommend`)".
2. **Operator-to-operator comparisons move into atomic metadata.** Comparisons such as "use `TEST_WELCH` instead of `TEST_T` when spreads differ" belong in each operator's `Purpose.NotFor` (guided-analysis 01). That metadata is already pruned per instance, so the comparison disappears automatically when either side is hidden.
3. **Fences only as a backstop.** Any operator name left in a topical skill must sit inside a `<!-- feature: NAME -->…<!-- /feature -->` fence and is stripped at render when hidden. `TestSkillsCoverFeatureFences` fails on an unfenced mention. Layers 1–2 keep the number of fences small.
4. **Generated guides follow the same rule.** Question guides, decision trees and the reference export (guided-analysis 02) are rendered from the pruned graph, so they need no fences.

This is also simply better writing for the default (full) instance. Topical skills shrink toward the 6000-char design budget, stop duplicating the manifest, and stop drifting when operators are added. It aligns with the guided-analysis context-budget principle (pull, don't push).

### Cost
There is a one-time rewrite of about 25 topical skills, done in FP5. During the rewrite the fence gate runs in report-only mode, and it flips to failing when the pass completes. Rewrites are best paired with the guided-analysis G2 back-fill, since both move comparison prose into `Purpose` metadata.

---

## P5. Tooling for embedders

| Command | Purpose |
|---|---|
| `pulse profile init [--from <example>]` | writes a profile listing every feature in the running Pulse by exact name, grouped and commented by category; the embedder deletes the lines they don't want |
| `pulse profile check <file>` | runs the `pulse.New` validation offline: unknown names, dependency gaps |
| `pulse profile diff <file>` | lists features in the running Pulse that the profile does not include, highlighting those with `Since` newer than `written_with`. This is the upgrade review step. It reports **to the embedder** only, never through an instance's runtime surfaces |
| `pulse profile show <file>` | the profile's features with each one's category, `Since` and dependencies |

These operate on profile *files* and run outside any profiled instance. They are the only place where "features you don't have" are listed, which keeps decision 3 intact.

---

## P6. Gates

| Gate | Asserts |
|---|---|
| `TestProfileDefaultIsFull` | no profile → manifest, payload schema, skills, examples, errors and CLI tree byte-identical to today's goldens |
| `TestProfileInvisibilityParity` | for each example profile and each hidden feature, every instance surface (manifest, schema, predict, process, MCP tools, prompts, skill and example discovery, errors lookup) is byte-identical to a build where that feature's registration was removed. In practice this is approximated by comparing responses for the hidden name and a never-registered name, plus "the name occurs nowhere in any rendered surface" |
| `TestSkillsCoverFeatureFences` | every operator name in a topical skill or generated guide sits inside a matching `feature` fence |
| `TestSkillsCoverProfileGet` | per example profile: list, search **and exact-name get** of every hidden skill or example match a never-existing name byte-for-byte; no served topical skill contains a hidden name |
| `TestProfileDependenciesComplete` | every operator that reads another operator's components or results, or needs a host capability, declares it |
| `TestFeaturesHaveSince` | every registered feature declares `Since` |
| `TestProfileRejectsPatterns` | a profile entry that is not an exact registered feature name is refused |
| Profile goldens | manifest and schema goldens for each example profile |

The `TestSkillsCover*` prefix puts `TestSkillsCoverFeatureFences` on CLAUDE.md's self-expanding gate list.

---

## Phasing

The profile mechanism lands **first**. Every surface the vector-matrix and guided-analysis themes add then renders from the instance's feature set from its first commit.

| Epic | Stories |
|---|---|
| **FP1 — Feature registry** | feature names and kinds across all registries; `Since` on every registration; dependency graph + gate; always-present core definition |
| **FP2 — Profile model** | profile file format (exact names), `pulse.New` validation and config codes, `Options.Profile` / `ProfileFile` / `PULSE_PROFILE` / `--profile` |
| **FP3 — Instance snapshot & request path** | `InstanceSnapshot`; name resolution at the validation choke point; strict refusal of hidden request slots; `feature_set_digest` |
| **FP4 — Self-description** | instance-scoped manifest, payload schema, predict, errors list; profile goldens; `TestProfileDefaultIsFull` |
| **FP5 — Skills & ontology** | ontology graph and pruning; discovery surfaces on the pruned graph; progressive-disclosure rewrite of topical skills (paired with guided-analysis G2); fence syntax; `TestSkillsCoverFeatureFences` (report-only until the rewrite completes) |
| **FP6 — MCP** | instance-scoped tool, prompt and resource registration; filtered tool schemas; `pulse mcp --profile`; `TestProfileInvisibilityParity` |
| **FP7 — Embedder tooling & export** | `pulse profile init / check / diff / show`; example profile files; `pulse docs export` (shares the guided-analysis generator) |

## Update Demand impact

- `Options.Profile` / `ProfileFile` and the new facade additions (`PayloadSchema()`, `ExportReference`) touch the CLAUDE.md design-principles facade list.
- `PULSE_PROFILE` → CLAUDE.md "Build / Env" + `skills/session-bootstrap.md`.
- CLI leaves `profile {init,check,diff,show}` and `docs export` → `docs/src/cli/flags.md`.
- Config error codes → `errors/fixup_metadata.go`.
- The manifest's `feature_set_digest` → manifest golden + CLAUDE.md "Output Format Contract" (additive; `format_version` stays `"1.1"`).
- New Update Demand rows:
  - "A new feature (operator, capability, I/O format, MCP surface) → its `Since` version and any dependency edges."
  - "A topical skill naming an operator → a `feature` fence around the mention."
- Profiles are documented for embedders in `docs/src/library/feature-profiles.md`, **not** as a runtime skill. A skill served by an instance that describes profiles would itself hint that hidden features exist.
- Long form in a new `.claude/reference/feature-profiles.md`; CLAUDE.md keeps a short summary plus a pointer.

## Open questions

1. **Exact names only (recommended; awaiting confirmation).** Profiles list exact feature names, and `pulse profile init` writes the full list. Pinned patterns are documented above as the rejected alternative.
2. ~~**Always-present core.**~~ **Decided:** as proposed; `inspect` stays always present.
3. ~~**CLI with a profile.**~~ **Decided:** the CLI is not profiled; only `pulse mcp --profile` and the library honour profiles.
4. ~~**Topical skills.**~~ **Decided:** every MCP-reachable path walks the pruned ontology; topical skills are rewritten for progressive disclosure with fences as a backstop and always served rendered (P4a).
5. ~~**Direct-get exposure.**~~ **Decided:** fetch by exact name honours the profile; a hidden feature's skill or example is "not found", identical to a nonexistent name.

# 01 — Feature Profiles: design & phasing

## Core rules

1. **No profile = the default feature set = everything**, including features added in future releases. Byte-identical to today.
2. **A profile is a complete, closed allowlist.** It declares every feature the instance offers. It never subtracts from "everything", and it never grows when Pulse is upgraded.
3. **Hidden is invisible.** Every surface is produced as if hidden features were never registered. There is no disabled error, disabled list, banner or hint.
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
  "baseline": "1.0.0",                     // Pulse feature-set version this profile was written against
  "features": [
    "capability:process", "capability:crosstab", "capability:facet",
    "AGG_*", "GROUP_*", "FILTER_*",        // patterns are expanded ONLY over features with Since ≤ baseline
    "TEST_CHISQ", "TEST_T", "TEST_WELCH",
    "OVERLAY_SHARE_OF_*", "OVERLAY_INDEX_VS_*", "OVERLAY_STD_RESIDUAL",
    "io_format:csv", "io_format:spss"
  ],
  "behaviour": { "disable_projection": false }   // optional; omitted = engine defaults
}
```

**Two ways to stay complete without being verbose:**

- **Pinned patterns (recommended).** Patterns and categories (`AGG_*`, `capability:*`) are allowed, but expand only over features whose `Since ≤ baseline`. A profile written against 1.0.0 that says `TEST_*` keeps exactly the 1.0.0 tests forever, and a test added in 1.2.0 stays invisible. To adopt new features, the embedder either raises `baseline` (and gets new matches deliberately) or names them explicitly. This satisfies decision 2 while keeping profiles readable.
- **Exact names only.** Always allowed. `pulse profile init` (P5) writes one for you.

**Validation at `pulse.New`.** These are embedder-facing config errors, never visible to the instance's users:

| Problem | Result |
|---|---|
| Unknown feature name, or a name with `Since > ` the running Pulse version | `PULSE_PROFILE_FEATURE_UNKNOWN`. This is a typo guard, and catches a profile written for a newer Pulse |
| A pattern matching nothing at `baseline` | `PULSE_PROFILE_SELECTOR_EMPTY` |
| `baseline` newer than the running Pulse | `PULSE_PROFILE_BASELINE_AHEAD` |
| An enabled feature whose dependency is missing | `PULSE_PROFILE_DEPENDENCY`, naming both. The dependency graph is declared in `descriptor/dependencies.go`, and `TestProfileDependenciesComplete` checks it against the registry |
| An operator enabled without the capability that hosts it (e.g. an overlay kind without `crosstab`, `facet`, `compose` or windowed `process`) | `PULSE_PROFILE_DEPENDENCY` |

**Removed features.** If a later Pulse release removes or renames a feature, a profile naming it fails with `PULSE_PROFILE_FEATURE_UNKNOWN` until it is edited. This is explicit, and matches the existing rule that a typo'd table must not silently become a table that isn't there.

---

## P3. Configuration sources

| Source | Form | Notes |
|---|---|---|
| `pulse.Options.Profile` | Go struct | highest precedence (the existing "Options always overrides" rule) |
| `pulse.Options.ProfileFile` | path to the JSON above | read through the instance's `afero.Fs` |
| `PULSE_PROFILE` | path to a profile file | lowest precedence; triggers the Update Demand env-var row |
| CLI `--profile <file>` | global flag | also `pulse mcp --profile <file>` for the MCP server |

Pulse ships **no built-in presets** as live profiles. Under decision 2, a preset that Pulse updated in a later release would grow an embedder's feature set without their consent. Instead, Pulse ships **example profile files** (in `examples/profiles/`). Each is pinned to the `baseline` it was written for. Embedders copy one and own it from then on.

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
- **Skills.**
  - Atomic skills of hidden features are "not found".
  - Topical skills are rendered with hidden fenced spans removed (00, hard part 1).
  - A topical skill left empty, or with no enabled feature, is not listed.
  - `## See` lines naming hidden skills are removed.
  - The `pulse-skill://` resource list follows the same rules.
- **Examples.** Any example tagged with a hidden operator is absent from search and get.
- **Errors.** Codes owned only by hidden features are absent from the errors list and `pulse errors lookup`. Codes shared with enabled features remain.
- **MCP.**
  - Only enabled tools, prompts and resources are registered.
  - Tool input schemas carry the instance's enums.
  - The `pulse-bootstrap` prompt text is rendered from the instance's view, and the prompt does not mention profiles.
- **CLI.**
  - `buildApp()` mounts only leaves for enabled capabilities and formats when a profile is configured.
  - `pulse --help` and the shell completions show only those.
  - `TestSkillsCoverAllCliLeaves` keeps running against the default (full) tree.
- **Smart defaults.** A default whose target operator is hidden doesn't apply. The request behaves exactly as it would for a field type with no default rule.
- **Guided analysis.**
  - Recommend, Explain, `NotFor` alternatives, follow-ups, intents (an intent with no enabled operator disappears), question guides, decision trees and the glossary are all rendered from the instance's view.
  - When every alternative is hidden, the alternatives line is simply absent.
  - Glossary terms used only by hidden features are absent.
- **Reference export.** `pulse docs export` / `p.ExportReference(fs, dir)` renders the catalog, guides, glossary and decision trees for this instance. This is how "documents react to the configuration" for an embedder's own published docs. The public Pulse site documents the default (full) set.

---

## P5. Tooling for embedders

| Command | Purpose |
|---|---|
| `pulse profile init [--from <example>] [--exact]` | writes a profile listing the **current full feature set** at today's version as the baseline. The embedder deletes what they don't want; `--exact` expands every pattern to names |
| `pulse profile check <file>` | runs the `pulse.New` validation offline: unknown names, dependency gaps, empty patterns |
| `pulse profile diff <file>` | lists features in the running Pulse that the profile does not include (e.g. new since `baseline`), which is the upgrade review step. It reports **to the embedder** only, never through an instance's runtime surfaces |
| `pulse profile show <file>` | the resolved, exact feature list |

These operate on profile *files* and run outside any profiled instance. They are the only place where "features you don't have" are listed, which keeps decision 3 intact.

---

## P6. Gates

| Gate | Asserts |
|---|---|
| `TestProfileDefaultIsFull` | no profile → manifest, payload schema, skills, examples, errors and CLI tree byte-identical to today's goldens |
| `TestProfileInvisibilityParity` | for each example profile and each hidden feature, every surface (manifest, schema, predict, process, MCP tools, prompts, CLI help, skills, examples, errors lookup) is byte-identical to a build where that feature's registration was removed. In practice this is approximated by comparing responses for the hidden name and a never-registered name, plus "the name occurs nowhere in any rendered surface" |
| `TestSkillsCoverFeatureFences` | every operator name in a topical skill or generated guide sits inside a matching `feature` fence |
| `TestProfileDependenciesComplete` | every operator that reads another operator's components or results, or needs a host capability, declares it |
| `TestFeaturesHaveSince` | every registered feature declares `Since` |
| `TestProfileBaselinePinning` | a profile at baseline N never resolves a feature with `Since > N`, even when its patterns would match |
| Profile goldens | manifest and schema goldens for each example profile |

The `TestSkillsCover*` prefix puts `TestSkillsCoverFeatureFences` on CLAUDE.md's self-expanding gate list.

---

## Phasing

The profile mechanism lands **first**. Every surface the vector-matrix and guided-analysis themes add then renders from the instance's feature set from its first commit.

| Epic | Stories |
|---|---|
| **FP1 — Feature registry** | feature names and kinds across all registries; `Since` on every registration; dependency graph + gate; always-present core definition |
| **FP2 — Profile model** | profile file format, pinned-pattern resolution, `pulse.New` validation and config codes, `Options.Profile` / `ProfileFile` / `PULSE_PROFILE` / `--profile` |
| **FP3 — Instance snapshot & request path** | `InstanceSnapshot`; name resolution at the validation choke point; strict refusal of hidden request slots; `feature_set_digest` |
| **FP4 — Self-description** | instance-scoped manifest, payload schema, predict, errors list; profile goldens; `TestProfileDefaultIsFull` |
| **FP5 — Skills & examples** | fence syntax; a fencing pass over topical skills; instance-rendered skills; example filtering; `TestSkillsCoverFeatureFences` |
| **FP6 — MCP & CLI** | instance-scoped tool, prompt and resource registration; filtered tool schemas; profile-aware `buildApp()`; `TestProfileInvisibilityParity` |
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

1. **Pattern pinning vs exact names.** Is the `baseline`-pinned pattern model acceptable, or should profiles be exact-name-only with `pulse profile init` doing the expansion?
2. **Always-present core.** Is the proposed core (manifest, schema, skills, examples, errors lookup, inspect, predict) right? Should `inspect` be optional for embedders who don't want cohort schemas browsable?
3. **CLI with a profile.** Should the CLI honour profiles at all, or only the library and MCP? The CLI is mostly an operator's tool, but `pulse mcp` needs `--profile` either way.
4. **Fencing ownership.** The fencing pass over existing topical skills is a one-time chore. Should it be done up front in FP5, or topic by topic as each is next edited (with the gate in report-only mode until complete)?

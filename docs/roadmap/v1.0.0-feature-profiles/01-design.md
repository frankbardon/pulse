# 01 — Feature Profiles: design & phasing

## Core rules

1. **Default is everything.** A `nil` / absent profile means the full feature set, byte-identical to today: the same manifest, schema, skills and goldens.
2. **Availability, not behaviour.** A profile decides what *exists* for an instance. Per-request output choices remain per-request. Example: the guided-analysis `Response.Interpretation` stays an opt-in request flag, so that it doesn't bloat context. When the profile *enables* it, a request may ask for it; when the profile *disables* it, the request flag is refused.
3. **One profile per `*Pulse` instance**, fixed at `pulse.New`. Multi-tenant embedders build one instance per profile, or use the request-scoped narrowing described in the stretch section.
4. **Everything a profile hides is also refused.** No surface may advertise less than the runtime executes, or execute more than it advertises.
5. **Profiles never weaken the build.** Coverage gates, goldens and the Update Demand continue to apply to the full registry.

## P1. What can be toggled — granularity

| Level | Selector examples | Notes |
|---|---|---|
| **Capability** (facade method / execution mode) | `compose`, `process_chain`, `facet`, `stream`, `watch`, `joins`, `crosstab`, `synth`, `import`, `export`, `shard_write`, `index_build`, `dedup`, `widen`, `templates`, `lookup`, `recommend`, `explain` | maps to facade methods, CLI leaves and MCP tools together |
| **Category** | `AGG`, `ATTR`, `FILTER`, `GROUP`, `WIN`, `FEAT`, `TEST`, `REG`, `OVERLAY`, `SYNTH`, `MAT` | coarse switch for a whole operator family |
| **Operator** (glob) | `TEST_*`, `OVERLAY_PAIRWISE_*`, `REG_GLM`, `MAT_FACTOR` | the finest unit; globs resolve against the registry at `pulse.New`, and a glob matching nothing is an error (typo protection, the same principle as the table-directory loaders) |
| **Intent** (guided analysis) | `intent:drivers`, `intent:segment` | convenience: expands to every operator whose `Purpose.Intents` is *only* in the disabled set |
| **Level** (guided analysis) | `level:advanced` | e.g. hide advanced methods from a self-serve product |
| **I/O format** | `io:spss`, `io:parquet` | import/export adapters |
| **MCP surface** | `mcp_tool:pulse_dedup`, `mcp_resource:cohorts` | for when an MCP host should expose less than the library |
| **Extension operators** | by name, like built-ins | an embedder can register an extension but gate it per instance |

Field types are **not** togglable. A cohort's schema is data, not a feature, and refusing to read a type would make valid files unreadable.

## P2. Shape

```go
pulse.New(pulse.Options{
    Profile: &pulse.Profile{
        Name:    "survey-self-serve",
        Base:    pulse.ProfileFull,            // or ProfileNone (allowlist mode), or a preset
        Disable: []string{"category:REG", "OVERLAY_PAIRWISE_*", "level:advanced",
                          "capability:import", "capability:shard_write", "capability:dedup"},
        Enable:  []string{"REG_OLS"},          // re-enable inside a disabled category
        Disclose: pulse.DiscloseNames,         // what the manifest says about hidden features
    },
})
```

- **Resolution order:** `Base` → `Disable` → `Enable`. The most specific selector wins, so an operator rule beats a category rule. This order is deterministic and documented.
- **Allowlist mode:** `Base: ProfileNone` plus `Enable: [...]` suits embedders who want a fixed, small surface that new Pulse releases cannot silently grow. **This matters for upgrades.** With a denylist, a new operator added in v1.1 appears automatically; with an allowlist, it does not. Both are legitimate, and the docs must say which to use when.
- **Dependency validation** at `pulse.New`: if an enabled feature depends on a disabled one, the result is a hard `PULSE_PROFILE_DEPENDENCY` naming both. (Example: `OVERLAY_T_CELL` enabled while `AGG_WELFORD` is disabled.) The dependency graph is declared in `descriptor/dependencies.go`, and a gate checks it against the registry.
- **Presets** ship as named profiles for common embeddings:

  | Preset | Contents |
  |---|---|
  | `full` | default |
  | `read_only` | every write path off: import, shard write, index build, dedup, widen, drop, synth write |
  | `descriptive` | `AGG` / `GROUP` / `FILTER` / `ATTR` / share and index overlays only; no inferential tests, regressions or `MAT_*` |
  | `survey_basic` | descriptive + crosstab significance overlays, `TEST_CHISQ`, `TEST_T` family, `MAT_RELIABILITY` |
  | `agent_safe` | `read_only` + `MaxMatrixDim` caps + no `watch` / `stream` (long-lived calls) |

  Presets are the main documentation story, because most embedders start from one.

## P3. Configuration sources

| Source | Form | Precedence |
|---|---|---|
| `pulse.Options.Profile` | Go struct | highest; always wins (the existing env-var rule) |
| Profile file | JSON (the same struct), via `Options.ProfileFile` | — |
| `PULSE_PROFILE` env var | preset name or file path | lowest |
| CLI `--profile <preset\|file>` | global flag | sets the file / preset for the CLI process |
| `pulse mcp --profile` | same | the MCP server's whole surface follows it |

A new env var triggers the Update Demand: CLAUDE.md "Build / Env" and `skills/session-bootstrap.md`.

## P4. How each surface reacts

- **Manifest.**
  - Built from a `ProfileSnapshot`, which travels alongside `ExtensionsSnapshot` (or the two merge into one `InstanceSnapshot`).
  - Hidden operators, tests, overlays, commands and MCP tools are absent.
  - Capability blocks shrink, e.g. `Overlays` lists only enabled kinds.
  - A new `profile` block carries `{name, digest, disclose}`.
  - Under `DiscloseNames` it also lists the disabled names, so an agent can tell a user "that's turned off here" instead of guessing. Under `DiscloseNone` they are absent entirely, for embedders who don't want to reveal them.
- **Predict / runtime.**
  - A disabled operator or capability returns `PULSE_FEATURE_DISABLED` (`details.feature`, `details.profile`).
  - Under `DiscloseNone` it returns the same error an unknown name would. This is deliberate, so that probing can't enumerate hidden features.
  - The check runs once, at the shared request-validation choke point, for every entry point (Process, Compose, ProcessChain, Facet, ProcessStream, Watch, template render, Recommend's drafts).
- **Payload schema.**
  - `p.PayloadSchema()` returns the profile-filtered schema: operator enums shrink, and request slots for disabled capabilities are removed.
  - `pulse schema` and `pulse://schema` serve this version.
  - The `$id` stays versioned by `format_version`, and the profile digest goes in a `$comment`, so caches can tell profiles apart.
- **Skills.**
  - `skills.List` / `Get` gain profile-aware forms.
  - Atomic skills of disabled surfaces are hidden: `pulse_skills_get` behaves as if they don't exist, and the `pulse-skill://` resource list omits them.
  - A topical skill whose operators are *all* disabled is hidden too.
  - Topical skills that remain are served with a one-line banner when any of their operators are disabled: "Some operators named here are disabled in this deployment — `pulse_manifest` is authoritative."
  - `## See` links to hidden skills are stripped.
- **Examples.** An example is hidden if any of its `_meta.operators` is disabled.
- **MCP.**
  - Tools for disabled capabilities are not registered.
  - Tool input schemas use the filtered enums, so a client's own validation catches disabled names before a call is made.
  - Prompts are filtered the same way.
  - `pulse-bootstrap` mentions the profile name.
- **CLI.** Leaves stay mounted, so `TestSkillsCoverAllCliLeaves` is unchanged. A disabled leaf exits with the coded error. `pulse manifest --json` reflects the profile.
- **Guided analysis.**
  - Recommend never proposes a disabled operator.
  - `NotFor` alternatives pointing at disabled operators are dropped from rendered text. If *every* alternative is gone, the line becomes "no alternative available in this deployment".
  - Intents with no enabled operators disappear from `intents[]`.
  - Explain names follow-ups only when they are enabled.
- **Generated reference docs.** `pulse docs export --profile X` (library: `p.ExportReference(fs, dir)`) renders the catalog, guides, glossary and decision trees for the profile into Markdown that the embedder hosts. The public mdBook site stays the full reference.
- **Request hashing / Watch.** The profile digest becomes part of any cache key that stores *responses*, because the same request can be valid under one profile and refused under another.

## P5. Gates

| Gate | Asserts |
|---|---|
| `TestProfileParity` | for each preset and each profile golden, every name absent from the manifest is refused by Process / Compose / ProcessChain / Facet / Stream / Predict, and every name present executes |
| `TestProfileDefaultIsFull` | `nil` profile → manifest, payload schema and skill list byte-identical to the existing goldens |
| `TestProfileDependenciesComplete` | every operator that reads another operator's components or results declares that dependency |
| `TestProfileSelectorsResolve` | every preset's selectors match ≥ 1 registered feature |
| `TestSkillsCoverProfileFiltering` | for each preset, no served skill or example names a disabled operator outside a bannered topical skill |
| Profile goldens | `descriptor/testdata/manifest.<preset>.json` for each preset |

The `TestSkillsCover*` prefix puts the last gate on CLAUDE.md's self-expanding gate list.

## P6. Stretch: request-scoped narrowing

```go
ctx = pulse.WithProfileNarrowing(ctx, pulse.Profile{Disable: []string{"category:TEST"}})
```

A context-carried profile can only **narrow** the instance profile, never widen it. This serves multi-tenant embedders who don't want one `*Pulse` per tenant. The manifest gains `p.ManifestFor(ctx)`. It is stretch because it multiplies cache-key and MCP-session considerations. An MCP session can't easily change its tool list mid-session, so MCP stays instance-scoped.

## Phasing

The profile mechanism should land **before or alongside** the first epics of the other two themes. Every new surface they add (`MAT_*`, overlays, Recommend, Explain, prompts, generated docs) then filters correctly from its first commit, instead of being retrofitted.

| Epic | Stories |
|---|---|
| **FP1 — Core** | `Profile` type, selector grammar and resolution, presets, `pulse.New` validation, `PULSE_FEATURE_DISABLED` / `PULSE_PROFILE_DEPENDENCY` / `PULSE_PROFILE_SELECTOR_UNKNOWN` codes, dependency graph declaration + gate |
| **FP2 — Enforcement** | single validation choke point across all entry points; `TestProfileParity`; Disclose modes |
| **FP3 — Self-description** | profile-filtered manifest, payload schema and predict; profile goldens; `TestProfileDefaultIsFull` |
| **FP4 — Skills, examples, MCP** | profile-aware skills/examples APIs; topical-skill banner and hiding; MCP tool, prompt and resource filtering; filtered tool-input enums |
| **FP5 — Config & CLI** | `ProfileFile`, `PULSE_PROFILE`, `--profile`; CLAUDE.md + session-bootstrap updates; mirror existing `Disable*` behaviour switches into the profile view |
| **FP6 — Reference export** | `pulse docs export --profile` / `p.ExportReference`, reusing the guided-analysis doc generator (lands with guided-analysis G3) |
| FP7 — *Stretch* | request-scoped narrowing |

## Update Demand impact

- A new public option `Options.Profile` plus facade additions (`PayloadSchema()`, `ExportReference`, later `ManifestFor`) touch the CLAUDE.md design-principles facade list.
- New env var `PULSE_PROFILE` → CLAUDE.md "Build / Env" + `skills/session-bootstrap.md` (`TestClaudeMdMentionsAllEnvVars`).
- New CLI leaf `docs export` → `docs/src/cli/flags.md` row.
- The manifest `profile` block → manifest golden.
- New codes → `errors/fixup_metadata.go`.
- New topical skill `skills/feature-profiles.md`, explaining to agents what a disabled feature looks like and how to respond to `PULSE_FEATURE_DISABLED`.
- **A new Update Demand row:** "A new operator, capability or MCP tool → its profile selector category and any dependency edges in `descriptor/dependencies.go`."
- Long form goes in a new `.claude/reference/feature-profiles.md`, with CLAUDE.md carrying a short summary plus a pointer (size budget).

## Open questions

1. **Default `Disclose` mode.** `DiscloseNames` is friendlier for agents ("that's turned off here"); `DiscloseNone` is safer for commercial tiering. Recommendation: `DiscloseNames`, with embedders opting into `None`.
2. **Incoherent profiles.** Refuse at `pulse.New` (recommended), or auto-disable dependants with a warning?
3. **Allowlist vs denylist guidance.** Should presets be allowlists, so they stay stable across Pulse upgrades, or denylists, so they gain new features automatically? Recommendation: presets are denylists over `full`, except `agent_safe`, which is an allowlist.
4. **Request-scoped narrowing.** Is it needed for v1.0.0, or is one-instance-per-profile acceptable to start?
5. **Topical-skill fencing.** Is the banner approach acceptable for v1.0.0, or should paragraph-level fences be planned now?

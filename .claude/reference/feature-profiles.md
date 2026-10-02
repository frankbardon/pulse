# Feature profiles — feature table, dependency model, profile model, validation

Relocated long form for CLAUDE.md "Feature profiles". CLAUDE.md keeps the always-load half — the naming rule, "stored, not yet applied", `pulse.New` never reads the env var — plus a pointer here.

**Load it before:** adding an operator, capability, I/O format, MCP tool or MCP prompt (each one is a feature and needs a row); touching `internal/descriptor/features.go`, root `feature_profile*.go`, an extension registration's `DependsOn`, `mcpserve/feature_profile.go`, `mcpserve/describe.go`, or the `pulse mcp --feature-profile` leaf.

Status at U04 (`profiles-model`): the feature vocabulary, its dependency graph and the profile model exist; `pulse.New` validates a profile and stores it on the instance. **Nothing is hidden or filtered yet.** The feature list takes effect in U05 (request path, manifest, payload schema, predict, errors) and U06 (MCP registration, tooling). The `behaviour` switches are the one part that takes effect today.

## Naming

The concept is a **feature profile**, never a bare "profile". "Profile" already belongs to synth data profiling: `Pulse.Profile`, `ProfileOptions`, `pulse profile create`, `PULSE_PROFILE_FIELD_UNSUPPORTED` and the `PULSE_PROFILE_*` prose in `skills/synthetic-data.md`. The public names are `pulse.FeatureProfile`, `pulse.FeatureProfileBehaviour`, `pulse.ParseFeatureProfile`, `Options.FeatureProfile` / `Options.FeatureProfileFile`, `mcpserve.NewPulse`, `mcpserve.Options.FeatureProfileFile`, env `PULSE_FEATURE_PROFILE`, flag `pulse mcp --feature-profile`, and the codes `PULSE_FEATURE_PROFILE_INVALID` / `_UNKNOWN` / `_DEPENDENCY`. Never shorten any of them to `Profile`.

## Feature kinds and spelling

Four kinds (`descriptor.AllFeatureKinds()` in `internal/descriptor/features.go`). **Operators are spelled bare; every other kind is `<kind>:<name>`.** There are no aliases: one feature has exactly one spelling (`descx.FeatureName`).

| Kind | Spelling | Rows |
|---|---|---|
| `operator` | `AGG_SUM`, `TEST_T`, `OVERLAY_YOY` | every `types.All*Types()` entry — AGG / ATTR / FILTER / GROUP / WIN / FEAT / TEST / REG / OVERLAY. A `TEST_*` is ONE name covering both its row-test and post-test tiers |
| `capability` | `capability:process` | `process`, `stream`, `watch`, `compose`, `process_chain`, `facet`, `sample`, `joins`, `crosstab`, `lookup`, `index`, `shard`, `import`, `export`, `filter_to_file`, `dedup`, `widen`, `templates`, `synth`, `labels`, `range_tables` |
| `io_format` | `io_format:csv` | one per `io.Formats()` entry; ONE name gates BOTH import and export |
| `mcp_extra` | `mcp_extra:cohort_resources` | the cohort-resource enumeration plus one row per registered MCP prompt (`prompt_bootstrap`, `prompt_author_request`) |

`capability:index` covers build / verify / list / drop; `capability:shard` covers every shard-archive method — read and write siblings collapse (the design's `index_build` / `shard_write` were dropped). The code comments on each `capability(...)` row name the facade methods it owns.

## Inventory rules

- **`builtinFeatures` is THE table**, maintained by hand. `TestFeaturesHaveSince` fails on a registry entry (operator, I/O format, MCP prompt, approved capability) without exactly one row and on a row without a live registry entry, and pins `mcpToolBindings` to `toolmeta.Names()` in both directions.
- **Not features** (`TestFeatures_NonFeaturesAbsent`): synth distributions (`capability:synth` gates every one; an extension `SYNTH_*` name never resolves), field types (a cohort's schema is data), named tables, expr functions, and the behaviour switches.
- **Extension operators ARE features** (seven categories: aggregator, attribute, filterer, grouper, window, feature, test) but are not table rows and carry no `Since`. The resolvable universe is the table plus the instance's registered extension operators.
- **Core surfaces are always present and are NOT features**: `open`, `inspect`, `predict`, `count_records`, `manifest`, `payload_schema`, `skills`, `examples`, `errors_lookup`, `cohort_artifacts` (`coreSurfaces`). Listing one, bare or kind-prefixed, is `PULSE_FEATURE_PROFILE_UNKNOWN` reason `core_surface`.
- **MCP tool bindings** (`mcpToolBindings`) map every tool to exactly one feature or core surface. Data only at U04; U06's registration filter consumes it.
- The table is internal on purpose: no public struct gained a field, so the manifest and public API goldens did not move for it.

## `Since`

Every row carries the release (major.minor.patch) that introduced it. Every built-in is `"1.0.0"` (`BuiltinFeatureSince`), and `TestFeaturesHaveSince` currently REQUIRES that: anything landing before the `v1.0.0` tag ships in 1.0.0, alpha and rc tags included. **The first feature added after `v1.0.0` gets the release that ships it — never back-dated — and that change must relax the gate's equality check to "parseable and ≤ the next release".** `ParseSince` accepts exactly `MAJOR.MINOR.PATCH`; `TestParseSince` pins it.

**Version compare.** A built-in row resolves only when the running build has reached its `Since` (`sinceReached`). The RUNNING version is reduced to its core: leading `v` dropped, everything from the first `-` or `+` cut — so `1.0.0-alpha.2` and a `git describe` build offer the `1.0.0` features. A running version with no usable core is treated as NEWEST: `devel`, `devel+<sha>`, a bare commit SHA, anything unparseable, and a `0.0.0` core (the Go pseudo-version of an untagged build). A row past the running version is `UNKNOWN` reason `newer_than_running` with `since`. `written_with` in the profile is never compared.

`pulse.Version()` (`internal/buildinfo`) is the running version: the ldflags value, else Pulse's OWN module version from build-info `Deps` (honouring a `replace`), never the embedder binary's `Main.Version` — otherwise an embedder's `v3.2.0` would unlock every future feature.

## Dependency model

`Feature.DependsOn` is an **AND of any-of groups**: for every group, at least one member must be enabled. Rows never spell it by hand — `withDependencies` derives it:

- non-overlay operators and the request-slot capabilities (`joins`, `crosstab`) → any-of the request hosts `capability:process` / `compose` / `process_chain`;
- Process-only modes (`stream`, `watch`, `filter_to_file`) → `capability:process`;
- overlay kinds → any-of the hosts whose engine handler map lists them (`overlayHostKinds`: `crosstab` ← matrix handlers; `compose` ← compose, multi-layer and series handlers; `process_chain` ← chain handlers; `facet` ← facet handlers);
- then each **hard edge** (`hardEdges`) appends a single-name group: `OVERLAY_T_CELL` / `Z_CELL` / `T_VS_REF` / `Z_VS_REF` / `PAIRWISE_WELCH_T` / `PAIRWISE_TWO_MEANS_Z` → `AGG_WELFORD`; `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` → `AGG_WEIGHTED_MEAN`; `ATTR_REG_FITTED` / `LEVERAGE` / `RESIDUAL` → `REG_OLS`; `OVERLAY_YOY` → `GROUP_DATE` (its series host must lead with a date grouper).

`TEST_TUKEY_HSD` after `TEST_ANOVA_F` is deliberately NOT an edge: its inputs are plain params, so the pairing is advice.

**Gates.** `TestFeatureDependenciesResolve` (`internal/descriptor`): every member resolves, and the hard edges are pinned by name. `TestProfileDependenciesComplete` (`internal/processing`, because the descriptor cannot import the handler maps): (a) host groups present, (b) overlay host sets equal handler-map membership both ways, (c) a source scan — a file owning operator X that references operator Y needs an X→Y edge or a justified `dependencyScanAllowlist` entry. The scan sees identifiers, not semantics.

**Extension `DependsOn`.** Every operator registration has optional `DependsOn []string`; each entry is a single-name AND group added after the request-host group every extension operator gets. Every entry must resolve (a built-in feature or another registered extension) at EVERY `pulse.New`, profile or not — a typo fails at construction, as `PULSE_FEATURE_PROFILE_UNKNOWN` with `extension` and `category` on each entry. Synth-distribution registrations have no `DependsOn`.

## The profile model

`pulse.FeatureProfile` mirrors the JSON file key for key: `profile` (free label), `written_with` (stored as-is, never validated), `features` (REQUIRED), `behaviour` (optional). Decode is strict (`DisallowUnknownFields`, trailing data refused): every other key — including the reserved `limits` (U19) and `return` (U17) — is refused. **A nil `Features` is invalid**; a Go embedder writes `[]string{}` for an empty profile, which is valid and offers no optional feature.

**Behaviour** switches OR into `Options`: `disable_defaults`, `disable_components`, `disable_projection` (also forces the deprecated `ProjectBufferedFields` off), `disable_cohort_scan`. A profile can turn a switch on; `Options` cannot turn it back off. `disable_cohort_scan` does nothing to the engine — `gosdk.Register` ORs it into `Config.DisableCohortScan` through the `internal/facadebridge.CohortScanDisabled` hook.

**Sources.** `Options.FeatureProfile` (Go value, copied) XOR `Options.FeatureProfileFile` (read through the INSTANCE afero Fs, so relative to `Options.FS` / `DataDir`). Both set → `INVALID` `both_options_set`. `pulse.ParseFeatureProfile([]byte)` is the same strict decode, decode-only — names and dependencies are checked when the value reaches `pulse.New`, and those errors carry no `path` detail.

**Order in `pulse.New`:** `validateExtensions` → `probeExtensions` → `validateExtensionDependsOn` → filesystem resolution → `resolveFeatureProfile` → `applyFeatureProfileBehaviour` → `service.New`. The stored profile has no public accessor (U05 decides one).

## Validation codes and order

Three classes, run in order; validation **stops at the first failing class and reports EVERY instance of that class, sorted** (`TestFeatureProfile_ClassOrder`). All raised with their own code; prose and fixups in `errors/fixup_metadata.go`.

| Code | Class | Details |
|---|---|---|
| `PULSE_FEATURE_PROFILE_INVALID` | structural | `reason`: `both_options_set`, `file_unreadable`, `malformed_json` (incl. trailing data), `unknown_key`, `missing_features`, `duplicate_feature` (+ `duplicates`); `path` when a file was read |
| `PULSE_FEATURE_PROFILE_UNKNOWN` | name resolution | `unknown[]` of `{name, reason}`, plus `names`, `version`. Reasons: `unregistered`, `pattern` (`*`, `?`, `[` — never expanded; `TestProfileRejectsPatterns`), `wrong_kind` (+ `did_you_mean`, the one valid spelling), `core_surface`, `newer_than_running` (+ `since`) |
| `PULSE_FEATURE_PROFILE_DEPENDENCY` | dependencies | `unmet[]` of `{feature, requires_any_of}`, plus `features` |

## Where a profile is read

**`pulse.New` never reads the environment** (`TestFeatureProfile_NotReadFromEnv`): every CLI leaf calls it, and the CLI is not profiled. The env var is read only by `mcpserve.NewPulse(popts, opts)`, which `pulse mcp` calls. Precedence: `mcpserve.Options.FeatureProfileFile` (flag `--feature-profile`) > a profile already on `popts` > `PULSE_FEATURE_PROFILE`; the field plus a `popts` profile is `INVALID` `both_options_set`. The flag and env forms are host OS paths (absolute or cwd-relative, never under `DataDir`), read with `os.ReadFile` — the one deliberate read outside the instance Fs, because they configure the process, not a cohort. `mcpserve.Serve` / `ServeStdio` ignore `FeatureProfileFile`: build through `NewPulse` or `pulse.New`. `mcpserve.Describe(p, opts)` reports the effective cohort-scan setting and loaded profile label, which the `pulse mcp` stderr notice prints.

**No runtime skill mentions feature profiles** — not `skills/session-bootstrap.md`, not any other. A served skill describing profiles tells an agent hidden features exist. This is the deliberate exception to the env-var row's session-bootstrap companion; embedder docs live in `docs/src/library/feature-profiles.md`.

## Notes for U05 / U06

- **`BindOnInspect` rebinds tools by name** (`internal/mcp/bind.go` `mergeEnumNames`, `buildRequestSchemaWithExtensions`), bypassing any registration-time filter — U06 must filter the rebind too.
- **`toolmeta` descriptions name operators in prose** (`internal/mcp/toolmeta/meta.go`); filtering enums alone leaves hidden names in tool text.
- **Prompts, the schema resource and skill resources take no `*Pulse`** (`registerPrompts`, `registerSchemaResource`, `registerSkillResources` in `mcp/gosdk`); instance-scoping needs the instance threaded in.
- **Extension operators omitted from a profile become hidden** once U05 applies the list; U05 must say so in the embedder docs.
- The stored-profile accessor and the profile-tooling CLI naming (`pulse profile create` collision) are open for U05 / U06.

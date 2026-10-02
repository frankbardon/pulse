# Feature profiles — feature table, dependency model, profile model, validation

Relocated long form for CLAUDE.md "Feature profiles". CLAUDE.md keeps the always-load half — the naming rule, "stored, not yet applied", `pulse.New` never reads the env var — plus a pointer here.

**Load it before:** adding an operator, capability, I/O format, MCP tool or MCP prompt (each one is a feature and needs a row); touching `internal/descriptor/features.go`, root `feature_profile*.go`, an extension registration's `DependsOn`, `mcpserve/feature_profile.go`, `mcpserve/describe.go`, or the `pulse mcp --feature-profile` leaf.

Status at U04 (`profiles-model`): the feature vocabulary, its dependency graph and the profile model exist; `pulse.New` validates a profile and stores it on the instance. **U05 in progress:** the resolved feature set, `InstanceSnapshot` and digest exist, omitted extension operators are dropped (see "The resolved feature set"), and a hidden built-in AGG / ATTR / FILTER / GROUP / WIN / FEAT / TEST / REG name or OVERLAY kind resolves at run time exactly as a never-registered one (see "Runtime hiding"). Predict (beyond the request-time overlay gates), request slots and self-description are not scoped yet. The feature list takes effect in U05 (request path, manifest, payload schema, predict, errors) and U06 (MCP registration, tooling). The `behaviour` switches are the one part that takes effect today.

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
- then each **hard edge** (`hardEdges`) appends a single-name group: `OVERLAY_T_CELL` / `Z_CELL` / `T_VS_REF` / `Z_VS_REF` / `PAIRWISE_WELCH_T` / `PAIRWISE_TWO_MEANS_Z` → `AGG_WELFORD`; `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` → `AGG_WEIGHTED_MEAN`; `ATTR_REG_FITTED` / `LEVERAGE` / `RESIDUAL` → `REG_OLS`; `OVERLAY_YOY` → `GROUP_DATE` (its series host must lead with a date grouper); `capability:filter_to_file` → `FILTER_EXPRESSION` (`FilterToFile` / `FilterToFileWithRequest` compile EVERY filterer — even a visible `FILTER_INCLUDE` — into one engine-internal `FILTER_EXPRESSION`, so a profile hiding it would fail every run with an "unknown filter type" that leaks the hidden name; the edge refuses that profile at `pulse.New` instead, and there is deliberately no run-time exemption).

`TEST_TUKEY_HSD` after `TEST_ANOVA_F` is deliberately NOT an edge: its inputs are plain params, so the pairing is advice.

**Gates.** `TestFeatureDependenciesResolve` (`internal/descriptor`): every member resolves, and the hard edges are pinned by name. `TestProfileDependenciesComplete` (`internal/processing`, because the descriptor cannot import the handler maps): (a) host groups present, (b) overlay host sets equal handler-map membership both ways, (c) a source scan — a file owning operator X that references operator Y needs an X→Y edge or a justified `dependencyScanAllowlist` entry. The scan sees identifiers, not semantics.

**Extension `DependsOn`.** Every operator registration has optional `DependsOn []string`; each entry is a single-name AND group added after the request-host group every extension operator gets. Every entry must resolve (a built-in feature or another registered extension) at EVERY `pulse.New`, profile or not — a typo fails at construction, as `PULSE_FEATURE_PROFILE_UNKNOWN` with `extension` and `category` on each entry. Synth-distribution registrations have no `DependsOn`.

## The profile model

`pulse.FeatureProfile` mirrors the JSON file key for key: `profile` (free label), `written_with` (stored as-is, never validated), `features` (REQUIRED), `behaviour` (optional). Decode is strict (`DisallowUnknownFields`, trailing data refused): every other key — including the reserved `limits` (U19) and `return` (U17) — is refused. **A nil `Features` is invalid**; a Go embedder writes `[]string{}` for an empty profile, which is valid and offers no optional feature.

**Behaviour** switches OR into `Options`: `disable_defaults`, `disable_components`, `disable_projection` (also forces the deprecated `ProjectBufferedFields` off), `disable_cohort_scan`. A profile can turn a switch on; `Options` cannot turn it back off. `disable_cohort_scan` does nothing to the engine — `gosdk.Register` ORs it into `Config.DisableCohortScan` through the `internal/facadebridge.CohortScanDisabled` hook.

**Sources.** `Options.FeatureProfile` (Go value, copied) XOR `Options.FeatureProfileFile` (read through the INSTANCE afero Fs, so relative to `Options.FS` / `DataDir`). Both set → `INVALID` `both_options_set`. `pulse.ParseFeatureProfile([]byte)` is the same strict decode, decode-only — names and dependencies are checked when the value reaches `pulse.New`, and those errors carry no `path` detail.

**Order in `pulse.New`:** `validateExtensions` → `probeExtensions` → `validateExtensionDependsOn` (against ALL registrations) → filesystem resolution → `resolveFeatureProfile` → `applyFeatureProfileBehaviour` → `resolveFeatureSet` → `withoutHiddenExtensions` → `service.New`, then `buildRuntimeExtensions` / `buildExtensionsSnapshot` over the VISIBLE registrations only. `p.FeatureProfile()` returns a copy of the stored profile (`nil, false` without one).

## The resolved feature set (U05)

`pulse.New` computes the instance's feature set once (`feature_set.go`): the universe is every built-in row whose `Since` is reached plus every registered extension operator; no profile enables the whole universe, a profile enables exactly its list and HIDES the rest of the universe. A hidden extension operator is never registered — dropped after `validateExtensionDependsOn`, so a `DependsOn` naming a hidden-but-registered extension still validates.

The set lives in `internal/descriptor.InstanceSnapshot` (`instance_snapshot.go`), installed with `Service.SetInstanceSnapshot` and read back through `Service.InstanceSnapshot()`; it wraps the `*ExtensionsSnapshot`, which `Service.ExtensionsSnapshot()` and the `facadebridge.ExtensionsSnapshot` hook still return. `Enabled(name)` is membership in the resolved set; `Hidden(name)` is "resolvable here but not offered" — the predicate a native lookup site consults, since an unknown name already fails on its own. Both are O(1) map lookups. A nil or `UnscopedInstanceSnapshot` (`Service.SetExtensionsSnapshot`, hand-wired services) enables everything, hides nothing and has an empty digest.

**`feature_set_digest`** (`p.FeatureSetDigest()`, `descx.FeatureSetDigest`) = `"fs1:" + sha256hex` over the sorted unique enabled names followed by the four EFFECTIVE behaviour switches (`behaviour:<switch>=<bool>`, fixed order, all newline-joined). Effective = the profile's `behaviour` ORed with `Options` (projection read the way `New` wires it, `ProjectBufferedFields` included); `disable_cohort_scan` comes from the profile alone. Every instance has one; it is unsalted so it keys caches across processes, which also lets a same-version observer tell a profiled digest from the default. Changing what it covers bumps the `fs1:` prefix.

## Runtime hiding (registry operators)

**Hide at the native lookup site, never with a pre-check.** `Service.SetExtensions` / `SetInstanceSnapshot` both re-derive the runtime registry as `processing.ExtensionRegistry.WithHidden(snap.Hidden)` (`rescopeExtensions`; no hidden names → the installed registry unchanged, nil stays nil). Every `Lookup*` (AGG, ATTR, FILTER, GROUP, WIN, FEAT, row TEST, post TEST) answers a hidden name not-found, so each execution path reaches its OWN unknown-name error — same code, message, details and ordering against zone / field-ref / label / strict faults. Windows and features resolve through `window.Resolver` / `feature.Resolver` (the registry's `LookupWindow` / `LookupFeature`), not an overlay map, so they cannot bypass it. The route-choosing facts read the same predicate: `IsStreamable`, `IsMergeable`, `AggregatorMarginReducibility`, the two-pass check and the merge-gate adapter (`registryMergeFacts`, which answers a hidden name known-and-not-mergeable) give a hidden name the never-registered answer, so it cannot take a different stream / merge / fused route to its error. `internal/mergegate` consults the adapter's attribute answer BEFORE its built-in row-local list (`ATTR_FORMULA`, `ATTR_DATE_PART`) for the same reason.

**Other sites.** The `FacetSchema` `additive_fields` scope filters build against the instance registry (they used a nil registry — which also broke embedder filterers there). `FilterToFileWithRequest`'s filterer→expression translator refuses a hidden type with its unsupported-type error. `descx.ResolveDefaults(req, schema, inst)` skips a hidden default target exactly like "no rule for this type" (no Type written, no `DefaultApplied` entry); the service passes its snapshot, the predict / chain / zone / `NormalizeRequest` callers pass nil until predict is scoped. Engine-internal uses that no request names stay unscoped: `FilterToFile`'s raw expression still compiles through `FILTER_EXPRESSION` and therefore fails when that operator is hidden; synth fidelity (`synth_fidelity.go`) runs `TEST_KS` / `TEST_CHISQ` on an unscoped processor.

**Regressions.** `regression.BuildWith` / `BuildStreamingWith` / `FitBufferedWith` take a `regression.Resolver`; the processor passes `ExtensionRegistry.LookupRegression`, which misses a hidden type, so both orchestrators raise their own `unknown regression type` error. `canStream` treats a hidden type as non-streamable (a never-registered type's `Streamable()` is false), so a hidden `REG_OLS` routes buffered too.

**Overlay kinds — route, don't re-word.** The five per-host handler tables are globals, so the hide is a ROUTE: `ExtensionRegistry.overlayRoute(kind)` returns the authored kind, or `""` (a kind no table, switch or types catalog knows) when hidden. Every kind-keyed decision keys on the route while messages and details keep naming the authored kind: the MATRIX table + the `OVERLAY_FORMULA` special case + the runtime Level/Within gate (`ApplyOverlaysWithExtensions`), the pairwise slab gate, the SERIES table + YoY frequency promotion + `canStreamOverlays` (so a streamable hidden kind routes buffered), the CHAIN table (`ApplyChainOverlaysWithExtensions`), the FACET table (`ApplyOverlaysFacetWithExtensions`), and the COMPOSE single- and multi-layer tables + panel slab gate + MATRIX-shape gate (`checkSlotShapeAndSchemaRouted`). A never-registered Compose kind SUCCEEDS with an empty stub layer, so a hidden one does too. The parallel shard / decode reducers fold SERIES overlays in the shared grouped tail, which carries the registry (`GroupedTail.Extensions`). The request-time descriptor gates take `PredictOptions.Instance` (`PredictOptions.overlayRoute`): the Request-host catalog probe and its Level/Within rules, the Compose gate 0 and per-spec cost, the Facet catalog probe (`ValidateFacetOverlaysWithOptions`). `types.ResolveBuiltinGroupFanOut` is unreachable for a hidden grouper: both slab gates run after their host materialised, which already resolved every grouper.

**Parity harness.** `runHiddenParity` (root `feature_parity_harness_test.go`, run by `TestHiddenOperatorParity`) drives fixture × category × entry point — Process, ProcessStream, Compose, ComposeParallel, ProcessChain stage 0 and stage 1, FacetSchema (plain and `additive_fields`), FilterToFile, plus the slots a composed / chain request carries itself (`Compose/overlays`, `ComposeParallel/overlays`, `ProcessChain/overlays`; `feature_parity_overlay_test.go` adds the REG and per-host OVERLAY categories); Facet and Watch carry no operator name. A category whose never-registered name succeeds (the Compose stub, the SERIES fold on an ungrouped host) says why in `neverOK`; its outcome is still byte-compared. Each cell byte-compares a hidden built-in with a never-registered name on the profiled instance after substitution, and requires the hidden name to behave DIFFERENTLY on an unprofiled control (else the cell is vacuous; `vacuousOK` records the by-design ones, e.g. slots the chain gate refuses for every name). Extend it with a `parityCategory` (an applier per payload) or a `parityEntryPoint`.

Fixture profiles for goldens live at `descriptor/testdata/profiles/{minimal,survey-crosstab,empty}.json` (a subdirectory, so `TestGoldensNotHandEdited` does not read them); `TestFeatureSet_FixturesLoad` keeps each one valid at `pulse.New`.

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
- **Extension operators omitted from a profile are hidden** (done in U05: dropped at `pulse.New`; documented in the embedder docs).
- The stored-profile accessor is `p.FeatureProfile()` (U05). The profile-tooling CLI naming (`pulse profile create` collision) is open for U06.

# Observability (U20)

Embedder-facing contract for how a host watches a Pulse instance: structured logs, hooks, metrics and the two adapter modules. Observability is **behaviour, not a feature**: no `features.go` row, absent from `feature_set_digest`, `format_version` stays `"1.1"`, no runtime skill (an embedder-only concern), no `PULSE_*` env var. Guide for embedders: `docs/src/library/observability.md`.

## Shape

Three independent `pulse.Options` fields, each nil by default:

- `Logger *slog.Logger` — stdlib `log/slog` only. Never `slog.Default()`; a nil Logger is silent.
- `Hooks *observe.Hooks` — `OnOperationStart` / `OnPhase` / `OnOperationEnd`, each independently optional.
- `Metrics observe.Metrics` — an instrument factory (`Counter` / `Histogram` / `UpDownCounter`).

Observability is ON iff any of the three is set (`p.observing()`); then each operation pays one request-hash and one clock read. **Off is allocation-identical to pre-U20** (`TestObservabilityOffNoAllocs`). No per-row clock read ever. The vocabulary lives in the leaf public package `observe` (stdlib only, `TestObserveImportBoundary`); the facade instruments through the private `p.observe` / `observed` helper (`observability.go`), never `Service.BoundRequest` (it nests per slot).

## Operation kinds, phases, arms, scopes

Generated from `observe.AllOperationKinds()` / `AllPhases()` / `AllArms()` / `AllScopes()`. `TestObservabilityDocCoversEnums` fails if a value is missing here.

- **OperationKind** (several methods share a kind: `Inspect` / `InspectEnvelope` / `InspectBytes` are `inspect`; `Import*` are `import`): `open`, `process`, `process_stream`, `compose`, `compose_parallel`, `process_chain`, `facet`, `facet_schema`, `lookup`, `count_records`, `inspect`, `predict`, `manifest`, `payload_schema`, `import`, `export`, `convert`, `imports_sweep`, `drop`, `sample`, `filter_to_file`, `synth`, `synth_stream`, `profile`, `dedup`, `widen`, `index_build`, `index_verify`, `index_list`, `index_drop`, `shard_create`, `shard_add`, `shard_remove`, `shard_list`, `shard_extract`, `shard_compact`, `shard_verify`, `template_render`, `template_reload`.
- **Phase** (execution order; reported only when it ran): `plan`, `open`, `decode`, `scan`, `reduce`, `post`, `overlay`, `shape`.
- **Arm** (`""` = no request engine ran): `buffered`, `streaming`, `fused_crosstab`, `parallel_decode`, `shard_parallel`, `join`.
- **Scope**: `top` (host call), `child` (a Compose slot or ProcessChain stage Pulse fired).
- **Code** (`OperationResult.Code`): `ok`, `uncoded` (plain Go error), else a registered error code. Never a message.

**Not operations** (no `OperationKind`, deliberately — the interview locked the kind list): the in-memory getters (`Ontology`, `Skills`, `Skill`, `ListTemplates`, `GetTemplate`, `Limits`, `LabelTables`, `RangeTables`, `ErrorLookup`, `ErrorsByDomain`, `ErrorsSearch`, `ExamplesSearch`, `ExampleGet`, `FeatureProfile`, `FeatureSetDigest`, `Fs`, `ResolveLabel`) and the filesystem-touching `CohortArtifacts`, `NewCohortBuilder` (its `Add` / `Create` fire `shard_add` / `shard_create` through the public calls), `Imports`, `ResolveImport`, `ResolveCanonicalSchema`, `ApplySeriesOverlays`, `InvalidatedSidecars`, `Watch*`. Reason: each is either pure memory (nothing to time) or a builder/handle whose real work reaches an instrumented call; instrumenting them would add label values without information. `TestObservedMethodsCoverSurface` forces any NEW exported `*Pulse` method into `obsCalls` or `uninstrumentedMethods`, so none skips silently.

## Logs

Stable **attribute keys** (the message text is NOT covered by the stability promise): `op`, `cohort` (when the operation addresses one), `request_hash` (when it takes a request), `duration_ms` (float64), `rows_scanned` (only when > 0), `code`; Debug plan record adds `arm`, `workers`, `shards`, `projected_fields`; `pulse.New` logs op `new` + `template_dirs`.

| Level | Record | When |
|---|---|---|
| Debug | `pulse: execution plan` | an operation that reached an execution arm (written by the facade from the carrier snapshot; not inspect/predict/lookup/facet/sample) |
| Info | `pulse: operation completed` | `index_build`, `index_drop`, `shard_create/add/remove/compact` — NOT `shard_extract`, NOT `widen`. `imports_sweep` and `template_reload` log from their own packages (they also see sweeps/rescans no facade call triggers); the facade `template_reload` logs only on failure; the MCP cohort scan logs op `mcp_cohort_scan` |
| Info | `pulse: instance ready` | `pulse.New` |
| Warn | `pulse: operation warning` | one per result warning, `code` only |
| Warn | `pulse: limit exceeded` | a `PULSE_LIMIT_EXCEEDED` trip: `limit`, `configured`, `observed` only |
| Error | `pulse: operation failed` | a failed operation, by `code` only (a limit trip logs both) |
| Warn | `pulse: observability hook panicked` | `hook` + `op` only |

**Privacy rule.** Logs carry identifiers, counts, timings, enums and error `code` — never an error message or `details` (beyond the limit triple), an expression, a filter value, a dictionary entry or a record. Warnings are reported by code only, and **string-only warnings (Facet, tests, synth) are skipped** because their text can echo values. Warning carriers: `Response` (+ overlay layers), `ComposedResponse`, `ChainResponse`, `SampleResult`, `Envelope`, shard results, Import/Export/Convert reports, `DedupResult` (`resultWarningCodes`). Enforced by `TestObservabilityNoRowData`. Logs go to **stderr only**: the CLI writes there and MCP stdout is JSON-RPC.

## Hook contract

- `OnOperationStart(ctx, info) ctx` fires before any work; a non-nil returned ctx replaces the operation's context for the work, `OnPhase` and `OnOperationEnd` (the place to start a span).
- `OnPhase` fires **at operation end**, once per phase that ran, in execution order, just BEFORE `OnOperationEnd` — not in real time. Phases are contiguous laps summed per phase (a phase that ran in pieces reports the sum). Per-arm lists (pinned by `TestObservabilityPhases`): streaming `plan,open,scan`; buffered `plan,open,decode,scan,post[,overlay]`; fused crosstab `plan,open,scan`; crosstab buffered / join `plan,open,decode,scan` / `plan,open,scan`; shard-parallel and parallel-decode `plan,open,scan,reduce`; `+shape` when a `return` plan exists on Process/Compose/Chain. **Streams have no `shape` phase** (shaping is per row inside `scan`); a stream's `scan` includes backpressure/consumer time once the 4-chunk buffer fills. Join's `open` includes the right-side hash build; shard-parallel's `open` is the archive read; parallel-decode's `open` is the mmap.
- `OperationInfo`: `Kind`, `Scope`, `ID` (never 0), `Parent`, `Index`, `Cohort`, `RequestHash`. `OperationResult`: `Duration`, `Code`, `RowsScanned/Matched/Out`, `BytesRead`, `Shards`, `Workers`, `Arm`, `Projected`.
- **Children.** Compose / ComposeParallel slots and ProcessChain stages are child operations with `Kind` = the PARENT's kind (a compose child, not `process`), `Scope` child, `Parent` = the parent ID, `Index` = slot/stage. Each has its own exec carrier. **Parent result aggregates**: rows and bytes summed, `Workers` / `Shards` the max, `Projected` any, `Arm` the children's arm if they all agree else `""`. Children log only Debug plan records; failure and warning records come from the parent once. Parent phases: Compose `plan[,overlay][,shape]`; Chain `plan,open[,overlay][,shape]`.
- **Carrier deviation (FR-9).** The per-op exec carrier is installed whenever ANY of Logger/Hooks/Metrics is set (the spec said Hooks/Metrics only) because the Debug plan record reads it. Rows come from `Response.Metadata`, independent of `Components`; `bytes_read` counts through a counting `afero` wrapper after `Open` plus mmap regions (the Open-time header/schema read is not counted; shard-parallel counts the whole archive). Lookup, facet and sample stamp no arm.
- **Streaming end.** `ProcessStream` wraps its iterator: the operation ends on exhaustion, a `Next` error, `Close`, or ctx cancel — whichever comes first. `ProcessStreamResult` / `SynthStream` end in the producer goroutine just before the terminator is sent. Cancel ⇒ code `uncoded`. **A leaked iterator (never drained, closed or cancelled) never ends** — its in-flight gauge stays up. Think-time between reads counts toward `scan`.
- **Panics.** A hook panic is recovered per call; the operation's outcome is unchanged; logged by hook + op only (never the panic value) and counted in `pulse_hook_panics_total{hook}` (`start`, `end`, `phase`). Panic recovery of the operation itself is U34, not this.
- Hooks run synchronously on the operation's goroutine and must be fast and non-blocking.

## Metrics

Eight metrics, names unexported (`observability_metrics.go` `metricNames`), HELP text in `obsprom.Help`. Values are float64. Label values come only from closed enums (kinds, scopes, phases, limit names from `limits.Names()`, hook names, `errors.AllCodes()`), so cardinality is bounded.

| Metric | Type | Labels | Scope |
|---|---|---|---|
| `pulse_operations_total` | counter | `op`, `code`, `scope` | top + child |
| `pulse_operation_duration_seconds` | histogram | `op`, `scope` | top + child |
| `pulse_phase_duration_seconds` | histogram | `op`, `phase` | top-level only |
| `pulse_rows_scanned_total` | counter | `op` | top-level only |
| `pulse_bytes_read_total` | counter | `op` | top-level only |
| `pulse_limit_trips_total` | counter | `limit` | top-level only |
| `pulse_hook_panics_total` | counter | `hook` | — |
| `pulse_operations_in_flight` | up-down | `op` | top-level only |

**Counting scope.** A parent already aggregates its children, so only operations_total and operation_duration_seconds count children (split by `scope`); the rest count top-level only to avoid double counting. A code not in `errors.AllCodes()` counts as `uncoded`; a limit name outside `limits.Names()` is not counted.

**Factory contract (`observe.Metrics`).** Pulse calls the factory **at most once per name+label tuple** and caches the result. Small-label-space instruments and `operations_total{code="ok"}` (~600 calls) resolve while building the instance; `operations_total` for a non-ok code resolves **lazily** on first use, so the factory **may be called after `New`, from any goroutine, and MUST be safe for concurrent use**. Pre-resolving all codes would cost ~22k factory calls per `New`; the lazy split keeps `New` with metrics ≈0.33 ms / 0.40 MB (`TestObservabilityMetricsNewCost`, `TestObservabilityMetricsLazyConcurrent`, `BenchmarkNewWithMetrics`). Bucket boundaries are the adapter's choice.

**`internal/obsprom`** (stdlib Prometheus text 0.0.4; backs `pulse mcp --metrics-addr`): prints HELP + TYPE for every family always, but a **series only after its first write** (Pulse pre-resolves thousands of instruments; printing them at zero would bloat every scrape); histogram buckets allocate on first `Observe`. `DefaultBuckets` = 0.5 ms–60 s, 16 bounds (`0.0005 … 60`), `le` inclusive. A negative counter delta is ignored; an invalid name or a kind clash yields a no-op instrument. `Listen(addr, reg)` binds synchronously (a bad or busy address fails there), serves `GET /metrics` only (404 otherwise). **`/metrics` has NO authentication** — bind loopback or front it; no port opens unless `--metrics-addr` is given.

## CLI and MCP

Root persistent flags `--log-level off|debug|info|warn|error` (default `off`) and `--log-format text|json`, stderr only; an unknown value is `CLI_INPUT`. `pulse mcp --metrics-addr HOST:PORT`. Leaves that never build a `*pulse.Pulse` (`import`, `convert`, `skills`, `errors`, `version`, `features`) accept the log flags but emit nothing. The `pulse mcp` startup line (stderr, after `gosdk.Register`) names the data dir, bind-on-open, cohort-scan, effective `return` preset (`(custom)` for include/exclude-only), log level and the metrics address; `mcpserve.ServeInfo` carries `DefaultReturn` and `Limits []descriptor.LimitMeta` (so it is no longer `==`-comparable). The test-only helper env `GO_WANT_PULSE_CLI_HELPER` is not a `PULSE_*` var.

## Adapter modules (`contrib/`)

`contrib/otelpulse` and `contrib/prompulse` are nested Go modules (own `go.mod`; no `go.work`). The core module takes no OpenTelemetry or Prometheus dependency (`TestCoreModuleNoObservabilityDeps`, root `core_module_deps_test.go`: `go.mod` requires and `go list -deps -test ./...`).

- **otelpulse**: `Hooks(tp trace.TracerProvider)` (spans) and `Metrics(mp metric.MeterProvider)`; symbols `SpanName`, `ScopeName`, `Attr*` keys, `DefaultBuckets`. Span name `pulse.<kind>`; a child span is `pulse.process` with the parent kind in `pulse.op`, `pulse.scope=child`, `pulse.index`. Attributes: `pulse.op`, `pulse.scope`, `pulse.cohort`, `pulse.request_hash`, `pulse.code` (every span), `pulse.error_code` (+ span status Error on failure), `pulse.arm`, `pulse.rows_scanned/matched/out`, `pulse.bytes_read`, `pulse.shards`, `pulse.workers`, `pulse.projected`; zero counters are omitted. Phases are span events (`pulse.phase`, `pulse.phase.duration_ms`) timestamped operation start + the summed earlier phase durations — APPROXIMATE, since phases are reported at end.
- **prompulse**: `Metrics(reg prometheus.Registerer, ...Option)`, `WithBuckets`, `DefaultBuckets`. Series appear on first write (children created lazily), vectors are reused on a shared registry, a name clash degrades to a no-op instrument. Both adapters' Help text and buckets are pinned equal to obsprom's (`parity_internal_test.go` in each, importing `pulse/internal/obsprom` legally from inside the tree).
- **Versioning.** Lockstep: tag `contrib/<module>/vX.Y.Z` = the root tag. On `main` each contrib `go.mod` keeps `require github.com/frankbardon/pulse v0.0.0` + `replace => ../..` (as `internal/embeddersmoke`). A Go consumer ignores a dependency's `replace`, so a tag must `require` the published root tag: `scripts/release-contrib.sh TAG [--push]` builds a **detached** release commit that bumps the requires and tidies, verifies each module builds against the published root with the `replace` dropped (`GOPROXY=direct`), then atomically pushes both contrib tags. It runs from `release.yml`'s `contrib` job after the root release. The bumped commit is never on a branch; contrib tags get no GitHub Release; the script is idempotent (re-run after a failure, or run locally with `--push`).
- **Build.** `make contrib` per module: `go mod tidy -diff`, vet, staticcheck, test (CI step; no `-mod=mod`, so a stale `go.mod` fails). `make contrib-tidy` re-tidies. **A dependabot bump of a ROOT dependency fails `make contrib` until `make contrib-tidy` runs in that PR**; contrib's own deps ride `.github/dependabot.yml` entries for each module dir.

## Gates (PRD FR-30, non-negotiable)

All four iterate `obsCalls()`, which `TestObservedMethodsCoverSurface` keeps complete, so a new operation is covered without editing them. Each records how it was falsified in its doc comment.

- `TestObservabilityDefaultsSilent` — default Options write nothing (stdout, stderr, default slog, std log) and never enter the observed path.
- `TestObservabilityNoRowData` — everything on at Debug: no record value, dictionary entry, filter literal, expression or error message reaches any output (logs, hook payloads, metric labels).
- `TestHookPanicRecovered` — a panic in any hook (start/end/phase) leaves every outcome unchanged and is reported by hook + op only, and counted in `pulse_hook_panics_total`.
- `TestObservabilityOffNoAllocs` — the observe helper allocates 0 off; `process` 164, `count_records` 45, `predict` 236, `inspect` 66 allocs/run pinned to pre-U20 `406c6b7c`. **Under `-race` only the helper's zero-alloc check applies** (the race runtime adds allocations; the pins run in plain builds, i.e. CI's `make test`). A moved pin: re-measure on the merge-base first; if only your change moves it, put the work behind `p.observing()` rather than re-pinning.

Beside them: `TestObservedMethodsCoverSurface`, `TestObserveFiresStartEndOncePerOperation`, `TestObserveChildOperations`, `TestObservabilityPhases`, `TestObserveImportBoundary`, `TestObspromImportBoundary`, `TestCoreModuleNoObservabilityDeps`, `TestObservabilityDocCoversEnums` (this file lists every enum value and metric name).

## Update rules

A new `OperationKind` / `Phase` / `Arm`, metric, log attribute key or hook field updates: `observe/observe.go` (+ the `All*` list), this file, `docs/src/library/observability.md`, `contrib/otelpulse` and `contrib/prompulse` (and their parity tests), `internal/obsprom` HELP/buckets, and `obsCalls()` for a new instrumented method. A new `OperationKind` also widens `pulse_operations_total` cardinality — bounded, but state it.

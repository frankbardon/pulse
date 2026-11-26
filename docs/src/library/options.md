# pulse.New & Options

**Audience:** Go embedders constructing a `Pulse` instance.

`pulse.New(pulse.Options{...})` is the single entry point. There is no
config file, no init function, no global state. Every option is
declared in code (or comes from `PULSE_DATA_DIR` when the field is
left empty).

> **LLM agents using MCP:** the MCP server constructs its own
> `Pulse` instance from CLI flags. Agents don't see this surface.

## The Options struct

From [`pulse.go`](https://github.com/frankbardon/pulse/blob/main/pulse.go):

```go
type Options struct {
    // DataDir is the base directory for cohort files.
    // Defaults to PULSE_DATA_DIR if empty and FS is not set.
    DataDir string

    // FS is an optional custom filesystem.
    // When set, DataDir is ignored for filesystem construction.
    FS afero.Fs

    // DisableDefaults turns off the smart-defaults pass that infers
    // operator Type from the named field's schema type when the caller
    // omits it. Defaults to false (defaults enabled). Predict still
    // computes and reports DefaultsApplied independently — this flag
    // governs only what the runtime mutates on the live request.
    DisableDefaults bool

    // DisableCrosstabFusion forces every crosstab request onto the
    // buffered path, skipping the fused in-decode streaming arm.
    DisableCrosstabFusion bool

    // DefaultTimeZone is the engine-wide IANA zone ("UTC" or an
    // Area/Location name) a zone-capable slot falls back to. Empty = UTC.
    DefaultTimeZone string

    // DefaultWeight is the row weight a weight-bearing slot inherits
    // when neither the slot nor the request names one. Nil = none.
    DefaultWeight *types.WeightSpec

    // FeatureProfile / FeatureProfileFile declare the instance's
    // feature set (at most one of the two). Nil / empty = no profile.
    FeatureProfile     *FeatureProfile
    FeatureProfileFile string

    // Limits bounds runaway requests (groups, crosstab cells, memory
    // estimate, matrix dim, compose/chain fan-out, join build rows,
    // request timeout). Zero field = default; pulse.Unlimited (-1) = none.
    Limits Limits

    // ... further fields: see "Other fields" below.
}
```

## Field reference

### `DataDir string`

The base directory for `.pulse` files. Relative cohort paths
(`{"filename": "data.pulse"}`) resolve against this directory.

| Source | Result |
|---|---|
| Non-empty `Options.DataDir` | Used directly |
| Empty + `FS` non-nil        | `DataDir` is ignored — the FS is the trust boundary |
| Empty + `FS` nil            | Pulse falls back to its default OS filesystem, rooted at `PULSE_DATA_DIR` |

A relative directory — in `DataDir` or in `PULSE_DATA_DIR` — is made
absolute against the working directory once, at `pulse.New`
(`filepath.Abs`). A later `os.Chdir` does not move the instance's root.

Example:

```go
p, err := pulse.New(pulse.Options{DataDir: "/var/data/pulse"})
```

### `FS afero.Fs`

A custom `afero.Fs` implementation. When set, it fully overrides the
filesystem layer — `DataDir` is unused, and `PULSE_DATA_DIR` is not
consulted. Use this for tests (`afero.NewMemMapFs()`) or non-local
backends (S3-backed `afero.Fs`, encrypted overlays, ...).

Example:

```go
import "github.com/spf13/afero"

p, err := pulse.New(pulse.Options{
    FS: afero.NewMemMapFs(),
})
```

See [Custom Filesystems](custom-fs.md) for in-depth usage and the
hermetic-test pattern.

### `DisableDefaults bool`

The runtime smart-defaults pass infers an operator's `Type` from the
named field's schema type when the caller omits it (e.g. `AGG_SUM` on
a numeric field defaults appropriately; categorical fields default
toward `AGG_COUNT`). Set `DisableDefaults = true` to require an
explicit `Type` on every aggregation and grouper — useful when you
want the request to be source-of-truth and never be silently
re-typed.

This option only governs the runtime mutation. `predict` independently
computes and reports `DefaultsApplied` in its result envelope, so
callers can see what would have been inferred even when defaults are
disabled.

CLI parity: `pulse api process --no-defaults`, `pulse api compose
--no-defaults`.

### `DisableCrosstabFusion bool`

A crosstab request the fusion gate admits normally runs on the fused
arm: the row × column grid is built in-decode, without materialising
the filter-passing record set. Set `DisableCrosstabFusion = true` to
force every crosstab onto the buffered path instead. Output is
identical either way — fusion is a peak-heap optimisation — so this is
a diagnostic and benchmarking knob, for example to compare fused and
buffered memory on the same request. It is fixed at `pulse.New` time;
there is no per-request override. `Predict` / `PredictEnvelope` / `PredictBytes` honour it:
`PredictResult.CrosstabFusable` answers `false`, with the knob named in
`CrosstabFusionReasons`. Without it, those fields report the fusion
gate's own answer and every reason it declines.

```go
p, err := pulse.New(pulse.Options{DisableCrosstabFusion: true})
```

### `DisablePanicRecovery bool`

By default a panic raised inside an operation (a faulty extension operator, an engine bug) is recovered and returned as a `PROCESSING_PANIC` coded error (`details.op`, `details.panic_type`, `details.stack`; never the panic value), and the instance stays usable. Set `DisablePanicRecovery = true` to re-panic with the original value instead, for a debugger, a test, or a supervisor that restarts the process. It is per instance, fixed at `pulse.New`, and library-only: the CLI and `pulse mcp` always recover. See [Observability](observability.md#panics-inside-an-operation).

```go
p, err := pulse.New(pulse.Options{DisablePanicRecovery: true})
```

### `DefaultTimeZone string`

The zone a zone-capable slot falls back to when neither its own `tz`
nor the request's `time_zone` names one — precedence is slot `tz` →
`time_zone` → `DefaultTimeZone` → `UTC`. The name must be exactly `UTC`
or an IANA `Area/Location` zone (`Europe/Berlin`); anything else
(`EST`, `Local`, `+05:00`) fails `pulse.New` with
`PULSE_TIMEZONE_UNKNOWN`. Names resolve only from Pulse's own embedded
copy of the tz database (never `$ZONEINFO` or the host's zoneinfo
files), so the accepted set and every offset are identical on every
host, and the lookup is case-sensitive everywhere.

Where it reaches a `datetime` field, the zone-aware operators read each
instant on its local calendar day in this zone (DST-correct). A request
in which a non-UTC default reaches a derived field (absent from the
schema) is refused with `PROCESSING_CONFIG` rather than silently
ignored. It does not apply to `date` fields at all. Predict reports the
resolved zone per slot under `time_zones`, with `source: "options"` when
this field supplied it. There is no env var or CLI flag for it. Full
guide: [Time Zones](time-zones.md).

```go
p, err := pulse.New(pulse.Options{DefaultTimeZone: "UTC"})
```

### `DefaultWeight *types.WeightSpec`

The row weight a weight-bearing slot (aggregations, the crosstab cell
and margin aggregations, tests, regressions, attributes, overlays)
falls back to when neither its own `weight` nor the request's `weight`
names one — precedence is slot `weight` → request `weight` →
`DefaultWeight` → none, and a slot's `weight: null` opts that slot out.
`pulse.New` refuses a spec with no `Field` or a `Kind` other than
`probability` (the default when empty) / `frequency` with
`PROCESSING_CONFIG`. The field is judged per request, only where the
default actually applies: there it must exist in the cohort and be an
unsigned-integer or float column. It reaches Process, Compose slots and
ProcessChain stages, never `Facet` / `FacetSchema`. Predict reports the
resolved weight per slot under `weights`, with `source: "options"` when
this field supplied it. There is no env var or CLI flag for it. Full
guide: [Row Weighting](weighting.md).

```go
p, err := pulse.New(pulse.Options{DefaultWeight: &types.WeightSpec{Field: "wt"}})
```

### `DefaultMultiplicity *types.Multiplicity`

The multiple-comparison correction a test or inferential overlay falls
back to when neither its own `multiplicity` block nor its request's
(nor, in Compose, the `ComposedRequest`'s) names one. Each of `method`,
`family` and `alpha` falls through on its own: slot, request,
`ComposedRequest`, `DefaultMultiplicity`, none. `pulse.New` validates it
(`PULSE_MULTIPLICITY_INVALID`); a family the surface does not offer
falls back to the surface default rather than failing. It reaches
Process, Compose slots, ProcessChain stages and Facet overlays. Unset
(the default) corrects nothing. There is no env var or CLI flag for it. Full guide:
[Multiple-Comparison Correction](multiplicity.md).

```go
p, err := pulse.New(pulse.Options{DefaultMultiplicity: &types.Multiplicity{Method: types.MultiplicityMethodHolm}})
```

### `DefaultReturn *types.Return`

The response selection (`preset`, `include`, `exclude`, `precision`) a
request without its own `return` block is shaped by. A request block
replaces it entirely, never merging. Precedence: the request's `return`,
then `DefaultReturn`, then the feature profile's `return`, then `full`
(the whole response, unchanged). `pulse.New` refuses a bad preset,
precision or path (`PULSE_RETURN_INVALID` / `PULSE_RETURN_PATH_UNKNOWN`),
including a path only a feature the instance hides has. Data-column paths
(`data[*].<column>`) are not checked at `New`; one that matches nothing
draws a `PULSE_RETURN_PATH_UNMATCHED` warning. `DisableComponents` folds
into this layer: while components are off for a request, the selection
also excludes `components`. A request `return` replaces the default
but never the engine switch: with `DisableComponents` on, components stay
off unless the request sets `disable_components: false`. Applies to `Process`, each Compose slot and each chain stage. There is no env var
or CLI flag for it.

```go
p, err := pulse.New(pulse.Options{DefaultReturn: &types.Return{Preset: types.ReturnPresetStandard}})
```

### `FeatureProfile *FeatureProfile` / `FeatureProfileFile string`

Declare the instance's feature set: a Go value, or a JSON file read
through the instance filesystem (relative to `DataDir` or `FS`). Set at
most one; both is `PULSE_FEATURE_PROFILE_INVALID`. `pulse.New` validates
the profile and stores it. The instance then hides every feature the
profile omits, and its `behaviour` switches are ORed into the matching
`Disable*` fields (a profile can turn a switch on, never off).
`pulse.New` never reads the `PULSE_FEATURE_PROFILE` env var — only
`pulse mcp` / `mcpserve.NewPulse` do. Full contract:
[Feature Profiles](feature-profiles.md).

```go
p, err := pulse.New(pulse.Options{
    DataDir:            "/var/data/pulse",
    FeatureProfileFile: "profiles/self-serve.json",
})
```

### `Limits Limits`

The instance resource limits: `RequestTimeout`, `MaxGroups`,
`MaxCrosstabCells`, `MaxEstimatedMemory`, `MaxMatrixDim`,
`MaxComposeSlots`, `MaxChainStages`, `MaxJoinBuildRows`,
`MaxDerivedColumns`, `MaxExpandedItems`, `MaxMMMGeos`,
`MaxMMMDrawValues`, `MaxDisclosureComparisons`. Per field, `0`
means the default (`pulse.DefaultMaxGroups`, …), `pulse.Unlimited` (`-1`)
means no limit, any other negative fails `pulse.New` with
`PULSE_LIMIT_INVALID`. Precedence per field: a non-zero `Options.Limits`
value, then the feature profile's `limits`, then the default. Read the
effective values with `p.Limits()`. A breach is `PULSE_LIMIT_EXCEEDED`
with `{limit, configured, observed, option}`. Defaults, checks and
reading a breach: [Tuning Limits](tuning-limits.md).

```go
p, err := pulse.New(pulse.Options{Limits: pulse.Limits{MaxGroups: 1_000_000}})
```

### `ShardDistinctLimit int` / `DisableShardDistinct bool`

Every shard archive write (create, add, remove, compact, `cohort filter`
to an archive) records, per shard, the exact count of distinct values of
each `u32`, `u64`, `date`, `datetime`, `f32` and `f64` field in the
archive's `_schema.pulse`, so predict can bound grouper keys without
reading records. `ShardDistinctLimit` caps the keys held per field per
shard while counting: `0` means the default 4,194,304 (up to 64 MiB of
keys per counted field, for the one shard being counted), past it the
shard is recorded as "more than the cap", and a negative value fails
`pulse.New` with `SERVICE_VALIDATION`. `DisableShardDistinct` records
newly written shards as opted out and reads nothing to count them
(create and compact then read each shard once). Single-file cohorts never
carry counts. Detail: [Managing a Shard
Archive](../internals/managing-shard-archives.md#per-shard-distinct-counts).

## Other fields

One line each; the GoDoc on
[`Options`](https://pkg.go.dev/github.com/frankbardon/pulse#Options) is
the full reference.

| Field | What it does | More |
|---|---|---|
| `Extensions` | registers extension operators, expression functions, label / range / lookup tables, populations, skills and examples | [Writing Operators (extend)](extend.md), [Label, Range and Lookup Tables](tables.md) |
| `Logger`, `Hooks`, `Metrics` | structured logs, operation callbacks, metric instruments; nil records nothing | [Observability](observability.md) |
| `DisableComponents` | omits `Response.Components` (a request's `disable_components` overrides) | [Response Shaping](response-shaping.md) |
| `DisableProjection` | decodes every field instead of only the fields the request reads; output is identical | |
| `ProjectBufferedFields` | deprecated no-op (projection is on by default) | |
| `ShardWorkers`, `DecodeWorkers` | worker caps for the per-shard and parallel-decode arms; `0` = one per CPU, `1` = serial | [Tuning Limits → Worker counts](tuning-limits.md#worker-counts) |
| `Strict` | promotes request-validation warnings to errors and refuses a partial import (`PULSE_IMPORT_ROWS_DROPPED`) | |
| `EchoRequest` | fills `Envelope.Request` with the normalised request that ran | |
| `SuppressAdvisories` | `PULSE_ADVISORY_*` codes predict never reports; an unknown code is `PULSE_SUPPRESS_ADVISORY_UNKNOWN` | [Guided Analysis](guided-analysis.md) |
| `AutoLabels` | label bindings applied to every read request whose cohort has the field as a categorical | [Label, Range and Lookup Tables](tables.md) |
| `LabelTablesDir`, `RangeTablesDir`, `LookupTablesDir`, `PopulationsDir` | directories loaded at `pulse.New` (env fallbacks below) | [Label, Range and Lookup Tables](tables.md) |
| `TemplateDirs` | request-template roots, first root wins | [Request Templating](request-templating.md) |
| `ImportsDir`, `ImportTTL` | managed-import directory and default TTL (negative pins) | [Readers, Writers & Jobs (io)](io.md) |
| `ImportSourceFS`, `ImportSourceJailRoot` | where an absolute import source path is read from, and the directory it is confined to (default: the working directory) | |
| `SetInferenceMinPct` | share of sampled cells that must hold a delimiter for an import to infer a `set_*` field (default 30) | |

## Defaults at a glance

| Field omitted from `Options` | Effective behaviour |
|---|---|
| `DataDir` and `FS` both empty | Pulse roots its OS filesystem at `PULSE_DATA_DIR`; unset, `pulse.New` fails with `SERVICE_VALIDATION`. |
| `DataDir` only                | Uses an `afero.NewBasePathFs(afero.NewOsFs(), DataDir)`. |
| `FS` only                     | Uses the provided FS verbatim. |
| Both                          | `FS` wins; `DataDir` is ignored. |
| `DisableDefaults` omitted     | Defaults enabled. |
| `DisableCrosstabFusion` omitted | Fusion engages whenever the gate admits the crosstab. |
| `DefaultTimeZone` omitted     | Zone-capable slots without `tz` / `time_zone` resolve to `UTC`. |
| `ShardDistinctLimit` / `DisableShardDistinct` omitted | Archive writes count distinct values per shard, capped at 4,194,304 keys per field. |
| `Limits` omitted              | Count limits at their high defaults; `RequestTimeout` and `MaxEstimatedMemory` unlimited. |
| `FeatureProfile` / `FeatureProfileFile` omitted | No profile: every feature. |

## Environment variables

Every `PULSE_*` variable Pulse reads is listed here (`TestEnvVarsDocumented`
fails when one is missing). An `Options` field always overrides its variable.

| Variable | Effect |
|---|---|
| `PULSE_DATA_DIR` | Base directory for `.pulse` cohorts, used by the default filesystem. The only required variable; bypass with `Options.DataDir` or `Options.FS`. |
| `PULSE_IMPORTS_DIR` | Managed-imports subdirectory under the filesystem root (default `imports`). |
| `PULSE_IMPORT_TTL` | Default TTL for managed imports: a Go duration (`24h`), a day form (`7d`), or `pin`. Default `7d`. |
| `PULSE_LABEL_TABLES_DIR` | `*.json` files load as label tables at `pulse.New`, keyed by file stem ([Label, Range & Lookup Tables](tables.md)); `Options.LabelTablesDir` wins. |
| `PULSE_RANGE_TABLES_DIR` | The same, for range tables; `Options.RangeTablesDir` wins. |
| `PULSE_LOOKUP_TABLES_DIR` | The same, for lookup tables (`lookup()`); parser `pulse.ParseLookupTableFile`; `Options.LookupTablesDir` wins. |
| `PULSE_POPULATIONS_DIR` | `*.pulse-population.json` files register as populations, named by file stem. |
| `PULSE_TEMPLATES_DIR` | Request-template roots, `os.PathListSeparator`-separated; the first root wins. |
| `PULSE_MCP_NO_COHORT_SCAN` | `pulse mcp` only (flag `--no-cohort-scan`): skip listing `.pulse` files as resources. |
| `PULSE_FEATURE_PROFILE` | `pulse mcp` / `mcpserve.NewPulse` only (flag `--feature-profile` wins): OS path to a feature profile. `pulse.New` never reads it. |
| `PULSE_BENCH_DIR` | Benchmarks only: cohort cache directory (default `<tmp>/pulse-bench`). |

A repo-root `.env` is loaded automatically by the build tooling.

## Re-using a Pulse instance

`Pulse` is safe for concurrent use across goroutines once constructed.
The internal registries are read-only after `New`; each `Process`
call constructs fresh stateful operators per request, so multiple
goroutines can call `Process`/`ProcessStream`/`Compose` in parallel
against the same `Pulse`.

For batch parallelism, prefer
[`ComposeParallel`](parallel-compose.md) — it shares the read-only
registries and bounds concurrency for you.

## Tearing down

There is no explicit `Close()` method on `Pulse`. The filesystem is a
borrowed handle; if you supply a custom `FS`, the embedder is
responsible for any cleanup that FS requires. Streaming consumers
should still call `RowIter.Close()` so that the underlying readers
release their buffers.

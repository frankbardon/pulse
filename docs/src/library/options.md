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

    // ... further fields (concurrency, projection, imports, labels,
    // templates) are documented on the Options type in pulse.go.
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
there is no per-request override. `Predict` / `PredictBytes` honour it:
`PredictResult.CrosstabFusable` answers `false`, with the knob named in
`CrosstabFusionReasons`. Without it, those fields report the fusion
gate's own answer and every reason it declines.

```go
p, err := pulse.New(pulse.Options{DisableCrosstabFusion: true})
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
guide: [Row Weighting](weighting.md).

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
(the default) corrects nothing and leaves every response
byte-identical. There is no env var or CLI flag for it. Full guide:
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
the profile and stores it. Its `behaviour` switches take effect now, ORed
into the matching `Disable*` fields; the feature list hides nothing yet.
`pulse.New` never reads the `PULSE_FEATURE_PROFILE` env var — only
`pulse mcp` / `mcpserve.NewPulse` do. Full contract:
[Feature Profiles](feature-profiles.md).

```go
p, err := pulse.New(pulse.Options{
    DataDir:            "/var/data/pulse",
    FeatureProfileFile: "profiles/self-serve.json",
})
```

## Defaults at a glance

| Field omitted from `Options` | Effective behaviour |
|---|---|
| `DataDir` and `FS` both empty | Pulse builds its default filesystem → reads `PULSE_DATA_DIR` env var. Errors if unset and the operation needs filesystem access. |
| `DataDir` only                | Uses an `afero.NewOsFs()` rooted at `DataDir`. |
| `FS` only                     | Uses the provided FS verbatim. |
| Both                          | `FS` wins; `DataDir` is ignored. |
| `DisableDefaults` omitted     | Defaults enabled. |
| `DisableCrosstabFusion` omitted | Fusion engages whenever the gate admits the crosstab. |
| `DefaultTimeZone` omitted     | Zone-capable slots without `tz` / `time_zone` resolve to `UTC`. |
| `FeatureProfile` / `FeatureProfileFile` omitted | No profile: every feature, byte-identical to a profile-free build. |

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

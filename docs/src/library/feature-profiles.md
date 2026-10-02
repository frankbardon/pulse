# Feature Profiles

**Audience:** Go embedders, and operators who run `pulse mcp`.

A **feature profile** declares exactly which features one `*pulse.Pulse`
instance offers: which capabilities, operators, I/O formats and
MCP-only surfaces exist for it. A profile is a complete, closed
allowlist. It never means "everything minus X", and a feature added in a
later Pulse release is not in an existing profile until you add it.

With no profile, an instance offers everything, including features added
in later releases. Its output is byte-identical to an instance built
before profiles existed.

> **Status (v1.0.0 alphas).** `pulse.New` parses, validates and stores a
> profile, and its `behaviour` switches take effect. **The feature list
> hides nothing yet.** Enforcement (request handling, manifest, payload
> schema, predict, errors) and MCP filtering land in later v1.0.0
> pre-releases. Writing and validating your profile now means it is
> ready when they do.

The name is "feature profile" because "profile" already means synthetic
data profiling (`Pulse.Profile`, `pulse profile create`).

## A profile file

```json
{
  "profile": "survey-self-serve",
  "written_with": "1.0.0",
  "features": [
    "capability:process",
    "capability:crosstab",
    "capability:facet",
    "AGG_COUNT",
    "AGG_FREQUENCY",
    "AGG_SUM",
    "GROUP_CATEGORY",
    "GROUP_DATE",
    "FILTER_INCLUDE",
    "TEST_CHISQ",
    "OVERLAY_SHARE_OF_ROW",
    "io_format:csv",
    "io_format:spss"
  ],
  "behaviour": { "disable_projection": true }
}
```

| Key | Required | Meaning |
|---|---|---|
| `profile` | no | Your own label. Informational; the `pulse mcp` startup line prints it |
| `written_with` | no | The Pulse version you wrote the profile against. Stored as-is, never compared, and never an error |
| `features` | **yes** | The exact feature names the instance offers. May be empty (`[]`), and must not repeat a name |
| `behaviour` | no | Engine switches the profile turns on (below) |

Any other key is refused. That includes `limits` and `return`, which are
reserved for later v1.0.0 units. Trailing data after the JSON object is
refused too.

## Feature names

| Kind | Spelling | Examples |
|---|---|---|
| operator | bare registered name | `AGG_SUM`, `GROUP_DATE`, `TEST_T`, `REG_OLS`, `OVERLAY_YOY`, and your extension operators |
| capability | `capability:<name>` | `capability:process`, `capability:compose`, `capability:process_chain`, `capability:facet`, `capability:sample`, `capability:crosstab`, `capability:joins`, `capability:stream`, `capability:watch`, `capability:filter_to_file`, `capability:lookup`, `capability:index`, `capability:shard`, `capability:import`, `capability:export`, `capability:dedup`, `capability:widen`, `capability:templates`, `capability:synth`, `capability:labels`, `capability:range_tables` |
| I/O format | `io_format:<name>` | `io_format:csv`, `io_format:parquet`, `io_format:spss`. One name covers both import and export |
| MCP extra | `mcp_extra:<name>` | `mcp_extra:cohort_resources`, `mcp_extra:prompt_bootstrap`, `mcp_extra:prompt_author_request` |

`pulse manifest --json` lists every operator name the running build
registers.

Rules:

- **Exact names only.** `TEST_*`, `capability:*` and other patterns are
  refused, never expanded.
- **One spelling per feature.** `operator:AGG_SUM` and `process` (without
  its kind) are refused, and the error suggests the right spelling.
- **A statistical test is one name.** `TEST_T` covers both its row-test
  and post-test forms.
- **Synth distributions are not features.** `capability:synth` gates all
  of them.
- **The core is always present and cannot be listed.** Open, inspect,
  predict, record counts, the manifest, the payload schema, skills,
  examples, errors lookup and cohort artifacts exist on every instance.
  Listing one is an error.
- **Extension operators are features.** List them by their registered
  name, like a built-in.

## Dependencies

Some features need others. A dependency is a list of groups, and at least
one name in every group must be enabled:

- Every operator, and `capability:crosstab` / `capability:joins`, needs a
  host that runs requests: `capability:process`, `capability:compose` or
  `capability:process_chain`.
- `capability:stream`, `capability:watch` and `capability:filter_to_file`
  need `capability:process`.
- An overlay kind needs a host that can run it: `capability:crosstab`
  for crosstab overlays, `capability:compose` for compose and series
  overlays, `capability:process_chain` for stage overlays,
  `capability:facet` for facet overlays.
- Some operators read another's results:

| Feature | Also needs |
|---|---|
| `OVERLAY_T_CELL`, `OVERLAY_Z_CELL`, `OVERLAY_T_VS_REF`, `OVERLAY_Z_VS_REF`, `OVERLAY_PAIRWISE_WELCH_T`, `OVERLAY_PAIRWISE_TWO_MEANS_Z` | `AGG_WELFORD` |
| `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` | `AGG_WEIGHTED_MEAN` |
| `ATTR_REG_FITTED`, `ATTR_REG_LEVERAGE`, `ATTR_REG_RESIDUAL` | `REG_OLS` |
| `OVERLAY_YOY` | `GROUP_DATE` |

`TEST_TUKEY_HSD` does not require `TEST_ANOVA_F`.

### Extension dependencies

Every extension operator registration has an optional `DependsOn`. Each
entry is a feature the operator needs, and all of them must be enabled
whenever the operator is:

```go
pulse.AggregatorRegistration{
    Name:      "AGG_ACME_SPREAD",
    DependsOn: []string{"AGG_WELFORD"},
    Factory:   newSpread,
}
```

Every `DependsOn` entry must name a feature this build knows: a built-in,
or another registered extension operator. This is checked at every
`pulse.New`, with or without a profile, so a typo fails at construction.
An enabled extension operator also needs a request host, like a built-in.

## Using a profile from Go

As a value:

```go
p, err := pulse.New(pulse.Options{
    DataDir: "/var/data/pulse",
    FeatureProfile: &pulse.FeatureProfile{
        Profile:  "survey-self-serve",
        Features: []string{"capability:process", "AGG_SUM", "GROUP_CATEGORY"},
    },
})
```

`Features` must be non-nil. Use `[]string{}` for an empty profile; a nil
slice is refused as a missing `features` key. `pulse.New` copies the
value, so changing it afterwards has no effect.

From a file read through the instance filesystem:

```go
p, err := pulse.New(pulse.Options{
    DataDir:            "/var/data/pulse",
    FeatureProfileFile: "profiles/self-serve.json", // relative to DataDir (or Options.FS)
})
```

Set at most one of `FeatureProfile` and `FeatureProfileFile`. For bytes
you hold yourself (a config service, an embedded file),
`pulse.ParseFeatureProfile(data)` applies the same strict decode and
returns a `*pulse.FeatureProfile` for `Options.FeatureProfile`. It only
decodes: names and dependencies are checked by `pulse.New`.

**`pulse.New` never reads `PULSE_FEATURE_PROFILE`.** Only the MCP server
entry point does (below).

## Behaviour switches

| Key | Turns on |
|---|---|
| `disable_defaults` | `Options.DisableDefaults` |
| `disable_components` | `Options.DisableComponents` |
| `disable_projection` | `Options.DisableProjection` |
| `disable_cohort_scan` | the MCP server's `DisableCohortScan` (no effect on the engine) |

Each switch is combined with `Options` by OR. A profile can turn a switch
on, and `Options` cannot turn it back off. Omitting a switch, or setting
it `false`, leaves it to `Options`.

## Errors

Validation runs in three classes, in order. It stops at the first class
that fails and reports every problem in that class, sorted. Look any
code up with `pulse errors lookup CODE`.

| Code | When | Read |
|---|---|---|
| `PULSE_FEATURE_PROFILE_INVALID` | The profile cannot be used as written | `details.reason`: `both_options_set`, `file_unreadable`, `malformed_json`, `unknown_key`, `missing_features`, `duplicate_feature` (the names are under `duplicates`). `details.path` names the file when one was read |
| `PULSE_FEATURE_PROFILE_UNKNOWN` | A name does not resolve | `details.unknown[]`, each with `name` and `reason`: `unregistered`, `pattern`, `wrong_kind` (see `did_you_mean`), `core_surface`, `newer_than_running` (see `since`; the running build is under `details.version`). An unknown extension `DependsOn` entry also carries `extension` and `category` |
| `PULSE_FEATURE_PROFILE_DEPENDENCY` | An enabled feature is missing what it needs | `details.unmet[]`, each with `feature` and `requires_any_of`. Add one name from the group, or remove the feature |

### Versions

Every built-in feature records the release that introduced it. A profile
naming a feature newer than the running Pulse fails with
`newer_than_running`, so a profile written for a newer release cannot
silently lose features on an older one. Only the running version's
`major.minor.patch` is compared, so `1.0.0-alpha.2` offers every `1.0.0`
feature. Development builds (`devel`, untagged builds) offer everything.
`pulse.Version()` reports Pulse's own version, even inside your binary.

## The MCP server

`pulse mcp --feature-profile FILE` loads a profile into the served
instance. Without the flag it reads the `PULSE_FEATURE_PROFILE`
environment variable. Both are host OS paths, absolute or relative to
the working directory, and never resolved under `--data-dir`. An invalid
profile fails startup. Every other `pulse` command ignores the variable,
because the CLI is an admin tool and is not profiled.

Embedders serving MCP themselves use `mcpserve.NewPulse`:

```go
p, err := mcpserve.NewPulse(
    pulse.Options{DataDir: "/var/data/pulse"},
    mcpserve.Options{FeatureProfileFile: "/etc/pulse/self-serve.json"},
)
if err != nil {
    return err
}
return mcpserve.ServeStdio(p, mcpserve.Options{BindOnOpen: true})
```

Its sources, highest first: `mcpserve.Options.FeatureProfileFile`, a
profile already on the `pulse.Options`, then `PULSE_FEATURE_PROFILE`.
Setting the field and a `pulse.Options` profile together is
`both_options_set`. `mcpserve.Serve` and `ServeStdio` ignore
`FeatureProfileFile`: a profile is applied when the instance is built.

`mcpserve.Describe(p, opts)` returns the effective serving settings: the
cohort-scan setting after the profile's `disable_cohort_scan`, and the
loaded profile's label. The `pulse mcp` startup line prints the same
thing:

```
pulse mcp: serving over stdio (data dir: /var/data/pulse, bind-on-open: true, cohort-scan: false, feature-profile: survey-self-serve)
```

## Related

- [pulse.New & Options](options.md)
- [Extension Points](../internals/extension-points.md) for `DependsOn`
- [`pulse mcp`](../cli/mcp.md)

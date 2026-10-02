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

> **Status (v1.0.0 alphas).** An instance enforces its profile. Everything
> the profile omits is hidden:
>
> - An extension operator the profile omits is not registered.
> - A built-in aggregator, attribute, filter, grouper, window, feature,
>   statistical test, regression or overlay kind the profile omits behaves
>   at run time exactly as a name Pulse has never heard of: same result
>   or error, on every entry point and every overlay host, predict
>   included. Smart defaults never pick it.
> - A request that sets a slot the profile omits (`crosstab`, `joins`, or
>   an `overlays` slot with no enabled host and kind) is refused with
>   `PULSE_REQUEST_UNKNOWN_FIELD`, exactly as an unrecognised JSON key,
>   and `valid_keys` lists only the slots the instance offers. A request
>   template that renders such a slot fails `PULSE_TEMPLATE_RENDER_INVALID`.
> - `p.Manifest`, `p.PayloadSchema()` and the errors lookup describe only
>   what the instance offers (see below).
>
> Library methods are **not** gated: `p.Process`, `p.Compose`, `p.Facet`,
> `p.Lookup`, `p.Import` and the rest stay callable on every instance. A
> profile hides names and slots, not methods. MCP registration and the
> served skills and examples follow in later v1.0.0 pre-releases.

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
  name, like a built-in. An extension operator the profile omits is
  hidden: `pulse.New` still validates its registration and its
  `DependsOn`, then does not register it.

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
- Some operators read another's results, and filter-to-file runs on the
  filter-expression engine:

| Feature | Also needs |
|---|---|
| `OVERLAY_T_CELL`, `OVERLAY_Z_CELL`, `OVERLAY_T_VS_REF`, `OVERLAY_Z_VS_REF`, `OVERLAY_PAIRWISE_WELCH_T`, `OVERLAY_PAIRWISE_TWO_MEANS_Z` | `AGG_WELFORD` |
| `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` | `AGG_WEIGHTED_MEAN` |
| `ATTR_REG_FITTED`, `ATTR_REG_LEVERAGE`, `ATTR_REG_RESIDUAL` | `REG_OLS` |
| `OVERLAY_YOY` | `GROUP_DATE` |
| `capability:filter_to_file` | `FILTER_EXPRESSION` (filter-to-file compiles every filterer into one filter expression) |

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

## Reading an instance's feature set

- `p.FeatureProfile()` returns a copy of the profile the instance was
  built with, or `nil, false` without one. Changing the copy changes
  nothing on the instance.
- `p.FeatureSetDigest()` returns the instance's feature-set digest,
  `"fs1:"` plus the hex SHA-256 of its sorted enabled feature names and
  its effective `behaviour` switches (the profile's ORed with `Options`).
  Every instance has one. It is the same across processes for the same
  features and switches, and any difference in either changes it, so it
  can key a cache of an instance's self-description. It hides the list
  but not its identity: anyone running the same Pulse version can tell
  an unprofiled instance's digest from a profiled one's.

## What a profiled instance describes

- **Manifest.** `p.Manifest(ctx)` lists only enabled operators, tests,
  regressions, overlays, commands, MCP tools, synth distributions and
  I/O formats. The `facet`, `process_chain`, `join`, `crosstab`, `export`
  and `import` capability blocks are omitted when their capability is
  off. This is a public type change: `descriptor.Manifest.Facet`,
  `ProcessChain`, `Join`, `Crosstab`, `Export` and `Import` are now
  pointers (nil when hidden), so code that read them as values must
  check for nil. Prose that names a hidden feature is dropped from
  descriptions and hints. The manifest's `skills` and examples counts
  are not scoped yet. `extensions.label_tables` is empty (`[]`) when
  `capability:labels` is off and `extensions.range_tables` when
  `capability:range_tables` is off, exactly as on an instance that
  registered no tables. `p.LabelTables()` and `p.RangeTables()` still
  return them: facade methods are not gated.
- **Payload schema.** `p.PayloadSchema()` returns the JSON Schema for
  the instance: enums keep only enabled names, hidden slots are not
  properties, and a request root whose capability is off (compose,
  process chain, facet, sample, lookup) is not offered. A profile-free
  instance's schema is byte-identical to `pulse schema`'s published
  golden apart from the digest comment. `pulse schema` serves the
  default instance; the MCP `pulse://schema` resource serves the
  mounted instance's `p.PayloadSchema()`.
- **Errors.** `p.ErrorLookup`, `p.ErrorsByDomain` and `p.ErrorsSearch`,
  and the manifest's error lists, show only codes the instance can
  raise. A hidden code looks up as unknown. `pulse errors lookup` on the
  CLI stays unscoped.
- **Wording follows the instance.** Some refusals and hints that used to
  name built-ins now name only what the instance offers: the
  zone-capable operator list, the `ATTR_RANK` to `WIN_RANK` hint (the
  generic unknown-attribute error when `WIN_RANK` is hidden), pairwise
  overlay advice, the facet overlay kind list, and the categorical and
  decimal suggestion reasons. Runtime refusals follow the same rule:
  one that recommends other operators (for example "use
  GROUP_SET_VALUE or GROUP_SET_PER_ELEMENT" or "use TEST_ANOVA_F for
  k>2") names only the ones the instance offers, keeping its error
  code. An instance without a profile reads byte-identically to before.
- **Digest.** Manifest and payload schema carry `feature_set_digest`
  (the schema as its root `$comment`), equal to `p.FeatureSetDigest()`.
  The field is additive; the envelope `format_version` stays `"1.1"`.
  Do not read the digest as hiding the profile: a same-version observer
  can tell a profiled digest from the default.

### Upgrading an existing profiled instance

An instance built with a profile before this release hid nothing. It now
hides everything the profile omits, including extension operators. A
request that used an omitted name or slot fails as if the name never
existed. Add the names you still need to `features`.

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

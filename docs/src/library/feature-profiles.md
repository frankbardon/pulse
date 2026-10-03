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
> profile hides names and slots, not methods. The MCP server registers
> only the tools, prompts, cohort listing, skills and examples the
> profile offers (see below).

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
    "AGG_MODE_COUNT",
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
| `PULSE_FEATURE_PROFILE_INVALID` | The profile cannot be used as written | `details.reason`: `both_options_set`, `file_unreadable`, `malformed_json`, `unknown_key`, `missing_features`, `duplicate_feature` (the names are under `duplicates`), `unknown_example` (from `pulse.ExampleFeatureProfile`). `details.path` names the file when one was read |
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
  descriptions and hints. The manifest's `skills` list and examples
  count, categories and tags cover only the skills and examples the
  instance serves (see the MCP section). `extensions.label_tables` is empty (`[]`) when
  `capability:labels` is off and `extensions.range_tables` when
  `capability:range_tables` is off, exactly as on an instance that
  registered no tables, and requests resolve them the same way: a label
  binding naming one fails `PULSE_LABEL_TABLE_UNKNOWN`, an
  `AutoLabels` default naming one is not applied, and a date-range
  `table:` fails `PULSE_RANGE_TABLE_UNKNOWN`. `p.LabelTables()` and
  `p.RangeTables()` still return them: facade methods are not gated.
  Lookup tables (the expression `lookup()` function) have no
  capability and always resolve.
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

The server mounts only what the profile offers. A tool tied to a
feature (`pulse_process` to `capability:process`, `pulse_facet` to
`capability:facet`, and so on) is registered only when that feature is
listed; the discovery tools (`pulse_inspect`, `pulse_predict`,
`pulse_manifest`, the skills, examples and errors lookups) are always
there. Calling a tool the profile hides fails exactly like calling one
that does not exist. Each prompt needs its own `mcp_extra:prompt_*`
feature. Without `mcp_extra:cohort_resources` the server skips the
startup walk and does not list cohorts under `resources/list`, as if
`--no-cohort-scan` were set; every cohort stays readable by its
`pulse://` URI.

Tool input schemas follow the profile too. With `BindOnInspect` on,
the schemas re-bound after an inspect list only the operators, tests
and overlay kinds the profile enables (an operator family with none
enabled carries no list at all), offer the `labels` slot only with
`capability:labels`, and leave out any request slot the profile hides.
A request naming a hidden slot (say `crosstab` without
`capability:crosstab`) is refused as `PULSE_REQUEST_UNKNOWN_FIELD`,
exactly like a misspelt key, and the valid-key list and suggestions
name only the slots the profile offers.

Prose follows the profile the same way the manifest's does. Tool
descriptions, every `description` in a tool's input schema (at
registration and after a re-bind), prompt descriptions and prompt
bodies drop each sentence that names a hidden operator or a tool the
profile does not mount; a numbered step dropped from a prompt
renumbers the rest of its list. A sentence about an enabled operator
that also names a hidden one goes too. The text a caller supplies to
`pulse-author-request` is never touched.

Skills and examples follow the profile as well. The reference skill for
a hidden operator, overlay kind, regression or synth distribution, and
for a tool the server does not mount, is left out of
`pulse_skills_list`, `pulse_skills_get`, the `pulse-skill://` resources
and the manifest's `skills` list. Field-type skills, the design
guides and the `glossary` / `intents` reference skills stay, and a design guide is served as written even where it
mentions an operator the profile hides. An example is left out of
`pulse_examples_search`, `pulse_examples_get`, `p.ExamplesSearch`,
`p.ExampleGet` and the manifest's examples count, categories and tags
when it uses a hidden operator or overlay kind, or needs a hidden
`facet`, `crosstab`, `joins` or `compose` capability. Asking for a
hidden skill or example by name fails exactly like asking for one that
does not exist.

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
cohort-scan setting after the profile's `disable_cohort_scan` and
`mcp_extra:cohort_resources`, and the
loaded profile's label. The `pulse mcp` startup line prints the same
thing:

```
pulse mcp: serving over stdio (data dir: /var/data/pulse, bind-on-open: true, cohort-scan: false, feature-profile: survey-self-serve)
```

`gosdk.RegisteredTools()` and `gosdk.RegisteredPrompts()` are global:
they list every canonical tool and prompt whatever the instance hides.
To learn what a profiled server offers, ask the server (`tools/list`,
`prompts/list`) or read `p.Manifest(ctx)`.

## Example profiles

Pulse publishes example profiles to copy, in the repository's
`examples/profiles/` directory and embedded in the library:

| Name | Offers |
|---|---|
| `minimal` | `capability:process` plus core aggregators and the default groupers |
| `survey-crosstab` | Process, Compose, crosstab, facet and labels, with survey tests and the crosstab, compose and facet overlays they feed, closed over their dependencies |
| `read-only-analyst` | Every analytic capability and operator; nothing that writes data (`import`, `export`, `filter_to_file`, `dedup`, `widen`, `shard`, `index`, `synth`) and no I/O formats |

```go
names := pulse.ExampleFeatureProfiles()        // sorted: minimal, read-only-analyst, survey-crosstab
fp, err := pulse.ExampleFeatureProfile("minimal") // a fresh *pulse.FeatureProfile you own
p, err := pulse.New(pulse.Options{FeatureProfile: fp})
```

Each example passes `pulse.New` validation. An unknown name fails with
`PULSE_FEATURE_PROFILE_INVALID`, `details.reason` `unknown_example`, and
the available names under `details.examples`.

**Published examples are never edited in place.** Each lists exact
feature names as of the release in its `written_with`, so upgrading Pulse
never grows the feature set of a profile copied from one. A changed
example ships under a new name. Copy one and own the copy.

## Writing, checking and upgrading profiles

Four root functions work on a profile without building a `*pulse.Pulse`,
so a CI job can keep a profile honest. Each optional `ext` argument is the
same `pulse.Extensions` value you hand to `pulse.New`. Every result type
is JSON-tagged, with list fields that are never `null`.

```go
// Every feature this build offers (plus your extension operators), in
// canonical order, stamped with the running release.
fp, err := pulse.InitFeatureProfile("", ext)
// Or start from a published example.
fp, err = pulse.InitFeatureProfile("survey-crosstab")

// Exactly the validation pulse.New runs: INVALID, then UNKNOWN, then
// DEPENDENCY, stopping at the first failing class.
report, err := pulse.CheckFeatureProfile(fp, pulse.FeatureProfileCheckOptions{Extensions: ext})

// What this build offers that the profile does not list, and what the
// profile lists that this build does not know.
diff, err := pulse.DiffFeatureProfile(fp, ext)

// Kind, category, Since and dependency groups of every listed feature.
desc, err := pulse.DescribeFeatureProfile(fp, ext)
```

- **`InitFeatureProfile(from, ext...)`** lists every built-in feature
  the running build has reached plus the given extension operators, by
  exact name, ordered by kind, then operator category, then table
  position (the order the examples use). A non-empty `from` seeds the
  profile from that example instead (label, features, behaviour; no
  extension names are added). `written_with` is the running release
  core: `1.0.0-alpha.2` stamps `1.0.0`; a development build with no
  release core stamps the newest release its feature table knows. The
  result always passes `CheckFeatureProfile` with the same extensions.
- **`CheckFeatureProfile(fp, opts)`** returns a report and the coded
  error `pulse.New` would return for the same profile and extensions
  (`nil` when valid). With `opts.Offline` — for checking without your
  extensions in hand — a name that is not registered but follows the
  extension naming policy (`AGG_ACME_THING`: an operator category other
  than `SYNTH`, a namespace other than `BUILTIN` / `STANDARD` / `CORE` /
  `PULSE`) becomes a warning, "unverified extension name, check
  in-process", coded `PULSE_FEATURE_PROFILE_UNKNOWN` with
  `details.reason` `unverified_extension`, instead of an error. It still
  needs a request host. Every other unknown name stays an error. Run the
  check in-process, with your extensions, before shipping.
- **`DiffFeatureProfile(fp, ext...)`** reports `missing` — features the
  running build offers that the profile omits, each flagged `new` when
  its `Since` is later than the profile's `written_with` — and `unknown`
  — names the build does not resolve, with the same reasons
  `PULSE_FEATURE_PROFILE_UNKNOWN` uses. Unknown names are reported, not
  fatal. A missing or unparseable `written_with` flags nothing `new`.
- **`DescribeFeatureProfile(fp, ext...)`** describes every listed name:
  `kind`, `category` (operators only), `source` (`builtin`, `extension`
  or `unknown`), `since` (built-ins) and `depends_on`, an AND of any-of
  groups. A name the build does not resolve carries an `unknown`
  explanation instead of failing.

Diff and describe fail only on a missing profile or a `nil` feature list
(`PULSE_FEATURE_PROFILE_INVALID`).

### From the command line

`pulse features` wraps the same four functions, with no extensions and no
data directory (the leaves describe the binary). A profile path is a host
OS path, relative to the working directory.

```sh
pulse features init > profile.json              # or: init --from minimal
pulse features check profile.json               # exits non-zero on any failure
pulse features diff profile.json                # missing (flagged [new]) + unknown
pulse features show profile.json
```

Every leaf takes `--json` for the standard envelope: `data` is the
profile, check report, diff or description; `pulse features check --json`
also lifts each unverified-extension warning onto the envelope's
`warnings`, and a fatal error carries its own code in `errors[0]` (the
report stays in `data`) before the command exits non-zero. `check` is
always offline (above), so check in-process before shipping a profile that
lists your extension operators.

## Related

- [pulse.New & Options](options.md)
- [Extension Points](../internals/extension-points.md) for `DependsOn`
- [`pulse mcp`](../cli/mcp.md)

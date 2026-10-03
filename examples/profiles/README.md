# Example feature profiles

Ready-to-copy feature profiles for `pulse.Options.FeatureProfile` /
`Options.FeatureProfileFile`, `pulse mcp --feature-profile` and
`PULSE_FEATURE_PROFILE`. Each file is also embedded in the library and the
binary: `pulse.ExampleFeatureProfiles()` lists them and
`pulse.ExampleFeatureProfile(name)` returns one, parsed.

**Published examples are frozen. They are never edited in place.** Each
lists exact feature names as of the release named in its `written_with`, so
a later Pulse release never grows the feature set of an instance that copied
one. A changed example ships as a NEW file under a new name; the old file
stays byte-identical. Copy an example and own the copy from then on.

| File | Offers |
|---|---|
| `minimal.json` | `capability:process` plus the core aggregators (`AGG_COUNT`, `AGG_SUM`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX`, `AGG_MODE_COUNT`) and the smart-default groupers (`GROUP_CATEGORY`, `GROUP_DATE`, `GROUP_RANGE`). |
| `survey-crosstab.json` | Process, Compose, crosstab, facet and label tables, with survey aggregators, filterers and groupers (multi-select included), the survey significance tests, and the crosstab, compose and facet overlays they feed — closed over their dependencies (`AGG_WELFORD`, `AGG_WEIGHTED_MEAN`). |
| `read-only-analyst.json` | Every analytic capability and every operator, plus the MCP extras. Nothing that writes or rewrites data: no `import`, `export`, `filter_to_file`, `dedup`, `widen`, `shard`, `index` or `synth`, and no `io_format:*` (only import and export use a format). |

Features are listed by kind, then operator category, then feature-table
order — the same order a generated profile uses. Every example passes
`pulse.New` validation; `TestExampleFeatureProfiles_*` pins that.

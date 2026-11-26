# Adding a Synth Distribution

> **Run the site checklist first.** `make sites ARGS="new-op synth"` prints every file and declaration a new synth distribution lands in — derived from where the existing ones appear — with the tests to rerun and the goldens to regenerate; `make sites ARGS="op <NAME>"` does the same for an existing one. See [Finding Change Sites](finding-change-sites.md). This recipe explains each step.

**Audience:** Pulse internals contributors adding a new synthetic-data
distribution kind to the `internal/synth/` package — Normal, Uniform,
Categorical, ZipfMandelbrot, etc.

The synth surface generates `.pulse` cohorts from either a declared
schema (`pulse synth from-schema`) or a profile sampled from an
existing cohort (`pulse synth from-profile`). Distributions plug into
both code paths via the `AllDistributions()` registry in `internal/synth`.

## 1. Implement in `internal/synth/`

Declare a `Dist*` kind constant in `internal/synth/distributions.go`,
add it to `AllDistributions()` so the generator surface and the manifest
enumerate it, and build its `sampler` in `buildSampler` (same file).
Re-declare the constant in the public alias facade `synth/facade.go`
(every built-in has one). A distribution that can feed a copula or
profile reconstruction also needs its arms in `copula.go` /
`profile.go`; `make sites ARGS="op <kind>"` on a neighbour shows them.

## 2. Capability declaration

Add a row to `internal/descriptor/capabilities_distributions.go` so the manifest
exposes the new distribution to LLM clients.
`TestManifestDistributionsComplete` enforces a capability row per
registered distribution kind.

**Params allowlist.** A spec field's `params` are checked strictly: a key the
distribution does not read is refused `PULSE_REQUEST_UNKNOWN_FIELD`. Declare
the exact key set three times, and the gates hold them together:

- `distributionParamKeys` in `internal/synth/spec_decode.go` — the runtime
  allowlist (`validateSpec`); add a `distributionParamAliases` entry for a
  misnomer no edit distance reaches (`rate` → `lambda`).
- the row's `Params` above — `TestDistributionParamsMatchManifest`
  (`internal/descriptor`) requires the same key set.
- a valid probe field in `paramsGateFields`
  (`internal/synth/params_allowlist_internal_test.go`) —
  `TestDistributionParamsDeclaredMatchRead` generates it under a recording
  of every params read and fails on a key read but undeclared, or declared
  but never read. Read params only through the `param*` helpers /
  `paramLookup` (`TestDistributionParamsReadThroughLookup`).

## 3. Write the atomic skill and list it

Create `skills/op-synth-<kebab>.md` covering the parameter shape, the
distribution's family (continuous, discrete, heavy-tailed,
categorical-like), and any sampling caveats;
`TestSkillsCoverAllSynthDistributions` enforces the file exists at that stem.
Its `## Params` is the one `| Name | Type | Default | Description |` table,
one row per declared param, Default `(required)` / `—` / the declared value
(`TestSkillParamsMatchManifest`; `.claude/reference/skill-pack.md`).
Add the kind's name to the "Distribution registry" list in
`skills/synthetic-data.md`, and — if it changes reconstruction, correlation
or model participation — the focused `skills/synth-*.md` skill that owns that
topic (`synth-marginals`, `synth-correlations`, `synth-models`).

## 4. Guidance

Add the kind's `Purpose` in `internal/descriptor/purposes_synth.go` and
its reading entry in `internal/descriptor/interpretation_reading.go`
(`.claude/reference/guided-analysis.md`). Load
`.claude/reference/synthetic-data.md` before any `internal/synth` change.
CLAUDE.md is not a companion.

## 5. Run the gates

```bash
go test -count=1 ./internal/skills/ -run TestSkillsCoverAllSynthDistributions
go test -count=1 ./internal/descriptor/ -run 'TestManifestDistributionsComplete|TestDistributionParamsMatchManifest|TestSkillParamsMatchManifest|TestSkillsCoverAllPurposes'
go test -count=1 ./internal/synth/... ./synth/
go test -count=1 ./descriptor/ -update && go test -count=1 ./descriptor/
make docs
```

The Update Demand row for synth distributions covers all of these in
one PR; see [The Update Demand](update-demand.md).

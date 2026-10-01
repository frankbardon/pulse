# Adding a Synth Distribution

**Audience:** Pulse internals contributors adding a new synthetic-data
distribution kind to the `internal/synth/` package — Normal, Uniform,
Categorical, ZipfMandelbrot, etc.

The synth surface generates `.pulse` cohorts from either a declared
schema (`pulse synth from-schema`) or a profile sampled from an
existing cohort (`pulse synth from-profile`). Distributions plug into
both code paths via the `AllDistributions()` registry in `internal/synth`.

## 1. Implement in `internal/synth/`

Add the distribution implementation in `internal/synth/` (the public `synth` package is an alias facade; re-declare a new `Dist*` constant there if embedders should spell it). Each distribution
satisfies the package's distribution interface (per-field RNG +
parameter validation + JSON marshalling). Register it in the
`AllDistributions()` slice (`internal/synth`) so the generator surface enumerates
it.

## 2. Capability declaration

Add a row to `internal/descriptor/capabilities_distributions.go` so the manifest
exposes the new distribution to LLM clients.
`TestManifestDistributionsComplete` enforces a capability row per
registered distribution kind.

## 3. Update the synthetic-data skill

Add an entry under "Supported distributions" in
`skills/synthetic-data.md` covering the parameter shape, the
distribution's family (continuous, discrete, heavy-tailed,
categorical-like), and any sampling caveats.
`TestSkillsCoverAllSynthDistributions` enforces presence by name.

## 4. Update CLAUDE.md

Bump the registered-synth-distribution count in CLAUDE.md's
"Skill Pack" section.

## 5. Run the gates

```bash
go test ./internal/skills/ -run TestSkillsCoverAllSynthDistributions
go test ./internal/descriptor/ -run TestManifestDistributionsComplete
go test ./internal/synth/...
```

The Update Demand row for synth distributions covers all of these in
one PR; see [The Update Demand](update-demand.md).

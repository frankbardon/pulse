# Adding a Synth Distribution

**Audience:** Pulse internals contributors adding a new synthetic-data
distribution kind to the `synth/` package — Normal, Uniform,
Categorical, ZipfMandelbrot, etc.

The synth surface generates `.pulse` cohorts from either a declared
schema (`pulse synth from-schema`) or a profile sampled from an
existing cohort (`pulse synth from-profile`). Distributions plug into
both code paths via the `synth.AllDistributions()` registry.

## 1. Implement in `synth/`

Add the distribution implementation in `synth/`. Each distribution
satisfies the package's distribution interface (per-field RNG +
parameter validation + JSON marshalling). Register it in the
`synth.AllDistributions()` slice so the generator surface enumerates
it.

## 2. Capability declaration

Add a row to `descriptor/capabilities_distributions.go` so the manifest
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
go test ./skills/ -run TestSkillsCoverAllSynthDistributions
go test ./descriptor/ -run TestManifestDistributionsComplete
go test ./synth/...
```

The Update Demand row for synth distributions covers all of these in
one PR; see [The Update Demand](update-demand.md).

# Feature profiles — feature table, dependency model, profile model, validation

**How to load.** This file is an INDEX: the rules below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/feature-profiles/` your change touches; find one with `grep -l '<identifier>' .claude/reference/feature-profiles/*.md`. A section-qualified pointer at this file resolves through the section map.

**Load it before:** adding an operator, capability, I/O format, MCP tool or MCP prompt (each one is a feature and needs a `features.go` row with `Since` and dependency edges); touching `internal/descriptor/features.go`, root `feature_profile*.go`, an extension registration's `DependsOn`, `mcpserve/feature_profile.go`, `mcpserve/describe.go`, or the `pulse mcp --feature-profile` leaf.

The rules every change here must keep:

- **Name.** A **feature profile** — never a bare "profile"; synth data profiling owns that word.
- **What it is.** An instance's closed feature allowlist, validated and enforced by `pulse.New` in three classes: `PULSE_FEATURE_PROFILE_INVALID` → `_UNKNOWN` → `_DEPENDENCY`.
- **Hidden == never registered.** A hidden feature acts exactly as if it had never been registered, on every path: runtime, predict, request decode, manifest, payload schema, error lists, MCP tools / prompts / resources, skills, examples and the reference export.
- **Facade methods are NOT gated.** `Process`, `Compose`, `Facet`, `Lookup`, `Import` … stay callable on every instance. A profile hides names and slots; a request needing a hidden name fails with that name's own unknown-name error.
- **Digest.** Every manifest and payload schema carries `feature_set_digest`.
- **Who reads a profile.** `pulse.New` never reads `PULSE_FEATURE_PROFILE`; only `mcpserve.NewPulse` (and so `pulse mcp`) does.
- **No runtime skill mentions feature profiles.** Embedder docs live in `docs/src/library/feature-profiles.md`.

## Section map

| Slice | Sections and bold leads, in file order |
|---|---|
| `feature-profiles/model.md` | Naming; Feature kinds and spelling; Inventory rules; `Since`; Version compare; Dependency model; Gates; Extension `DependsOn`; The profile model; Behaviour; `return`; `limits`; Sources; Order in `pulse.New`; The resolved feature set; `feature_set_digest` |
| `feature-profiles/hiding-runtime-and-slots.md` | Runtime hiding (registry operators); Hide at the native lookup site, never with a pre-check; Other sites; Regressions; Overlay kinds — route, don't re-word; Predict hiding; Predict sees only the instance; The shared predict ↔ runtime rules take the instance on BOTH sides; Request-slot gate; A set slot the instance hides is an unknown field; Slot → owning feature; Nested slots (`capability:weighting`); Nested slots (`capability:multiplicity`); Order: first, at the decode point; Templates refuse a hidden slot; Parity harness |
| `feature-profiles/hiding-surfaces.md` | Manifest hiding; `Pulse.Manifest` describes only the instance; Prose scrub; Slot tokens; Skills and examples follow the instance's discovery prune; Payload schema hiding; `Pulse.PayloadSchema() ([]byte, error)` serves the instance's schema; Error hiding; The error-owner table is internal; Instance-dependent wording; Runtime remedies; Hard edges; Validation codes and order |
| `feature-profiles/tooling-and-discovery.md` | Where a profile is read; `pulse.New` never reads the environment; No runtime skill mentions feature profiles; Example profiles; Tooling; Discovery prune (ontology); Hidden == nonexistent, per discovery surface; Reference export; The MCP surface |

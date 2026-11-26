# Architecture — Public vs internal (the package tree)

Per-topic slice of the architecture reference. Index (rules + section map): `.claude/reference/architecture.md` — load it first, then only the slice(s) your change touches.

## Public vs internal

Under Go module semantics every non-`internal/` package is public API, and `v1.0.0` freezes every exported identifier in it (`TestPublicAPIGolden`, aliases expanded). The classification decisions are in `docs/roadmap/v1.0.0-api-and-release/00-public-surface.md`; the embedder migration from the earlier layout is `docs/roadmap/v1.0.0-api-and-release/03-embedder-migration.md`.

**Public packages.** Each one's role and the rule that keeps it in shape:

| Package | Role | Rule |
|---|---|---|
| `pulse` (root) | the facade: `New`, `Options`, `Pulse` + methods, root aliases, extension registration | no public root signature names an `internal/processing` type (`TestRootSurfaceNamesNoProcessing`) |
| `types/` | wire payload | Go AND JSON names frozen |
| `errors/` | `CodedError`, every code constant, lookup, `codeMetadata` | left whole |
| `encoding/` | schema nouns + ungrouped (`0x01`) raw-byte primitives; `RecordLocator` is geometry only | split in place (`package-build.md`) |
| `descriptor/` | result + envelope types only | split in place |
| `io/`, `synth/` | alias facades over `internal/io` (+ `internal/iocore`) and `internal/synth` | `package-build.md` (The `io` import boundary) |
| `extend/` | extension-authoring contract (`Record`, `Rows`, factories, instance interfaces, the finalize-only `MatrixOperator`) | leaf over `encoding` / `types` / `linalg` (`TestExtendImportBoundary` also admits `errors`) |
| `linalg/` | Pulse-owned `Matrix` / `Sym` / `Vec`, FMA-free reference kernels, gonum-backed decompositions | leaf; gonum never appears on the surface (`TestLinalgImportBoundary`, `TestLinalgSurfaceNamesNoGonum`). Contract: `.claude/reference/matrix-and-vectors.md` |
| `linalg/detmath/` | FMA-free pure-Go ports of the `math` transcendentals + ziggurat normal / exponential variates, identical bits on every GOARCH | stdlib-only leaf; `TestDetmath_GoldenBits`, `TestDetmath_NoFMAContraction`. Use it, never `math.*`, wherever a value feeds a bit-exact result |
| `linalg/metric/` | the ONE distance / similarity metric registry (`Resolve`, `Eval` under one pairwise-complete NaN policy) | leaf (`TestMetricImportBoundary`). Contract: `matrix-and-vectors.md` (Metric registry) |
| `optimize/` | `LeastSquares` (bounded Levenberg–Marquardt) and `Minimize` (SLSQP, a statement-for-statement SciPy port; BSD-3 notice in `optimize/SCIPY_LICENSE.txt`) | FMA-free (`TestOptimize_NoFMAContraction`); bad input `PULSE_OPTIMIZE_INVALID` (nil result); a short stop returns its iterate with `PULSE_OPTIMIZE_NOT_CONVERGED` / `_INFEASIBLE` |
| `mcmc/` | `NUTS` and `HMC` samplers, transforms, `RHat` / `ESSBulk` / `ESSTail` (held to ArviZ by `mcmc/testdata/gen_diagnostics.py`) | per-chain PCG from `(seed, chain)`, chains merged in chain order, bit-identical across workers and arch (`TestNUTS_GoldenHash`, `TestHMC_GoldenHash`); refusals `PULSE_MCMC_INVALID` / `PULSE_MCMC_FAILED`; no model knowledge (`TestMCMC_ImportBoundary`) |
| `observe/` | the hook / metric vocabulary `Options.Hooks` / `Options.Metrics` take | stdlib-only leaf (`TestObserveImportBoundary`). Contract: `.claude/reference/observability.md` |
| `observe/otel/`, `observe/prom/` | OpenTelemetry and Prometheus adapters (packages `otelpulse`, `prompulse`; root module) | the only non-CLI packages allowed a vendor import (`TestObservabilityVendorImportBoundary`) |
| `mcp/gosdk/` | the ONLY go-sdk importer: `Register`, `Config` | `mcp.md` |
| `mcpserve/` | `Serve`, `ServeStdio`, `Options`, `NewPulse` (the feature-profile flag / env reader), `Describe` | left whole |

**Internal packages.** Do not keep a list here: `go list ./internal/...` enumerates them, and each package's doc comment states its role and import ceiling. The rules that matter when you add or move one:

- **No-execute packages** — `internal/descriptor` (manifest, predict, inspect, schema, `capabilities_*.go`), `internal/guide` (Recommend and Explain), `internal/docgen` (the reference renderer) — never import `internal/service` or `internal/processing`, directly or transitively (`TestPredictNoExecutionImports`, `TestGuideNoExecutionImports`, `TestDocgenImportBoundary`).
- **One shared rule, two callers.** A rule both predict and the runtime enforce lives ONCE, in a small no-execute leaf package that both call (examples: `internal/strictjson` — the one strict request decoder; `internal/paramread` — the one way a runtime reads a slot's params, `TestParamsReadThroughParamread`; `internal/windowparams`, `internal/overlayhost`, `internal/seriesfield`, `internal/seriespartition`, `internal/mergegate`, `internal/limits`, `internal/vectors`, `internal/model`, `internal/sweep`, `internal/banner`, `internal/extspec`). When you add a refusal both sides raise, put it in such a leaf, give the leaf a `Test*ImportBoundary`, and add a predict ↔ runtime parity test. Two copies of a rule drift; that is how most shipped predict/runtime mismatches happened.
- **Engine layering.** `internal/processing` never imports `internal/descriptor`. `internal/regadapt` (the shared extend-contract regression adapter) sits above both and registers the model regressions (`internal/regmmm`) on the engine's built-in registry at init; `internal/service` imports it, so every binary and engine test runs them (`TestModelRegressions_RegisteredByEngine`).
- **`internal/returnshape`** applies `return` shaping once per `Response`, in the facade after the engine returns — never inside `Service.Process`.
- **`internal/jsonfinite`** is the read-side inverse of `types.MarshalFinite`: a result handed back to the CLI or MCP decodes through it, so a JSON `null` float reads NaN, never 0.
- **`internal/template`** imports stdlib + `types` + `errors` + the stdlib-only `internal/strictjson` / `internal/exprslots` only (`TestTemplatePackage_ImportBoundary`; `.claude/reference/request-templating.md`).
- **`internal/temporal`** holds all zone math (`TestNoZoneMathOutsideTemporal`).
- **Bridges.** `internal/encodingbridge` installs hooks from the public `encoding` into its internal twin; `internal/facadebridge` hands `mcp/gosdk` / `mcpserve` the instance snapshot without an exported engine handle; `internal/testhook` gives black-box tests the engine handle. Never add an exported engine accessor instead.
- **`internal/buildinfo`** supplies `pulse.Version()`: the ldflags value, else Pulse's OWN module version (never an embedder's).
- **`internal/embeddersmoke`** is a nested module (own `go.mod`) that uses only public spellings; `make smoke` builds it, and `./...` never reaches it.

`examples/profiles/` holds the published, frozen example feature profiles (root `//go:embed`). `docs/` is the mdBook source (`docs/book/` is generated).

The skill pack is ADDRESSED pack-relative in prose — `skills/<stem>.md` — and resolved under `internal/skills/` by `TestSkillsCoverAllCrossReferences`; examples are addressed as `examples/<dir>/*.json` the same way.

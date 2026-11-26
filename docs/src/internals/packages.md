# Package Layout

> **Source of truth:** the contributor contract is
> [`.claude/reference/architecture/packages.md`](https://github.com/frankbardon/pulse/blob/main/.claude/reference/architecture/packages.md)
> (each public package's role and the gate that keeps it in shape). The
> frozen exported surface is `internal/apigolden/testdata/public_api.txt`.

Every package outside `internal/` and `cmd/` is public Go API: tagging
`v1.0.0` freezes each of its exported identifiers (`TestPublicAPIGolden`).
The public set is deliberately small; everything else lives under
`internal/`.

```
pulse/
├── pulse.go, *.go          # PUBLIC facade — pulse.New, pulse.Options, *Pulse methods, root aliases
├── types/                  # PUBLIC request/response structs; Go AND JSON names frozen
├── errors/                 # PUBLIC typed error codes (CodedError, every code, Lookup/Search/ByDomain)
├── encoding/               # PUBLIC schema nouns + ungrouped (0x01) raw-byte primitives
├── descriptor/             # PUBLIC result + envelope types (Envelope, Manifest, PredictResult, InspectResult)
├── io/                     # PUBLIC alias facade: jobs, Reader/Writer, io.Format + factory
├── synth/                  # PUBLIC alias facade: Spec, Profile, Synth, SynthBytes, ProfileFile, …
├── extend/                 # PUBLIC extension-authoring contract: Record, Rows, operator factories + interfaces
├── linalg/                 # PUBLIC Matrix / Sym / Vec, FMA-free kernels, decompositions
│   ├── detmath/            # PUBLIC FMA-free math transcendentals + variates (identical bits on every GOARCH)
│   └── metric/             # PUBLIC the one distance / similarity metric registry
├── optimize/               # PUBLIC LeastSquares (bounded Levenberg–Marquardt), Minimize (SLSQP)
├── mcmc/                   # PUBLIC NUTS / HMC samplers and convergence diagnostics
├── observe/                # PUBLIC hook + metric vocabulary for Options.Hooks / Options.Metrics
│   ├── otel/               # PUBLIC OpenTelemetry adapter (package otelpulse)
│   └── prom/               # PUBLIC Prometheus adapter (package prompulse)
├── mcp/
│   └── gosdk/              # PUBLIC go-sdk adapter — the only package importing the MCP SDK; gosdk.Register
├── mcpserve/               # PUBLIC ready-made MCP server (Serve, ServeStdio, NewPulse)
├── examples/profiles/      # published example feature profiles (embedded by the root package)
├── cmd/
│   └── pulse/              # CLI binary (the only binary)
├── docs/                   # mdBook source for this site (published to GitHub Pages)
└── internal/               # everything else — `go list ./internal/...`; key packages:
    ├── cli/                # CLI leaves, flags, envelopes (no business logic)
    ├── service/            # orchestration; wires processing to encoding
    ├── processing/         # operator engine (window/, feature/, regression/, multiplicity/, arena/)
    ├── descriptor/         # no-execute manifest / predict / inspect / payload-schema builders, capabilities_*.go
    ├── guide/, docgen/     # no-execute Recommend / Explain and the Analysis Guide renderer
    ├── encoding/           # codec remainder: shard archives, groups, decode plans, sidecar index
    ├── iocore/, io/        # Reader / Writer contracts; import/export/convert jobs and the format adapters (io/<fmt>/)
    ├── temporal/           # epoch-day / calendar / zone math (TestNoZoneMathOutsideTemporal)
    ├── synth/              # synthetic generator, profile capture, structural rules, fidelity
    ├── template/           # request templating
    ├── mcp/                # SDK-free MCP core (toolmeta/ holds tool names + descriptions)
    ├── skills/, examples/  # embedded skill pack (no index file) and runnable request library
    ├── fs/                 # afero filesystem config (fs.Default, fs.NewMemMap)
    ├── apigolden/          # TestPublicAPIGolden + testdata/public_api.txt
    └── embeddersmoke/      # nested module compiled by `make smoke` against public spellings only
```

Each internal package's doc comment states its role and import ceiling;
import boundaries are gates (`grep -rn 'func Test.*ImportBoundary' --include='*_test.go' .`).

## How the narrowed packages are built

- **Split in place** — `encoding` and `descriptor` keep their public
  subset at the public path; the remainder lives in an `internal/` twin with
  the same package name.
- **Alias facade** — `io` and `synth` hold only type aliases, re-declared
  constants and thin wrappers over `internal/io` (+ `internal/iocore`) and
  `internal/synth`. Nothing under `internal/io/**` may import the public
  `io` (its factory imports every adapter); `TestIOImportBoundary` enforces it.
- **Root aliases** — facade-returned types from internal packages keep their
  `pulse.X` spelling as aliases (`ComposeOptions`, `RowIter`, the shard and
  index result types, `ImportSpec`, `Example`, `Template`, …).

# Package Layout

> **Source of truth:** this tree is mirrored from
> [`.claude/reference/architecture.md`](https://github.com/frankbardon/pulse/blob/main/.claude/reference/architecture.md),
> which CLAUDE.md's "Architecture" section points at. If the project structure
> changes, that file is updated first; this page follows.

Every package outside `internal/` and `cmd/` is public Go API: tagging
`v1.0.0` freezes each of its exported identifiers. The public set is
deliberately small; everything else lives under `internal/`.

```
pulse/
├── pulse.go, *.go          # PUBLIC facade — pulse.New, pulse.Options, *Pulse methods, root aliases
├── types/                  # PUBLIC request/response structs; Go AND JSON names frozen
├── errors/                 # PUBLIC typed error codes (CodedError, every code, Lookup/Search/ByDomain)
├── encoding/               # PUBLIC schema nouns + ungrouped (0x01) raw-byte primitives
├── descriptor/             # PUBLIC result + envelope types (Envelope, Manifest, PredictResult, InspectResult)
├── io/                     # PUBLIC alias facade: jobs, Reader/Writer, io.Format + factory
├── synth/                  # PUBLIC alias facade: Spec, Profile, Synth, SynthBytes, ProfileFile, …
├── mcp/
│   └── gosdk/              # PUBLIC go-sdk adapter — the only package importing the MCP SDK; gosdk.Register
├── mcpserve/               # PUBLIC ready-made MCP server (Serve, ServeStdio)
├── extend/                 # PUBLIC extension-authoring contract: Record, Rows, operator factories + interfaces
├── cmd/
│   └── pulse/              # CLI binary (the only binary)
├── docs/                   # mdBook source for this site (published to GitHub Pages)
└── internal/
    ├── cli/                # CLI leaves, flags, envelopes (no business logic)
    ├── service/            # Orchestration layer; wires processing to encoding
    ├── descriptor/         # manifest / predict / inspect / payload-schema builders, capabilities_*.go
    ├── encoding/           # codec remainder: shard archives, groups, decode plans, sidecar index
    ├── encodingbridge/     # init-installed hooks from public encoding to its internal twin
    ├── iocore/             # Reader / Writer contracts the public io aliases
    ├── io/                 # import/export/convert jobs, inference, transfer
    │   ├── csv/ tsv/ ndjson/ jsonarray/ jsonshared/
    │   ├── arrow/ parquet/ excel/ spss/
    │   └── exportoverlay/ nullcell/ settristate/ setwide/
    ├── synth/              # synthetic generator, profile capture, structural rules, fidelity
    ├── template/           # request templating
    ├── mcp/                # SDK-free MCP core (typed In/Out, reflected schemas, handlers, bind)
    │   └── toolmeta/       # leaf metadata (tool names + descriptions) shared by descriptor + core
    ├── skills/             # embedded markdown skill pack (//go:embed *.md; no index file)
    ├── examples/           # embedded runnable request library
    ├── fs/                 # afero-based filesystem config (fs.Default, fs.NewMemMap)
    ├── imports/            # managed-imports manager
    ├── daterange/          # compiled labeled-date-range model
    ├── spsssidecar/        # SPSS sidecar path helpers
    ├── facadebridge/       # root -> mcp/gosdk hooks
    ├── processing/         # operator engine: operators, crosstab, joins, registry
    │   ├── window/         # WIN_* operators (LAG, LEAD, RANK, RUNNING_*, EWMA, ...)
    │   ├── feature/        # FEAT_* pre-filter feature engineers (LOG, SQRT, BUCKETIZE, ...)
    │   └── regression/ arena/  # REG_* engine, arena allocator
    ├── buildinfo/          # version injected by ldflags
    ├── apigolden/          # TestPublicAPIGolden + testdata/public_api.txt
    └── embeddersmoke/      # nested module compiled by `make smoke` against public spellings only
```

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

Embedders adapting from the earlier layout: see the embedder migration guide
in the repository roadmap (`docs/roadmap/v1.0.0-api-and-release/03-embedder-migration.md`).

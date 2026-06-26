# Package Layout

> **Source of truth:** this tree is mirrored from the "Package layout" section
> of [`CLAUDE.md`](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md). If
> the project structure changes, that file is updated first; this page follows.

```
pulse/
├── cmd/
│   └── pulse/              # CLI binary (the only binary)
├── pulse.go                # Public facade — pulse.New, pulse.Options
├── service/                # Orchestration layer; wires processing to encoding
├── processing/             # Aggregators, attributes, filterers, groupers, windows, features
│   ├── window/             # WIN_* operators (LAG, LEAD, RANK, RUNNING_*, EWMA, ...)
│   └── feature/            # FEAT_* pre-filter feature engineers (LOG, SQRT, BUCKETIZE, ...)
├── encoding/               # Dynamic schema + record codec (.pulse binary format)
├── io/                     # Bidirectional tabular <-> .pulse adapters
│   ├── csv/                # CSV reader + writer
│   ├── tsv/                # TSV reader + writer
│   ├── ndjson/             # NDJSON reader + writer
│   ├── jsonarray/          # JSON-array reader + writer (single top-level array of flat objects)
│   ├── jsonshared/         # Value coercion helpers shared by ndjson and jsonarray
│   ├── arrow/              # Arrow IPC / Feather V2 reader + writer; shared Arrow<->Pulse type maps
│   ├── parquet/            # Parquet reader + writer (delegates type maps to io/arrow)
│   └── excel/              # Excel reader + writer (Excelize)
├── fs/                     # afero-based filesystem abstraction + extension hook
├── errors/                 # Typed error codes (CodedError system)
├── types/                  # Request/response structs (JSON-serializable)
├── descriptor/             # Self-description: manifest, predict, inspect, envelope
├── skills/                 # Embedded markdown skill pack (//go:embed)
│   ├── index.json          # Manifest of all bundled skills
│   └── *.md                # Individual skill files with YAML frontmatter
├── mcp/                    # Public MCP library surface
│   └── toolmeta/           # Leaf metadata package (tool names + descriptions) consumed by descriptor + internal/mcp
├── synth/                  # Synthetic data generator (from-schema, from-profile)
├── docs/                   # mdBook source for this site (published to GitHub Pages)
└── internal/
    ├── cli/                # CLI internals (descriptor walker, json action)
    └── mcp/                # MCP server: tool + resource handlers wrapping pulse.Pulse
```

# Pulse

High-performance, self-describing tabular data processing engine written in Go. Ships as a CLI binary and an embeddable Go library.

Pulse reads and writes `.pulse` files — a compact binary format with an inline schema, categorical dictionaries, and per-field descriptions. Import from CSV, TSV, NDJSON, Parquet, or Excel; run aggregations, filters, and groupings; export results back to any supported format.

Designed for LLM-native workflows: every command supports `--json`, a bundled skill pack teaches agents how to operate Pulse, and `api predict` validates requests before execution.

## Installation

### From source

```bash
git clone https://github.com/frankbardon/pulse.git
cd pulse
make build
# Binary at ./bin/pulse
```

### Go install

```bash
go install github.com/frankbardon/pulse/cmd/pulse@latest
```

Requires Go 1.22+.

## Quickstart

### Import a CSV into a .pulse file

```bash
pulse import csv --input data.csv --output data.pulse
```

Schema is inferred automatically (up to 500 rows sampled). To supply an explicit schema:

```bash
# Generate a schema template from your data
pulse import schema-template data.csv > schema.json

# Edit schema.json — add descriptions, adjust types
# Then import with the schema
pulse import csv --input data.csv --schema schema.json --output data.pulse
```

### Inspect the file

```bash
pulse cohort inspect data.pulse --json
```

Returns field names, types, byte offsets, descriptions, and categorical dictionaries.

### Run an aggregation

Create a request file (`request.json`):

```json
{
  "cohort": {"filename": "data.pulse"},
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id", "label": "total"},
    {"type": "AGG_AVERAGE", "field": "score", "label": "avg_score"}
  ]
}
```

```bash
pulse api process --request request.json --json
```

### Filter, group, and aggregate

```json
{
  "cohort": {"filename": "data.pulse"},
  "filterers": [
    {"type": "FILTER_RANGE", "field": "score", "values": ["80", "100"]}
  ],
  "groups": [
    {"type": "GROUP_CATEGORY", "field": "brand"}
  ],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id", "label": "count"},
    {"type": "AGG_AVERAGE", "field": "score", "label": "avg_score"},
    {"type": "AGG_STDDEV", "field": "score", "label": "stddev"}
  ]
}
```

### Validate before executing

```bash
pulse api predict --request request.json --json
```

Returns the proposed schema, warnings (e.g., numeric aggregation on a categorical field), and estimated row count without touching the data.

### Export back to tabular

```bash
pulse export csv --input data.pulse --output results.csv
pulse export parquet --input data.pulse --output results.parquet
```

### Convert between formats directly

```bash
pulse convert data.csv data.parquet
pulse convert data.xlsx output.tsv --schema schema.json
```

Format is auto-detected from file extensions. No intermediate `.pulse` file is written unless `--keep-pulse=path` is specified.

### Sample rows

```bash
pulse api sample --input data.pulse --count 10
```

### Get distinct values for a field

```bash
pulse api facet --input data.pulse --field brand
```

## CLI Reference

```
pulse
├── --json                          Root manifest (self-description)
├── import
│   ├── csv|tsv|ndjson|parquet|excel  --input FILE --output FILE [--schema FILE]
│   ├── predict                       --input FILE [--schema FILE] --json
│   └── schema-template INPUT         Generate editable schema from source
├── export
│   ├── csv|tsv|ndjson|parquet|excel  --input FILE --output FILE
│   └── predict                       --input FILE --format FORMAT --json
├── convert INPUT OUTPUT [--schema FILE] [--keep-pulse PATH]
│   └── predict INPUT OUTPUT --json
├── cohort
│   ├── inspect PATH [--json] [--full-dict]
│   └── filter --input FILE --output FILE --filter EXPR
├── api
│   ├── process --request FILE [--json]
│   ├── compose --request FILE [--json]
│   ├── sample --input FILE --count N
│   ├── facet --input FILE --field NAME
│   └── predict --request FILE --json [--strict]
└── skills
    ├── list [--json]
    └── show NAME
```

Every leaf command supports `--json` for structured output wrapped in an envelope with `format_version`, `data`, `errors`, and `warnings`.

## Embedding Pulse in a Go Application

Pulse is library-first. The CLI is a thin adapter over the public API.

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/frankbardon/pulse"
    pio "github.com/frankbardon/pulse/io"
    "github.com/frankbardon/pulse/io/csv"
    "github.com/frankbardon/pulse/types"
)

func main() {
    ctx := context.Background()

    // Create a Pulse instance.
    p, err := pulse.New(pulse.Options{DataDir: "/var/data"})
    if err != nil {
        log.Fatal(err)
    }

    // Import a CSV.
    importJob := &pio.ImportJob{
        Source: csv.NewReader(nil, "input.csv"),
        Target: "dataset.pulse",
    }
    report, err := p.Import(ctx, importJob)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Imported %d rows\n", report.RowsImported)

    // Run an aggregation.
    resp, err := p.Process(ctx, &pulse.Request{
        Cohort: &types.Cohort{Filename: "dataset.pulse"},
        Aggregations: []*types.Aggregation{
            {Type: types.AGG_AVERAGE, Field: "score", Label: "avg"},
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(resp.Data)

    // Inspect a file.
    result, err := p.Inspect(ctx, "dataset.pulse")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Fields: %d\n", result.FieldCount)

    // Validate a request before execution.
    prediction, err := p.Predict(ctx, &pulse.Request{
        Cohort: &types.Cohort{Filename: "dataset.pulse"},
        Aggregations: []*types.Aggregation{
            {Type: types.AGG_SUM, Field: "revenue"},
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Warnings: %v\n", prediction.Warnings)
}
```

### Custom filesystem

Pulse accepts any `afero.Fs` for testing or non-local backends:

```go
import "github.com/spf13/afero"

// In-memory filesystem for testing
p, _ := pulse.New(pulse.Options{
    FS: afero.NewMemMapFs(),
})

// Or a custom S3-backed filesystem
p, _ := pulse.New(pulse.Options{
    FS: myS3Fs,
})
```

### Available types

```go
import "github.com/frankbardon/pulse/types"

// Aggregations: AGG_COUNT, AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX,
//               AGG_STDDEV, AGG_RANGE, AGG_FREQUENCY, AGG_ZSCORE

// Filters: FILTER_INCLUDE, FILTER_EXCLUDE, FILTER_RANGE, FILTER_EXPRESSION

// Groups: GROUP_CATEGORY, GROUP_ROUNDED

// Attributes: ATTR_ZSCORE, ATTR_TSCORE, ATTR_NORMALIZED,
//             ATTR_FORMULA, ATTR_PERCENTILE, ATTR_DATE_PART

// Windows: WIN_LAG, WIN_LEAD, WIN_ROW_NUMBER, WIN_RANK, WIN_DENSE_RANK,
//          WIN_RUNNING_SUM, WIN_RUNNING_AVG, WIN_MOVING_AVG,
//          WIN_EWMA, WIN_PCT_CHANGE
```

## LLM Skill Pack

Pulse bundles 12 skill documents that teach LLM agents how to operate it. Skills are embedded in the binary via `//go:embed` — no external files needed.

### Discovering skills

```bash
# List all bundled skills
pulse skills list

# List with metadata (for programmatic consumption)
pulse skills list --json

# Read a specific skill
pulse skills show aggregation-guide
```

### Bundled skills

| Skill | Purpose |
|---|---|
| `getting-started` | Pulse vocabulary, file format, CLI overview |
| `cohort-schema-design` | Field types, nullability, descriptions |
| `aggregation-guide` | When and how to use each aggregator |
| `attribute-composition` | Derived attributes: z-score, formula, etc. |
| `grouper-design` | GROUP_CATEGORY vs GROUP_ROUNDED |
| `compose-requests` | Multi-request composition |
| `debugging-with-predict` | Iterating with `api predict` |
| `error-code-reference` | Error codes and recovery steps |
| `import-best-practices` | Schema inference, fail-closed semantics |
| `export-format-selection` | Choosing the right output format |

### Integrating with an LLM agent

The recommended workflow for an LLM agent using Pulse:

1. **Discover the surface**: `pulse --json` returns the full manifest — commands, components, field types, and skills.
2. **Load relevant skills**: based on the task, call `pulse skills show <name>` to inject domain guidance into the agent's context.
3. **Validate before execution**: use `pulse api predict` to check a request for structural errors and warnings before running it.
4. **Execute**: `pulse api process --request req.json --json` returns structured results.

From Go:

```go
import "github.com/frankbardon/pulse/skills"

// At agent boot, load all skill metadata.
for _, s := range skills.List() {
    fmt.Printf("%s: %s\n", s.Name, s.Description)
}

// Inject a specific skill into the agent's context.
content, ok := skills.Get("aggregation-guide")
if ok {
    agent.AddContext(content)
}
```

The root manifest (`pulse --json`) includes a `skills[]` array so agents can discover available skills in a single call.

## .pulse File Format

Binary, self-describing, fully transportable:

- **9-byte header**: magic bytes (`PULSE\x00\x00\x00`) + format version (`0x01`)
- **Schema block**: field count, per-field descriptors (type, name, byte offset, bit position, CSV column index, optional description)
- **Dictionary blocks**: one per categorical field (string-to-integer mapping stored inline)
- **Record data**: fixed-width binary records, one per row

15 field types:

| Type | Bytes | Notes |
|---|---|---|
| `u8`, `u16`, `u32`, `u64` | 1, 2, 4, 8 | Unsigned integers |
| `f32`, `f64` | 4, 8 | IEEE 754 floats |
| `date` | 4 | Days since Unix epoch |
| `packed_bool` | 1 | Single boolean |
| `nullable_bool` | 1 | Tri-state: true/false/null |
| `nullable_u4` | 1 | 4-bit unsigned, nullable |
| `nullable_u8`, `nullable_u16` | 1, 2 | Nullable unsigned integers |
| `categorical_u8`, `categorical_u16`, `categorical_u32` | 1, 2, 4 | Dictionary-encoded strings |

Categorical width is auto-selected from sample cardinality during import.

## Configuration

Pulse uses a single environment variable:

```bash
export PULSE_DATA_DIR=/path/to/data
```

When set, the CLI resolves relative cohort paths against this directory. Not required — absolute paths always work.

No config files. No install command.

## Development

### Build

```bash
make build    # Binary at ./bin/pulse
make test     # go test ./...
make lint     # staticcheck (auto-installed via go run)
make cover    # Coverage report
make clean    # Remove artifacts
```

### Run tests

```bash
# Full suite (17 packages, ~5 seconds)
go test ./...

# Single package
go test ./processing/...

# Verbose with specific test
go test ./service/... -v -run TestProcess

# Fuzz tests
go test ./encoding/... -fuzz FuzzPulseFileHeader -fuzztime 30s
```

### Project structure

```
pulse/
├── pulse.go              Public facade: New, Open, Process, Import, Export, ...
├── cmd/pulse/            CLI binary (thin adapter)
├── encoding/             .pulse binary format: header, schema, records
├── processing/           Aggregators, attributes, filterers, groupers
├── service/              Orchestration layer
├── io/                   Bidirectional I/O pipeline
│   ├── csv/              CSV reader + writer
│   ├── tsv/              TSV reader + writer
│   ├── ndjson/           NDJSON reader + writer
│   ├── parquet/          Parquet reader + writer (Apache Arrow)
│   └── excel/            Excel reader + writer (Excelize)
├── fs/                   Filesystem abstraction (afero)
├── errors/               Typed error codes
├── types/                Request/response types
├── descriptor/           Self-description: manifest, predict, inspect
├── skills/               Embedded LLM skill pack
└── internal/cli/         CLI internals
```

## License

MIT. See [LICENSE](LICENSE).

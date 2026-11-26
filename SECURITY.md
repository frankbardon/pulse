# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly.

**Do not open a public issue.** Report it through [GitHub's private vulnerability reporting](https://github.com/frankbardon/pulse/security/advisories/new).

Please include:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

You should receive a response within 72 hours. We'll work with you to understand the issue and coordinate a fix before any public disclosure.

## Scope

Security issues in the following areas are in scope:

- **Encoding** — `.pulse` file parsing, header/schema validation, dictionary handling
- **Processing** — Request execution (every operator category, expressions, joins, compose)
- **I/O adapters** — CSV, TSV, NDJSON, JSON array, Parquet, Arrow, Excel and SPSS readers/writers; shard archives and sidecar files
- **Filesystem boundary** — Path handling through `afero.Fs`, symlink handling
- **Descriptor** — Manifest, predict, inspect contracts and JSON envelope
- **CLI** — Flag parsing, input handling, JSON output
- **MCP server** — Tool, resource and prompt handlers (`pulse mcp`, `mcpserve`, `mcp/gosdk`), including path resolution under the data directory

## Known Considerations

- **File paths**: All filesystem access is funneled through the `afero.Fs` abstraction. Direct `os.Open`/`os.ReadFile` is forbidden in library code.
- **Field descriptions**: Capped at 1000 bytes (`PULSE_IMPORT_DESCRIPTION_TOO_LONG`).
- **Categorical dictionaries**: Bounded by the field's `categorical_u8`/`u16`/`u32` rung. An inferred rung widens; a declared rung that overflows returns `PULSE_IMPORT_CATEGORICAL_OVERFLOW`.
- **Resource limits**: Per-instance ceilings (request timeout, group and crosstab-cell counts, estimated memory, …) bound a single request; set them with `pulse.Options` or `--limit` ([Tuning limits](https://frankbardon.github.io/pulse/library/tuning-limits.html)).
- **Predict / inspect**: No-execute by structural ban: `internal/descriptor/` MUST NOT import `internal/service/` or `internal/processing/` (`TestPredictNoExecutionImports`).

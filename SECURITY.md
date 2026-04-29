# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly.

**Do not open a public issue.** Instead, email security concerns to the maintainer or use [GitHub's private vulnerability reporting](https://github.com/frankbardon/pulse/security/advisories/new).

Please include:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

You should receive a response within 72 hours. We'll work with you to understand the issue and coordinate a fix before any public disclosure.

## Scope

Security issues in the following areas are in scope:

- **Encoding** — `.pulse` file parsing, header/schema validation, dictionary handling
- **Processing** — Aggregator, attribute, filterer, and grouper execution
- **I/O adapters** — CSV, TSV, NDJSON, Parquet, Excel readers/writers
- **Filesystem boundary** — Path handling through `afero.Fs`, symlink handling
- **Descriptor** — Manifest, predict, inspect contracts and JSON envelope
- **CLI** — Flag parsing, input handling, JSON output

## Known Considerations

- **File paths**: All filesystem access is funneled through the `afero.Fs` abstraction. Direct `os.Open`/`os.ReadFile` is forbidden in library code.
- **Field descriptions**: Capped at 1000 bytes (`PULSE_IMPORT_DESCRIPTION_TOO_LONG`).
- **Categorical dictionaries**: Bounded by the chosen `categorical_u8`/`u16`/`u32` field type. Overflow returns `PULSE_IMPORT_CATEGORICAL_OVERFLOW`.
- **Predict / inspect**: No-execute by structural ban. `descriptor/predict.go` and `descriptor/inspect.go` MUST NOT import `service/` or `processing/`.

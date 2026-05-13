# Pulse Documentation

This directory holds the mdBook source for the Pulse user manual, published to
GitHub Pages at <https://frankbardon.github.io/pulse/>.

## Local preview

```
$ make docs-serve
```

(Equivalent to `mdbook serve docs --open`.)

## One-shot build

```
$ make docs
```

(Equivalent to `mdbook build docs`. Build output lands in `docs/book/`, which is gitignored. `make docs-clean` removes it.)

## Audience

This site documents the CLI, library embedding in Go, the `.pulse` file format,
internals, and operations for human readers. LLM-facing guidance lives in the
embedded skill pack under `skills/` and is loaded via MCP at runtime (see
`docs/src/mcp/index.md`).

# Pulse Documentation

This directory holds the mdBook source for the Pulse user manual, published to
GitHub Pages at <https://frankbardon.github.io/pulse/>.

## Local preview

```
$ mdbook serve --open docs
```

## One-shot build

```
$ mdbook build docs
```

The build output lands in `docs/book/`, which is gitignored.

## Audience

This site documents the CLI, library embedding in Go, the `.pulse` file format,
internals, and operations for human readers. LLM-facing guidance lives in the
embedded skill pack under `skills/` and is loaded via MCP at runtime (see
`docs/src/mcp/index.md`).

---
id: U77
slug: ci-platform
title: "CI exercises every shipped platform and untrusted-input surface, and scans for vulnerabilities."
track: API & release
size: M
status: not-started
depends_on: []
soft_depends_on: []
blocks: [U32, U66, U67, U68, U69, U70, U71, U72, U73, U74, U75, U76]
todo_items: [668, 669, 670, 671]
branch: ci-platform
---

# U77 — ci-platform

**Outcome:** CI exercises every shipped platform and untrusted-input surface, and scans for vulnerabilities.

**Track:** API & release · **Size:** M · **Depends on:** none (runs first, in parallel with the other first-wave unit)

## Summary

Platform and supply-chain CI coverage: Windows and macOS jobs, a Windows mmap decision, fuzzing of untrusted input, and vulnerability scanning with dependency updates for workflows.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [x] **#668** Windows and macOS CI jobs: every `ci.yml` job is `runs-on: ubuntu-latest` while release ships Windows and macOS binaries by cross-compile only; run the fast tier and `make smoke` on `windows-latest` and `macos-latest`. Evidence: `.github/workflows/ci.yml`. Done in U77 (E1-S1 to E1-S3): blocking `test-windows (1)` / `(2)` (the fast tier split by package, `citest fast-run` + `.github/fast-shards.txt`), `test-macos`, `smoke-windows` and `smoke-macos`, each under 6 minutes cold; jobs and Windows reproduction in `docs/src/contributing/local-ci.md`; the owner adds them to the required checks at finalize · [U77](units/U77-ci-platform.md)
- [x] **#669** Windows mmap: `internal/service/mmap_other.go:11` states Windows `MapViewOfFile` is "not implemented" (the non-mmap fallback is in use); implement it, or declare the fallback final and document it. Done in U77 (E2-S1): `internal/service/mmap_windows.go` maps cohorts read-only; every atomic rewrite releases its own source first (in-place `FilterToFile` fixed); a view another reader holds makes a rewrite fail with its coded rename error, recorded in `.claude/reference/byte-layout/mmap-and-rewrites.md` · [U77](units/U77-ci-platform.md)
- [ ] **#670** Fuzz tests on untrusted input, run in CI: request JSON decode, the expr parser, the NDJSON / JSON / Arrow / Parquet / Excel importers, full `.pulse` record read and shard-archive open (only `encoding`, `errors`, spss, csv/tsv header and arena fuzz exist; no `-fuzz` step in CI). Evidence: the eight `func Fuzz` sites · [U77](units/U77-ci-platform.md)
- [ ] **#671** `govulncheck ./...` in `ci.yml` and as a release gate, plus the `github-actions` ecosystem in `.github/dependabot.yml` (none today) · [U77](units/U77-ci-platform.md)

## Handed on

| TODO | Item | Owner |
|---|---|---|
| #684 | Path messages quote with `%q` (doubled backslashes on Windows): one path-quoting helper across ~137 engine sites | [U33](U33-v1-release.md) |

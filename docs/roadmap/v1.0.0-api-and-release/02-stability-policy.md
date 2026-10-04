# 02 — Stability policy (draft of `STABILITY.md`)

**Decided:** write a stability file. The draft below is ready to land at the repository root (U33). Its public package list is filled from the decided classification in [00](00-public-surface.md); the draft has no remaining placeholders. It stays here rather than at the root until the package moves (U02, U02b) land, because publishing the promise earlier would promise packages that are still importable but due to go internal.

---

```markdown
# Pulse Stability Policy

Pulse follows Semantic Versioning from v1.0.0. This file says exactly what is
covered by that promise, so you know what you can rely on across upgrades.

## What "stable" means

Within a major version (v1.x.y):
- **Patch** releases (v1.2.3 → v1.2.4) fix bugs only.
- **Minor** releases (v1.2 → v1.3) add features. Nothing covered below
  breaks.
- Anything covered below changes incompatibly only in a new **major** version
  (v2), which for Go means a new import path.

## Covered

| Surface | Promise |
|---|---|
| Go API | Exported identifiers in these packages: `github.com/frankbardon/pulse` (root), `types`, `encoding`, `io`, `descriptor`, `errors`, `synth`, `extend`, `mcp/gosdk`, `mcpserve`. Root aliases of internal types are covered like any other root identifier. Additions only. Checked in CI against the previous release. |
| `extend` consumer-only interfaces | `extend.Record` and `extend.Rows` are implemented by Pulse and only CALLED by embedders. Methods may be added to either in a minor release, so an embedder-side implementation (outside tests) is not covered. The operator-authoring interfaces an embedder implements (`extend.Aggregator`, `extend.Grouper`, …) follow the additions-only rule: their method sets do not grow, new capabilities arrive as optional sibling interfaces. |
| Raw-byte primitives | The `encoding` read/write primitives (header, schema, geometry, field values, null bitmap) are covered for **ungrouped (`0x01`) cohorts only**. Grouped (`0x02`) cohorts are written through `CohortBuilder`; reading both versions is covered by the file-format row below. |
| Third-party types | `afero.Fs` appears in the v1 API (`Options.FS`, the `io` factory) and is frozen with it. Pulse does not replace it within v1. |
| `.pulse` file format | Every cohort written by any v1 release stays readable by every later v1 release. Format versions 0x01 and 0x02 are readable forever. A newer format version is only written when a cohort uses a feature that needs it, and older binaries refuse it with a coded error rather than misreading it. |
| Shard archives | Same promise as `.pulse` files. |
| `--json` envelope | `{format_version, data, request, errors, warnings}`. New fields may appear; existing fields are not renamed, removed or retyped without a `format_version` bump, and a bump only happens in a major release. |
| Request / response payloads | The JSON Schema served by `pulse schema` evolves additively within v1. |
| Operator names | `AGG_*`, `TEST_*`, `OVERLAY_*` etc. are not renamed or removed within v1. Deprecated operators keep working until v2. |
| Error codes | Code strings are stable. Message text and fixup prose may improve at any time. |
| CLI | Leaf names and documented flags are stable. Human-readable (non-`--json`) output is **not** covered. |
| MCP | Tool names, tool input fields, resource URI schemes and prompt names are stable; additions only. |
| Feature profiles | A profile file valid for v1.x stays valid for every later v1 release. New features never appear in an existing profile. |
| Numerical results | For the same input and request, results are deterministic. A minor release may change a result only to fix a documented bug, and the release notes say so. |

## Not covered

- Anything under `internal/`.
- Skill-pack prose, examples, docs wording and guided-analysis text (Explain sentences, glossary wording). These improve continuously. Their *structure* (frontmatter keys, required sections) is covered by the MCP promise.
- Log messages and log fields.
- Performance characteristics, memory use, and default resource-limit values, which may be raised.
- Golden test files.
- Pre-release tags (`-rc.N`, `-beta.N`).

## Deprecation

Something deprecated in v1.x:
- is marked in GoDoc (`// Deprecated:`), in the manifest (`deprecated: true` with a replacement), and in release notes;
- keeps working for the rest of v1;
- is removed no earlier than v2.

## Reporting a break

If an upgrade within v1 breaks something covered above, that is a bug. Please
open an issue with the two versions and a reproducing request.
```

---

## Notes for review

- **The package list mirrors [00](00-public-surface.md)'s public, public-narrowed and new classes exactly.** For a narrowed package only its kept subset is exported, so "exported identifiers" is the whole promise. Embedder-facing differences from today's layout are listed in [03](03-embedder-migration.md).

- **The numerical-results row is a deliberate promise.** Pulse's goldens and determinism conventions (eigenvector sign, merge order, seeded k-means) already make it achievable, and harness and LLM consumers rely on it.
- **"Default resource-limit values may be raised"** pairs with the embedder-operations decision: high defaults that can be tuned. Lowering a default would be breaking, so the policy only allows raising one.
- **The feature-profiles row** restates the allowlist decision as a compatibility promise.

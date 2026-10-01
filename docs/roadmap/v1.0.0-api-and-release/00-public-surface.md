# 00 — Public Go surface

**Status:** proposal · **Target:** v1.0.0 · **Owner of the decision:** project maintainer, informed by a catalog of downstream usage

## Why this must happen before v1.0.0

Under Go module semantics, tagging `v1.0.0` promises that **no exported identifier in any non-`internal/` package** breaks until `v2`, and a `v2` means a new import path. Today every top-level package except `cmd/` and `internal/` is importable:

```
descriptor/  encoding/  errors/  examples/  fs/  imports/  io/  mcp/  mcpserve/
processing/  service/  skills/  synth/  template/  types/  (+ the root package pulse)
```

CLAUDE.md treats `pulse.go` as *the* public API, but the module layout does not enforce that. Anything left exported at v1.0.0 is frozen, including packages that are clearly implementation detail (`processing`, `service`). Moving a package under `internal/` after v1 is a breaking change.

## Process (decided: maintainer catalogs downstream use, then decides)

1. **Catalog.** The maintainer lists every Pulse import in the downstream library (which writes `.pulse` files) and in any other known consumer. The catalog template below captures it.
2. **Classify** each exported package as one of:
   - **Public**: frozen at v1 and covered by `STABILITY.md`.
   - **Public, narrowed**: stays importable, but most identifiers move to an `internal/` twin, and only what consumers need is re-exported.
   - **Internal**: moves under `internal/`. The root `pulse` package re-exports any type a public signature needs.
3. **Move** in one milestone, before any other v1 work lands on the same files, to keep merge conflicts short-lived.
4. **Freeze with tooling.** After the move, an API-compatibility check runs in CI against the latest tag (`golang.org/x/exp/cmd/gorelease` or `apidiff`). From `v1.0.0` on, an incompatible change to a public package fails CI.

### Catalog template

| Downstream symbol (package.Identifier) | Used for | Could the root `pulse` facade serve it instead? | Proposed class |
|---|---|---|---|
| `encoding.WritePreamble` | writing `.pulse` files | maybe — a `pulse.Writer` facade | ? |
| … | | | |

### Starting hypothesis (to be confirmed or overturned by the catalog)

| Package | Likely class | Reason |
|---|---|---|
| root `pulse` | Public | the facade |
| `types` | Public | request/response wire types, already re-exported as `pulse.*` |
| `errors` | Public | coded errors are part of the contract |
| `fs` | Public | custom-storage extension hook |
| `encoding` | **Public, narrowed** | downstream writes `.pulse` files; expose a deliberate writer/reader surface (schema building, preamble, record writing), not the codec internals |
| `io` | Public, narrowed | import/export adapters used by embedders; adapter internals go to `internal/` |
| `descriptor` | Public, narrowed | manifest / predict / inspect results are returned by the facade; builders stay internal |
| `mcp`, `mcp/gosdk`, `mcp/toolmeta`, `mcpserve` | Public (`gosdk.Register`, `mcpserve`), narrowed | embedders mount Pulse into their own MCP servers |
| `synth` | Public, narrowed | `Spec`, `Profile` and `Options` are re-exported by the facade |
| `template` | Public, narrowed | facade methods return its types |
| `processing`, `service` | **Internal** | engine; extension authors reach it through `pulse.Options.Extensions` |
| `imports`, `skills`, `examples` | Internal unless the catalog shows use | reached through facade and MCP tools |

## Interaction with other themes

- **Feature profiles** and **guided analysis** add new public types (`Profile`, `Purpose`, `Interpretation`). They should be born in their final package location, so land this audit first or at least decide each new type's home up front.
- `descriptor.ExtensionsSnapshot` and the planned `InstanceSnapshot` are implementation types. They should not be in the public surface.

## Deliverables

- [ ] Downstream catalog completed (maintainer)
- [ ] Classification decided per package
- [ ] Package moves / narrowing done; facade re-exports added
- [ ] API-compatibility check in CI against the latest release tag
- [ ] Public package list recorded in `STABILITY.md` ([02](02-stability-policy.md))

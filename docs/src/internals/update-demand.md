# The Update Demand

A change to Pulse code, configuration, file format or public surface ships
with its companions in the same PR: the skill file(s), capability
declarations, book pages and regenerated goldens that describe it, plus its
tests. A companion deferred to a follow-up PR does not get written, and the
next session reads stale docs and writes wrong code.

## Finding the companions

1. **Run `sites`.** `make sites ARGS="op <TYPE>"` (an existing operator),
   `make sites ARGS="new-op <category>"` (a new one),
   `make sites ARGS="slot <request|compose|response> <name>"` or
   `make sites ARGS="fixture <stem>"` (an example fixture cohort) prints every file and
   declaration the change lands in, the gates to rerun and the goldens to
   regenerate. See [Finding Change Sites](finding-change-sites.md).
2. **Read the trigger row.** `.claude/reference/update-demand.md` is an index
   of areas; load only the slice your change touches. Each row names the
   trigger, the companions and the gate that enforces them.
3. **Let the gates tell you.** Most rows are enforced by a test (skill
   coverage, manifest completeness, goldens, docs-generated-current). A
   failing gate names what is missing — fix the companion, never the gate.

Generated pages (`docs/src/guide`, the payload schema and manifest goldens)
are regenerated with `make docs` or `-update`, never hand-edited.

## Common companions

- **A registered operator** of any category: its atomic skill
  `skills/op-<kebab>.md` (the type in kebab case: `AGG_SET_UNION` →
  `op-agg-set-union`, synth `normal` → `op-synth-normal`), its capability declaration in
  `internal/descriptor/capabilities_*.go`, a `builtinFeatures` row in
  `internal/descriptor/features.go`, an example tagged with it under
  `internal/examples/`, and `make docs`.
- **An error code:** a `codeMetadata` entry in `errors/fixup_metadata.go` and an
  owner in `internal/descriptor/error_owners.go`.
- **A CLI leaf:** `make docs`, which regenerates its section in `docs/src/cli/reference.md`.
- **An MCP tool:** `skills/tool-<kebab>.md` (name minus `pulse_`),
  `internal/mcp/toolmeta/meta.go` and a `builtinFeatures` row.
- **An environment variable:** the table in
  [Options → Environment variables](../library/options.md#environment-variables).
- **A field type or format change:** `skills/type-<kebab>.md`,
  `skills/cohort-schema-design.md` and `.claude/reference/byte-layout.md`.
- **A `--json` envelope change:** `format_version` is additive-only — a new
  `data` field does not bump it, a rename or removal does; regenerate the
  payload-schema golden.

CLAUDE.md is not a companion: it carries only the rules every session needs.

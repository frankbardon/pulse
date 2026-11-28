# Adding an Error Code

**Audience:** Pulse internals contributors adding a new `PULSE_*` /
`SERVICE_*` / `ENCODING_*` / `PROCESSING_*` / `DATA_*` / `CLI_*` error
code, or renaming / removing one.

> **Run the site checklist first.** `make sites ARGS="error-code <CODE>"` prints, for an existing code or a new one, every table that registers codes (`codeMetadata`, `errorOwners`, `allCodes`, …), the code-family tables of its `DOMAIN_CATEGORY` head, its emitters and the gates below. See [Finding Change Sites](finding-change-sites.md).

The Pulse error system runs under six domains. Every code carries a
typed `CodedError`, a human-readable `Message`, and at least one
`Fixup` template explaining how to recover. `pulse errors list` prints
every registered code.

## 1. Declare the code constant

Add the constant in `errors/codes.go` and append it to the `allCodes`
slice. Pick the domain prefix:

| Domain | When to use |
|---|---|
| `CLI_*` | Bad invocation, missing flag, ambiguous subcommand |
| `DATA_*` | Source file unreadable, encoding mismatch, IO failure |
| `ENCODING_*` | `.pulse` magic / version / schema parse failure |
| `PROCESSING_*` | Operator-level configuration or runtime error |
| `PULSE_*` | Top-level Pulse-API error visible to embedders and the MCP surface (most common for new codes) |
| `SERVICE_*` | Orchestration / validation error caught before execution |

## 2. Declare the message + fixup template

Add an entry to the `codeMetadata` map in `errors/fixup_metadata.go`.
Every code MUST have either:

- A `Message` plus one or more `Fixup` templates explaining how to
  recover (the canonical case), or
- `FixupNotApplicable: true` when there is truly no remediation the
  caller can apply (rare — most error codes are recoverable in
  principle).

A message that names a filesystem path quotes it with
`pathq.Quote(path)` (`internal/pathq`) and formats it with `%s`, never
`%q` or `strconv.Quote`: those escape every backslash, so a Windows path
would read `"C:\\Users\\…"`. `pathq.Quote` writes `"C:\Users\…"` and
leaves a Unix path byte-identical to `%q`. `details.path` (or whichever
details key names the path) carries the raw path. `TestNoQuotedPathVerbs`
refuses a `%q` of an operand named like a path (`dir`, `opts.SourcePath`)
or after a path noun (`"source file %q"`); its allowlist names the
look-alikes that are not filesystem paths (a JSON path, a Go import path).

`TestCodesHaveFixups` enforces that every code has either fixups or
the explicit `FixupNotApplicable: true` flag. The fixup templates are
surfaced to MCP clients via `pulse_errors_lookup` and to CLI users via
`pulse errors lookup CODE`.

## 3. Name its owner and document it

- Add an owner entry in `internal/descriptor/error_owners.go`: the
  feature names that can raise it, or `shared`. A profile that hides
  every owner hides the code, and the Message may name only owning
  features (`TestErrorOwners_Complete`,
  `TestErrorMessagesNameOnlyOwners`).
- Mention the code verbatim on a book page or in the skill that
  describes the failure (the skill renders into the guide):
  `TestDocsCoverageNoGaps` requires an mdBook mention for every code.
- Regenerate the manifest golden (`go test -count=1 ./descriptor/
  -update`, then rerun without `-update`) and run `make docs`.

## 4. Wire any test that emits the new code

If your change introduces the code as part of a new failure mode,
make sure the test that exercises that mode asserts on the code
constant rather than the error message.

## 5. Run the gates

```bash
go test -count=1 ./errors/ -run 'TestCodesHaveFixups|TestErrorsLookup'
go test -count=1 ./internal/descriptor/ -run 'TestManifestErrorCodesComplete|TestManifest_ErrorCodesSlim|TestErrorOwners_Complete|TestErrorMessagesNameOnlyOwners'
make doc-checks
```

The Update Demand row for error codes covers all of these in one PR;
see [The Update Demand](update-demand.md).

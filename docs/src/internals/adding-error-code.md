# Adding an Error Code

**Audience:** Pulse internals contributors adding a new `PULSE_*` /
`SERVICE_*` / `ENCODING_*` / `PROCESSING_*` / `DATA_*` / `CLI_*` error
code, or renaming / removing one.

The Pulse error system runs under six domains. Every code carries a
typed `CodedError`, a human-readable `Message`, and at least one
`Fixup` template explaining how to recover. The full taxonomy is in
[The Update Demand](update-demand.md).

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

`TestCodesHaveFixups` enforces that every code has either fixups or
the explicit `FixupNotApplicable: true` flag. The fixup templates are
surfaced to MCP clients via `pulse_errors_lookup` and to CLI users via
`pulse errors lookup CODE` — no separate skill-file edit is required.

## 3. Wire any test that emits the new code

If your change introduces the code as part of a new failure mode,
make sure the test that exercises that mode asserts on the code
constant rather than the error message.

## 4. Run the gates

```bash
go test ./errors/ -run 'TestCodesHaveFixups|TestErrorsLookup'
go test ./descriptor/ -run 'TestManifestErrorCodesComplete|TestManifest_ErrorCodesSlim'
```

The Update Demand row for error codes covers all of these in one PR;
see [The Update Demand](update-demand.md).

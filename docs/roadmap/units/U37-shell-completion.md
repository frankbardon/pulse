---
id: U37
slug: shell-completion
title: "The pulse CLI completes commands, flags and values natively in the terminal"
track: API & release
size: M
status: done
depends_on: []
soft_depends_on: [U06]
blocks: [U32]
todo_items: [208, 209, 210, 211, 212]
branch: shell-completion
---

# U37 — shell-completion

**Outcome:** The `pulse` CLI completes commands, flags and values natively in the terminal.

**Track:** API & release · **Size:** M · **Depends on:** none · **Soft:** [U06](U06-profiles-mcp-tooling.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit documents it)

## Summary

The CLI has no shell completion today: `cmd/pulse` / `internal/cli` never enable urfave/cli v3's completion support (`github.com/urfave/cli/v3` v3.10.1). This unit adds a `pulse completion <shell>` leaf that prints an installable script for bash, zsh, fish and PowerShell. Every command, subcommand and flag in the `buildApp()` tree completes statically. Flags whose values come from a closed vocabulary complete dynamically, and so do cohort paths and field names. Every dynamic candidate list is **sourced from the library**: the manifest, `ListTemplates`, the feature table, the I/O format registry, the error-code list and header-only `Inspect`. None is hard-coded in the CLI, which keeps the "CLI never contains business logic" rule and keeps feature-profile scoping honest.

Numbering note: appended as U37 after U36, not renumbered.

## References

**Read before starting:**
- CLAUDE.md "Design principles" (library-first; the CLI is a thin adapter) and "Update Demand" (the CLI-leaf row)
- `.claude/reference/update-demand.md`: the CLI leaf row (`docs/src/cli/flags.md`, `commandBindings`, `TestSkillsCoverAllCliLeaves`)
- `.claude/reference/feature-profiles.md`: `commandBindings`, core surfaces, instance scoping
- urfave/cli v3 shell-completion docs (`EnableShellCompletion`, `ShellComplete` funcs, the built-in completion command)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#208** (1. API surface & release pipeline › CLI shell completion) `pulse completion {bash,zsh,fish}` prints an installable script; every command, subcommand and flag of `buildApp()` completes
- [x] **#209** (1. › CLI shell completion) Closed-vocabulary flag values complete dynamically from the library, never from a CLI-side list: the vocabularies that have a consumer flag: example feature profiles, I/O formats, error codes (`pulse errors lookup`), skills, examples and enum-valued flags (operator, feature and template completion are out: no consumer flag)
- [x] **#210** (1. › CLI shell completion) Cohort-aware completion: `.pulse` cohorts (single-file and shard archive) under `PULSE_DATA_DIR`, and field names from a cohort's header for field-taking flags. Header only: never reads a record
- [x] **#211** (1. › CLI shell completion) Gates: every runnable leaf and flag is reachable by completion; candidates are deterministic and quick; completion mode never writes anything but candidates to stdout and never fails noisily (a bad cohort or env just yields no candidates)
- [x] **#212** (1. › CLI shell completion) Install docs per shell (`docs/src/cli/completion.md` + SUMMARY), a `flags.md` row, and the release archives (`make dist`) ship the generated scripts

## Scope

**In scope**
- The `completion` leaf with its `commandBindings` entry. It is a core surface, so feature profiles never hide it.
- Static completion for the whole command tree
- Dynamic value completion through library calls, with an embedder-free default instance (honouring `PULSE_FEATURE_PROFILE`?, see Open decisions)
- Cohort path and field-name completion through header-only reads
- Script generation for bash, zsh, fish and pwsh; scripts bundled in `make dist`
- Tests, docs and the Update Demand companions

**Out of scope**
- Completion inside request JSON (`--request` bodies), i.e. editor/LSP-style JSON completion
- An interactive TUI or REPL
- Package-manager formulae (Homebrew tap, etc.), unless a later release unit adds them
- MCP-side completion (MCP `completion/complete`), a separate surface

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(shell-completion/E<n>-S<m>): …`; close each epic with `milestone(shell-completion/E<n>): vertical slice complete — <epic title>`.

### E1 — Commands and flags complete in every supported shell
- S1: enable completion across `buildApp()`; `pulse completion <shell>` leaf + `commandBindings` + `flags.md` row; a gate walking the tree to assert every leaf and flag completes
- S2: script generation for bash, zsh, fish and pwsh; `make dist` bundles them; install docs

### E2 — Values complete from the library
- S1: closed-vocabulary flag values from library calls (manifest operators, features, I/O formats, templates, error codes, enum flags)
- S2: cohort paths under `PULSE_DATA_DIR` plus header-only field names; quiet failure; latency budget test

## Acceptance criteria

- [ ] `source <(pulse completion zsh)` (and the bash / fish / pwsh equivalents) completes `pulse api pro<TAB>` → `process`, `process-chain`
- [ ] `pulse errors lookup PULSE_MAT<TAB>` lists the matrix codes from the library's error list
- [ ] Field-taking flags complete the field names of the named cohort without decoding a record
- [ ] No candidate list is a literal in `cmd/pulse` / `internal/cli` (grep or AST gate)
- [ ] `format_version` unchanged; no wire change
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverAllCliLeaves` / `TestCommandBindings_Complete` pick up the new leaf
- New completion-coverage gate over the `buildApp()` tree (avoid the prefix-matched gate families unless it is listed in CLAUDE.md)
- Header-only assertion for field completion (no record read)

## Update Demand companions

- `docs/src/cli/flags.md` command index; `internal/descriptor/features.go` `commandBindings`
- `skills/session-bootstrap.md` only if a flag is agent-relevant (probably not)
- CLAUDE.md "Architecture" CLI command list (keep under 40,000 bytes)

## Human inputs & decisions

- **Decided:** feature profiles are ignored; candidates are always the full built-in vocabularies
- **Decided:** no public `Complete` API; candidate generation stays CLI glue over existing public library listers
- **Decided:** field-taking flags read their cohort from `--input`, else the positional cohort, else `--cohort`; `--request` is never read
- **Decided:** PowerShell is dropped (its script never passes the preceding words); shells are bash, zsh and fish
- **Decided:** the scripts stay byte-equal to urfave/cli's own output; `completion` is bound Ungated in `commandBindings`
- **Accepted limit:** `--labels field=table[:mode]` completes the field part only; `--vector-kind NAME:kind` does not complete
- **Accepted limit:** zsh offers only the data-dir cohorts (no file fallback) when `PULSE_DATA_DIR` holds cohorts; urfave's zsh script falls back to files only on empty output
- **Accepted limit:** comma-list flags complete the first element only
- **Accepted limit:** bash needs the `bash-completion` package (the script calls `_get_comp_words_by_ref`)

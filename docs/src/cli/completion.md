# completion

`pulse completion bash`, `pulse completion zsh` and `pulse completion fish`
print a shell completion script on stdout. Install it once and the shell
completes Pulse's command line as you type. Supported shells: bash, zsh and
fish. PowerShell is not supported (see [Unsupported shells](#unsupported-shells)).

## Install

Every release archive also ships the three scripts, pre-generated, under
`completions/`:

| Shell | Shipped file | Equivalent command |
|---|---|---|
| bash | `completions/pulse.bash` | `pulse completion bash` |
| zsh | `completions/_pulse` | `pulse completion zsh` |
| fish | `completions/pulse.fish` | `pulse completion fish` |

Each shipped file is byte-for-byte the output of the matching command for
that release, so either route installs the same script. Regenerate it after
upgrading `pulse` (or source it live, as below) so new commands and flags
complete.

### bash

The script needs the [`bash-completion`](https://github.com/scop/bash-completion)
package (it calls `_get_comp_words_by_ref`; under plain bash it prints
`_get_comp_words_by_ref: command not found`). Install that first, then
source the script from your shell startup file:

```sh
# ~/.bashrc — always matches the installed binary
source <(pulse completion bash)
```

bash 3.2 (the macOS system bash) cannot `source` a process substitution;
use `eval "$(pulse completion bash)"` there instead.

Or install the shipped file where `bash-completion` looks for it:

```sh
mkdir -p ~/.local/share/bash-completion/completions
cp completions/pulse.bash ~/.local/share/bash-completion/completions/pulse
```

### zsh

Source it from `~/.zshrc` after `compinit`:

```sh
# ~/.zshrc
autoload -U compinit && compinit
source <(pulse completion zsh)
```

Or put the shipped `_pulse` on your `fpath` (it starts with `#compdef pulse`,
so `compinit` autoloads it):

```sh
mkdir -p ~/.zsh/completions
cp completions/_pulse ~/.zsh/completions/_pulse
# ~/.zshrc, before compinit:
fpath=(~/.zsh/completions $fpath)
autoload -U compinit && compinit
```

### fish

fish autoloads completions from `~/.config/fish/completions/`:

```sh
pulse completion fish > ~/.config/fish/completions/pulse.fish
# or, from a release archive:
cp completions/pulse.fish ~/.config/fish/completions/pulse.fish
```

## What completes

- **Commands and subcommands.** A word that does not start with `-`
  completes the subcommands of the command typed so far
  (`pulse coh<TAB>` → `cohort`, `pulse cohort <TAB>` → its leaves).
- **Flags.** A word starting with `-` completes the flags of the command
  typed so far, the root's persistent flags (`--log-level`,
  `--log-format`, `--shard-workers`, `--decode-workers`, `--limit`) included.
- **Flag values.** After a flag that takes a value (`--mode <TAB>`), a
  closed vocabulary completes from the same library list the flag's
  validator reads, so the candidates are always exactly what the flag
  accepts:

  | Flag | Candidates |
  |---|---|
  | `--intent` (root, `recommend`, `examples search`) | intent IDs |
  | `--log-level`, `--log-format` | the logging levels and formats |
  | `--format`; `convert --from` / `--to` | I/O format names |
  | `--float` | float widths |
  | `--dst-policy` | DST policies (`earlier`, `error`, `later`) |
  | `--spss-missing` | SPSS user-missing modes |
  | `api lookup --mode` | `all`, `assert_unique`, `first` |
  | `errors list --domain` | error domains |
  | `explain --root` / `--detail` | request roots / detail levels |
  | `recommend --level` | guidance levels |
  | `rake --missing` | missing-value policies |
  | `widen --to` | the wider set types |
  | `examples search --category` / `--tag` | example categories / tags |
  | `features init --from` | the built-in example feature profiles |
  | `mcp --return` | response presets |
  | `--limit` (root, every leaf) | `name=` for every resource limit |

  Type the value after a space (`--mode first`), not `--mode=<TAB>`: the
  `=` form does not complete.
- **Positional arguments.** `errors lookup CODE` completes error codes
  (`pulse errors lookup PULSE_MAT<TAB>` lists the matrix codes),
  `skills show NAME` skill names, `examples show NAME` example names,
  `import drop HANDLE` managed-import handles, and the second argument of
  `shard remove` / `shard extract` the shard basenames of the archive
  named first.
- **Cohort paths.** A cohort flag (`--input` on a cohort-reading leaf,
  `recommend --cohort`, `synth from-profile --source`) and a cohort
  positional (`cohort inspect`, `widen`, `dedup`, `rake`,
  `index build|list|verify|drop`, `shard add|list|compact|verify|remove|extract`)
  complete the `.pulse` cohorts under `PULSE_DATA_DIR` — single files and
  shard archives, in subdirectories too (managed imports included), never
  a `.pulse.zst` transfer artifact. Each prints as the data directory joined with the cohort's
  path under it, the path the leaf then opens. With `PULSE_DATA_DIR`
  unset (or holding no cohort) nothing is offered and the shell completes
  file names; in bash a typed prefix none of the candidates matches
  (`./`, `~/`) falls back to file names too. In zsh, when
  `PULSE_DATA_DIR` holds cohorts, a cohort slot offers only those cohorts
  and not local files (the zsh script falls back to file completion only
  on empty output): unset `PULSE_DATA_DIR` or type the path by hand.
- **Field names.** A field-taking flag completes the schema fields of the
  cohort the leaf names, read from `--input`, else the leaf's positional
  cohort, else `--cohort`. A `--request` file is never read. The flags:
  `export --include` and the field of `--labels field=table`,
  `cohort filter --include-field`, `api facet --field` / `--additive`,
  `api lookup --key` / `--return`, `api sample --labels`,
  `widen --field`, `dedup --group`, `rake --by` / `--base-weight`,
  `index build|verify|drop --key`, `recommend --field`. Only the field name completes:
  not the table or mode after the colon of `--labels field=table[:mode]`,
  and in a comma list (`api lookup --key` / `--return`, `index --key`,
  `dedup --group`) only a first element (the scripts never pass a partly
  typed word to the binary). `--vector-kind NAME:kind` does not complete.
  Name the cohort first: the cohort must be typed before the field flag;
  with none named nothing is offered.

Candidates are sorted and the same on every run. They are the full
built-in vocabularies; a feature profile does not narrow them.

Not completed: request JSON bodies, time zones, charsets, numbers, free
text and the source-file columns of the `import` family. Host file paths
fall to the shell's default completion, normally file names. Completion reads only what a candidate list needs (the error-code
table, the skill and example library, the managed-imports directory, the
`PULSE_DATA_DIR` directory listing, a cohort's header and schema, an
archive's central directory and shard headers) — never a record — and
never writes a file: it runs on a read-only filesystem. An unset or
missing `PULSE_DATA_DIR`, an unreadable directory, a missing or corrupt
cohort or archive, or any other failure leaves the candidates empty,
never an error.

## Quiet by design

A completion request never prints to stderr, always exits 0 and has no
side effect, whatever the words typed so far contain (a bad `--log-level`,
a negative worker count, an unknown flag or a missing `PULSE_DATA_DIR`).
A half-typed command line never disturbs the shell.

## Unsupported shells

PowerShell is not supported: its completion script hands the binary only
the word being completed, never the words before it, so the command and
flag context is lost. `pulse completion` offers bash, zsh and fish only.

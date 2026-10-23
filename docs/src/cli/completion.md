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

Source the script from your shell startup file; it works with or without
the `bash-completion` package:

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
  `--log-format`, `--shard-workers`, `--decode-workers`) included.

Flag values and positional arguments (cohort paths, field names) are not
completed by Pulse today; the shell falls back to its default completion,
normally file names. Completion reads no cohort, so it does not depend on
`PULSE_DATA_DIR`: an unset or missing data directory never breaks it.

## Quiet by design

A completion request never prints to stderr, always exits 0 and has no
side effect, whatever the words typed so far contain (a bad `--log-level`,
a negative worker count, an unknown flag or a missing `PULSE_DATA_DIR`).
A half-typed command line never disturbs the shell.

## Unsupported shells

PowerShell is not supported: its completion script hands the binary only
the word being completed, never the words before it, so the command and
flag context is lost. `pulse completion` offers bash, zsh and fish only.

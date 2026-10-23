# completion

`pulse completion bash`, `pulse completion zsh` and `pulse completion fish`
print a shell completion script on stdout. Install it once and the shell
completes Pulse's commands, subcommands and flags:

```sh
# bash (e.g. in ~/.bashrc)
source <(pulse completion bash)

# zsh (e.g. in ~/.zshrc)
source <(pulse completion zsh)

# fish
pulse completion fish > ~/.config/fish/completions/pulse.fish
```

A word starting with `-` completes the flags of the command typed so far,
the root's persistent flags (`--log-level`, `--shard-workers`, …) included;
any other word completes subcommands. PowerShell is not supported: its
script hands the binary only the word being completed, so the command and
flag context is lost.

Completion is quiet: a completion request never prints to stderr, always
exits 0 and has no side effect, whatever the words typed so far contain (a
bad `--log-level`, a negative worker count, an unknown flag or a missing
`PULSE_DATA_DIR`).

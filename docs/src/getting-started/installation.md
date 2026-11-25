# Installation

**Audience:** new users who want a working `pulse` binary on their PATH.

Pulse is one static binary: no installer, no daemon, no config file. The
library needs only `go get github.com/frankbardon/pulse`; embedders start
at the [Embedder Walkthrough](../library/embedder-walkthrough.md).

> **LLM agents using MCP:** the `session-bootstrap` skill (via
> `pulse_skills_get`) covers the session, not the install.

## Download a release

Each [GitHub Release](https://github.com/frankbardon/pulse/releases)
carries one archive per platform plus `checksums.txt` (SHA-256 of every
archive). `<version>` is the tag, `v` included (`v1.0.0`):

| Platform | Asset |
|---|---|
| Linux x86-64 | `pulse_<version>_linux_amd64.tar.gz` |
| Linux ARM64 | `pulse_<version>_linux_arm64.tar.gz` |
| macOS Intel | `pulse_<version>_darwin_amd64.tar.gz` |
| macOS Apple silicon | `pulse_<version>_darwin_arm64.tar.gz` |
| Windows x86-64 | `pulse_<version>_windows_amd64.zip` |
| Windows ARM64 | `pulse_<version>_windows_arm64.zip` |

An archive holds the binary (`pulse`, or `pulse.exe` on Windows) at its
root, with `LICENSE`, `THIRD_PARTY_NOTICES.md`, `README.md` and shell
completion scripts under `completions/` (bash, zsh, fish). On Linux or
macOS:

```bash
VERSION=v1.0.0 OS=linux ARCH=amd64
BASE=https://github.com/frankbardon/pulse/releases/download/$VERSION
curl -fLO "$BASE/pulse_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fLO "$BASE/checksums.txt"
grep "_${OS}_${ARCH}.tar.gz" checksums.txt | shasum -a 256 -c -
tar -xzf "pulse_${VERSION}_${OS}_${ARCH}.tar.gz" pulse
sudo mv pulse /usr/local/bin/
```

Pre-release tags (`v1.0.0-alpha.N`) are marked pre-release on GitHub and
carry the same assets.

## Install with `go install`

With a Go toolchain at the version `go.mod` names (1.26.1 today):

```bash
go install github.com/frankbardon/pulse/cmd/pulse@latest
```

The binary lands in `$(go env GOBIN)` (default `~/go/bin`); put it on
your `PATH`. Replace `@latest` with a tag (`@v1.0.0`) to pin a release.

## Build from source

```bash
git clone https://github.com/frankbardon/pulse.git
cd pulse
make build      # binary at ./bin/pulse
```

[Development Setup](../contributing/setup.md) covers the other targets.

## Set the data directory

`PULSE_DATA_DIR` is the one required environment variable: `pulse mcp`
refuses to start without it and serves the cohorts under it. The other
CLI commands resolve paths against the working directory, so the next
pages work inside the data directory:

```bash
export PULSE_DATA_DIR="$HOME/pulse-data"
mkdir -p "$PULSE_DATA_DIR" && cd "$PULSE_DATA_DIR"
```

Every other `PULSE_*` variable is optional; the table is in
[pulse.New & Options](../library/options.md#environment-variables).

## Verify

```bash
pulse --version
pulse version --json
```

`pulse version --json` prints the build in the standard envelope
(`"format_version": "1.1"`). Shell completion:
[`pulse completion`](../cli/completion.md).

## Next

[Your First Cohort](first-cohort.md): a CSV to a `.pulse` file in four
commands.

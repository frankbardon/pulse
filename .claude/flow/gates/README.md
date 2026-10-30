# flow-* gate scripts

Each `<event>.sh` here runs before the matching flow action. Non-zero exit blocks.

Events: pre-commit, pre-push, pre-pr-open, pre-pr-ready, pre-merge, pre-release.

- `pre-commit` — every story commit: gofmt check + `go vet` (fast).
- `pre-push` — every test-group checkpoint: full `make lint` + tests, both
  tiers (`-tags heavy` runs fast + heavy). Under `FLOW_GATE_SCOPE=changed`
  only the affected packages are tested (`go-affected-pkgs.sh`); otherwise
  `./...`.
- `pre-pr-ready` — before the effort PR leaves draft: `make lint`, the full
  suite with `-tags heavy` (fast + heavy tier), `make smoke`.

Lint pins `GOTOOLCHAIN=go1.26.1` (a newer local toolchain breaks
staticcheck's export data). Tests run with `-count=1` so a cached pass can't
hide a stale golden. CI status (`gh pr checks`) is still checked by hand after
every push — amd64 FMA divergence is invisible on arm64.

Re-run `/flow-backfill` (Mode B) to add or edit gates.

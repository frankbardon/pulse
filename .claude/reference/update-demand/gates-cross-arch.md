# Update Demand — cross-arch determinism gates

Per-area slice of the Update Demand reference. Index: `.claude/reference/update-demand.md`; the other engine, CI-job and CLAUDE.md-listed gates are in `.claude/reference/update-demand/gates.md`. Policy (FMA-free products, repo-wide enforcement): `.claude/reference/matrix-and-vectors/linalg.md`.

## Cross-arch determinism gates

**Prefix-matched (test-hygiene, TODO #598).** `TestClaudeMdMentionsAllNonSkippableGates` matches the `TestCrossArch` prefix, so every `TestCrossArch*` test is listed by name in CLAUDE.md "Non-Skippable CI Gates" (the "Cross-arch determinism:" bullet). A new cross-architecture gate takes the prefix and its CLAUDE.md entry in the same commit. The two gates below took the prefix in a rename; history docs keep their former names.

**Numeric determinism (U61).** `TestCrossArchBannedMath_RepoWide` (fast) + `TestCrossArchFMAReport_RepoWide` (heavy, catchall shard), `internal/testutil/fmagate`, scan every non-test package minus `cmd/` and `internal/cli` — the first for a banned stdlib transcendental (`math.Exp` / `Log` / `Pow` / `Erf` / …; `Sqrt`, `Abs` and the other IEEE-exact calls never count), the second for a product the linux/arm64 compiler contracts into an FMA (`-d=fmahash=vy`, a plain `GOARCH=arm64` cross-build on any host). Each unlisted site fails with file:line, function and fix — a `linalg/detmath` twin or a `float64(a*b)` barrier — or a reviewed entry with a reason in `testdata/numeric-allowlist.txt`; a stale, malformed or duplicate entry fails too. The per-package `Test*_NoFMAContraction` gates stay (not prefix-matched). Blind to contractions inside gonum and to `math/cmplx`.

- `TestCrossArchBannedMath_RepoWide` — no unlisted banned stdlib transcendental on a numeric result path (fast tier).
- `TestCrossArchFMAReport_RepoWide` — no unlisted contractible product in the arm64 FMA report (heavy tier).

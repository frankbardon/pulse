# Update Demand — cross-arch determinism gates

Per-area slice of the Update Demand reference. Index: `.claude/reference/update-demand.md`; the other engine and CI-job gates are in `.claude/reference/update-demand/gates.md`. Policy (FMA-free products, repo-wide enforcement): `.claude/reference/matrix-and-vectors/linalg.md`.

## Cross-arch determinism gates

A new cross-architecture gate takes the `TestCrossArch` prefix and a row in the `docs/src/contributing/testing.md` gate table.

`TestCrossArchBannedMath_RepoWide` (fast) and `TestCrossArchFMAReport_RepoWide` (heavy), both on `internal/testutil/fmagate`, scan every non-test package except `cmd/` and `internal/cli`:

- `TestCrossArchBannedMath_RepoWide` — no unlisted banned stdlib transcendental (`math.Exp` / `Log` / `Pow` / `Erf` / …) on a numeric result path. `Sqrt`, `Abs` and the other IEEE-exact calls never count.
- `TestCrossArchFMAReport_RepoWide` — no unlisted product that the linux/arm64 compiler contracts into an FMA (`-d=fmahash=vy`, a plain `GOARCH=arm64` cross-build on any host).

Each unlisted site fails with file:line, function and the fix — a `linalg/detmath` twin or a `float64(a*b)` barrier — or needs a reviewed entry with a reason in `internal/testutil/fmagate/testdata/numeric-allowlist.txt`; a stale, malformed or duplicate entry fails too. The per-package `Test*_NoFMAContraction` gates stay alongside. Both gates are blind to contractions inside gonum and to `math/cmplx`.

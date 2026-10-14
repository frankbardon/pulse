# Agent working practices

**Load before any delegated or agent work.** Every `.claude/agents/pulse-*.md` persona points here first. These rules are the same for every role. A persona adds only the rules specific to its own role and repeats the few that matter most for it. If a persona and this file disagree, this file wins; report the conflict.

Each rule below has a one-line reason. The reasons come from failures this repo has already shipped.

## Context loading

- Read `CLAUDE.md` first. Next read the reference INDEX for your change from its "Reference Docs" table, for example `.claude/reference/update-demand.md`. Then load only the per-area slice or slices your change triggers. A file with a same-named subdirectory is an index, and its "How to load" note names the slices. *Why: the reference corpus is several hundred KB, and bulk-loading it uses up the context the actual change needs.*
- Never load a whole `.claude/reference/` directory or every slice "to be safe". Unsure which slice holds a rule? Run `grep -l '<identifier>' .claude/reference/<index>/*.md`.
- Load a `history.md` slice only when you need the rationale for a past decision. *Why: it holds superseded wording, and acting on it reintroduces fixed bugs.*
- If you were handed an effort brief or a dispatch contract, read it before re-exploring the repo.

## Sites first

- Before grepping, run the `sites` tool for the operator, slot or category you are changing: `make sites ARGS="op <TYPE>"`, `ARGS="new-op <category>"`, `ARGS="slot <request|response> <name>"` or `ARGS="categories"` (also `go run ./internal/tools/sites [--json] …`). Recipe: `docs/src/internals/finding-change-sites.md`. *Why: the tool derives the file checklist, the gates to rerun and the goldens to regenerate from code. A hand grep misses the sites that do not name the identifier.*
- Use grep only to fill gaps. Report every site the tool missed in your hand-back so the tool can be fixed.
- The tool finds the sites, and the reference slices say what each site must contain. Read the slices it lists under "Read before editing".

## TDD and falsification

- Tests go in the same commit as the implementation, with no exceptions. *Why: CLAUDE.md "What NOT to Do". An untested change gets merged and never gets its test.*
- Prove each key new test can fail. Revert one production line, confirm the test fails, then restore the line. *Why: about half of past work packages contained a test that passed whether or not the code was right.*
- Never use `t.Skip` and never disable a gate to get green. Report the failing gate with its name and an error excerpt.

## Goldens and generated files

- Never hand-edit a golden or a generated file. Regenerate with `-update` (`go test ./descriptor/ -run 'Test.*Golden' -update`, and the apigolden equivalent) or with the generator (`make docs` regenerates `docs/src/guide`; `make reference`). Then re-run without `-update`. *Why: `TestGoldensNotHandEdited` and `TestDocsGeneratedCurrent` catch hand edits, and a hand edit hides a real diff.*
- Always pass `go test -count=1`. *Why: a cached pass once hid a stale API golden.*

## Cross-architecture numerics

- Values that pass through gonum decompositions (SymEigen, SVD, QR), rotations or math-library tails (chi-square and similar) can differ by a few ulps between amd64 (CI) and arm64 (local). `.claude/reference/matrix-and-vectors.md` permits this.
- Never store the exact bits of such a value in a golden or an oracle fixture. Compare stored fixtures within the documented tolerance. *Why: a fixture that matched bit for bit on arm64 failed in amd64 CI.*
- Keep bit-exact checks only in two cases: (a) both sides are computed live in the same process, for example across `DecodeWorkers` or `ShardWorkers` counts; (b) the path is declared FMA-free (`CoMoment`, the SPD reference kernels, the metric registry).
- In an FMA-free kernel, write the barrier as an explicit `float64(x*y)`.
- To reproduce on amd64: `GOOS=linux GOARCH=amd64 go test -c -o t.amd64 ./<pkg>`, then `docker run --rm --platform linux/amd64 -v "$PWD":/w -w /w/<pkg> golang ./t.amd64` (adjust paths).

## Local gate set

This set covers everything CI runs, plus formatting:

1. `gofmt -l .` must print nothing. Neither lint nor CI checks formatting, so nobody else will catch it.
2. `GOTOOLCHAIN=go1.26.1 make lint` (vet + staticcheck). *Why: with a newer local toolchain, staticcheck breaks on export data.*
3. `go test -count=1 ./...`
4. `make smoke`. The embedder module is nested, so `./...` never reaches it.
5. `GOTOOLCHAIN=go1.26.1 make contrib` (the nested contrib modules; tidy, vet, staticcheck, test).

A story or companion agent runs only the tests for the packages it touched, plus any whole-repo gate it added or changed, plus lint, unless it is told otherwise. The checkpoint or the PR owner runs the full set. After a push, check CI itself (`gh pr checks`). *Why: local arm64 misses amd64 divergence.*

## Shared-checkout safety

Other agents may be working in the same checkout and git index.

- Never run `git add -A`, `git commit -a`, `reset`, `stash`, `--amend`, `checkout` / `switch`, or a rebase of commits you did not write.
- Stage and commit explicit paths with `git commit -F <msgfile> -- <paths>`. Check `git status` for surprise files first.
- Never commit `.planning/`.
- Never put a CI-skip directive in any commit message. *Why: a skipped milestone commit once left the final commits without CI and blocked the PR.*
- Commit, but do not push, unless your dispatch says otherwise.

## Known limits and decisions

- Do not defer silently. If a known limit is contained, fix it in the same story or PR. *Why: deferred doc and skill updates never happened.*
- If a limit is not contained, hand it back with a size estimate (the sites involved, roughly how much work) so the owner can add a fix story. Do not leave it as a release-note caveat.
- A contract-level choice (wire shape, a default, a refusal versus a coercion, a `format_version` question) belongs to the owner. Return it as needs-decision with the question. Do not guess.

## Predict/runtime parity

- Every new refusal, execution arm, dispatch decision or streamability verdict needs a test that predict and runtime agree on the same request. *Why: `internal/descriptor/predict.go` cannot import the engine, so the two sides drift unless a parity test pins them. Several shipped predict/runtime mismatches came from exactly this.*

## CLAUDE.md budget

- `CLAUDE.md` must stay at or under 40,000 bytes (`TestClaudeMdSizeBudget`). To fit new content, move long-form prose into the per-area reference slice and leave a one-line pointer. Never raise the ceiling. *Why: CLAUDE.md is loaded on every turn of every session.*
- Reference indexes and slices have their own ceilings (`reference_docs_test.go`). A slice that grows past its ceiling is split again, and the new slice is named in its index.
- Every `.claude/reference/...` path or section pointer you write must resolve (`TestReferenceDocPointersResolve`, `TestSkillsCoverAllCrossReferences`).

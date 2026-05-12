# Pull Request Process

**Audience:** contributors preparing to open or land a PR.

This page is a checklist. The longer prose lives in
[`CONTRIBUTING.md`](https://github.com/frankbardon/pulse/blob/main/CONTRIBUTING.md)
and the Update Demand chapter.

## 1. Branch and commit shape

- One feature or fix per PR. Keep the diff focused.
- Conventional Commits in the subject line: `feat(...)`, `fix(...)`,
  `chore(...)`, `docs(...)`, `perf(...)`, `refactor(...)`, `test(...)`.
- The PR title is usually the lead commit's subject.

## 2. Tests first

A PR that adds a new aggregator, error code, field type, I/O format,
statistical test, or skill **must** include tests in the same PR. The
testing-first preference is documented in [Testing
Conventions](testing.md). Implementation that lands without tests will
be sent back; tests that pass without the implementation are
suspicious and probably wrong.

## 3. The Update Demand

The single biggest source of "your PR was bounced" feedback. The full
table lives in [The Update Demand](../internals/update-demand.md);
the cliff-notes are:

| Change category | Doc/skill update required in the same PR |
|---|---|
| Registered aggregator / attribute / filterer / grouper | The matching skill file + the operator capability table |
| Registered window / feature / synth distribution / statistical test | Same — skill + capability file |
| Error code (added / removed / renamed) | `errors/codes.go`, `skills/error-code-reference.md`, `descriptor/capabilities_errors.go` |
| CLI leaf (added or flag added) | `CLAUDE.md` "Common Claude Code Workflows" + `skills/getting-started.md` if user-facing |
| `--json` envelope change | `CLAUDE.md` "Output Format Contract" |
| `.pulse` file format change | `CLAUDE.md` "Code Conventions" + `skills/cohort-schema-design.md` |
| New environment variable | `CLAUDE.md` "Build / Dev / Test Workflow" + `skills/getting-started.md` |
| New non-skippable CI gate | List it by name in `CLAUDE.md` |

If you find yourself wanting to defer the doc update to a follow-up
PR, stop. The follow-up PR will not happen, and the next contributor
will read stale guidance. Update in the same PR or do not merge.

## 4. Pre-flight checks

```bash
make fmt
make lint
make test
```

For change-category-specific gates, see [Testing → Running a subset of
gates locally](testing.md#running-a-subset-of-gates-locally).

## 5. Open the PR

- Use the bug-report or feature-request template as a starting point if
  applicable.
- Fill in the PR template's "Summary" and "Test plan" sections.
- Link related issues with `Closes #N`.
- Do **not** push `--force` to `main`. Force-pushing your own feature
  branch is fine before review starts.

## 6. Review and CI

CI runs the full `go test ./...` plus the non-skippable gates listed in
[Testing → Non-skippable CI gates](testing.md#non-skippable-ci-gates).
A failing gate means a structural invariant is broken, not a flaky
test; fix the root cause rather than retrying.

When a pre-commit hook or PR check fails, create a **new** commit with
the fix. Do not `git commit --amend` after a hook failure; the prior
commit may not exist or may have already been pushed.

## 7. Merge

- Squash-merge is the default; the squash message follows Conventional
  Commits.
- Once merged, the deploy workflow rebuilds and publishes this docs
  site to <https://frankbardon.github.io/pulse/>.

For changes that introduce a new architectural decision, also update
the relevant section of `CLAUDE.md` and reference the PRD (if one
exists) in the PR description.

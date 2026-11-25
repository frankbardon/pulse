# Documentation Checks

**Audience:** contributors who change a page under `docs/src/`, a skill,
an example, CLI help or a public symbol, and want to know which
documentation check can fail and how to fix it.

The checks are ordinary Go tests. What each one guards in detail is in
the [Structural gates](testing.md#structural-gates) table; this page
covers running them and their shared rules.

## Running them

```bash
make doc-checks      # every fast doc check (about 5 s warm)
make docs            # regenerate the Analysis Guide and the coverage page, then build the book
make linkcheck-external   # http(s) links; needs the network, manual only
```

`make doc-checks` is what CI's `docs` job runs after `mdbook build docs`.
The same tests also run in `make test`. The heavy ones run in
`make test-heavy`. To add a fast doc check, add its name to `DOC_CHECKS`
in the `Makefile`, and its package to `DOC_CHECK_PKGS` if it is new.
Before running anything, `make doc-checks` lists the tests with
`go test -list` and fails when a `DOC_CHECKS` name matches no fast-tier
test, so a renamed or moved check cannot drop out silently.

| Check | Tier | Package | Allowlist |
|---|---|---|---|
| `TestDocsLinksResolve` | fast | `internal/integration` | `links.txt` |
| `TestDocsSnippetsRun` | fast | `internal/integration` | `snippets.txt` |
| `TestDocsSnippetsCompile` | heavy | `internal/integration` | `snippets-go.txt` |
| `TestCliHelpMatchesFlagsDoc` | fast | `cmd/pulse` | `cli-flags.txt` |
| `TestNoRemovedNamesInDocs` | fast | root | `removed-names.txt` |
| `TestDocsCoverageCurrent`, `TestDocsCoverageNoGaps` | fast | `internal/cli` | `coverage.txt` |
| `TestProfileServedTextNoHiddenFeature` | heavy | root | `served-text.txt` |
| `TestFacadeExamplesPresent` | fast | root | none: the pinned list in the test |
| `TestSkillExampleTagPointersResolve` | fast | `internal/descriptor` | none |
| `TestDocsGeneratedCurrent` | fast | root | none: run `make docs` |

The `docs` job also fails when `mdbook build` prints a `WARN` or `ERROR`
line, so keep the build warning-free. CI pins mdBook to 0.5.4, the
release the warning match, the heading-slug rule the link check mirrors
and the search-index size were checked against. Bump the pin in
`.github/workflows/ci.yml` and `docs.yml` on purpose, and re-check those
three when you do.

## Allowlists

Each allowlist is a file under `internal/doccheck/allowlists/`, one
entry per line: `path<TAB>entry<TAB>reason`. The lists hold the
violations that existed when the check landed. They only shrink:

- A new violation that the list does not name fails the check. Fix it.
  Never add it to the list.
- An entry that no longer violates fails as stale. When you fix one,
  delete its line in the same change.

The content work in the U32b unit (`docs/roadmap/units/U32b-docs-content.md`)
empties every list.

## Snippet tags

`TestDocsSnippetsRun` runs every fenced `json` block in `docs/src/**`
that is a request, on the example fixture cohorts. Its result is not
asserted. A block is a request when its keys say so (a `cohort` object,
or `requests`, `base` or `sweep`) or when it decodes as one. Which
request (single, compose, chain or facet) comes from the same key rule
the engine's read maps use (`internal/requestroot`). A block whose keys
say request but which does not decode, say a misspelled slot, fails
with the decode error. Responses, slot fragments and JSON elided with
`...` are skipped. A `jsonc` block is never read: its
comments are not strict JSON. The tag goes after the fence's language:

| Fence | Meaning |
|---|---|
| `json cohort=<name>[,<name>…]` | run on these `internal/examples/fixtures` cohorts |
| `json norun` | skip this block: no decode, no run (a deliberately wrong request, an error example) |
| `go compile` | the block must build (`TestDocsSnippetsCompile`, heavy tier) |

A runnable request with no `cohort=` tag fails the check. A `go` block
without `compile` is never built. A Go block without a package clause
gets `package snippet`, so it must carry its own imports.

For a page under `docs/src/guide/`, the tag goes in the generator's
source (the skill, or `internal/docgen`). Then run `make docs`.

## Generated pages

- `docs/src/guide/**` and `docs/src/contract/coverage.md` are generated
  by `make docs`. Never edit them by hand.
- The coverage page mentions every name it tracks. So any check that
  scans `docs/src` for mentions must skip that page. If it does not, the
  check passes no matter what the other pages say.
- Adding or removing a mention on any page can change the coverage page.
  Run `make docs` before you commit.
- The guide names every operator, field type and MCP tool, so for those
  rows the coverage page's mdBook cell counts only hand-written pages. A
  Request or Response slot counts only as a code span (`` `sort` ``) or
  as `Request.<Field>` / `Response.<Field>`: many slot keys are common
  words. The page's Rules section states the full rule.

## Search index

`docs/book.toml` keeps the generated Analysis Guide and the coverage
page out of the search index (`[output.html.search.chapter]`), and
indexes by h2 section (`heading-split-level = 2`). The index measured
9.2 MB this way, against 23.0 MB with every page indexed. mdBook warns
when the index passes 10 MB, and that warning fails the `docs` job. If
the index grows past the limit, exclude more generated pages or cut
text. Do not remove the warning check.

This page follows the same rules: the link checker and the snippets
check read it too.

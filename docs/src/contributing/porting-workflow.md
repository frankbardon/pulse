# Porting Workflow

**Audience:** contributors porting functionality into Pulse from an
external source — a predecessor project, a one-off prototype, an
upstream library being absorbed.

Porting is more error-prone than greenfield work. The mistakes
cluster around two pitfalls: leaking the source project's naming
through, and writing tests against the source's behaviour rather than
the Pulse-native shape the new code should expose. This workflow
exists to keep both at bay.

## 1. Map the boundary

Identify the source behaviour you are porting and the Pulse package
that will own the result. The destination is rarely a 1:1 mirror of
the source — Pulse's package layout (library-first facade, no
business logic in `cmd/pulse/`, all I/O through the injected
`afero.Fs`) often demands a refactor on the way in.

## 2. Write Pulse-native tests first

Write the destination package's tests in Pulse-native form before
porting any source code. Every identifier — type names, error codes,
constants — uses Pulse conventions (`AGG_*`, `PULSE_*`,
`SCREAMING_SNAKE` for component types, `DOMAIN_CATEGORY` for error
codes). No predecessor-project string prefixes leak in.

## 3. Confirm the tests fail informatively

Run `go test ./...` and confirm the new tests fail with informative
messages. If they pass without the implementation, the test is
wrong — fix it. A green test before the implementation lands is
almost always a sign that the test is asserting on the wrong shape.

## 4. Port the implementation

Port the source code into the destination package. Refactor for
Pulse-native idioms as you go — there is no benefit to landing a
direct copy and then refactoring later. Embedders and reviewers will
read the final shape, not the migration trace.

## 5. Re-run tests until green

Iterate on the implementation until the tests pass. If a test
refuses to come green, lean on the test rather than the
implementation — porting often surfaces edge cases the source
project handled implicitly.

## 6. Update target skill and CLAUDE.md

Update the target skill file(s) per [The Update
Demand](../internals/update-demand.md). Run the skill-coverage gates
locally (see [Running CI Gates Locally](local-ci.md)). Update
CLAUDE.md if the change touches a contract, env var, format version,
or registered surface.

## 7. Predecessor-reference scrub

Run the hygiene gate before opening the PR and confirm zero matches:

```bash
go test . -run TestNoOrbit
```

The gate is non-skippable; CI will catch any leak, but catching it
locally saves a round-trip.

## 8. Open the PR

Follow the [Pull Request Process](pr-process.md). The porting PR is
otherwise identical to any other PR — same Update Demand obligations,
same test-first preference, same Conventional Commits subject line.

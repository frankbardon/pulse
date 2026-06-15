# Regenerating Goldens

**Audience:** Pulse internals contributors after a legitimate change
to a descriptor / manifest generator.

Golden files live in `descriptor/testdata/`. Each file ends with a
`// golden-hash: <sha256>` line; `TestGoldensNotHandEdited` verifies
the hash against the file's body. Hand-editing a golden file flips
the hash and fails the gate.

## When regeneration is justified

You should regenerate after:

- Adding or removing a registered operator (the manifest enumerates
  every entry).
- Changing the capability shape for an existing operator.
- Renaming an error code (the manifest carries the canonical list).
- Updating the `format_version` (rare — additive changes do not bump
  the version).
- Any other change that legitimately changes the deterministic
  manifest output.

If you cannot articulate which deterministic output changed, the
golden update is probably wrong and the underlying generator is
emitting non-deterministic content (e.g. iterating a map without
sorting). Fix that first.

## The regenerate flow

```bash
go test ./descriptor/ -run 'Test.*Golden' -update
```

The `-update` flag asks the test runner to rewrite the goldens with
the current generator output. The generator stamps a fresh
`// golden-hash: <sha256>` line at the end of every regenerated
file.

Then verify the gate accepts the new hashes:

```bash
go test ./descriptor/ -run TestGoldensNotHandEdited
```

If the gate still fails after `-update`, the cause is usually one of:

- The hash trailer is missing from a golden you added by hand —
  the generator only stamps files it owns.
- An extra blank line or comment was added by editor configuration
  before the final hash line — the hash is computed over the body
  before the trailer.
- The generator emits map iteration without a sort — non-determinism
  itself.

## Never hand-edit a golden

The gate exists to catch the case where a contributor edits a golden
to make a test pass instead of fixing the underlying drift in the
generator. If a golden diff surprises you in code review, that is a
red flag — ask the contributor to show the generator change that
justifies the new hash.

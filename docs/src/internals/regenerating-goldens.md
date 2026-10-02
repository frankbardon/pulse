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

## Per-feature-profile goldens

Beside the full-registry `manifest.json` and `payload-schema.json`, each
fixture feature profile under `descriptor/testdata/profiles/` (`minimal`,
`survey-crosstab`, `empty`) pins its instance's self-description:
`manifest.<fixture>.json` (the `--json` envelope around
`Pulse.Manifest`) and `payload-schema.<fixture>.json` (`Pulse.PayloadSchema`
byte for byte), written by `TestProfileManifestGolden` and
`TestProfilePayloadSchemaGolden`. Both match `Test.*Golden`, so the
regenerate command above rewrites them too, and they sit at the top of
`testdata/` where `TestGoldensNotHandEdited` checks their hashes. The
profile inputs themselves live in the `profiles/` subdirectory, which the
gate skips because they are hand-written.

Regenerate them whenever a change moves what an instance offers or how it
describes it: a feature added to or removed from the feature table, a
change to a fixture profile, a capability block or schema root that
gating omits, or prose the hidden-name scrub redacts. The manifest
goldens also carry the skill list, so a skill-pack edit churns them like
`manifest.json`.

## Never hand-edit a golden

The gate exists to catch the case where a contributor edits a golden
to make a test pass instead of fixing the underlying drift in the
generator. If a golden diff surprises you in code review, that is a
red flag — ask the contributor to show the generator change that
justifies the new hash.

## The `0x01` format golden is a compatibility fixture, not a snapshot

`encoding/testdata/format_v1.pulse` is a synthetic `0x01` cohort that
every build must still read — and that the current writer must still
reproduce byte-for-byte for the same schema and rows. It exists because
old cohorts stay readable forever. **A failure there is a
backward-compatibility break, not a stale golden:** do not run
`go test ./internal/encoding/ -run TestFormatV1Golden -update` to make it pass.
The `-update` flag exists only to create the file; to cover more of the
`0x01` surface, add a second fixture rather than rewriting this one, so
the bytes an earlier binary wrote stay under test.

## Shard-archive fixtures are generator-pinned snapshots

`testdata/sharding/*.pulse` are produced by `internal/shardfixtures`
(`go run ./testdata/sharding/build_fixtures.go` writes them) and
`TestShardFixtures_MatchGenerator` fails when the committed bytes and the
generator disagree. Unlike the `0x01` golden these are snapshots of the
CURRENT writer: they once went stale across the nullable-flag
schema-block change (a declared clean break) and stopped opening, with
nothing noticing. Regenerate with the command above or
`go test ./internal/shardfixtures/ -update`, then `git add -f` them
(`*.pulse` is gitignored). The two JSON envelopes beside them are pinned
to live CLI output by `TestShardingSnapshots_MatchCLIOutput`
(`go test ./internal/cli/ -run TestShardingSnapshots -update`).

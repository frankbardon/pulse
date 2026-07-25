# Point Lookup & Index Management

**Audience:** Go embedders resolving a single row (or a small
duplicate set) by an exact key value instead of scanning or filtering
a whole cohort.

`Pulse.Lookup` is a keyed point-lookup store: **O(1)** — flat latency
regardless of cohort size — against a **prebuilt sidecar index**
(`Pulse.BuildIndex`). It only works against a **single-file** cohort
(shard archives are rejected with `PULSE_INDEX_UNSUPPORTED_SHARDED`;
the `archive.pulse#shard.pulse` anchor is a tested workaround), it is
**equality-only** (no ranges, no expressions), and it always resolves
the **full key** — no prefix or partial-key matching. For anything
else (ranges, expressions, unindexed ad hoc queries), use
[`Process`](overview.md) with `FILTER_INCLUDE` / `FILTER_EXPRESSION`
instead.

## Build then look up

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/frankbardon/pulse"
    "github.com/frankbardon/pulse/types"
)

func main() {
    ctx := context.Background()

    p, err := pulse.New(pulse.Options{DataDir: "/var/data/pulse"})
    if err != nil {
        log.Fatal(err)
    }

    // Build the sidecar index once — cheap to re-run (idempotent,
    // byte-identical sidecar for an unchanged cohort), but there's no
    // need to call it on every lookup.
    if _, err := p.BuildIndex(ctx, "sales.pulse", []string{"order_id"}); err != nil {
        log.Fatal(err)
    }

    result, err := p.Lookup(ctx, &pulse.LookupRequest{
        Cohort: &types.Cohort{Filename: "sales.pulse"},
        Field:  "order_id",
        Value:  "42",
    })
    if err != nil {
        log.Fatal(err)
    }
    for _, row := range result.Rows {
        fmt.Println(row)
    }
}
```

`BuildIndex` returns a `*pulse.BuildIndexResult{IndexPath, Index}` —
`IndexPath` is the derived sidecar path
(`encoding.SidecarIndexPath`), `Index` is the in-memory
`*encoding.Index` that was serialized there, handed back in case you
want to inspect it without a round-trip read.

`Lookup`'s single-key convenience path — `Field` + `Value`, both
strings — is the E1 shape and stays the ergonomic default. `Value` is
always text, the same string-literal convention `Filterer.Values`
uses for `FILTER_INCLUDE`/`FILTER_EXCLUDE`.

## Composite keys

A multi-column key is an **ordered tuple** — column order matters end
to end, from the `BuildIndex` call that wrote the sidecar through
every `Lookup` that probes it. Use `Keys` instead of `Field`/`Value`:

```go
if _, err := p.BuildIndex(ctx, "sales.pulse", []string{"region", "date"}); err != nil {
    log.Fatal(err)
}

result, err := p.Lookup(ctx, &pulse.LookupRequest{
    Cohort: &types.Cohort{Filename: "sales.pulse"},
    Keys: []pulse.LookupKey{
        {Field: "region", Value: "EU"},
        {Field: "date", Value: "2026-01-01"},
    },
    ReturnColumns: []string{"order_id", "revenue"},
    Multiplicity:  pulse.LookupMultiplicityAll,
})
if err != nil {
    log.Fatal(err)
}
```

`Keys` takes precedence over `Field`/`Value` when both are set.
`{region, date}` and `{date, region}` name the same two logical
values but resolve against two different sidecar indexes — build the
index with the same column order you'll query with.

`ReturnColumns` projects the result down to named fields; empty means
every schema field (`LookupResult.Rows[i]` is a full
`map[string]any`), matching `Sample`'s no-projection default.

## Multiplicity

`Multiplicity` controls what happens when the matched key resolves to
more than one row-id (a duplicate key value in the source cohort):

The mode type and its constants are re-exported on the `pulse` package,
so you never need to import `types` to set `LookupRequest.Multiplicity`:

| Constant | Wire value | Behavior |
|---|---|---|
| `pulse.LookupMultiplicityAssertUnique` | `assert_unique` | **Default** (zero value). Fails with `PULSE_LOOKUP_AMBIGUOUS` on more than one match; a single match succeeds normally. |
| `pulse.LookupMultiplicityFirst` | `first` | Takes the lowest row-id, never errors on a multi-row match. |
| `pulse.LookupMultiplicityAll` | `all` | Returns every matched row in `Rows`, ascending row-id order. |

```go
result, err := p.Lookup(ctx, &pulse.LookupRequest{
    Cohort: &types.Cohort{Filename: "sales.pulse"},
    Field:  "region",
    Value:  "EU",
})
if err != nil {
    if errors.HasCode(err, errors.PULSE_LOOKUP_AMBIGUOUS) {
        // region isn't unique — rebuild the index on a real key, or
        // opt into LookupMultiplicityFirst / LookupMultiplicityAll.
    }
    log.Fatal(err)
}
```

`errors.HasCode(err, errors.CODE)` (`github.com/frankbardon/pulse/errors`)
is the coded-error check used across the codebase — the same pattern
covers `PULSE_INDEX_MISSING`, `PULSE_INDEX_STALE`,
`PULSE_INDEX_UNSUPPORTED_SHARDED`, and `PULSE_LOOKUP_NOT_FOUND`.

## Index lifecycle

Three more facade methods manage sidecars without a lookup:

```go
// Freshness check — O(1) size+mtime fast-path before an authoritative
// full-content SHA-256 recompute.
verify, err := p.VerifyIndex(ctx, "sales.pulse", []string{"order_id"})
// verify.Fresh, verify.Reason ("stat_mismatch" | "fingerprint_match" |
// "fingerprint_mismatch"), verify.FastPath

// Enumerate every sidecar built against a cohort.
indexes, err := p.ListIndexes(ctx, "sales.pulse")
// []pulse.IndexInfo{IndexPath, Keys, DistinctKeys, IndexedRecords}

// Remove a sidecar. Non-interactive — no confirmation prompt.
err = p.DropIndex(ctx, "sales.pulse", []string{"order_id"})
```

`Lookup` itself only pays the **O(1) size+mtime stat** for staleness
(`PULSE_INDEX_STALE` on mismatch) — full-hash-per-lookup would defeat
the point of an O(1) store. `VerifyIndex` is the authoritative path:
a matching size+mtime pair alone is inconclusive (mtime resolution,
same-size rewrites) and always falls through to a full content-hash
recompute before reporting `Fresh: true`. Run `VerifyIndex`
explicitly wherever you need a stronger guarantee than the fast-path
`Lookup` gives you for free — e.g. before a batch of lookups against a
cohort you don't fully trust, or in a periodic health check.

## Keyable-type policy

Not every field type can serve as a lookup key — `set_*` is rejected
(a multi-select bitmask has no single unambiguous equality value; use
`FILTER_SET` via `Process` instead). See the full allow/reject table
in [`pulse api lookup`](../cli/api-lookup.md#keyable-type-policy) or
`skills/cohort-schema-design.md`.

## When to use this vs `Process` + `FILTER_INCLUDE`

Reach for `Lookup` when you have an exact key and want the fastest
possible single/few-row read; reach for `Process` with `FILTER_*`
for anything that isn't a full-key equality match (ranges,
expressions, `set_*` membership, or querying a column you haven't
built an index for). The performance shape is the load-bearing claim,
not any particular absolute number: indexed lookup is **flat**
regardless of cohort size, while an unindexed scan is **linear** in
record count — the gap widens, not narrows, as a cohort grows. Build
the index once with `BuildIndex`; every subsequent `Lookup` against
that key combination pays only the flat O(1) cost.

## See also

- [`pulse api lookup`](../cli/api-lookup.md) — the CLI leaf for
  `Lookup`, plus `pulse index build/list/verify/drop` for the
  lifecycle methods above.
- [Go API Overview](overview.md) — the rest of the public facade.

# Deterministic FilterToFile

**Audience:** Go embedders that materialize filtered subsets of a
canonical cohort and want to dedup the work across consumers.

`Pulse.FilterToFileWithRequest` reads a source `.pulse` cohort,
applies a predicate, and writes the surviving records to a new
`.pulse` file. Three guarantees make the operation dedup-safe:

1. **Deterministic output naming** — same source + same predicate
   resolves to the same output path.
2. **Atomic write** — the engine never leaves a partially-written
   `.pulse` file at the target path.
3. **Pre-existence dedup** — if the deterministic output already
   exists, the engine returns it without re-running the filter.

## Shape

```go
type FilterToFileRequest struct {
    SourcePath string             // Path to the source .pulse file
    Expression string             // FILTER_EXPRESSION-style predicate
    Filterers  []*types.Filterer  // Structured-predicate alternative
    OutputDir  string             // Where to write the result
    OutputName string             // Optional override; default: {srcHash}_{predHash}.pulse
}

type FilterToFileResult struct {
    OutputPath string
    OutputHash string
    RowCount   int64
    ElapsedMs  int64
    Reused     bool   // true when a pre-existing output was returned
}
```

Exactly one of `Expression` or `Filterers` must be set — both empty
or both set is a configuration error so the predicate hash is
unambiguous.

## Basic usage

```go
res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{
    SourcePath: "cohort-2025-Q3.pulse",
    Expression: "age >= 18 && state in [\"CA\", \"NY\"]",
    OutputDir:  "derived/",
})
if err != nil {
    return err
}
fmt.Printf("%s (%s) %d rows reused=%v in %dms\n",
    res.OutputPath, res.OutputHash, res.RowCount, res.Reused, res.ElapsedMs)
```

On a fresh OutputDir the first call writes the file and reports
`Reused: false`. A subsequent identical call hits the pre-existence
check, reports `Reused: true`, and skips the filter work.

## Predicate shapes

### Expression

A string evaluated under the `FILTER_EXPRESSION` engine — same expr-
lang semantics as `Request.Filterers[*].Expression`:

```go
Expression: "age >= 18 && (region == \"NA\" || region == \"EU\")",
```

### Filterers

A slice of structured `types.Filterer` entries translated into the
equivalent expression and AND-combined. Useful when the predicate is
built programmatically:

```go
Filterers: []*types.Filterer{
    {Type: types.FILTER_RANGE, Field: "age", Values: []string{"18", "65"}},
    {Type: types.FILTER_INCLUDE, Field: "state", Values: []string{"CA", "NY"}},
    {Type: types.FILTER_NULL, Field: "email", Values: []string{"is_not_null"}},
},
```

Supported types: `FILTER_EXPRESSION` (pass-through), `FILTER_INCLUDE`,
`FILTER_EXCLUDE`, `FILTER_RANGE`, `FILTER_NULL`.

Each filterer's `Field` is judged by the same field rule `Process` and
predict use, against the cohort schema (a shard archive's canonical
schema) and before the translated expression compiles: an unknown or
empty `Field` is `SERVICE_VALIDATION` `filter references unknown field:
<f>` with details `{field, filter}`, exactly as `Process` refuses it.

## Deterministic output naming

When `OutputName` is empty, the basename is computed as:

```
{sourceHash[:16]}_{predicateHash[:16]}.pulse
```

- `sourceHash` is the SHA-256 of the source file's bytes.
- `predicateHash` is `types.CanonicalHash` of the predicate payload
  (Expression or Filterers slice, normalised to the same canonical
  JSON form used by `Request.Hash`).

Independent consumers building the same request reach the same path
without coordination. Combined with a `Watch` on `OutputDir`, this is
the foundation for shared materialization caches: many callers ask
for the same derived cohort, only the first request pays the filter
cost, every subsequent caller hits the pre-existence dedup.

## Atomic write

The engine stages writes to `OutputDir/.<name>.partial` and renames
into place only after the filter completes. A failed run removes the
temp file and never publishes a partial result at the target path.
Combined with the `Watch` API's rename coalescing, downstream
observers see a single `ChangeCreated` or `ChangeRenamed` event for
the completed file — no spurious intermediate states.

## Deduped (parent-group) sources

A grouped (`0x02`) source produces a grouped output. The header,
schema, group descriptors and group dictionaries are copied
byte-for-byte and the surviving physical rows are copied as-is, so
every row's dictionary index stays valid. The output opens, inspects
and processes exactly like the same surviving rows deduped afresh
with the same groups.

The dictionary is **carried, not pruned**. After a selective filter
it still holds entries no surviving row references, so `pulse
inspect` reports a group ratio of surviving rows ÷ carried entries.
A low ratio there is expected and is not a defect. Pruning would cost
a second pass and renumber indices, so v1 defers it. To prune,
re-dedup the output with the same groups (`pulse dedup OUT --group
...`). It regroups from scratch.

A filter that keeps no rows writes a valid, empty `0x02` cohort (the
preamble alone). The predicate's field projection narrows only what
is DECODED to evaluate it. Output rows always carry every field,
group members included. A filter over one group's members is
evaluated once per dictionary entry, not once per row.

## Manifest annotation

`filter_to_file` appears in `Manifest.Operations` with annotations
`{streamable: false, deterministic: true, expensive: true}`. The
`expensive: true` hint is the signal to embedders that this is the
operation worth caching aggressively — which the function itself
already does via pre-existence dedup, so the typical consumer pattern
is to simply call `FilterToFileWithRequest` repeatedly and trust the
result.

## See also

- [Request Hashing](./request-hashing.md) — the hashing primitives
  underlying the deterministic naming.
- [Watch & WatchDir](./watch.md) — observe derived-output changes.

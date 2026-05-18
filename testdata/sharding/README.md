# testdata/sharding/

Canonical Pulse shard archives used as snapshot inputs by the non-skippable
CI gates listed in CLAUDE.md (`TestShardArchive*`).

## Contents

| File | Description |
|---|---|
| `two_shards.pulse` | Two-shard archive over a u32/f64/categorical_u8 schema, six records total, both shards sharing the seed dictionary `[US, CA]`. |
| `dict_growth.pulse` | Two-shard archive where shard 2 extends the categorical dictionary with `MX`; the canonical `_schema.pulse` reflects the extended dict `[US, CA, MX]`. |
| `three_shards.pulse` | Three-shard archive (two records each) for parallel-merge / order-preservation tests. |
| `two_shards.inspect.json` | Expected JSON envelope of `pulse cohort inspect two_shards.pulse --json`. |
| `two_shards.process.json` | Expected JSON envelope of `pulse api process --request request.json --json` against `two_shards.pulse` with the canonical sum/count/min/max aggregation request. |

## Regenerating the binary fixtures

The `.pulse` files are reproducible byte-for-byte because `build_fixtures.go`
writes uncompressed (Method 0) zip entries with a fixed modtime (2020-01-01 UTC).
Re-run the generator only when the canonical schema or shard contents change.

```
go run ./testdata/sharding/build_fixtures.go
```

## Regenerating the JSON snapshots

```
cd testdata/sharding
/path/to/pulse cohort inspect two_shards.pulse --json > two_shards.inspect.json
/path/to/pulse api process --request /tmp/request.json --json > two_shards.process.json
```

The canonical process request is:

```json
{
  "cohort": {"filename": "two_shards.pulse"},
  "aggregations": [
    {"label": "score_sum",   "type": "AGG_SUM",   "field": "score"},
    {"label": "score_count", "type": "AGG_COUNT", "field": "score"},
    {"label": "score_min",   "type": "AGG_MIN",   "field": "score"},
    {"label": "score_max",   "type": "AGG_MAX",   "field": "score"}
  ]
}
```

These fixtures are deliberately tiny (each < 5 KB) and committed to git.

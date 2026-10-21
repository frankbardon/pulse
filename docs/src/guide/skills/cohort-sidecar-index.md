```yaml
name: cohort-sidecar-index
description: The sidecar point-lookup index beside a cohort — the v3 file format, the O(1) seek path, three-outcome staleness, discovery through the indexes.json manifest, invalidation by a cohort rewrite, keyable types, shard-archive indexes and constraints.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [sidecar index, point lookup, idx, indexes.json, staleness, keyable types, shard archive index]
```

# Sidecar index

Part of the `.pulse` schema surface; entry skill [`cohort-schema-design`](cohort-schema-design.md) (field-type matrix — per-type key caveats).

## Sidecar index

`Pulse.Lookup` / `pulse index build` use a **separate file** — `cohort.pulse.<keyhash>.idx`; the `.pulse` layout stays untouched. Format **v3**, in order: 9-byte header (magic `PULSEIDX` + version `0x03`, its own magic/version distinct from `encoding.MagicBytes` / `FormatVersion`) → 32-byte SHA-256 source fingerprint → key-spec (ordered key columns + field types) → `SourceSize` (u64) + `SourceModTime` (i64 Unix ns) staleness snapshot → `u32 bucket_count` → fixed-width `bucket_count × u64` offset table (directly addressable, O(1) single-bucket seek) → self-delimited bucket data, FNV-1a hash buckets → `[]uint64` row-id multimap.

A lookup hashes the key, seeks its offset entry, seeks that bucket's data, then seeks each matched record via `RecordLocator` — never a full-cohort or full-index read.

**Staleness, three outcomes, not two.** A **size** mismatch is an instant `PULSE_INDEX_STALE` (the fingerprint covers the whole file, so a different length cannot hash to it). A full size+mtime **match** serves hash-free — that is the O(1) property. An **mtime that moved while the size held is NOT staleness**: mtime resolution is not preserved across filesystems (the snapshot is Unix ns, an S3 `LastModified` is whole seconds, and every copy/restore/cache hop truncates), so it escalates to the sidecar's own SHA-256 and refuses only if the content really differs. The digest is memoised per `(path, size, mtime)`, so a backend that never preserves ns pays one hash, not one per lookup. `pulse index verify` is authoritative: it always hashes and never reads that memo. The residual gap is unchanged — an in-place edit preserving BOTH size and mtime passes `Lookup` and is caught only by `verify`.

**Discovery: `cohort.pulse.indexes.json`.** A sidecar's filename is a hash OF its key tuple, so it cannot be opened without already knowing the answer, and object storage cannot list a directory. `pulse index build` therefore also upserts a keyless JSON manifest at that deterministic path carrying every index's ordered key tuple (plus types and counts), so `pulse index list` / `Pulse.ListIndexes` answers with one read and no directory listing. Absent ⇒ falls back to the directory listing; both available ⇒ union. Malformed ⇒ `PULSE_INDEX_MANIFEST_INVALID` (never a silent degrade to the listing, which would succeed locally and answer "no indexes" on a bucket); names a sidecar that is gone ⇒ `PULSE_INDEX_MANIFEST_STALE`, repaired by `pulse index build` or `pulse index drop` (drop prunes an orphaned entry). It is one of Pulse's own `*.json` sidecars, so a `PULSE_LABEL_TABLES_DIR` / `PULSE_RANGE_TABLES_DIR` walk skips it by suffix.

**A cohort rewrite invalidates it.** `pulse widen` changes the cohort's byte length, so the index (and the SPSS metadata sidecar) self-invalidate on the size fingerprint — neither can serve stale data. Nothing is rebuilt automatically: that would attach unbounded work to a bounded metadata operation. `pulse widen` instead REPORTS what it invalidated and the exact `pulse index build ... --key a,b` that rebuilds it, discovered through the manifest (the key tuple is unrecoverable from the hashed filename). `--json` carries it as `data.invalidated_sidecars`; both arms print nothing when there are no sidecars. Library: `Pulse.InvalidatedSidecars`.

**Keyable types** (`processing.IsIndexKeyableFieldType`): every type in the field-type matrix EXCEPT `set_*` — a multi-select mask has no single unambiguous equality value, so filter on it with the set filter instead. Per-type equality caveats are in that matrix's Notes column. A `u64` key is EXACT (stored word at build, `ParseUint` probe): ids above 2^53 one float apart stay distinct; an index built before that over such ids needs a rebuild.

**Shard archives.** `pulse index build archive.pulse` writes ONE sidecar per shard — the anchor's path `archive.pulse#shard.pulse.<keyhash>.idx`, byte-identical to an anchored build, so `archive.pulse#shard.pulse` lookups read it too — plus a manifest entry listing every covered shard by name + size + zip CRC32 (never a zip entry). A lookup resolves the literal once on the canonical dictionary, probes every shard, unions hits in archive order (`assert_unique` across the union). A shard the entry does not cover (added, or replaced under the same name) ⇒ `PULSE_INDEX_STALE` with `details.shards_uncovered` — never a silent miss. Verify hashes every shard; list / drop work on the archive. `shard add` builds the new shard's sidecar for every archive index BEFORE the archive rename (a failed build aborts the add, nothing touched); `shard remove` deletes the removed shard's sidecar and record, no other file; both warn `PULSE_SHARD_INDEXES_UPDATED`. An add that re-encodes the stored shards (set widen, group promotion) keeps nothing and lists them as `invalidated_sidecars`. `compact` copies shards raw: sidecars stay valid.

**Stored-record references.** A comparison attribute's `reference: {record: {cohort?, keys}}` ([`op-attr-vec-distance`](op-attr-vec-distance.md)) runs this same lookup, assert-unique, once before the run (the request's own cohort when `cohort` is omitted), so the index must exist on exactly those key columns in that order; every refusal below refuses the run — never a scan fallback — and predict, which reads no index, checks only the shape.

**Constraints:** equality-only, full-key required, composite-key order significant end to end. The `PULSE_INDEX_*` / `PULSE_LOOKUP_*` codes carry their fixups in `pulse errors lookup CODE`; the point-lookup MCP surface is [`tool-lookup`](tool-lookup.md).

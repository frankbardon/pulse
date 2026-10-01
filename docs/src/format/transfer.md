# Transfer Compression (`.pulse.zst`)

**Audience:** anyone moving cohorts between machines. Source of truth:
[`internal/io/transfer.go`](https://github.com/frankbardon/pulse/blob/main/internal/io/transfer.go).

Compression in Pulse is **transport-only**. A cohort is compressed to
move it, and decompressed back to a byte-identical `.pulse` before it is
used. A compressed file is never opened as a cohort.

```sh
# sender
pulse export transfer --input cohort.pulse            # writes cohort.pulse.zst
# receiver
pulse import transfer --input cohort.pulse.zst --output cohort.pulse
```

Library: `Pulse.ExportTransfer(ctx, *pio.TransferExportJob)` and
`Pulse.ImportTransfer(ctx, *pio.TransferImportJob)`, each returning a
`*pio.TransferReport`.

## Why not compress at rest

Every fast read path depends on the uncompressed bytes being on disk:
fixed-stride record offsets (`record_at`, point `Lookup`), stride-aligned
segments (parallel decode), the record count derived from file length
(`Inspect`, `CountRecords`), whole-file mmap during a scan, and shard
archive entries stored uncompressed (Method 0) so `Archive.OpenAt` can
return a byte range straight into the file. A compressed stream supports
none of these. So Pulse adds no third magic-byte variant, and archive
entries stay store-only.

## The artifact

A transfer artifact is **one standard zstd stream** (RFC 8878, with the
frame content checksum on). Decompressed, it is the exact byte image of
the source. Anything a `.pulse` path can name can be sent this way: a
single-file cohort at format `0x01` or `0x02` (grouped, elided) or a
whole shard archive. Pulse does not wrap the stream in a container of
its own, so the stock `zstd -d` gives the same bytes, and
`pulse import transfer` accepts a `.pulse` compressed with the stock
`zstd`.

| Flag | Leaf | Meaning |
|---|---|---|
| `--input` / `-i` | both | the source file |
| `--output` / `-o` | export | the artifact; default `<input>.zst`; overwritten |
| `--output` / `-o` | import | the cohort to write; must not exist |
| `--level` | export | zstd level 1..22, default 3. The encoder maps levels onto four speed classes: 1-2 fastest, 3-5 default, 6-9 better, 10-22 best |
| `--overwrite` | import | allow replacing an existing output cohort |
| `--json` | both | the result as a `descriptor.Envelope` |

The report (`data` under `--json`) carries `source`, `output`, `codec`,
`level` (on export only), `layout` (`single_file` / `shard_archive`),
`cohort_bytes`, `compressed_bytes`, `ratio` and `sha256`. **`sha256` is
always the digest of the UNCOMPRESSED cohort**, so the sender's and the
receiver's figures can be compared directly.

## Guarantees

- **Byte identity.** Decompressing gives a file whose SHA-256 equals the
  source's. This is tested for `0x01`, `0x02` grouped, `0x02` elided, and
  shard archives of each.
- **Atomic arrival.** `import transfer` writes to a temp file next to the
  output, fsyncs it, and renames it into place. This happens only after
  the whole stream has decoded, the checksum has matched and the first
  decoded bytes have proved to be a Pulse magic. A truncated or damaged
  artifact leaves nothing behind.
- **Bounded memory.** Both directions stream. Peak heap is the codec
  window plus fixed buffers, and does not grow with the size of the
  cohort.
- **Opt-in.** Nothing else changes. `export <format>`, the read path and
  the archive layout are untouched.

## Refusals

| Code | When |
|---|---|
| `PULSE_COHORT_COMPRESSED` | A transfer artifact was passed where a cohort goes: `Open`, `Process`, `CountRecords`, `Inspect`, `Predict` and every other read surface, every `pulse shard` leaf that takes an archive (`verify`, `compact`, `remove`, `add`, `list`, `extract` — all open it through `encoding.OpenArchive`), and `pulse_import`, which refuses it even when it has been renamed to `.pulse`. The fix is to decompress it first. |
| `PULSE_TRANSFER_INVALID` | `details.reason` is one of: `level` (outside 1..22); `not_a_cohort` (the export source, or the decompressed bytes, are not a Pulse cohort); `already_compressed`; `not_zstd` (the import source is not a zstd stream — an uncompressed `.pulse` needs no import); `corrupt_stream` (truncated or damaged in transit); `output_exists`. |

## Compression and parent groups do overlapping work

Parent groups (`0x02`) and zstd both remove the same redundancy: a parent
block repeated on every child row. Once a cohort is grouped, zstd has
much less left to find. Figures from the synthetic join-shaped bench,
`go test ./internal/io/ -run '^$' -bench BenchmarkTransfer`, at 240K rows, fanout
12, a 12-field parent block, and random-valued child columns:

| Shape | Cohort | Artifact | Ratio |
|---|---|---|---|
| flat, sorted by parent | 9.16 MB | 3.53 MB | 2.60x |
| flat, scattered | 9.39 MB | 5.59 MB | 1.68x |
| grouped, sorted | 5.02 MB | 3.38 MB | 1.48x |
| grouped, scattered | 5.04 MB | 3.48 MB | 1.45x |

- **Sorted:** a flat cohort sent as `.zst` comes within about 5% of a
  grouped one sent as `.zst`. For transfer alone, grouping adds little.
- **Scattered:** grouping still saves about 38% over the wire, because
  it removes repeats that sit too far apart for zstd to exploit cheaply.
- **Grouping also pays off at rest.** It speeds up scans and lowers
  memory, which compression never does.

The two choices are independent, and it is fine to use both.

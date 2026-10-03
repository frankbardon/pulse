---
name: spss-export-fidelity
description: How far the .sav round-trip claim is proven — what CI gates, what was checked only locally against independent readers, what nothing corroborates — and the write-side charset rules (source charset kept, no replacement characters, no silent truncation, encode-measure-segment).
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, sav, export, round trip, fidelity, charset, --charset]
requires: [io_format:spss]
---

# SPSS export — fidelity and charset

Part of the SPSS surface; entry skill `spss-cohorts`. The writer and its CLI surface: `spss-export`.

## What the fidelity claim rests on

Fidelity is this adapter's whole justification, so be precise about PROVEN vs. PSPP-specification-only.

- **Gated in CI.** `TestRoundTrip_*` runs import → export → import over a matrix covering all three source encodings, both MR flavours, all three missing-spec shapes, a non-UTF-8 charset, very long strings, both endiannesses. Asserts: re-imported `.pulse` **byte-identical** to the first (cohort identity — `.sav` byte-identity is unreachable for the reasons in `spss-export-values`); emitted `.sav` a **fixed point** under a second export; exactly the source's own variables declared. `TestRoundTrip_MatrixCoversFR62` fails if an axis loses its last fixture.
- **Local only, not CI.** `internal/io/spss/dict_ecosystem_test.go` (incl. `TestRoundTrippedFile_ReadsIdenticallyInReadStatAndForeign`) hands emitted and cycled files to haven 2.5.5 (ReadStat — the C reader behind haven, pyreadstat and most of what opens a `.sav`) and to independent `foreign` 0.8.91, requiring each reader's cycled read to match its own source read. Both pass — but they `t.Skip` without R / haven / foreign, which is CI's state. A recorded result, not a gate.
- **Corroborated by no independent reader.** MR set subtypes `7/5` / `7/19` — neither haven nor `foreign` exposes MR metadata, so nothing outside Pulse has read our set definitions back; the round trip proves only that Pulse reads what Pulse writes. Same for the two-`int32` form of record `7/11`: Pulse READS two- and three-`int32` shapes, always WRITES three, so that branch rests on synthetic fixtures.
- **Not implemented.** ZSAV emission — read-only, no partial or degraded path.

## Charset on the way out — the same two hard rules, mirrored

**Written in the charset its source declared, in the source's own spelling.** A `cp1252` source goes out as `cp1252` bytes under a `cp1252` record `7/20`, and `7/3` re-emits the source's own character code — including a stale one, because the disagreement is real information. No sidecar ⇒ UTF-8. UTF-8 under a `windows-1252` header would corrupt every non-ASCII label: the declaration follows the bytes, the bytes follow the source.

**Never a replacement character.** An unformable character ⇒ `PULSE_SPSS_CHARSET_UNENCODABLE` naming variable, value, code point — never `?`, `0x1A` or U+FFFD. Usual cause: a cohort edited since import, since text a Pulse operation produced is UTF-8. Every encode is decoded back and compared, so a character that encodes but returns as a *different* one (GB18030 does this across the Private Use Area) is refused too.

**Never a silent truncation.** SPSS widths are BYTE counts, so transcoding moves them (`Zürich` = 6 bytes `windows-1252`, 7 UTF-8). Widths recompute from the ENCODED bytes and a source-recorded width only ever **widens** — SPSS pads, the read path trims, so widening loses nothing while narrowing changes a declaration the source made. Where the format fixes the width: `PULSE_SPSS_WIDTH_OVERFLOW` (string past 32767 bytes, 8-byte short name, 255-byte value label, 64-byte file label, 80-byte document line).

**Order is the crux: encode, measure, segment.** A >255-byte string re-segments per `7/14` on a fixed 252-byte stride, so a multi-byte character *can* straddle a boundary — the reader joins pieces before decoding, exactly as the writer encodes the whole value before slicing. Segmenting the UTF-8 form first would put a partial character on the wire.

`--charset` / `io.SPSSWriterOptions{Charset}` overrides the target — the answer to a cohort whose text outgrew its source codepage. `io.SPSSReaderOptions{Charset}` is read-side *decoding* only, not consulted here. An unwritable name ⇒ `PULSE_SPSS_CHARSET_UNSUPPORTED`, never a silent fall back. Records `7/10`, `7/17`, `7/18` pass through **verbatim** (the reader never decodes them) — the one case where overriding the target is lossy, so a non-ASCII payload rides `PULSE_SPSS_CHARSET_MISMATCH` as a warning rather than being guessed at.

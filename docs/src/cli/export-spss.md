# pulse export spss

**Audience:** CLI users writing a `.pulse` cohort back out as an IBM SPSS
Statistics system file (`.sav`). Defined in
[`internal/cli/export.go`](https://github.com/frankbardon/pulse/blob/main/internal/cli/export.go);
the adapter is
[`io/spss/`](https://github.com/frankbardon/pulse/tree/main/io/spss).

This is the write half. [`pulse import spss`](import-spss.md) is the read
half, and the two are designed as one round trip: an import writes a
metadata sidecar carrying every dictionary element the `.pulse` byte
format has no slot for, and this leaf reads it back to reproduce the
source dictionary rather than inventing one.

> **`.sav` only — ZSAV is read-only.** All three data encodings *import*
> (uncompressed, bytecode, ZSAV zlib blocks). Only the first two are
> emitted. Asking for ZSAV output is
> `PULSE_SPSS_COMPRESSION_UNSUPPORTED`, not a silent downgrade.

> **`--include` and `--labels` are refused, not ignored.** The `.sav`
> writer encodes from the cohort's raw storage, not from the rendered row
> stream those two transform. See [What this leaf
> refuses](#what-this-leaf-refuses).

> **Illegal variable names stop the export by default.** `--sanitise-names`
> is the opt-in escape hatch. See [The name boundary](#the-name-boundary).

## Synopsis

```
pulse export spss --input PATH.pulse --output PATH.sav
                  [--ignore-sidecar] [--uncompressed]
                  [--charset NAME] [--sanitise-names] [--json]
pulse export predict --input PATH.pulse --format spss
                     [--ignore-sidecar] [--uncompressed]
                     [--charset NAME] [--sanitise-names] [--json]
pulse convert PATH.pulse OUT.sav
pulse convert PATH.csv  OUT.sav
```

`pulse convert` reaches the same writer and detects the format from the
`.sav` extension.

## A worked export

```bash
pulse export spss --input survey.pulse --output survey.sav
```

```
Exported 2 rows to survey.sav
```

A cohort with **no SPSS provenance** warns instead, because the dictionary
it gets is a reconstruction rather than a reproduction:

```bash
pulse export spss --input d.pulse --output out.sav
```

```
Exported 2 rows to out.sav
Warning [PULSE_SPSS_SIDECAR_ABSENT]: spss: no metadata sidecar found at
d.pulse.spss.json; the export will synthesise a default SPSS dictionary
from the .pulse schema alone, so value labels, measure levels, print
formats and missing-value specifications will not be restated. This is the
normal state for a cohort that was never SPSS-derived
```

This is the correct state for a cohort produced by `pulse synth` or
imported from CSV. It is a warning and not an error precisely because
nothing was lost — there was never any source metadata to lose.

With `--json`, the same run emits the standard envelope. Note that the
encode-side diagnostics appear **twice**: once on `data.TargetWarnings`,
which is the adapter's own report slot, and once on the envelope's
top-level `warnings` array, which is where a generic client should read
them:

```json
{
  "format_version": "1.1",
  "data": {
    "RowsExported": 2,
    "RowErrors": null,
    "LabelWarnings": null,
    "OverlayWarnings": null,
    "TargetWarnings": [
      {
        "code": "PULSE_SPSS_SIDECAR_ABSENT",
        "message": "spss: no metadata sidecar found at d.pulse.spss.json; ...",
        "details": {
          "cohort": "d.pulse",
          "sidecar": "d.pulse.spss.json"
        }
      }
    ]
  },
  "errors": [],
  "warnings": [
    {
      "code": "PULSE_SPSS_SIDECAR_ABSENT",
      "message": "spss: no metadata sidecar found at d.pulse.spss.json; ...",
      "details": {
        "cohort": "d.pulse",
        "sidecar": "d.pulse.spss.json"
      }
    }
  ]
}
```

`ExportReport` carries no `json:` struct tags, so its `data` keys are the
Go field names in Go casing — unlike the `errors` / `warnings` entries,
which are the standard `{code, message, details}` shape.

> **`pulse export predict` does not validate the `.sav` write.**
> `ExportJob.Predict` reads only the source cohort's header and schema and
> reports the field count and estimated rows; the target writer is never
> constructed, so `--format spss` is accepted and unused there. Nothing
> short of a real export exercises the name boundary, the sidecar verdict
> or the charset encode.

## The metadata sidecar: absent, stale, ignored

The writer's first act is `spss.LoadSidecar` against
`<cohort>.pulse.spss.json`. Four outcomes, and the split between them is
the single most load-bearing design decision on this page:

| State | Verdict | Code | What the export does |
|---|---|---|---|
| No sidecar file | **warning** | `PULSE_SPSS_SIDECAR_ABSENT` | synthesises a default dictionary from the `.pulse` schema |
| Cohort size or mtime moved since the sidecar was written | **error** | `PULSE_SPSS_SIDECAR_STALE` | nothing — the export stops |
| Not JSON / foreign `kind` / unknown `format_version` / bad digest | **error** | `PULSE_SPSS_SIDECAR_INVALID` | nothing — the export stops |
| `--ignore-sidecar` given, file present or not | **warning** | `PULSE_SPSS_SIDECAR_IGNORED` | synthesises a default dictionary |

**Absent is benign; stale is the dangerous one.** An absent sidecar means
the cohort never had source metadata, so nothing is being discarded. A
stale sidecar is the highest-fidelity-risk state available: the dictionary
is complete and plausible, so applying it to changed data yields a `.sav`
in which `IF q1 EQ 5` addresses a category that has since moved. It looks
authoritative, it is wrong, and no downstream reader can tell. So the
refusal returns **no resolution object at all** — there is no shape in
which a caller ends up holding the stale document and writing it by
accident.

The staleness check is a cheap O(1) size + mtime comparison, the same
model `PULSE_INDEX_STALE` uses, with the same residual gap: an in-place
edit that preserves both is not caught. `Document.VerifyDigest` recomputes
the authoritative full SHA-256 for a verify-style pass; hashing a
multi-gigabyte cohort on every export would cost more than the export.

**No option applies a stale dictionary.** The choice is always recorded
metadata or a synthesised default — never fresh-or-stale.

## Flags

Four flags, each exactly one field of `spss.WriterOptions`.
`--ignore-sidecar`, `--uncompressed` and `--sanitise-names` are also on
`pulse convert` / `convert predict`; `--charset` on `convert` names the
*source* charset, so the write charset is settable only on this leaf.

### `--ignore-sidecar`

Do not read the metadata sidecar; synthesise the dictionary from the
`.pulse` schema alone.

It suppresses the **read**, not merely the staleness verdict. A healthy
sidecar is ignored too, so the flag's effect never flips with an mtime,
and an unreadable one cannot block you. The resulting
`PULSE_SPSS_SIDECAR_IGNORED` warning deliberately cannot say which refusal
(if any) it silenced — nothing on that path read the file.

> **It cannot round-trip a cohort whose derived `set_*` column is still
> present.** That column's dictionary entries *are* its constituents'
> field names, so with the derived registry suppressed, synthesis mints
> indicator variables `Q1A` / `Q1B` beside the real `Q1A` / `Q1B` and
> stops on `PULSE_SPSS_NAME_COLLISION`. Export *without* the flag so the
> registry folds the column away.

### `--uncompressed`

Write flat 8-byte data elements instead of SPSS's bytecode compression.

The two are losslessly equivalent, so this trades file size for a readable
hex dump. Bytecode is the default because it is what SPSS's own `SAVE`
writes. It does **not** select ZSAV.

### `--charset NAME`

The charset the emitted file is written in **and declares**.

Default: whatever the source declared, in the source's own spelling — a
cohort imported from a `cp1252` file goes back out as `cp1252` bytes under
a `cp1252` record `7/20`. A cohort with no SPSS provenance defaults to
UTF-8. Set it when the cohort now holds text the source's codepage cannot
express, which is otherwise `PULSE_SPSS_CHARSET_UNENCODABLE`.

`spss.WithCharset` on the read side changes *decoding* only and is
deliberately not consulted here.

### `--sanitise-names`

Rewrite cohort field names a `.sav` cannot carry, instead of refusing.

See below — the refusal is the default on purpose.

## The name boundary

A `.pulse` field name is any UTF-8 string. An SPSS variable name is at
most 64 bytes, opens with a letter, and is unique **without regard to
case**. All three ways an illegal name fails are quiet — records `7/13`,
`7/7` and the case-fold rule each produce a well-formed file that says
something other than what was meant — so the export refuses instead:

```bash
pulse convert bad.csv bad.sav
```

```
error: PULSE_SPSS_NAME_INVALID: spss: "household income (gross)" is not a
name a .sav can carry as a variable: it contains ' ' at byte 9; an SPSS
name may carry only letters, digits and '.', '_', '$', '#', '@'
```

```bash
pulse convert bad.csv bad2.sav --sanitise-names
```

```
Converted 1 rows: bad.csv -> bad2.sav
Warning [PULSE_SPSS_SIDECAR_ABSENT]: spss: no metadata sidecar found at
converted-rows.pulse.spss.json; ...
Warning [PULSE_SPSS_NAME_SANITISED]: spss: --sanitise-names rewrote 2
name(s) that a .sav cannot carry: "household income (gross)" ->
"household_income__gross_", "2024 total" -> "V2024_total". The full list
is under "renames"; the emitted variables carry the rewritten names.
```

The rewrite is deterministic and collision-safe — against other renames
**and** against names that were already legal, which never move. The prose
caps the list at three; the `renames` detail never truncates. A leading
digit is prefixed rather than dropped (`2024 total` → `V2024_total`),
because dropping it would collide `2024 total` with `total`.

Note the sidecar path in that transcript: a `convert` from a text source
builds an intermediate cohort named `converted-rows.pulse`, so the absent
sidecar it reports is that intermediate's, not any file on disk.

`--sanitise-names` is **inert on the sidecar path**: those names came from
SPSS and are already legal by construction. It exists for the synthesised
path, where a CSV header's spaces and brackets are perfectly ordinary.

## Derived columns are export-transparent

An SPSS import can produce a cohort **wider** than the source dictionary:
a `<var>_missing` reason sibling per numeric variable declaring
user-missing values, and one `set_*` bitmask per multiple-dichotomy
response set. Both are additive by design.

On the way out they are **consumed, never emitted**. An emitted `.sav`
carries exactly the source's own variables and no `_missing` or `set_*`
artefacts. The fold reads the sidecar's `derived` registry and nothing
else — never the column name, because `income_missing` is a perfectly
legal SPSS variable name and a survey that declares one is not
hypothetical:

| Registry `kind` | Fold | Why |
|---|---|---|
| `numeric_missing` | **restore** — the encoder writes the recorded SPSS code back into every null, instead of the sysmis sentinel | the `.pulse` null bitmap is one bit, so *which* missing state each row was in exists nowhere else |
| `multiple_dichotomy` | **drop** — after checking the constituents really are being emitted | every bit is a second reading of a constituent still in the cohort under its own name |

Two refusals guard the fold, and both are about silence:

- `PULSE_SPSS_COLUMN_UNMAPPED` — a cohort column no emitted variable is
  written from that the registry does not account for. It is data about to
  leave the export invisibly. Checked on the synthesised path too, where
  the registry is empty and every field must bind.
- `PULSE_SPSS_DERIVED_UNFOLDABLE` — a registry entry this binary cannot
  honour: a `kind` outside its vocabulary (a document from a newer
  import), a `numeric_missing` entry with no `reasons` dictionary, an
  entry naming an unemitted source column, or an entry naming a column the
  export is *also* emitting as a variable. Both available guesses — emit
  it, or drop it — are invisible data faults, so it refuses.

**Value labels declared only on user-missing codes come back from the
registry, not from the categories.** An income column labelled at `97` /
`98` and nowhere else is not a coded variable, so the import maps it to
plain `f64` and moves those labels into the sibling's `Derived.Reasons` —
the only place they survive. The write path re-emits them as ordinary
records `3` / `4`.

## What this leaf refuses

`--include` and `--labels` are **refused with
`PULSE_SPSS_EXPORT_UNSUPPORTED`**, not silently dropped:

```bash
pulse export spss -i d.pulse -o out.sav --include age
```

```
error: PULSE_SPSS_EXPORT_UNSUPPORTED: spss: --include is not available on
a .sav export (age). The .sav writer encodes from the cohort's raw storage
rather than from the rendered row stream those options transform, so
honouring them would mean silently emitting something other than what was
asked for. Project or relabel first — `pulse api process` writing a
narrowed cohort — then export that.
```

The reason is structural. A `.sav` value is derived from a categorical's
dictionary **ID**, a `set_*`'s mask **bits** and the **null bitmap**, and
all three are gone by the time `ExportJob` has rendered a row: a
categorical resolves to its label text (and two SPSS codes may share one
label), and a null renders as `""`, which a string categorical can hold
legitimately. So the writer implements `io.CohortWriter` — the one
optional writer interface that *replaces* the row loop rather than
decorating it — and `WriteRow` is never called on the cohort path.

Overlays are **warn-and-skip**, the same posture as CSV.

Anything else with no honest `.sav` form is
`PULSE_SPSS_EXPORT_UNSUPPORTED` naming the variable, never a quiet
substitution: a dictionary ID the plan records no SPSS code for, an ID two
source values collapsed onto, a `set_*` column with an empty dictionary.

Most of these are answerable before a single byte is written — see
[Checking first](#checking-first-pulse-export-predict---format-spss).

## Checking first: `pulse export predict --format spss`

```bash
pulse export predict --input survey.pulse --format spss --json
```

```json
{
  "format_version": "1.1",
  "data": { "Schema": { "Fields": [ ... ] }, "EstimatedRows": 1200,
            "TargetWarnings": [ ... ] },
  "errors": [],
  "warnings": [
    { "code": "PULSE_SPSS_SIDECAR_ABSENT",
      "message": "spss: no metadata sidecar beside survey.pulse; the .sav dictionary was synthesised from the .pulse schema" }
  ]
}
```

`predict` writes **nothing**. It declares no `--output`, and the throwaway
writer it builds to ask the question is pointed at an in-memory
filesystem it never flushes.

**Pass the flags you will export with.** The four write knobs are mounted
on this leaf too, and the answer depends on them: `--sanitise-names` in
particular turns a `PULSE_SPSS_NAME_INVALID` refusal into a
`PULSE_SPSS_NAME_SANITISED` warning, so a predict that could not be told
about it would refuse an export that would have succeeded.

**A refusal carries the export's own code**, not a `PREDICT_ERROR`
placeholder, so it is usable with `pulse errors lookup`:

```bash
pulse export predict -i survey.pulse --format spss --json
```

```json
{ "format_version": "1.1", "data": null,
  "errors": [ { "code": "PULSE_SPSS_NAME_INVALID",
                "message": "spss: the cohort field \"household income\" cannot be an SPSS variable name (' ')",
                "details": { "spss_field": "household income" } } ],
  "warnings": [] }
```

Running the export against that cohort fails with the same code. That
parity is the point, and it is asserted end to end rather than assumed.

### What predict can and cannot see

It runs the writer's own **non-data pass** — the sidecar resolution, the
dictionary build, the name policy, the charset transcode of the
dictionary text, the derived fold and the encoder's column checks — and
throws the result away. It never reads a record. That covers most of the
refusal set, because a `.pulse` cohort's records are fixed-width numerics
and every string lives in the schema block's dictionaries.

| Reachable without records | Needs the data pass |
|---|---|
| `PULSE_SPSS_NAME_INVALID` / `_COLLISION` / `_SANITISED` | `PULSE_SPSS_WIDTH_OVERFLOW` on a value |
| `PULSE_SPSS_SIDECAR_ABSENT` / `_STALE` / `_INVALID` / `_IGNORED` | `PULSE_SPSS_CHARSET_UNENCODABLE` in cell text |
| `PULSE_SPSS_CHARSET_UNSUPPORTED` / `_UNENCODABLE` in dictionary text | `PULSE_SPSS_EXPORT_UNSUPPORTED` for a dictionary ID with no source code |
| `PULSE_SPSS_COLUMN_UNMAPPED`, `PULSE_SPSS_DERIVED_UNFOLDABLE` | |
| `PULSE_SPSS_EXPORT_UNSUPPORTED` for `--include` / `--labels` | |
| `PULSE_SPSS_COMPRESSION_UNSUPPORTED` / `_INVALID` | |

**So predict is a sound but incomplete filter.** A refusal is real; a pass
means no schema-level refusal was found, not that the export cannot fail.
It deliberately does not guess at the right-hand column — a false refusal
would block an export that would have worked, with no way to appeal it.

Without `--format` the leaf is target-blind exactly as it was before, and
says so:

```
Schema: 12 fields
Estimated rows: 1200
Target: not checked (pass --format to validate against the format you will export to)
```

Predict against a **non-`.sav`** target is also unchanged: no other writer
implements `io.CohortValidator` today, so `--format csv` reports the same
schema and row estimate it always did.

## Converting from a text source

```bash
pulse convert data.csv out.sav
```

A text source has no cohort behind it, so the writer buffers every row,
builds an intermediate cohort in memory through the ordinary import path,
and exports that. Consequences worth expecting: an **inferred** schema, no
metadata sidecar (so `PULSE_SPSS_SIDECAR_ABSENT`), synthesised SPSS names
(so `--sanitise-names` is live), and a memory profile proportional to the
input, because an intermediate cohort cannot be written until the last row
has been seen. A row path with no rows at all is a refusal, not an empty
file.

## Nulls, and what a synthesised dictionary will not invent

Nulls take the missing state the SPSS type actually has: numeric → the
**sysmis sentinel**; string → **blanks** (no string sentinel exists, and
blank reads back as null); every member of a null `set_*` → sysmis, which
is what keeps null apart from an empty mask on the way back.

Two rules govern the dictionary:

- **Original SPSS codes, never dictionary positions.** The sidecar's
  `code ↔ label ↔ Pulse dictionary ID` triple supplies them. `IF q1 EQ 5`
  addresses a *value*, so renumbering would silently re-point every
  reference in every downstream syntax file.
- **With no sidecar, nothing is invented.** A categorical becomes a
  **string** variable holding the dictionary text, rather than a numeric
  with codes taken from positions. A `set_*` expands into one indicator
  variable per entry (named for that entry, so the mask round-trips) plus
  a `7/7` dichotomy definition. `CategoryCode.Known` stays `false`, so the
  plan itself records which of the two it is.

Two things deliberately do **not** reproduce the source, because
re-emitting them would be wrong rather than merely less faithful:

- **Byte order is always little-endian** (record `7/3` agrees). One
  corollary is easy to miss: a numeric missing-value slot is a `flt64`, so
  the sidecar's verbatim slots are **byte-reversed** when the source was
  big-endian. Re-emitting them as read would declare eight bytes that
  decode here as an unrelated subnormal, and the variable would silently
  stop declaring anything missing at all. A **string** slot is characters
  and is never reversed.
- **`prod_name` identifies Pulse**, not the originating SPSS build.

Records `7/21` and `7/22` carry each variable's **final** name; ReadStat
(haven, pyreadstat) refuses a file that spells the short name there.
MOYR / QYR / WKYR keep both their raw seconds and their format code, so
such a column still renders as a month or a quarter.

## Charset on the way out

Two hard rules, mirroring the read side.

**Never a replacement character.** A character the target charset has no
form for is `PULSE_SPSS_CHARSET_UNENCODABLE` naming the variable, the
value and the code point — never `?`, `0x1A` or U+FFFD. The usual cause is
a cohort edited since import: text a Pulse operation produced is UTF-8 and
need not fit a legacy codepage. Every encode is also decoded back and
compared, so a character that encodes but returns as a *different* one
(GB18030 does this across the Private Use Area) is refused too.

**Never a silent truncation.** SPSS widths are BYTE counts, so transcoding
moves them — `Zürich` is 6 bytes in `windows-1252` and 7 in UTF-8.
Declared widths are recomputed **from the encoded bytes**: a synthesised
width is computed exactly, and a width the source recorded is only ever
**widened** to fit. (SPSS pads to the declared width and the read path
trims it back, so widening loses nothing; narrowing would change a
declaration the source made.) Where the format fixes the width there is
nothing to widen and it is `PULSE_SPSS_WIDTH_OVERFLOW` — a string past
32767 bytes, an 8-byte short name, a value label past the 255 its
one-byte length field counts, the 64-byte file label, an 80-byte document
line.

**Order is the crux: encode, then measure, then segment.** A string past
255 bytes is re-segmented across physical variables per record `7/14` on a
fixed 252-byte stride, so a multi-byte character *can* straddle a
boundary — the reader joins the pieces before decoding, exactly as the
writer encodes the whole value before slicing.

Records `7/10`, `7/17` and `7/18` pass through **verbatim**: the reader
never decodes them, so their bytes are the only record of what the source
said. That is correct whenever the target charset is the source's, and it
is the only case where overriding the target is lossy — a non-ASCII
payload then rides `PULSE_SPSS_CHARSET_MISMATCH` as a warning rather than
being guessed at or dropped.

## Diagnostics raised by the write side

Encode-side diagnostics ride the `io.TargetWarningEmitter` optional
interface onto `ExportReport.TargetWarnings` / `ConvertReport.TargetWarnings`,
and the CLI lifts them onto the `--json` envelope's `warnings` array or
onto `Warning [CODE]` lines on the text path. The CLI re-reads them
**after** `Close`, because a writer that encodes at `Close` — which the
row path does — has raised none of them when the job builds its report.

| Code | Severity | Meaning |
|---|---|---|
| `PULSE_SPSS_SIDECAR_ABSENT` | warning | No sidecar; dictionary synthesised from the `.pulse` schema |
| `PULSE_SPSS_SIDECAR_IGNORED` | warning | `--ignore-sidecar` given; the read was skipped entirely |
| `PULSE_SPSS_SIDECAR_STALE` | error | Cohort size/mtime moved since the sidecar was written |
| `PULSE_SPSS_SIDECAR_INVALID` | error | Not JSON, foreign `kind`, unknown `format_version`, bad digest, or a broken `multiple_response_sets[].fields` array |
| `PULSE_SPSS_NAME_INVALID` | error | A cohort field name a `.sav` cannot carry as a variable |
| `PULSE_SPSS_NAME_COLLISION` | error | Two cohort fields resolve to one SPSS variable name (case-insensitively) |
| `PULSE_SPSS_NAME_SANITISED` | warning | `--sanitise-names` rewrote names; details carry the full `field → name` list |
| `PULSE_SPSS_COLUMN_UNMAPPED` | error | A cohort column would leave the export silently, and the registry does not account for it |
| `PULSE_SPSS_DERIVED_UNFOLDABLE` | error | A registry entry this binary cannot fold back |
| `PULSE_SPSS_EXPORT_UNSUPPORTED` | error | `--include` / `--labels` on a `.sav` export, or a value with no honest `.sav` form |
| `PULSE_SPSS_COMPRESSION_UNSUPPORTED` | error | ZSAV emission — read-only, not implemented |
| `PULSE_SPSS_CHARSET_UNENCODABLE` | error | A character the target charset cannot represent |
| `PULSE_SPSS_CHARSET_UNSUPPORTED` | error | A `--charset` name Pulse cannot write |
| `PULSE_SPSS_CHARSET_MISMATCH` | warning | A verbatim `7/10` / `7/17` / `7/18` payload under an overridden target charset |
| `PULSE_SPSS_WIDTH_OVERFLOW` | error | An encoded value exceeds a width the format fixes |

`pulse errors lookup CODE` is authoritative for the per-code fixups.

## What the fidelity claim rests on

The effort's justification was fidelity, so it is worth being precise
about which parts are *proven* and which rest on the PSPP specification
alone.

**Gated in CI.** `TestRoundTrip_*` runs import → export → import over a
matrix covering all three source encodings, both multiple-response
flavours, all three missing-specification shapes, a non-UTF-8 charset,
very long strings and both endiannesses. It asserts that the re-imported
`.pulse` is **byte-identical** to the first — cohort identity, not `.sav`
byte-identity, which is unreachable for the correct reasons above — that
the emitted `.sav` is a **fixed point** under a second export, and that it
declares exactly the source's own variables.
`TestRoundTrip_MatrixCoversFR62` fails if any axis loses its last fixture,
so nothing can be quietly skipped.

**Verified locally, not in CI.** The R cross-checks
(`io/spss/dict_ecosystem_test.go`) hand emitted and round-tripped files to
haven 2.5.5 (ReadStat — the C reader behind haven, pyreadstat and most of
what else opens a `.sav`) and to the independent implementation in
`foreign` 0.8.91, and require each reader's own reading of the cycled file
to match its own reading of the source. Both read every value, value
label, variable label and measure level back exactly, in both write modes.
**These tests `t.Skip` when R, haven or foreign is absent, which is the
state on CI** — they are a recorded local result, not a standing gate.

**Corroborated by no independent reader.** Two parts of the write path are
implemented from the PSPP specification and nothing else has confirmed
them:

- **Multiple-response set subtypes `7/5` and `7/19`.** Neither haven nor
  `foreign` exposes MR metadata at all, so there is no third party that
  can read our set definitions back. The round-trip gate proves only that
  *Pulse* reads what Pulse writes.
- **The two-`int32` form of record `7/11`.** Pulse *reads* both the
  two-int32 and three-int32 shapes; it always *writes* the three-int32
  shape. The two-int32 branch is exercised only by synthetic fixtures.

**Not implemented.** ZSAV emission. Pulse reads `.zsav` and does not write
it; there is no partial or degraded path.

## Related

- [`pulse import spss`](import-spss.md) — the read half, in full detail
- [`pulse cohort inspect`](cohort-inspect.md) — see the cohort's schema and dictionaries before exporting
- [`pulse manifest`](manifest.md) — `import.formats[]` declares `spss` with `export: true`; `export.formats[]` declares its `warn_and_skip` overlay support
- [Adding an I/O Format](../internals/adding-io-format.md) — the `CohortWriter` / `CohortValidator` / `TargetWarningEmitter` contracts this adapter implements
- `skills/spss-cohorts.md` — the agent-facing SPSS surface, read and write
- `skills/session-bootstrap.md` — the four write flags in the CLI flag map

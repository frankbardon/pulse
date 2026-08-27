# pulse import spss

**Audience:** CLI users bringing an IBM SPSS Statistics system file
(`.sav`, `.zsav`) into a `.pulse` cohort. Defined in
[`internal/cli/import.go`](https://github.com/frankbardon/pulse/blob/main/internal/cli/import.go);
the adapter itself is
[`io/spss/`](https://github.com/frankbardon/pulse/tree/main/io/spss).

SPSS is the one import format Pulse does **not** guess at. Every other
source (CSV, NDJSON, Parquet, …) is sampled and voted on by
`io/infer.go`; a `.sav` file carries a dictionary that *declares* every
variable's type, its missing-value rules and its value labels, so the
adapter implements `io.SchemaAwareReader` and hands that dictionary
straight to the encoder. Inference never runs.

> **Writable too.** [`pulse export spss`](export-spss.md) re-emits the
> `.sav`, reproducing the source dictionary from the metadata sidecar
> this import writes. That page covers the write half; this one covers
> the read half.

> **Codepages decode.** Text is transcoded out of the charset the file
> declares (record `7/20` / `7/3`) into UTF-8, and a byte that charset
> cannot decode is a coded error rather than a `?`. See
> [Character encoding](#character-encoding).

> **`.zsav` imports directly.** All three data-section encodings read:
> uncompressed, bytecode (SPSS's own save default) and ZSAV zlib blocks.
> Nothing needs to be re-saved or converted first. See
> [Compression](#compression) below.

## Synopsis

```
pulse import spss --input PATH --output PATH.pulse [--schema FILE] [--json]
pulse import predict --input PATH [--format spss] [--json]
pulse import auto PATH [--handle NAME] [--ttl DUR] [--charset NAME] [--json]
pulse convert PATH.sav OUT.csv
```

`.sav` and `.zsav` both resolve to the same `spss` format identifier.
`import predict`, `import auto` and `convert` detect it from the
extension, so `--format` is optional on all three; only the explicit
`pulse import spss` leaf names the format positionally.

## A worked import

```bash
pulse import spss --input survey.sav --output survey.pulse
```

```
Imported 2 rows to survey.pulse
```

With `--json` the same run emits the standard envelope, carrying the
schema the SPSS dictionary declared:

```json
{
  "format_version": "1.1",
  "data": {
    "RowsImported": 2,
    "Schema": {
      "Fields": [
        {"Name": "ID",   "Type": 5, "Nullable": false, "CsvColumnIdx": 0, "Description": ""},
        {"Name": "SEX",  "Type": 9, "Nullable": true,  "CsvColumnIdx": 1, "Description": "Sex"},
        {"Name": "NAME", "Type": 9, "Nullable": false, "CsvColumnIdx": 2, "Description": ""}
      ]
    },
    "RowErrors": null,
    "PromotedFields": null,
    "SourceWarnings": null
  },
  "errors": [],
  "warnings": []
}
```

`Description` is lifted from the SPSS variable label. `PromotedFields`
is **always** `null` for an SPSS import — see
[Flags](#flags-and-what-spss-ignores).

## Type mapping

| SPSS | Pulse | Notes |
|---|---|---|
| numeric (`F`, `E`, `COMMA`, `DOT`, `PCT`, …) | `f64` | No integer narrowing by range probe — a probe would type two otherwise identical files differently |
| numeric with value labels | `categorical_u8` / `u16` / `u32` | Width from the distinct code count; past `u32` → `PULSE_SPSS_CATEGORICAL_OVERFLOW` |
| string (`A*`) | `categorical_*` | Near-unique columns warn `PULSE_SPSS_CARDINALITY_HIGH` and still import |
| very long string (wider than 255 bytes) | one `categorical_*` column | Reassembled from the record `7/14` segments — see [Very long strings](#very-long-strings) |
| `DATE` / `ADATE` / `EDATE` / `SDATE` / `JDATE` | `date`, or `datetime` with `PULSE_SPSS_DATE_WIDENED` | Widens when a value carries a time of day or predates 1970 |
| `DATETIME` / `TIME` / `DTIME` | `datetime` (epoch seconds) | A fractional-second / non-finite / out-of-`int64` value demotes the column to `f64` raw SPSS seconds with `PULSE_SPSS_TEMPORAL_PRECISION` |
| system-missing (sysmis) | null (bitmap bit) | The one missing state the format has a sentinel for |
| numeric user-missing values | null, plus a generated `<var>_missing` sibling column | See [Missing values](#missing-values) — the analytic column stays arithmetically clean and the reason is kept beside it |
| categorical user-missing values (string, or a value-labelled numeric) | ordinary dictionary entries, flagged in the sidecar | See [Categorical user-missing codes](#categorical-user-missing-codes) — the value *is* the label, so nothing is lost and a sibling would be redundant |
| multiple-**dichotomy** response set (records `7/5`, `7/7`, `7/19`) | every constituent variable **as its own column**, plus an extra `set_u8`/`u16`/`u32`/`u64` convenience column | See [Multiple-response sets](#multiple-response-sets) — the derived column is *additive*, because a bit cannot tell "not selected" from "not asked" |
| multiple-**category** response set | N separate `categorical_*` columns, definition on the sidecar | Positional and duplicate-tolerant, so it is genuinely not a set |

## Missing values

SPSS separates more missing states than a `.pulse` null bitmap can hold.
A variable can declare, on top of the system-missing sentinel, up to
three discrete **user-missing** codes — or a `lo..hi` range, or a range
plus one discrete code — and survey research reports `refused` versus
`don't know` versus `not applicable` separately and weights on the
difference. The null bitmap is **one bit**: it records *that* a value is
absent and can never record *why*.

Both simple answers lose something:

- Keep the codes as ordinary data and `AGG_SUM` over an income column
  silently adds `99999` for every refusal — a wrong answer that looks
  like a right one.
- Collapse every missing state to null and the item-non-response
  distinction is gone.

So Pulse does neither. A **numeric** variable that declares user-missing
values contributes **two** columns to the cohort:

| Column | Holds |
|---|---|
| `<var>` | the real values, and **null** at every missing position |
| `<var>_missing` | a `categorical_*` whose value is *why* — `sysmis`, the value label the file declared for the code, or the code itself |

```bash
pulse import spss --input survey.sav --output survey.pulse
pulse cohort inspect survey.pulse --json
```

```json
{
  "name": "income_missing",
  "type": "categorical_u8",
  "nullable": true,
  "dictionary": { "values": ["sysmis", "Refused", "Don't know", "99"] }
}
```

`AGG_MEAN` over `income` is now the mean of the incomes. `GROUP_CATEGORY`
over `income_missing` is the item-non-response breakdown. Neither needed
a filter.

**Rules worth knowing.**

- **Dictionary ID 0 is always `sysmis`.** After it come the *declared*
  discrete codes in the file's own order, then any further missing value
  the data carried, first-seen.
- **A range is never enumerated.** Only its *observed* members get
  entries, which is why a wide range can push the sibling from
  `categorical_u8` to `u16`.
- **A present value has no reason.** The sibling is null there. The empty
  reason is the null bitmap bit and is deliberately *not* a dictionary
  entry — the import path reads an empty cell as null before it consults
  any dictionary, so such an entry could never appear on the wire.
- **Reasons stay distinguishable.** If two missing codes carry the same
  value label, the second falls back to its numeric code and
  `PULSE_SPSS_VALUE_COLLISION` warns. Sharing one entry would destroy the
  exact distinction the column exists for.
- **A value-label set naming only missing codes does not make the
  variable categorical.** `INCOME` labelled solely on 97/98/99 is a
  continuous measurement whose labels annotate its missing states.
  `Q1: 1=Yes, 2=No, 9=Refused` still maps categorical, because two of its
  labels name ordinary answers.
- **Name collisions are a hard error.** A file that already declares
  `income_missing` alongside an `income` that needs a sibling is
  `PULSE_SPSS_DERIVED_NAME_COLLISION`, naming both — never a silent
  rename.
- **Categorical columns get no sibling.** A string variable, and a
  numeric one whose labels genuinely code it, keep their missing codes as
  ordinary dictionary entries: the value *is* the label, so nothing is
  lost. See [Categorical user-missing codes](#categorical-user-missing-codes)
  below — the asymmetry is deliberate.

**The opt-out.** `--spss-missing=null` suppresses the sibling columns.
The nulls are identical; what disappears is the reason.

```bash
pulse import spss -i survey.sav -o survey.pulse --spss-missing=null
```

An unrecognised value is `PULSE_SPSS_MISSING_MODE_INVALID` rather than a
fall back to the default, because the two modes produce different
schemas.

**Cross-checked against R.** `haven::read_sav(user_na = TRUE)` and
`foreign::read.spss`'s `missings` attribute report the same codes,
labels and range bounds Pulse's siblings carry, for all three
specification shapes. haven's *default* (`user_na = FALSE`) turns every
user-missing value into `NA` — value for value, that is Pulse's
`--spss-missing=null`.

The full missing-value specification — all three shapes, with the raw
eight-byte slots verbatim — rides the metadata sidecar
(`survey.pulse.spss.json`) under both modes, alongside a `derived`
registry naming each generated column, its cohort position, its source
variable and its reason dictionary: reason ID ↔ text ↔ original SPSS code
↔ label. That registry is what lets an export drop the derived column and
write the original codes back, rather than re-deriving the mapping and
hoping it lands on the same answer. It is shared with the other derived
kind — the multiple-dichotomy `set_*` columns of
[Multiple-response sets](#multiple-response-sets), which need no reason
dictionary because nothing they show is absent from the cohort.

## Categorical user-missing codes

A **categorical** column takes the opposite treatment, and the asymmetry
with the numeric case above is deliberate rather than an oversight.

A numeric variable needs a `<var>_missing` sibling because there is
nowhere else for the reason to live: `f64` has no dictionary, and the
null bitmap is one bit that can say a value is absent but never why. A
categorical variable already stores the SPSS code as a dictionary entry,
so `Q1: 1=Yes, 2=No, 9=Refused` imports as a `categorical_u8` whose
dictionary holds `"1"`, `"2"`, `"9"` — the refusal is preserved
losslessly by the ordinary mapping and the row that was refused still
says `9`. A sibling here would restate a value that is already present,
and it would do it on **every** variable of an all-categorical survey: a
200-question questionnaire whose items each carry a `Refused` code would
double to 400 columns to carry no new information.

So the codes stay, and Pulse records **which** entries are missing-coded
instead. String variables — including record `7/22` long-string missing
values — take exactly the same path, because a string maps to
`categorical_*` where the value *is* the dictionary entry.

**Two surfaces, answering different questions.**

The metadata sidecar is the persistent, exact, per-entry record. Each
flagged dictionary entry carries `"missing": true` alongside its code,
label and Pulse ID:

```json
{
  "name": "Q1",
  "pulse_type": "categorical_u8",
  "categories": [
    { "id": 0, "value": "1", "code": 1, "label": "Yes",     "labelled": true, "observed": true },
    { "id": 1, "value": "2", "code": 2, "label": "No",      "labelled": true, "observed": true },
    { "id": 2, "value": "9", "code": 9, "label": "Refused", "labelled": true, "observed": true, "missing": true }
  ]
}
```

The flag is additive and `omitempty`, so a file declaring no categorical
user-missing codes writes a document byte-identical to the shape before
it existed; the sidecar's own `format_version` does not move.

The import raises `PULSE_SPSS_CATEGORICAL_USER_MISSING` as the
**import-time** signal — one informational diagnostic for the whole file,
never one per variable, because an all-categorical survey would otherwise
emit hundreds of lines and bury the thing they are meant to carry. It
rides `SourceWarnings`, so it appears on the `--json` envelope's
`warnings` array and as a `Warning [PULSE_SPSS_CATEGORICAL_USER_MISSING]`
line on the text path. Its prose names the first few variables and their
codes; `details.missing_categories` carries every one of them as a field
name to flagged-entry map, uncapped.

Nothing is wrong when it fires. It exists because the loss it prevents is
downstream: a percentage base computed over a coded question silently
includes its refusal category unless somebody excludes it.

**The exclusion is over the CODE, not the label.** The cohort dictionary
holds SPSS codes (see [Value labels](#value-labels-the-cohort-stores-codes)),
so `"Refused"` appears nowhere in it:

```json
{ "type": "FILTER_EXCLUDE", "field": "Q1", "values": ["9"] }
```

`values: ["9"]` — the dictionary entry, which is exactly what
`details.missing_categories` hands you. A value not in the dictionary is
a `PROCESSING_CONFIG` error, so a label typed there fails loudly rather
than filtering nothing.

`--spss-missing` does not reach this arm. The flag governs sibling
columns, and a categorical column has none, so its codes, its dictionary
and its flags are identical under `auto` and `null` — two imports of one
file must never disagree about what a field *is*.

**Cross-checked against R.** The shape matches what an independent reader
produces. `haven::read_sav(user_na = TRUE)` returns a
`haven_labelled_spss` vector that **keeps the code in the data** and
reports the missingness **separately**, on the `na_values` / `na_range`
attributes, alongside the value labels — value kept, missingness stated
beside it. `foreign::read.spss(use.value.labels = TRUE)` does the same by
a different route: the missing code comes back as an ordinary factor
level, with the codes on the `missings` attribute. That is exactly the
division Pulse reproduces — the code is an ordinary dictionary entry, and
`categories[].missing` is the separate statement about it. The contrast
with the numeric arm holds on their side too: neither reader manufactures
a second column, because neither is constrained to a one-bit null flag.

The one deliberate divergence is the *default*: `haven`'s default
(`user_na = FALSE`) turns every user-missing value into `NA`, dropping
the distinction. Pulse has no equivalent default for categoricals —
`--spss-missing=null` governs sibling columns only, and a categorical
column has none — because for a categorical, nulling the code destroys
the value itself.

## Multiple-response sets

A survey's "select all that apply" question is stored in SPSS as a
**multiple-response set**: a named definition listing N member variables.
There are two flavours and they are not equivalent.

- **Multiple dichotomy (MD).** N binary indicator variables. A member
  holding the declared **counted value** means that option was selected.
- **Multiple category (MC).** N variables each holding a code from a
  shared value-label set. Positional, duplicate-tolerant, order-bearing —
  genuinely N categorical columns and not a set. It imports as exactly
  that; only its definition rides the sidecar.

This is the one mapping in the whole adapter where SPSS *declares* what
every other ingest path has to guess. A CSV importer looking at
`"tv|radio"` runs `io/infer.go`'s delimited-token heuristic and votes; a
`.sav` states the set outright. And Pulse has a type built for the shape:
`set_u8`/`u16`/`u32`/`u64`, a fixed-width bitmask over an inline
dictionary, with `FILTER_SET_*`, `GROUP_SET_PER_ELEMENT` and
`AGG_SET_FREQUENCY` over it.

### The derived column is additive — that is the whole design

The obvious mapping is to *collapse*: replace the N indicators with one
bitmask. Pulse does **not** do that, because it is lossy, and not in a
corner case:

| `Q1B` (Radio) | Means | Bit 1 |
|---|---|---|
| `0` | shown the option, did not pick it | clear |
| `.` (sysmis) | never asked — skipped or filtered past | clear |

Both are the same bit. Item non-response is not a rounding error in
survey work — it drives weighting and it is reported — and once the
constituents were gone nothing downstream could recover it.

So an MD set produces **N + 1** columns. Every constituent variable is
imported as its own ordinary column, with its own null bitmap bit and its
own `<var>_missing` sibling where it declares one; the derived `set_*`
column sits beside them, immediately after the **last** of its
constituents.

```bash
pulse import spss -i survey.sav -o survey.pulse
pulse cohort inspect survey.pulse --json
```

```json
{
  "fields": [
    { "name": "RESPID", "type": "f64" },
    { "name": "Q1A",    "type": "f64" },
    { "name": "Q1B",    "type": "f64" },
    { "name": "Q1C",    "type": "f64" },
    { "name": "media",  "type": "set_u8",
      "dictionary": { "values": ["Q1A", "Q1B", "Q1C"] } }
  ]
}
```

The constituents carry the **fidelity**; the derived column carries the
**ergonomics**. The cost is schema width, which is the only currency the
trade could have been paid in.

```json
{ "type": "FILTER_SET_CONTAINS_ANY", "field": "media", "values": ["Q1C"] }
{ "type": "GROUP_SET_PER_ELEMENT",   "field": "media" }
```

One request slot for the per-option base, instead of one aggregation per
option. And when the answer raises a question the mask cannot settle —
"did the people without Radio decline it, or were they never asked?" —
`Q1B` is still right there.

### Rules worth knowing

- **The dictionary holds constituent field NAMES**, not option labels.
  Bit `i` ↔ entry `i` ↔ `Sources[i]` on the sidecar. Field names are
  unique in a cohort, so the dictionary is injective for free; a variable
  label may be empty, duplicated or contain a `|`. It also means a bit
  names the column holding its own fidelity, which is the round trip the
  additive design exists to make one step. The option labels are each
  constituent's own `variables[].label` in the sidecar.
- **The mask uses the DECLARED counted value.** Nothing assumes `1`. A
  set declaring `2`, or a string set declaring `"YES"`, produces the mask
  the file describes.
- **A user-missing code is not a selection.** A refusal on a constituent
  sets no bit and is not evidence the row was answered.
- **The `$` is dropped from the field name.** `$media` becomes `media`,
  because a leading sigil is not a legal expr-lang identifier and a field
  named `$media` would be unreachable from `ATTR_FORMULA` /
  `FILTER_EXPRESSION`. The full name is kept on the sidecar as
  `derived[].set_name` and in `multiple_response_sets[].name`.
- **Three row states, and the middle one has its own spelling.** A row
  selecting nothing but having answered the battery is an **empty
  mask** — a real "none of these" answer, distinct from null. A row whose
  every constituent is missing is **null**: nothing is known.
- **Over 64 constituents, no derived column.** A `set_u64` has 64 bits
  and there is nothing wider. The import emits the constituents and warns
  `PULSE_SPSS_MR_SET_NOT_DERIVED` naming the set.

### When a set does not derive

Every refusal is a **warning** and the import succeeds, because the
derived column is additive: a set that does not derive costs ergonomics
and never data. Failing an otherwise-readable file to protect a
convenience column would be the wrong trade — which is why the same
`PULSE_SPSS_DERIVED_NAME_COLLISION` code that is a *hard error* for a
`<var>_missing` sibling is only a warning here.

| Reason | Code |
|---|---|
| more than 64 constituents | `PULSE_SPSS_MR_SET_NOT_DERIVED` |
| a member no record type 2 declares, or one named twice | `PULSE_SPSS_MR_SET_NOT_DERIVED` |
| a counted value that will not compare against a numeric member | `PULSE_SPSS_MR_SET_NOT_DERIVED` |
| a constituent whose name contains `\|` or *is* a null token (`NA`, `N/A`, `NULL`) | `PULSE_SPSS_MR_SET_NOT_DERIVED` |
| the derived name is one a real variable already holds | `PULSE_SPSS_DERIVED_NAME_COLLISION` |

### Multiple-category sets stay N categorical columns

An MC set gets **no** derived column, and that is a fidelity decision
rather than an omission. Its N members are *slots*, and two facts about
them survive only as separate columns:

| | MC set | `set_*` bitmask |
|---|---|---|
| slot order | first choice ≠ third choice | unordered |
| a repeated code | two slots may both hold `2` | idempotent — one bit |

Collapsing an MC set would lose both. So each member imports as its own
ordinary `categorical_*` column over the shared value-label set, exactly
as it would if the set definition were not there, and only the
*definition* rides the sidecar.

```json
{ "R1": "2", "R2": "2", "R3": "1" }
```

Both `2`s are still there, in the slots they arrived in.

### On the sidecar

The set definitions ride `payload.multiple_response_sets` verbatim —
name, kind (`dichotomy` / `category`), label, subtype, member short
names, and (dichotomy only) `counted_value`. `fields` sits beside
`variables` and resolves each member, index for index, to the Pulse
field name that member became — `""` for a member no record type 2
declares. Duplicate members are kept, because for an MC set a repeated
member is meaningful.

Every set is recorded, including the MC sets that derive nothing by
design and the MD sets that [refused to derive](#when-a-set-does-not-derive).
The block is the write-back record for the *definitions*; the derived
registry below answers the different question of which cohort columns
are synthetic, and a set that produced no column must still be written
back.

Each derived column gets a `payload.derived` entry of kind
`multiple_dichotomy` carrying its cohort position, its `set_name` and its
`sources` **in bit order**. It needs no reason dictionary, unlike a
`<var>_missing` sibling: nothing it shows is absent from the cohort, so
an export drops the column outright and re-emits the constituents.

**Cross-checked against R — and there is nothing to check against.**
Neither `haven` (ReadStat) nor `foreign::read.spss` exposes
multiple-response set metadata at all, from subtype `7` or `19`; both
read a fixture carrying both records cleanly and report only the member
variables as ordinary columns. So Pulse's reading of record `7/19`'s
extended `E` grammar rests on the PSPP specification alone. The residual
risk is bounded by the additive design: a misread definition can only
mis-derive or fail to derive a *convenience* column, and can never touch
a constituent.

## Derived columns

Two of the mappings above add columns the `.sav` never declared. They
are the only two, they are always **additive**, and they are the reason
an imported cohort can be wider than the file it came from.

| Kind | Where it comes from | What it holds |
|---|---|---|
| `numeric_missing` | a numeric variable declaring user-missing values | `<var>_missing`, the *reason* each null had — see [Missing values](#missing-values) |
| `multiple_dichotomy` | a multiple-dichotomy response set | a `set_*` bitmask over the constituents — see [Multiple-response sets](#multiple-response-sets) |

Both are **interleaved**, not appended. A reason sibling sits
immediately after its source variable; a set column sits immediately
after the **last** of its constituents, because a summary must not
precede its parts. So a cohort position is a cohort position — never an
ordinal into the source dictionary — which is why the sidecar records
`variables[].position` explicitly.

### They are export-transparent

Neither kind is ever written back as an SPSS variable. An export folds
them away and reconstructs exactly the variables the source declared:

- a `multiple_dichotomy` column is **dropped**. Every bit it shows is a
  second reading of a constituent that is still in the cohort under its
  own name, so there is nothing to reconstruct.
- a `numeric_missing` column is **consumed**. Its per-row value decides
  what its source variable writes wherever that variable is null — the
  original SPSS code, or the system-missing sentinel.

That fold is driven by the sidecar's `payload.derived` registry, never by
matching on names. `_missing` is a legal SPSS name suffix and a survey
that genuinely declares `income_missing` is not hypothetical; a set
column's name is whatever the set was called minus its `$`, which matches
no pattern at all. Guessing wrong is silent in both directions — a
phantom variable that was never in the source, or a real column dropped.

```json
{
  "derived": [
    {
      "name": "income_missing",
      "kind": "numeric_missing",
      "sources": ["income"],
      "position": 1,
      "reasons": [
        { "id": 0, "reason": "sysmis", "sysmis": true, "observed": true },
        { "id": 1, "reason": "Refused", "code": 97, "label": "Refused",
          "declared": true, "observed": true }
      ]
    },
    {
      "name": "media",
      "kind": "multiple_dichotomy",
      "set_name": "$media",
      "sources": ["Q1A", "Q1B", "Q1C"],
      "position": 4
    }
  ]
}
```

`kind` is a **closed vocabulary** — those two values today. A consumer
meeting a third must refuse rather than skip it: a column whose fold-back
is unknown has no safe default.

**A cohort with no derived columns writes `"derived": []`**, not a
missing key. "Nothing was derived" and "this document cannot tell you"
are different answers, and the second is the one an export has to stop
on.

### Opting out

| Kind | Opt out | Cost |
|---|---|---|
| `numeric_missing` | `--spss-missing=null` / `spss.WithMissingMode(spss.MissingNull)` | Identical nulls in the analytic column; the *reason* is no longer in the cohort. The full specification still rides the sidecar, so a re-import recovers the vocabulary — but not which row had which reason. |
| `multiple_dichotomy` | no flag | It is the ergonomic half of the additive design and costs one column per set. The constituents carry the fidelity either way, so suppressing it would remove convenience and change nothing else. |

Neither knob changes the categorical arm: a `categorical_*` column keeps
its user-missing codes in its own dictionary and never gets a sibling
(see [Categorical user-missing codes](#categorical-user-missing-codes)).

## Value labels: the cohort stores codes

A labelled SPSS variable becomes a `categorical_*` column whose
dictionary holds the **numeric codes**, not the labels:

```bash
pulse cohort inspect survey.pulse --json
```

```json
{
  "name": "SEX",
  "type": "categorical_u8",
  "description": "Sex",
  "categorical": true,
  "dictionary": { "total_entries": 2, "values": ["1", "2"] }
}
```

`"1"` / `"2"` — not `"Male"` / `"Female"`. This is deliberate. SPSS
permits two distinct codes to share one value label, so a label-keyed
dictionary would silently collapse them and the original code could not
be recovered. Dictionary entry order also *is* the on-wire encoding, so
preserving the source's own code order is what preserves the round trip.

To see labels in output, register a **LabelTable** mapping code → label
(`pulse.Options.LabelTables`, or a directory of JSON files pointed at by
`PULSE_LABEL_TABLES_DIR`) and bind it per request. Labels are an
output-time projection, never a property of the stored cohort; the
`label-display` skill is the full surface. The sidecar's `categories[]`
block is where the code → label pairs came from, so it is the natural
place to generate a label table from.

> **`PULSE_LABEL_TABLES_DIR` may safely point at a directory of
> cohorts.** The loader parses **every** `*.json` beneath that root as a
> label table, but it excludes Pulse's own sidecars by suffix before
> reading them — the SPSS metadata sidecar written next to every
> imported cohort (`cohort.pulse.spss.json`, `spss.SidecarSuffix`) and
> the managed-import sidecar (`cohort.pulse.meta.json`,
> `imports.SidecarSuffix`). A skipped sidecar registers no label table
> under any name.
>
> That is an exclusion of files Pulse knows are its own, **not**
> tolerance of unparseable JSON. Any other `*.json` that fails to parse
> still fails `pulse.New` outright, naming the offending path:
>
> ```
> pulse: parsing label table /data/cohorts/regions.json:
> json: cannot unmarshal number into Go value of type string
> ```
>
> A typo'd label table must surface as an error, not as a table that is
> quietly not there. A dedicated label-tables directory remains the
> tidier habit, but it is no longer a requirement.

## Flags and what SPSS ignores

`pulse import spss` takes the shared import flags plus one of its own,
`--charset`.

| Flag | Alias | Effect on an SPSS import |
|---|---|---|
| `--input` | `-i` | required — the `.sav` / `.zsav` path |
| `--output` | `-o` | required — the `.pulse` path to write |
| `--schema` | | **wins outright.** An explicit schema file overrides the SPSS dictionary and the adapter's `PulseSchema()` is never called |
| `--charset` | | override the encoding the file declares about itself — see [Character encoding](#character-encoding) |
| `--spss-missing` | | `auto` (default) or `null` — how numeric user-missing values are represented; see [Missing values](#missing-values) |
| `--sample-rows` | | **inert.** Nothing is sampled |
| `--json` | | standard envelope |

`--charset` and `--spss-missing` are also on `pulse import predict`,
`pulse import schema-template`, `pulse convert` and `pulse convert
predict`, since a `.sav` can arrive through any of them. Every other
format ignores both.

`--charset` — and only `--charset` — additionally reaches `pulse import
auto` and the `pulse_import` MCP tool, where it is spelled `charset`:

```console
$ pulse import auto legacy.sav --charset windows-1252
```

**The asymmetry is deliberate.** `--charset` is the only recourse for a
file that is wrong about itself, so without it a legacy `.sav` is
unimportable through the managed pool — and over MCP, unimportable full
stop, since `pulse_import` is the only import path MCP exposes.
`--spss-missing` is the opposite shape: its default (`auto`) is the
fidelity-preserving mode, and its only other value suppresses the
`<var>_missing` siblings that record *why* each numeric value is
missing. A knob whose sole effect is to discard information does not
belong on an auto-detect convenience path or on a general-purpose tool;
`pulse import spss --spss-missing=null` is where asking for it is an
explicit act. Both surfaces send no missing-mode at all, and an unset
mode means "leave the default in force" — never an override with the
empty string.

The same inertness applies to the library and managed-import knobs that
exist only to steer inference — `ImportJob.SampleRows`,
`SetInferenceMinPct`, `SetDelimiters` and the `force_type` column
overrides on `pulse import auto`. Forcing a type onto a
dictionary-carrying column would discard the source's category IDs and
rebuild them in first-seen order, which is exactly what an authoritative
schema exists to prevent.

There is also **no null promotion**. For inferred formats a null found
past the sample window widens the field to nullable and reports it in
`promoted_fields`. A declared schema is a contract instead: a null in a
column SPSS declares non-nullable stays a `PULSE_IMPORT_ROW_ERROR`.

## Diagnostics

Non-fatal parse findings are coded `PULSE_SPSS_*` warnings. They do not
stop the import, but they change what the resulting cohort *means*, so
read them. On the text path they print one per line:

```
Warning [PULSE_SPSS_CARDINALITY_HIGH]: spss: variable "COMMENT": the variable has
150 distinct value(s) across 150 case(s), which maps to a categorical_u8 dictionary
of one entry per 1.0 case(s); a near-unique categorical is the free-text signature
and its inline dictionary block is read on every open [record type 2 at byte offset 208]
```

With `--json` they are lifted onto the envelope's `warnings` array —
not buried inside `data`, so a generic envelope consumer sees them:

```json
{
  "code": "PULSE_SPSS_CARDINALITY_HIGH",
  "message": "spss: variable \"COMMENT\": the variable has 150 distinct value(s) ...",
  "details": {
    "variable": "COMMENT",
    "distinct": 150,
    "actual": 150,
    "record_type": "2",
    "offset": 208
  }
}
```

`pulse errors lookup CODE` carries the canonical prose and fixups for
each.

| Code | Raised when |
|---|---|
| `PULSE_SPSS_CARDINALITY_HIGH` | A string column is near-unique — a free-text signature |
| `PULSE_SPSS_DATE_WIDENED` | A date column widened to `datetime` |
| `PULSE_SPSS_TEMPORAL_PRECISION` | A temporal column demoted to `f64` raw seconds |
| `PULSE_SPSS_VALUE_COLLISION` | Two distinct SPSS values resolve to one dictionary entry (reachable causes: the shared import path trims cells, so `" X"` and `"X"` merge; or two user-missing codes share a value label, in which case the second reason falls back to its code — see [Missing values](#missing-values)) |
| `PULSE_SPSS_MEASURE_LEVEL_MISMATCH` | A `scale`-level variable carries value labels, so it mapped to a categorical whose smart defaults are `AGG_FREQUENCY` / `GROUP_CATEGORY` rather than `AGG_SUM` / `GROUP_RANGE` |
| `PULSE_SPSS_NULL_TOKEN_COLLISION` | A cell's text is a null sentinel (`""`, `NA`, `N/A`, `NULL`) and imports as null |
| `PULSE_SPSS_CATEGORICAL_USER_MISSING` | Informational, once per file: categorical columns carry user-missing codes as ordinary dictionary entries — see [Categorical user-missing codes](#categorical-user-missing-codes) |
| `PULSE_SPSS_EXTENSION_UNKNOWN` | A record type 7 extension subtype this reader does not interpret; its bytes are retained verbatim |
| `PULSE_SPSS_EXTENSION_INVALID` | An interpreted subtype carried a payload of the wrong shape; framing stayed sound, only the interpretation is dropped |
| `PULSE_SPSS_VERY_LONG_STRING_INVALID` | A record `7/14` segmentation could not be reassembled; the segments import as separate columns — see [Very long strings](#very-long-strings) |
| `PULSE_SPSS_DATA_CASE_COUNT_MISMATCH` | The header's declared case count disagrees with the cases present |
| `PULSE_SPSS_CHARSET_MISMATCH` | The file states its character encoding twice and the two disagree — see [Character encoding](#character-encoding) |
| `PULSE_SPSS_MAGIC_FLAG_MISMATCH` | The 4-byte magic and the compression flag disagree about whether the file is a ZSAV — see [Byte order and damaged files](#byte-order-and-damaged-files) |
| `PULSE_SPSS_VALUE_LABELS_DROPPED` | A record `3`/`4` value-label set names variables it cannot be bound to; that set is dropped and the data imports — see [Byte order and damaged files](#byte-order-and-damaged-files) |

## Character encoding

Pre-Unicode `.sav` files hold text in a codepage — windows-1252,
ISO-8859-1, Shift_JIS and the rest — not in UTF-8. Pulse decodes every
string it reads into UTF-8, so a French or German survey saved in 2004
imports as `Zürich` and `Männlich`, not as `Z?rich` and `M?nnlich`.

Decoding is done with [`golang.org/x/text`](https://pkg.go.dev/golang.org/x/text/encoding),
which this feature promoted from an indirect to a **direct** module
dependency — it was already in the graph via the Arrow and Excel
adapters. No cgo, so the `CGO_ENABLED=0` build is unaffected.

**Where the charset comes from**, highest precedence first:

1. the record `7/20` character-encoding **name** (`windows-1252`,
   `UTF-8`, …), which is what PSPP and modern SPSS write;
2. the record `7/3` **character code** — the legacy numeric field
   (`2`/`3` ASCII, `1252`, `65001`, …);
3. UTF-8, for a file that declares neither.

Spelling is forgiving on purpose: `windows-1252`, `Windows_1252`,
`cp1252`, `CP-1252` and `1252` are the same request. It is not *loose* —
`1250` never resolves to windows-1252 no matter how it is punctuated.

**When both are present and they disagree, the `7/20` name wins** and
`PULSE_SPSS_CHARSET_MISMATCH` is raised as a warning. The name is
strictly the more informative statement — code `3` ("8-bit ASCII") is
what a writer emits for ISO-8859-1, windows-1252 and half a dozen
national codepages alike — and writers routinely leave the numeric field
at an ASCII default while filling in `7/20` correctly. A `7/3` code of
`2` or `3` is therefore never treated as a disagreement.

**A byte the declared charset cannot decode is an error, never a
replacement character.** The usual behaviour of a text decoder is to
substitute U+FFFD and carry on, which would fill a cohort with
replacement characters no later stage could tell from data:

```
error: PULSE_SPSS_CHARSET_INVALID: spss: a data value of variable "CITY":
byte 0x81 at position 1 of the value "Z\x81rich" is not decodable in the
declared character encoding windows-1252
```

Almost always the file is wrong about itself rather than the bytes being
wrong — a dictionary transcoded by one tool and re-saved by another keeps
its old `7/20` name. Only the caller can say which is right, so the
library reader takes an override:

```console
$ pulse import spss -i survey.sav -o survey.pulse --charset windows-1252
```

```go
r := spss.NewReader(fs, "survey.sav", spss.WithCharset("windows-1252"))
```

It changes **decoding only**; the file's own declaration is still
retained, so a future export re-encodes into what the source said. The
name goes through the same forgiving lookup the file's own declaration
does, so `windows-1252`, `cp1252` and `1252` are one request; a name
with no decoder behind it is `PULSE_SPSS_CHARSET_UNSUPPORTED` naming it.

The case that most needs the flag is the file that declares **nothing at
all** — no `7/20`, no `7/3` — and carries an 8-bit byte. It falls to the
UTF-8 default, which is strict, so it fails on the first such byte. The
file has no further evidence to offer and only you can supply the
answer.

**A charset with no decoder is refused** rather than read as UTF-8:
`PULSE_SPSS_CHARSET_UNSUPPORTED` names it. That covers an unregistered
name, a registered one with no implementation (EBCDIC codepages), and
any encoding that is not an ASCII superset — UTF-16 among them, because
a `.sav` pads its fields with the byte `0x20` and delimits its record
`7/5` and `7/13` payloads with ASCII, so an encoding that does not write
ASCII as itself cannot express the format at all.

## Very long strings

SPSS cannot state a string width above 255 in a variable record: the
`type` field is one byte's worth of range. A wider string is therefore
stored as **several physical variables**, and a record `7/14` extension
says how to put them back together.

That is a *second* segmentation, stacked on one that is already there.
Every string over 8 bytes is spread across 8-byte data elements
(continuation records); a very long string is spread across physical
*variables*, each of which is itself spread across elements. A 600-byte
string is three physical variables of 255, 255 and 96 declared bytes,
occupying 32 + 32 + 12 = 76 elements.

The number that decides everything is **252, not 255**. A non-final
segment *declares* 255 bytes but only its first 252 carry the value; the
remaining three are unused. The clearest proof is a 256-byte string: it
is two segments declaring 255 and 4, whose declared widths sum to 259 —
three more bytes than the variable can hold. Only a 252-byte stride
reproduces a 256-byte value.

Pulse folds all of that away. `pulse inspect` and the imported cohort
show **one column**, under the variable's own name; the generated
segment names (`COMMENT0`, `COMMENT1`, …) never appear:

```console
$ pulse import spss -i survey.sav -o survey.pulse
Imported 2 rows to survey.pulse

$ pulse cohort inspect survey.pulse
Fields: 2
  ID                             f64                  Numeric field: ID
  Comments                       categorical_u8       Free text
    dictionary: 2 entries
```

Two properties are worth knowing:

- **Bytes are joined before they are decoded.** A segment boundary falls
  at a fixed byte offset that knows nothing about characters, so a
  multi-byte character can straddle it. Pulse concatenates the raw bytes
  first and runs the charset decoder once over the result. Decoding each
  segment separately would cut such a character in half.
- **The layout is kept, not discarded.** The number of physical
  variables, their names and their declared widths survive the fold, so
  an export can re-segment the value the way the source had it. The
  retained *width* is the logical total (600), not the 255 any one
  segment declares.

A record `7/14` that cannot be applied is
`PULSE_SPSS_VERY_LONG_STRING_INVALID`, and it is a **warning**. The
record only says how to *join* columns that are already in the file, so
declining to join loses no bytes at all: the segments import as the
separate columns the dictionary literally declares, under their own
names, and the warning says which variable and why.

Two sibling records decorate wide strings, and both apply to *any*
string over 8 bytes — not only very long ones, because the 8-byte value
slot in a variable record is what they exist to get around:

| Record | Carries | Where it lands |
|---|---|---|
| `7/21` | value labels for a wide string | The column's `categorical_*` dictionary, in record order, exactly as records `3`/`4` do for narrow ones |
| `7/22` | up to three missing values for a wide string | The variable's missing-value specification. The slot is fixed at 8 bytes because SPSS itself compares only a long string's first 8 |

Both name their variable by its **long** name (record `7/13`) in every
file the wider ecosystem will read; Pulse falls back to the short name
for writers that used it.

> **Cross-check note.** R's `foreign` reads `7/14` files but deliberately
> does *not* reassemble — it imports each segment as its own variable and
> says so in a warning. R's `haven` (ReadStat) does reassemble, but
> concatenates each non-final segment's full **255** declared bytes
> rather than its 252 content bytes, so it returns a 600-byte value as
> 606 bytes with three spurious spaces at every segment boundary, and a
> 256-byte value as 259. The divergence is invisible for the common case
> — a wide field holding a short value, where trailing-space trimming
> hides it — and only appears once a value actually exceeds 252 bytes.

## Compression

A `.sav` data section arrives in one of three encodings, and the file
header says which. All three import.

| Encoding | Header flag | Header magic | Status |
|---|---|---|---|
| Uncompressed | 0 | `$FL2` | Read |
| Bytecode | 1 | `$FL2` | Read — **this is what SPSS writes by default** |
| ZSAV (zlib blocks) | 2 | `$FL3` | Read — **this is what a `.zsav` carries** |

Nothing needs to be passed to select one. The flag is read from the
header and the right decoder runs:

```bash
pulse import spss --input survey.sav --output survey.pulse
```

A compressed and an uncompressed copy of the same data produce
identical cohorts, whichever of the three encodings was used.

### How bytecode compression works

The data section becomes a stream of blocks. Each block is **eight
command bytes** followed immediately by the eight-byte payloads that
those commands asked for:

| Command | Means |
|---|---|
| `0` | Padding — occupies a command slot, produces no value. Fills out the final block. |
| `1..251` | The whole number `command - bias`. The bias is read from the header; it is conventionally 100, giving the range −99…151. |
| `252` | End of the data section. |
| `253` | The next eight bytes of the stream are the value, verbatim. |
| `254` | An all-spaces eight-byte string segment. |
| `255` | System-missing. |

The saving comes from survey data being mostly small whole numbers: one
byte instead of eight. It is lossless — anything the commands cannot
express falls through to `253` unchanged.

### When a compressed file will not read

```
error: reading authoritative source schema: PULSE_SPSS_COMPRESSION_INVALID:
spss: data section: the compressed stream asks for an all-spaces string
segment (command 254) at element 1 of a case, where the dictionary declares
a numeric element; the stream has lost sync with the dictionary
[at byte offset 372 (0x174)]
```

`PULSE_SPSS_COMPRESSION_INVALID` means the command stream and the
dictionary disagree — a command landed on an element position it cannot
apply to. Every element after that point would be read against the wrong
variable, so the import stops rather than emitting plausible numbers.
Re-export the file from SPSS or PSPP; a desynchronised stream cannot be
repaired by hand.

A stream cut short — mid-case, or with a `253` whose eight bytes never
arrived — is `PULSE_SPSS_DATA_TRUNCATED` instead.

### How ZSAV works

ZSAV is **two layers, not a third encoding**. The zlib blocks do not
hold case data — they inflate to a *bytecode command stream*, exactly the
one described above, which is then decoded exactly as it would be in a
plain `.sav`. A reader that treated the inflated bytes as values would
produce plausible numbers from every file, which is why the layering is
spelled out rather than assumed.

The blocks are described by an index at both ends of the data section:

| Structure | Where | Carries |
|---|---|---|
| `ZHEADER` | first 24 bytes of the data section | its own offset, the trailer's offset, the trailer's length |
| compressed blocks | after the `ZHEADER` | one independent zlib stream each |
| `ZTRAILER` | end of the file | the bias (negated), a reserved zero, the uncompressed block size, the block count, then one 24-byte entry per block |

Each entry gives its block's offset **and** size in two coordinate
spaces — where the block actually sits in the file, and where it would
sit if the file were not compressed. That redundancy is the point: the
entries must tile the compressed region exactly, each block starting
where the previous one ended, and Pulse checks every one of them
*before* inflating anything. Inflating from an offset no writer ever
wrote a stream at either fails or, worse, succeeds on something.

### When a `.zsav` will not read

```
error: reading authoritative source schema: PULSE_SPSS_ZSAV_INVALID:
spss: data section: ZSAV block 3 of 40 declares compressed offset 1857,
but the block before it ends at 1856; the compressed offsets must run on
without a gap [at byte offset 4218 (0x107A)]
```

`PULSE_SPSS_ZSAV_INVALID` means the block index does not describe the
file it sits in. The message **names the block**, and the same 1-based
number is carried structurally under `details.block`, so a fault in one
block of a thousand is actionable rather than a shrug. Re-export the
file; a block index cannot be repaired by hand.

`PULSE_SPSS_ZSAV_BLOCK_CORRUPT` is the other half: the index was
coherent, the offsets were right, and the bytes at them are damaged — a
block that will not inflate, fails its zlib checksum, or inflates to a
size other than the one its entry declares. A short or long block is as
fatal as one that fails outright, because the blocks concatenate into a
single command stream and a wrong-length block shifts every later value
onto the wrong variable. That one is usually a truncated download:
compare the file's byte length against the source.

A `.zsav` whose *inflated* stream disagrees with the dictionary raises
the bytecode codes above (`PULSE_SPSS_COMPRESSION_INVALID`,
`PULSE_SPSS_DATA_TRUNCATED`), not a ZSAV code — the zlib layer was
intact, so pointing at it would send you to the wrong place.

> **Read-only by design.** Pulse never writes ZSAV, and there is no
> `pulse export spss` at all.

## Fatal errors

| Code | Meaning |
|---|---|
| `PULSE_SPSS_DICT_INVALID` | The dictionary is malformed |
| `PULSE_SPSS_DICT_TRUNCATED` | The file ends mid-dictionary |
| `PULSE_SPSS_FILE_EMPTY` | The source has no bytes at all — a zero-length file, not a truncated one |
| `PULSE_SPSS_ENDIANNESS_MISMATCH` | The header layout code and record `7/3` contradict each other about byte order — see [Byte order and damaged files](#byte-order-and-damaged-files) |
| `PULSE_SPSS_COMPRESSION_UNSUPPORTED` | A compression flag the format does not define — all three defined encodings are read |
| `PULSE_SPSS_COMPRESSION_INVALID` | A bytecode stream that disagrees with its own dictionary, or an unusable compression bias — see above |
| `PULSE_SPSS_ZSAV_INVALID` | A ZSAV block index that does not describe its file — names the block — see above |
| `PULSE_SPSS_ZSAV_BLOCK_CORRUPT` | A ZSAV zlib block that will not inflate, or inflates to the wrong length — names the block — see above |
| `PULSE_SPSS_DATA_TRUNCATED` | The data section ends mid-case, or a `253` command's value is missing |
| `PULSE_SPSS_CATEGORICAL_OVERFLOW` | A labelled variable has more distinct codes than `categorical_u32` holds |
| `PULSE_SPSS_DERIVED_NAME_COLLISION` | A generated column has the same name as a real variable — names both sides. A **hard error** for a `<var>_missing` sibling (see [Missing values](#missing-values)); a **warning** for a multiple-dichotomy `set_*` column, which is additive (see [Multiple-response sets](#multiple-response-sets)) |
| `PULSE_SPSS_MR_SET_NOT_DERIVED` | A multiple-dichotomy set got no `set_*` convenience column — names the set and why. Warning only: every constituent is imported regardless — see [Multiple-response sets](#multiple-response-sets) |
| `PULSE_SPSS_MISSING_MODE_INVALID` | `--spss-missing` was given a value other than `auto` or `null` |
| `PULSE_SPSS_CATEGORICAL_USER_MISSING` | Informational: one or more categorical columns carry user-missing codes as ordinary dictionary entries — one diagnostic per file, naming every flagged variable and entry under `details.missing_categories` — see [Categorical user-missing codes](#categorical-user-missing-codes) |
| `PULSE_SPSS_CHARSET_UNSUPPORTED` | The declared character encoding resolves to no decoder — see [Character encoding](#character-encoding) |
| `PULSE_SPSS_CHARSET_INVALID` | A byte sequence is not decodable in the declared character encoding — names the variable and the value |
| `PULSE_SPSS_EXPORT_UNSUPPORTED` | Something in the cohort has no honest `.sav` form — an empty `set_*` dictionary, a value with no recorded SPSS code, or a rendered row stream with no cohort behind it. Raised on the WRITE side |

Parse-stage diagnostics (dictionary walk, data pass) carry `record_type`
and `offset` details pinpointing where in the file they were raised, and
the `PULSE_SPSS_ZSAV_*` pair additionally carries `block`;
schema-mapping diagnostics carry `variable`.

> On the `--json` path a **fatal** import error is currently reported
> under the generic envelope code `IMPORT_ERROR` (`CLI_ERROR` for a
> convert), with the `PULSE_SPSS_*` code carried inside the `message`
> string. Non-fatal `PULSE_SPSS_*` warnings do reach `warnings[].code`
> structurally.

## Byte order and damaged files

### Either byte order reads

A `.sav` is written in the byte order of the machine that wrote it, and
Pulse reads both. The header **layout code** decides: it always holds
`2` or `3`, and neither value byte-swaps into the other or into anything
in range, so reading those four bytes both ways identifies the file's
order with no residual doubt. That is what the field is for.

Record `7/3` states the byte order a **second** time, as a numeric field
(`1` big-endian, `2` little-endian). It is a corroboration and never a
source — reading it at all already requires knowing the order. When the
two contradict each other the import stops with
`PULSE_SPSS_ENDIANNESS_MISMATCH`.

That is a deliberately harsher answer than the sibling charset
cross-check one field away, which warns and carries on. Byte order
governs *every* multi-byte field in the file — every count, every offset,
every double — so there is no partial damage to weigh: one reading yields
a coherent file and the other yields numbers that are plausible and
wrong. When the file's own two statements disagree, no evidence remains
about which the writer meant.

Shapes that are **not** a contradiction, and read normally: a file with
no record `7/3` at all, an endianness field left at `0`, and any value
outside `{1, 2}` (which raises `PULSE_SPSS_EXTENSION_INVALID` and is
otherwise ignored — an unfilled field is not a claim).

### Magic versus compression flag

`$FL2` marks an ordinary system file and `$FL3` marks a ZSAV, but the
**compression flag** is what actually decides how the data section is
decoded. When the two disagree — `$FL3` with flag `0` or `1`, or `$FL2`
with flag `2` — the flag wins, the file reads, and
`PULSE_SPSS_MAGIC_FLAG_MISMATCH` warns.

Warning rather than error, on the same reasoning as the charset
cross-check: the flag describes the bytes, while the magic is a coarse
generation label a re-saving tool can leave stale. And the failure mode
is safe either way — the wrong decoder fails loudly on the first case
rather than quietly succeeding.

### Value labels that cannot be bound

A record `3`/`4` pair can name variables it cannot legitimately attach
to: a set mixing a numeric with a string, a set attached to a string
wider than the 8 bytes a record `3` value slot holds (those belong in the
record `7/21` extension), or an element index landing on a string
continuation rather than on a variable. Each of those drops **that one
label set** with `PULSE_SPSS_VALUE_LABELS_DROPPED` naming the variable,
and imports everything else.

The reasoning is that a value label is display metadata. Refusing the
file would cost the data to save the labels; binding the set anyway
would attach labels to values they do not describe — an 8-byte slot
matched against a 20-byte string labels everything sharing a prefix —
without saying so. Dropping loudly is the only option that loses nothing
silently.

Genuinely **corrupt** indices are still fatal. An element index below
`1`, or past the end of the dictionary, cannot come from a writer reading
the format differently, and it puts the record's framing in doubt:
`PULSE_SPSS_DICT_INVALID`.

### Damage has distinct codes

Four different things can be wrong with a file, and they have four
different fixes, so they get four different codes:

| Code | What happened | What to do |
|---|---|---|
| `PULSE_SPSS_FILE_EMPTY` | zero bytes | the target was created and never written — check the export or the redirection |
| `PULSE_SPSS_DICT_TRUNCATED` | stops mid-dictionary | re-transfer; compare byte sizes |
| `PULSE_SPSS_DATA_TRUNCATED` | stops mid-case | re-transfer |
| `PULSE_SPSS_DICT_INVALID` | structurally wrong — bad magic, unidentifiable byte order, unknown record tag, out-of-range field | this is not the file you think it is, or it is corrupt beyond a truncation |

No input panics. Every read is bounds-checked before it happens, and
that is verified rather than asserted: a corruption sweep overwrites
every byte of the dictionary with all 256 values across all three
compression modes, a truncation sweep cuts the file at every offset in
each mode, and two fuzz targets (`FuzzParseDictionary`, `FuzzReadRows`)
carry the invariant beyond single-byte edits. Every failure is a coded
error a caller can switch on.

## Converting out

`pulse convert` reads `.sav` and writes any format Pulse can write:

```bash
pulse convert survey.sav survey.csv
```

```
Converted 2 rows: survey.sav -> survey.csv
```

The reverse direction writes a `.sav`:

```bash
pulse convert data.csv out.sav
```

A text source has no cohort behind it, so the writer buffers the rows,
builds an intermediate cohort in memory through the ordinary import path
and exports that — an inferred schema and no metadata sidecar, which
means synthesised SPSS names. If the CSV's header carries names a `.sav`
cannot express — a space, a bracket, a hyphen, a leading digit — it stops
with `PULSE_SPSS_NAME_INVALID` rather than mangling them; add
`--sanitise-names` to rewrite them and have every rename reported:

```
error: PULSE_SPSS_NAME_INVALID: spss: "household income (gross)" is not a
name a .sav can carry as a variable: it contains ' ' at byte 9
```

## Related

- [`pulse cohort inspect`](cohort-inspect.md) — read the schema and dictionaries the import produced
- [`pulse manifest`](manifest.md) — `import.formats[]` declares `spss` with `schema_source: "authoritative"` and `export: true`
- [`pulse export spss`](export-spss.md) — the write half: the sidecar read, the derived-column fold, the name boundary, the four write flags
- [Adding an I/O Format](../internals/adding-io-format.md) — the `SchemaAwareReader` / `SourceWarningEmitter` / `SidecarEmitter` / `CohortWriter` / `TargetWarningEmitter` contracts this adapter implements
- `skills/spss-cohorts.md` — the agent-facing SPSS surface: mapping table, derived columns, missing-value split, metadata sidecar, diagnostics
- `skills/cohort-schema-design.md` — the `.pulse` field-type matrix
- `skills/label-display.md` — turning the stored codes into labels at output time
- `skills/tool-import.md` — the `pulse_import` MCP surface

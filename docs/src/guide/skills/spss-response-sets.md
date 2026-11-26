```yaml
name: spss-response-sets
description: SPSS multiple-response sets and the fields an import synthesises — the derived set_* mask beside a multiple-dichotomy set's constituents, why multiple-category sets derive nothing, and the closed derived registry the export folds by. Read this when a cohort has more fields than its .sav had variables.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, multiple-response sets, derived fields, derived registry, set fields]
requires: [io_format:spss]
```

# SPSS response sets and derived fields

Part of the SPSS surface; entry skill [`spss-cohorts`](spss-cohorts.md). The other derived kind, the numeric `<var>_missing` sibling: [`spss-missing-values`](spss-missing-values.md).

## Multiple-response sets

SPSS is the one source that DECLARES a multi-select; every other path guesses from delimited strings. A multiple-**dichotomy** set gets a derived `set_*` mask **beside** its constituents, never instead — a bit cannot separate `Q1B=0` ("shown, not picked") from `Q1B=.` ("never asked"). MD set ⇒ **N + 1** fields; each constituent keeps its own null bit and `<var>_missing` sibling; the derived field follows the LAST constituent (a summary must not precede its parts).

- Dictionary holds constituent **field names**, not option labels — cohort-unique, so injective for free. Labels stay on `variables[].label`.
- Mask uses the **declared counted value**, never a guessed `1`. A user-missing code sets no bit, and is not evidence of an answer.
- Set name loses its `$` (`$media` → `media`) — a sigil is no legal expr-lang identifier. Full name on `derived[].set_name`.
- **Three row states:** option(s) selected → bits set; answered, nothing selected → **empty mask**, a real "none of these", NOT null; all constituents missing → null.
- `PULSE_SPSS_MR_SET_NOT_DERIVED` — WARNING, import succeeds — on >**256 constituents** (`set_u256` is the widest rung; a 206-option battery derives), an undeclared or duplicated member, a counted value that will not compare against a numeric member, or a constituent whose field name holds the set delimiter `|` or IS a null token. The additive design paying out: a set that does not derive costs ergonomics, never data.

**Multiple-CATEGORY sets derive nothing — a fidelity call.** N answer SLOTS over a shared value-label set, so slot ORDER ("first choice" vs "third") and a REPEATED code (two slots both `2`) are real; a bitmask is unordered and idempotent. Members import as ordinary `categorical_*` as if the definition were absent; only the definition rides the sidecar.

## Derived fields and the `derived` registry

`payload.derived` names every SYNTHESISED field ⇒ a field absent from it is a source variable **by construction**. Name-matching cannot substitute: `_missing` is a legal SPSS suffix (`income_missing` is not hypothetical) and a set field matches no pattern.

| `kind` | Fold action | Opt out |
|---|---|---|
| `numeric_missing` | `restore` — its per-row ID decides what its one source variable writes wherever that variable is null | `--spss-missing=null` |
| `multiple_dichotomy` | `drop` — every bit re-reads a constituent still in the cohort | none by design; one field per set |

- `kind` is a **CLOSED vocabulary** (`DerivedKinds()` in `internal/io/spss`), one action each via `DerivedFoldFor`, which reports `false` otherwise — an older binary meeting a newer document stops rather than defaults.
- Entries are self-sufficient (`Derived.Complete()`): a reason sibling carries its reason dictionary (ID ↔ reason ↔ SPSS code ↔ label), the only record of which state each row was in; a set field carries `set_name` + `sources` in BIT order.
- Derived fields INTERLEAVE ⇒ `variables[].position` is a cohort position, not a source ordinal.
- **Nothing derived ⇒ `"derived": []`, never a missing key** — "nothing derived" and "cannot tell you" are different answers.

**Export-transparent by fold, not by name.** `foldDerived` (`internal/io/spss/dict_fold.go`) consumes the registry at plan time, so an emitted `.sav` carries exactly the source's own variables. `restore` binds a sibling to its variable; the encoder then writes the recorded SPSS code into every null instead of sysmis, from `Derived.Reasons`, never re-derived. `drop` releases a set field after checking its constituents are emitted. The encoder is driven by `DictionaryPlan.Columns` alone, so an unbound field is decoded (the record stride demands it) and written nowhere — a derived field cannot leak out as a variable even if the fold missed it.

**The audit is worth more than the fold.** `DictionaryPlan.UnboundFields` = every cohort field no emitted variable is written from; on the sidecar path, exactly the derived fields. Unaccounted ⇒ `PULSE_SPSS_COLUMN_UNMAPPED`: a field leaving the export silently, the outcome this path exists to refuse. Checked on the synthesised path too, where the registry is empty and every field must bind. An entry the binary cannot honour ⇒ `PULSE_SPSS_DERIVED_UNFOLDABLE`, four shapes — unknown `kind` from a newer import; `numeric_missing` missing its `reasons`; an entry naming an unemitted source field; an entry naming a field also emitted as a variable. A refusal: both available guesses are invisible data faults.

Sibling name colliding with a real variable (case-insensitively, as SPSS names are) ⇒ `PULSE_SPSS_DERIVED_NAME_COLLISION` naming both sides — hard ERROR for a sibling, warning on the MD-set arm (pure convenience).

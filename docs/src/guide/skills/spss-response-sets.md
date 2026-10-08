```yaml
name: spss-response-sets
description: SPSS multiple-response sets and the columns an import synthesises — the derived set_* mask beside a multiple-dichotomy set's constituents, why multiple-category sets derive nothing, and the closed derived registry the export folds by. Read this when a cohort has more columns than its .sav had variables.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, multiple-response sets, derived columns, derived registry, set fields]
requires: [io_format:spss]
```

# SPSS response sets and derived columns

Part of the SPSS surface; entry skill [`spss-cohorts`](spss-cohorts.md). The other derived kind, the numeric `<var>_missing` sibling: [`spss-missing-values`](spss-missing-values.md).

## Multiple-response sets

SPSS is the one source that DECLARES a multi-select; every other path guesses from delimited strings. A multiple-**dichotomy** set gets a derived `set_*` mask **beside** its constituents, never instead — a bit cannot separate `Q1B=0` ("shown, not picked") from `Q1B=.` ("never asked"). MD set ⇒ **N + 1** columns; each constituent keeps its own null bit and `<var>_missing` sibling; the derived column follows the LAST constituent (a summary must not precede its parts).

- Dictionary holds constituent **field names**, not option labels — cohort-unique, so injective for free. Labels stay on `variables[].label`.
- Mask uses the **declared counted value**, never a guessed `1`. A user-missing code sets no bit, and is not evidence of an answer.
- Set name loses its `$` (`$media` → `media`) — a sigil is no legal expr-lang identifier. Full name on `derived[].set_name`.
- **Three row states:** option(s) selected → bits set; answered, nothing selected → **empty mask**, a real "none of these", NOT null; all constituents missing → null.
- `PULSE_SPSS_MR_SET_NOT_DERIVED` — WARNING, import succeeds — on >**256 constituents** (`set_u256` is the widest rung; a 206-option battery derives), an undeclared or duplicated member, a counted value that will not compare against a numeric member, or a constituent whose field name holds the set delimiter `|` or IS a null token. The additive design paying out: a set that does not derive costs ergonomics, never data.

**Multiple-CATEGORY sets derive nothing — a fidelity call.** N answer SLOTS over a shared value-label set, so slot ORDER ("first choice" vs "third") and a REPEATED code (two slots both `2`) are real; a bitmask is unordered and idempotent. Members import as ordinary `categorical_*` as if the definition were absent; only the definition rides the sidecar.

## Derived columns and the `derived` registry

`payload.derived` names every SYNTHESISED column ⇒ a column absent from it is a source variable **by construction**. Name-matching cannot substitute: `_missing` is a legal SPSS suffix (`income_missing` is not hypothetical) and a set column matches no pattern.

| `kind` | Fold action | Opt out |
|---|---|---|
| `numeric_missing` | CONSUMED — its per-row ID decides what its one source variable writes wherever that variable is null | `--spss-missing=null` |
| `multiple_dichotomy` | DROPPED — every bit re-reads a constituent still in the cohort | none by design; one column per set |

- `kind` is a **CLOSED vocabulary** (`DerivedKinds()` in `internal/io/spss`), one action each via `DerivedFoldFor`, which reports `false` otherwise — an older binary meeting a newer document stops rather than defaults.
- Entries are self-sufficient (`Derived.Complete()`): a reason sibling carries its reason dictionary (ID ↔ reason ↔ SPSS code ↔ label), the only record of which state each row was in; a set column carries `set_name` + `sources` in BIT order.
- Derived columns INTERLEAVE ⇒ `variables[].position` is a cohort position, not a source ordinal.
- **Nothing derived ⇒ `"derived": []`, never a missing key** — "nothing derived" and "cannot tell you" are different answers.

**Export-transparent by fold, not by name.** `foldDerived` (`internal/io/spss/dict_fold.go`) consumes the registry at plan time, so an emitted `.sav` carries exactly the source's own variables. `restore` binds a sibling to its variable; the encoder then writes the recorded SPSS code into every null instead of sysmis, from `Derived.Reasons`, never re-derived. `drop` releases a set column after checking its constituents are emitted. The encoder is driven by `DictionaryPlan.Columns` alone, so an unbound field is decoded (the record stride demands it) and written nowhere — a derived column cannot leak out as a variable even if the fold missed it.

**The audit is worth more than the fold.** `DictionaryPlan.UnboundFields` = every cohort field no emitted variable is written from; on the sidecar path, exactly the derived columns. Unaccounted ⇒ `PULSE_SPSS_COLUMN_UNMAPPED`: a column leaving the export silently, the outcome this path exists to refuse. Checked on the synthesised path too, where the registry is empty and every field must bind. An entry the binary cannot honour ⇒ `PULSE_SPSS_DERIVED_UNFOLDABLE`, four shapes — unknown `kind` from a newer import; `numeric_missing` missing its `reasons`; an entry naming an unemitted source column; an entry naming a column also emitted as a variable. A refusal: both available guesses are invisible data faults.

Sibling name colliding with a real variable (case-insensitively, as SPSS names are) ⇒ `PULSE_SPSS_DERIVED_NAME_COLLISION` naming both sides — hard ERROR for a sibling, warning on the MD-set arm (pure convenience).

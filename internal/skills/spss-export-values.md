---
name: spss-export-values
description: What a .sav export writes for each value — the variable-name policy and --sanitize-names, set_* masks at every width, the missing state each null takes, original codes versus dictionary positions, byte order, and labels that live only on user-missing codes.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, sav, export, variable names, --sanitize-names, nulls, set fields, value labels]
requires: [io_format:spss]
---

# SPSS export — names, nulls and values

Part of the SPSS surface; entry skill `spss-cohorts`. The writer and its CLI surface: `spss-export`.

## Writing `.sav` — names, nulls and dictionary rules

**Names are policed and the default is a refusal.** A `.pulse` field name is any UTF-8 string; an SPSS variable name is ≤64 bytes, opens with a letter, unique ignoring case. All three ways an illegal name fails are quiet — records `7/13`, `7/7` and the case-fold rule each produce a well-formed file saying something else — so an offender is `PULSE_SPSS_NAME_INVALID` / `PULSE_SPSS_NAME_COLLISION`. `--sanitize-names` is the opt-in escape hatch for the synthesised path, where a CSV header's spaces and brackets are ordinary: deterministic, collision-safe against other renames **and** against already-legal names (which never move), every rename reported as `PULSE_SPSS_NAME_SANITIZED` with the full `field → name` list. Inert on the sidecar path — those names came from SPSS.

**`--ignore-sidecar` cannot round-trip a cohort still carrying a derived MD `set_*` column.** Its dictionary entries *are* its constituents' field names, so with the registry suppressed, synthesis mints indicator variables `Q1A`/`Q1B` beside the real `Q1A`/`Q1B` → `PULSE_SPSS_NAME_COLLISION`. Export *without* the flag so the registry folds the column away.

**The mask is width-blind on the way out, and the bit bound is a PLAN-time check.** The encoder carries a set value as an `encoding.SetMask` at every rung, not a `uint64`: the wide rungs (`set_u128`/`set_u256`) are read through `encoding.ReadSetMask` because `ReadFieldValue` refuses them outright, so a 206-option battery exports its bits above 63 instead of failing the whole file. Two consequences worth stating. A member variable's bit is bounded against **its own rung** inside `NewDataEncoder`'s column check, not per case — a per-case bound is a refusal `ValidateCohort` cannot see, so `export predict` would pass a cohort the export fails on record 1 (a `set_u8` declaring ten dictionary entries has no honest `.sav` form and says so before a record is read). And folding a derived MD column away is decided by `Derived.Kind`, never by width: a 206-constituent `set_u256` is dropped and its 206 constituents rebuilt exactly as a 3-constituent `set_u8` is.

**Nulls take the missing state the SPSS type has:** numeric → **sysmis sentinel**; string → **blanks** (no string sentinel exists, and blank reads back as null); every member of a null `set_*` → sysmis, keeping null apart from an empty mask on the way back. No honest form ⇒ `PULSE_SPSS_EXPORT_UNSUPPORTED` naming the variable, never a quiet substitution.

**Two dictionary rules.** Original SPSS codes, never dictionary positions — the sidecar triple supplies them, and `IF q1 EQ 5` addresses a value, so renumbering re-points every reference. No sidecar ⇒ nothing invented: a categorical becomes a STRING variable holding the dictionary text; a `set_*` expands to one indicator variable per entry (named for that entry, so the mask round-trips) plus a `7/7` dichotomy definition; `CategoryCode.Known` stays `false` so the plan says which it is.

**Two things deliberately do NOT reproduce the source:** byte order is always **little-endian** (`7/3` agrees), and `prod_name` identifies pulse. Easily-missed corollary — a NUMERIC missing-value slot is a `flt64`, so the sidecar's verbatim slots are **byte-reversed** when the source was big-endian; re-emitted as read they declare eight bytes decoding here as an unrelated subnormal, and the variable silently stops declaring anything missing. A **string** slot is characters, never reversed. Records `7/21`/`7/22` carry each variable's **FINAL** name (ReadStat refuses a file spelling the short name there). MOYR/QYR/WKYR keep both raw seconds and format code.

**Value labels declared only on user-missing codes come back from the derived registry, not the categories.** An income column labelled at `97`/`98` and nowhere else is not coded, so import maps it to `f64` and moves those labels into the sibling's `Derived.Reasons` — the only place they survive. Write re-emits them as ordinary records `3`/`4`; a categories-only export would return the *code* while losing what it MEANT, and the re-imported reason column would read as bare numerals.

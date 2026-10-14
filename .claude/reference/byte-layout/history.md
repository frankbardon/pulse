# Byte layout — history (NOT loaded by default)

Superseded wording relocated out of the live file at E9-S4; do not load it for contract work — the index `.claude/reference/byte-layout.md` and its slices are live.

**CLAUDE.md's sidecar contract sentence, pre-compression (relocated verbatim at weighting-descriptive E1-S1).** CLAUDE.md now keeps a shorter list; this is the original:

**Contract: `.claude/reference/byte-layout.md` — load it before touching any of these. None is a `.pulse` layout, and every one fails SILENTLY when got wrong:** the sidecar point-lookup index (`cohort.pulse.<keyhash>.idx`, v3, THREE-outcome staleness), its discovery manifest (`cohort.pulse.indexes.json`), the SPSS metadata sidecar (`cohort.pulse.spss.json` — the only place the `code ↔ label ↔ dictionary-ID` triple lives), SPSS derived columns (an import can yield a cohort WIDER than its source dictionary — count columns from the schema), SPSS export via `pio.CohortWriter`, SPSS convert's source-facts carry via `io.SourceAwareWriter` (a `.sav` → `.sav` convert rebuilds from rendered TEXT, so it needs the source's schema AND sidecar or re-infers both), target-aware export predict via `io.CohortValidator`, and default-on projected buffered decode.

```yaml
name: cohort-parent-groups
description: Parent groups in format 0x02 cohorts — storing a repeated parent block once, when a group pays for itself, declaring one at import with a key check, suggesting and gating groups, converting an existing cohort with dedup, and opt-in global-constant elision.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [parent groups, 0x02, --group, --suggest-groups, dedup, constant elision, --elide-constants]
```

# Parent groups

Part of the `.pulse` schema surface; entry skill [`cohort-schema-design`](cohort-schema-design.md). Byte layout: `.claude/reference/byte-layout.md` (Parent groups).

## Parent groups (format 0x02)

A denormalised join repeats its parent block on every child row. A **parent group** stores each distinct tuple of its member fields once, in a dictionary in the schema block, and each row carries a 4-byte index — N independent groups per cohort, a field in at most one. A **constant group** holds one entry and no per-row index (a column with one value everywhere). Members keep their own types, nullability and dictionaries; decode is indistinguishable from the ungrouped cohort, so requests, filters, groupers and exports address member fields by name exactly as before. Worth it only when the group is wider than its 4-byte index and repeats heavily — a narrow or rarely-repeating group makes the file bigger and the resident dictionary costs memory. `pulse widen` does not accept a grouped cohort (coded refusal); a shard archive reconciles every arrival to its ONE group layout ([`cohort-sharding`](cohort-sharding.md), Cohesion). `pulse_inspect` reports each group's realized ratio and resident dictionary bytes, header-only. A filter whose fields all sit in ONE group is evaluated once per dictionary entry instead of per row (same results and counts) — keep a filter off a parent/child mix to get it. Filter-to-file keeps a cohort grouped: header, groups and dictionaries are carried unchanged, and entries no surviving row uses are NOT pruned. Inspect's ratio (surviving rows ÷ carried entries) therefore drops after a selective filter. That is expected. Re-dedup the output to prune.

**Declaring a group** at import: `pulse import <fmt> --group KEY[,KEY...]:MEMBER[,MEMBER...]`, repeatable, one group per flag (`ImportJob.Groups`). Key fields identify the parent; members are what the key determines. Two rows with the same key but a differing member (value or null) FAIL the import (`PULSE_GROUP_MEMBER_NOT_CONSTANT`, naming field, record and source row) — never a silently bigger dictionary. No colon = a plain tuple group, no key check. A field in two groups, an unknown field or a malformed value fails before the row pass. Declare what you know from the upstream join; the 500-row inference sample cannot confirm a parent block. To find or size one first, run `pulse import predict --suggest-groups`. It nominates single-field keys over the first ≤10,000 rows, then measures each over EVERY row with the import's own gate. You get the ratio, resident dictionary, projected file size and a ready-to-paste `--group` value. It only suggests. `import predict --group …` evaluates a declaration without writing anything. Each group is gated on its own numbers: members no wider than the 4-byte index → dropped (`PULSE_GROUP_TOO_NARROW`); dedup ratio below `--dedup-ratio-floor` (default 2) or no net saving → still written with `PULSE_DEDUP_LOW_RATIO` (ratio, resident dictionary bytes, byte delta); `--strict` makes either an error. `ImportReport.Groups` carries each verdict and its numbers.

**Managed imports** (`pulse import auto --group`, `pulse_import` `groups` / `suggest_groups`) take the same declarations at the DEFAULT floor, findings as warnings — no floor, strict or elision slot.

**An existing cohort** converts with `pulse dedup COHORT --group …` (`Pulse.Dedup`, `pulse_dedup`): same declarations, gate and flags, in place (atomic) or `--out`; `--suggest-groups` finds candidates over its records. It reports invalidated sidecars and rebuilds none. Single-file only.

**Global-constant elision** is opt-in: `pulse import <fmt> --elide-constants` / `ImportJob.ElideConstants`. Fields holding one value on EVERY imported row — decided over the full row pass, never the 500-row inference sample — go into one constant group (`ImportReport.ElidedConstants`). A column null everywhere is a (null) constant; one value plus some nulls is NOT. Nothing is elided below 2 rows, or when declaring the constants costs more schema bytes than it saves — the cohort then stays byte-identical `0x01`. All-constant keeps the lowest-index field in the row. The output is `0x02`: binaries predating it cannot open it.

# Time Zones

Storage is always UTC: a `datetime` is epoch seconds, a `date` is epoch days. A time zone only changes how an instant is read (local calendar day, wall clock) or rendered (export). Zone names are `UTC` or an exact IANA `Area/Location` from Pulse's own embedded tz database, so results never depend on the host; anything else is `PULSE_TIMEZONE_UNKNOWN`. The manifest reports the release as `tzdata_version` (see [Refreshing the Time-Zone Database](../internals/refreshing-tzdata.md)).

## Choosing a zone

Per zone-capable slot: slot `tz`, then request `time_zone`, then `Options.DefaultTimeZone`, then UTC. There is no environment variable or default-zone CLI flag. With a per-row zone (below) the order is slot `tz`, slot `tz_field`, request `time_zone_field`, request `time_zone`, `Options.DefaultTimeZone`, UTC. Predict echoes the resolved zone per slot in `time_zones[]`. An explicit `tz` on a `date` field or a non-zone-capable operator is `PROCESSING_CONFIG`; a non-UTC zone on a derived field is refused. UTC or no zone is byte-identical to the pre-zone output.

## Local days in every mode

A `datetime` buckets and filters on its local calendar day in process, stream, compose, shards, crosstab, join, chain and facet, DST changes included. `GROUP_DATE` also takes `component: "hour"` (local wall-clock keys; the skipped DST hour has no bucket, the repeated hour is one) and `week_start`. `ATTR_DATE_PART` and `FEAT_DATE_FEATURES` read the zone's wall clock over a `datetime`. Range literals are local calendar days. `OVERLAY_YOY` follows its host grouper's zone.

## Per-row zones

When each record carries its own zone, name a `categorical_*` cohort field of IANA names: `tz_field` on a group, filterer, attribute or feature slot, or `time_zone_field` on a `Request` / `FacetRequest`. One request then equals the single-zone slots combined. `tz` and `tz_field` on one slot is `PROCESSING_CONFIG`, as is a non-categorical zone field; a dictionary entry that is not an IANA name is `PULSE_TIMEZONE_UNKNOWN` (with `dictionary_index`) from predict and the run alike. The predict echo reads `source: "field"`, `tz_field`, `tz: null`.

A null zone cell (or out-of-dictionary code) gives the row no local value and never falls back to UTC: groupers count it in `n_null`, `ATTR_DATE_PART` / `FEAT_DATE_FEATURES` yield null, `FILTER_DATE_RANGES` drops it and counts it in `n_null_input`. Expression functions (`to_day`, `weekday`) read only the request `time_zone`; filter per row through `FEAT_DATE_FEATURES` plus a filter. `FilterToFileRequest.TimeZone` is filter-to-file's request zone ([Filter to file](filter-to-file.md)).

## Import and export

`--source-tz [col=]Zone` reads naive datetimes in a source zone on every import path; `--dst-policy error|earlier|later` decides what happens to a skipped or repeated wall time (`earlier` = pre-transition offset, `later` = post-transition; for a skipped time `earlier` lands on the later wall clock, Berlin `02:30` → `01:30Z` = `03:30` local, and `later` on `01:30` local) (`PULSE_IMPORT_DST_NONEXISTENT`, `PULSE_IMPORT_DST_AMBIGUOUS`, `PULSE_IMPORT_DST_RESOLVED`). `--source-tz-field COL` (library `ImportJob.SourceTZField`, MCP `source_tz_field`) reads each row's source zone (IANA or `±HH:MM`) from another column for every naive datetime column; `column_source_tz` wins, an empty cell falls back to `--source-tz` or is a row error, and an unknown name drops the row with `PULSE_TIMEZONE_UNKNOWN` (fatal under `--strict`). Offset and `Z` literals never move. Native Arrow and Parquet timestamps type `datetime`; SPSS `DATETIME` is a wall clock while `TIME` / `DTIME` are `f64` durations. `export --tz Zone` renders local offsets; SPSS export refuses a non-UTC zone. Flag detail: [CLI flags](../cli/flags.md). Agent-facing form: skill `time-zones`.

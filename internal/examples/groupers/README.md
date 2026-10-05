# Grouper Examples

Runnable JSON requests demonstrating each `GROUP_*` operator. The
processor honors a single grouper per request (`req.Groups[0]`); compose
multiple requests for multi-level breakdowns.

Setup is documented in [`../README.md`](../README.md). Run all:

```
./internal/examples/groupers/run-all.sh
```

## Catalog

| File | Operator | Demonstrates |
|---|---|---|
| `01_category.json` | `GROUP_CATEGORY` | Partition on a categorical field (one group per region) |
| `02_date.json` | `GROUP_DATE` | Bucket dates by ISO month (`params.component`) |
| `03_quantile.json` | `GROUP_QUANTILE` | Quartiles by income (`Q1..Q4`) |
| `04_range.json` | `GROUP_RANGE` | Equal-width bins by income (keys `low-high`) |
| `05_rounded.json` | `GROUP_ROUNDED` | Round age down to nearest 10 (decade buckets) |
| `09_date_hour.json` | `GROUP_DATE` | Local wall-clock hour buckets of a `datetime` (`component: hour`, request `time_zone`) |
| `10_date_week_start.json` | `GROUP_DATE` | Sunday-start weeks keyed by their first day (`week_start: sunday`) |

`GROUP_DATE` components: `year`, `quarter`, `month`, `week`, `day`,
`hour` (`datetime` only), `day_of_week`; `week_start` (`week` only)
picks the day a week begins. `GROUP_QUANTILE` interval picks the bucket count; key
prefix changes by N (Q for 4, D for 10, P for 100, B otherwise).
`GROUP_RANGE` and `GROUP_ROUNDED` use the same binning formula but
different key formats: `GROUP_RANGE` keys are `"low-high"` strings,
`GROUP_ROUNDED` keys are the bin's lower bound.

# Grouper Examples

Runnable JSON requests demonstrating each `GROUP_*` operator. Several
groupers in `groups` nest: one output row per key tuple.

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
| `06_set_per_element.json` | `GROUP_SET_PER_ELEMENT` | One bucket per held issuer of a `set_u8` multi-select (a row lands in every bucket it holds) |
| `07_set_value.json` | `GROUP_SET_VALUE` | One bucket per distinct issuer combination (`AMEX\|MC` keys) |
| `08_date_ranges.json` | `GROUP_DATE_RANGES` | Inline labelled date ranges plus an `unmatched_label` bucket |
| `09_date_hour.json` | `GROUP_DATE` | Local wall-clock hour buckets of a `datetime` (`component: hour`, request `time_zone`) |
| `10_date_week_start.json` | `GROUP_DATE` | Sunday-start weeks keyed by their first day (`week_start: sunday`) |
| `11_kmeans.json` | `GROUP_KMEANS` | Three seeded k-means segments over age and income (`cluster_1..3` by size) |
| `12_numeric_ranges.json` | `GROUP_NUMERIC_RANGES` | Train/holdout split on `id` with labeled half-open ranges (`[1,161)`, `[161,201)`), supplied order |
| `13_date_per_row_zone.json` | `GROUP_DATE` | Each order on its own warehouse's local day (request `time_zone_field` over a `categorical` IANA-name field; a null zone cell is null, never UTC) |

`GROUP_DATE` components: `year`, `quarter`, `month`, `week`, `day`,
`hour` (`datetime` only), `day_of_week`; `week_start` (`week` only)
picks the day a week begins. `GROUP_QUANTILE` interval picks the bucket count; key
prefix changes by N (Q for 4, D for 10, P for 100, B otherwise).
`GROUP_RANGE` and `GROUP_ROUNDED` use the same binning formula but
different key formats: `GROUP_RANGE` keys are `"low-high"` strings,
`GROUP_ROUNDED` keys are the bin's lower bound. Quantile, range and
rounded rows come out in bucket order (`D1…D10`, ranges by low edge,
rounded keys by value) and numeric-ranges rows in supplied range order,
with no `sort`; a `sort` on the key column would compare the key strings
instead.

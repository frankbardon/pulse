# Groupers

The groupers this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`GROUP_CATEGORY`](#op-group_category) | Splits the rows into one group per distinct value of a field, such as one group per region or per answer option. | What is the average spend in each region? | basic | [`GROUP_RANGE`](#op-group_range) when the field is a number you want in bands, not one group per value.<br>[`GROUP_DATE`](#op-group_date) when the field is a date and you want months or quarters.<br>[`GROUP_SET_PER_ELEMENT`](#op-group_set_per_element) when the field is a multi-select answer. |
| [`GROUP_DATE`](#op-group_date) | Splits the rows by calendar period of a date, such as month, quarter, ISO week or weekday, to see a measure over time. | How many orders came in each month? | basic | [`GROUP_DATE_RANGES`](#op-group_date_ranges) when your periods are custom, such as campaign windows.<br>[`OVERLAY_INDEX_VS_PRIOR`](overlay.md#op-overlay_index_vs_prior) when you want each period against the one before it.<br>[`WIN_RUNNING_SUM`](window.md#op-win_running_sum) when you want a running total down the periods. |
| [`GROUP_DATE_RANGES`](#op-group_date_ranges) | Labels each row with the named date range its date falls in, such as custom fiscal quarters or before and after a launch. | How did sales compare before, during and after the campaign? | basic | [`GROUP_DATE`](#op-group_date) when plain calendar months, quarters or weeks are enough.<br>[`FILTER_DATE_RANGES`](filterer.md#op-filter_date_ranges) when you want to keep only the rows inside the ranges. |
| [`GROUP_QUANTILE`](#op-group_quantile) | Splits the rows into equal-sized groups by rank of a number, such as quartiles or deciles of spend, to compare top and bottom. | How do the top 25% of spenders differ from the bottom 25%? | intermediate | [`GROUP_RANGE`](#op-group_range) when you want bands of a fixed width, such as every 10 years of age.<br>[`ATTR_PERCENTILE`](attribute.md#op-attr_percentile) when you want each row's rank as a number, not a group.<br>[`AGG_PERCENTILE`](aggregator.md#op-agg_percentile) when you want the cut-off values themselves. |
| [`GROUP_RANGE`](#op-group_range) | Splits a number into bands of equal width, such as ages 20-30 and 30-40, to see how values spread or to compare bands. | How many customers fall in each 10-year age band? | basic | [`GROUP_QUANTILE`](#op-group_quantile) when you want groups of equal size rather than equal width.<br>[`GROUP_ROUNDED`](#op-group_rounded) when you want each band named by its lower edge alone.<br>[`FILTER_RANGE`](filterer.md#op-filter_range) when you want to keep one band, not split into all of them. |
| [`GROUP_ROUNDED`](#op-group_rounded) | Groups a number by rounding it down to a multiple of a step, such as 23 and 27 both to 20, naming each group by that multiple. | How many orders are there at each price point, to the nearest lower 10? | basic | [`GROUP_RANGE`](#op-group_range) when you want the band's both edges in its name.<br>[`GROUP_QUANTILE`](#op-group_quantile) when you want groups of equal size. |
| [`GROUP_SET_PER_ELEMENT`](#op-group_set_per_element) | Splits a multi-select answer into one group per option, counting each row once in every option it picked. | How many respondents picked each brand they are aware of? | intermediate | [`GROUP_SET_VALUE`](#op-group_set_value) when you want each exact combination of options as one group.<br>[`AGG_SET_FREQUENCY`](aggregator.md#op-agg_set_frequency) when you only want the count per option, without other figures. |
| [`GROUP_SET_VALUE`](#op-group_set_value) | Groups the rows by the exact combination of options picked in a multi-select answer, such as A only, A and B, or none. | Which combinations of payment methods do customers use, and how common is each? | intermediate | [`GROUP_SET_PER_ELEMENT`](#op-group_set_per_element) when you want one group per option, counting a row in each it picked.<br>[`FILTER_SET_EQUALS`](filterer.md#op-filter_set_equals) when you want to keep one combination rather than see them all. |

## Operators

<a id="op-group_category"></a>

### `GROUP_CATEGORY`

Splits the rows into one group per distinct value of a field, such as one group per region or per answer option.

**Level:** basic

**Questions it answers:**

- What is the average spend in each region?
- How many respondents chose each answer?

**Use cases by domain:**

- *survey:* Satisfaction score by age band or by brand.
- *ops:* Orders and revenue per store.
- *science:* Mean reading per treatment arm.

**Assumptions:**

- Buckets are built from the rows the filters kept; each aggregation is then worked out once per bucket.
- A row whose field is missing lands in no bucket; it is counted as missing, not as a bucket of its own.
- Groups appear in alphabetical order unless an include list names them, which also sets their order and drops the rest.
- A field with very many distinct values makes very many groups; narrow it with a filter first.

**Use something else:**

- [`GROUP_RANGE`](#op-group_range) when the field is a number you want in bands, not one group per value.
- [`GROUP_DATE`](#op-group_date) when the field is a date and you want months or quarters.
- [`GROUP_SET_PER_ELEMENT`](#op-group_set_per_element) when the field is a multi-select answer.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-group-category`](../skills/op-group-category.md)

<a id="op-group_date"></a>

### `GROUP_DATE`

Splits the rows by calendar period of a date, such as month, quarter, ISO week or weekday, to see a measure over time.

**Level:** basic

**Questions it answers:**

- How many orders came in each month?
- Which day of the week gets the most complaints?

**Use cases by domain:**

- *survey:* Satisfaction by fieldwork month.
- *ops:* Weekly order volume, or revenue by fiscal quarter.
- *science:* Daily count of observations.

**Assumptions:**

- Buckets are built from the rows the filters kept; each aggregation is then worked out once per bucket.
- Named with no period it groups by month; picked by default for a date field with no grouper type, it groups by day.
- Weeks are ISO weeks, Monday to Sunday, numbered within the ISO year.
- A fiscal offset applies to years and quarters only; a fiscal year is named for the calendar year it ends in.
- A date-and-time field is grouped by the day it falls on.
- Only buckets that hold at least one row appear; an empty band or period is left out, not shown as zero.
- A row whose field is missing lands in no bucket; it is counted as missing, not as a bucket of its own.

**Use something else:**

- [`GROUP_DATE_RANGES`](#op-group_date_ranges) when your periods are custom, such as campaign windows.
- [`OVERLAY_INDEX_VS_PRIOR`](overlay.md#op-overlay_index_vs_prior) when you want each period against the one before it.
- [`WIN_RUNNING_SUM`](window.md#op-win_running_sum) when you want a running total down the periods.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-group-date`](../skills/op-group-date.md)

<a id="op-group_date_ranges"></a>

### `GROUP_DATE_RANGES`

Labels each row with the named date range its date falls in, such as custom fiscal quarters or before and after a launch.

**Level:** basic

**Questions it answers:**

- How did sales compare before, during and after the campaign?
- What are the totals for each of our custom fiscal quarters?

**Use cases by domain:**

- *survey:* Scores per fieldwork wave, where waves have irregular dates.
- *ops:* Revenue per promotion period.

**Assumptions:**

- Buckets are built from the rows the filters kept; each aggregation is then worked out once per bucket.
- Both ends of each range are inclusive; the ranges may not overlap.
- A row outside every range goes to an unmatched group (named "unmatched" unless you rename it), not dropped.
- A date-and-time field is matched by the day it falls on.
- A row whose field is missing lands in no bucket; it is counted as missing, not as a bucket of its own.

**Use something else:**

- [`GROUP_DATE`](#op-group_date) when plain calendar months, quarters or weeks are enough.
- [`FILTER_DATE_RANGES`](filterer.md#op-filter_date_ranges) when you want to keep only the rows inside the ranges.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-group-date-ranges`](../skills/op-group-date-ranges.md)

<a id="op-group_quantile"></a>

### `GROUP_QUANTILE`

Splits the rows into equal-sized groups by rank of a number, such as quartiles or deciles of spend, to compare top and bottom.

**Level:** intermediate

**Questions it answers:**

- How do the top 25% of spenders differ from the bottom 25%?
- What is the average basket in each decile of customer value?

**Use cases by domain:**

- *survey:* Satisfaction by income quartile.
- *ops:* Return rate by decile of order value.
- *science:* Outcome by quartile of exposure.

**Assumptions:**

- Buckets are built from the rows the filters kept; each aggregation is then worked out once per bucket.
- Groups are cut by rank among the rows with a value, so each holds as close to the same count as possible; under a row weight each holds as close to the same total weight as possible, a heavy row is never split, and counts stay row counts.
- Equal values can fall in two neighbouring groups when a cut lands among them.
- Groups are named Q1 to Q4 for quartiles, D1 to D10 for deciles, P1 to P100 for percentiles and B1, B2 and so on otherwise; Q1 is the lowest.
- Groups come back in text order of their names, so deciles read D1, D10, D2, ... D9; sort them by number before reading a trend.
- It needs every row before it can cut, so it cannot stream.
- A row whose field is missing lands in no bucket; it is counted as missing, not as a bucket of its own.

**Use something else:**

- [`GROUP_RANGE`](#op-group_range) when you want bands of a fixed width, such as every 10 years of age.
- [`ATTR_PERCENTILE`](attribute.md#op-attr_percentile) when you want each row's rank as a number, not a group.
- [`AGG_PERCENTILE`](aggregator.md#op-agg_percentile) when you want the cut-off values themselves.

**Glossary:** [`percentile`](../glossary.md#term-percentile), [`rank`](../glossary.md#term-rank), [`ties`](../glossary.md#term-ties)

**Skill:** [`op-group-quantile`](../skills/op-group-quantile.md)

<a id="op-group_range"></a>

### `GROUP_RANGE`

Splits a number into bands of equal width, such as ages 20-30 and 30-40, to see how values spread or to compare bands.

**Level:** basic

**Questions it answers:**

- How many customers fall in each 10-year age band?
- What does the spread of delivery times look like in 1-hour bands?

**Use cases by domain:**

- *survey:* Respondents per age band.
- *ops:* Orders per 50-unit order-value band, as a histogram.
- *science:* Count of readings per band of temperature.

**Assumptions:**

- Buckets are built from the rows the filters kept; each aggregation is then worked out once per bucket.
- Each band is named low-high and holds values from its low edge up to, but not including, its high edge.
- Bands start at multiples of the width, counted from 0; with no width given it is 1, or 10 when picked by default for a numeric field.
- Rows come back in text order of the band names (100-150 before 50-100); sort by the low edge and add the missing bands before charting a histogram.
- Only buckets that hold at least one row appear; an empty band or period is left out, not shown as zero.
- A row whose field is missing lands in no bucket; it is counted as missing, not as a bucket of its own.

**Use something else:**

- [`GROUP_QUANTILE`](#op-group_quantile) when you want groups of equal size rather than equal width.
- [`GROUP_ROUNDED`](#op-group_rounded) when you want each band named by its lower edge alone.
- [`FILTER_RANGE`](filterer.md#op-filter_range) when you want to keep one band, not split into all of them.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-group-range`](../skills/op-group-range.md)

<a id="op-group_rounded"></a>

### `GROUP_ROUNDED`

Groups a number by rounding it down to a multiple of a step, such as 23 and 27 both to 20, naming each group by that multiple.

**Level:** basic

**Questions it answers:**

- How many orders are there at each price point, to the nearest lower 10?
- What is the count of scores in each block of 5 points?

**Use cases by domain:**

- *survey:* Respondents per 5-point block of a 0-100 score.
- *ops:* Orders per 10-unit price step, keyed by the step's start.
- *science:* Readings binned to a whole-number step.

**Assumptions:**

- Buckets are built from the rows the filters kept; each aggregation is then worked out once per bucket.
- Values are rounded down, not to the nearest: 19 with a step of 10 goes to 10, and -3 goes to -10.
- It makes the same groups as equal-width bands of the same step; only the names differ.
- Only buckets that hold at least one row appear; an empty band or period is left out, not shown as zero.
- A row whose field is missing lands in no bucket; it is counted as missing, not as a bucket of its own.

**Use something else:**

- [`GROUP_RANGE`](#op-group_range) when you want the band's both edges in its name.
- [`GROUP_QUANTILE`](#op-group_quantile) when you want groups of equal size.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-group-rounded`](../skills/op-group-rounded.md)

<a id="op-group_set_per_element"></a>

### `GROUP_SET_PER_ELEMENT`

Splits a multi-select answer into one group per option, counting each row once in every option it picked.

**Level:** intermediate

**Questions it answers:**

- How many respondents picked each brand they are aware of?
- What is the average rating among users of each feature?

**Use cases by domain:**

- *survey:* Awareness count per brand from a pick-all-that-apply question.
- *ops:* Tickets per tag, where a ticket can carry several tags.

**Assumptions:**

- Buckets are built from the rows the filters kept; each aggregation is then worked out once per bucket.
- A row that picked three options is counted in three groups, so group counts add up to more than the number of rows.
- A row that picked nothing lands in no group, and neither does a missing answer.

**Use something else:**

- [`GROUP_SET_VALUE`](#op-group_set_value) when you want each exact combination of options as one group.
- [`AGG_SET_FREQUENCY`](aggregator.md#op-agg_set_frequency) when you only want the count per option, without other figures.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-group-set-per-element`](../skills/op-group-set-per-element.md)

<a id="op-group_set_value"></a>

### `GROUP_SET_VALUE`

Groups the rows by the exact combination of options picked in a multi-select answer, such as A only, A and B, or none.

**Level:** intermediate

**Questions it answers:**

- Which combinations of payment methods do customers use, and how common is each?
- How many respondents use our app only, versus our app and a competitor's?

**Use cases by domain:**

- *survey:* Brand-repertoire groups: which sets of brands people buy together.
- *ops:* Orders per combination of add-ons.

**Assumptions:**

- Buckets are built from the rows the filters kept; each aggregation is then worked out once per bucket.
- Each row lands in exactly one group, named by its picked options joined with |.
- A row that picked nothing gets its own group with an empty name, which differs from a missing answer.
- A row whose field is missing lands in no bucket; it is counted as missing, not as a bucket of its own.

**Use something else:**

- [`GROUP_SET_PER_ELEMENT`](#op-group_set_per_element) when you want one group per option, counting a row in each it picked.
- [`FILTER_SET_EQUALS`](filterer.md#op-filter_set_equals) when you want to keep one combination rather than see them all.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-group-set-value`](../skills/op-group-set-value.md)

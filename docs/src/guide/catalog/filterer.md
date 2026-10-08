# Filterers

The filterers this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`FILTER_DATE_RANGES`](#op-filter_date_ranges) | Keeps only the rows whose date falls inside one of a set of named date ranges, such as custom fiscal quarters. | What happened during the two campaign periods only? | basic | [`GROUP_DATE_RANGES`](grouper.md#op-group_date_ranges) when you want each row labelled with its range instead of dropping the rest.<br>[`GROUP_DATE`](grouper.md#op-group_date) when you want plain calendar periods such as months.<br>[`FILTER_RANGE`](#op-filter_range) when the field is a number rather than a date. |
| [`FILTER_EXCLUDE`](#op-filter_exclude) | Drops the rows whose field holds one of the listed values and keeps the rest, such as removing test accounts. | What are the totals once internal test accounts are taken out? | basic | [`FILTER_INCLUDE`](#op-filter_include) when you want to keep only the listed values.<br>[`FILTER_NULL`](#op-filter_null) when you want to drop the rows where the field is missing.<br>[`FILTER_SET_CONTAINS_NONE`](#op-filter_set_contains_none) when the field is a multi-select answer. |
| [`FILTER_EXPRESSION`](#op-filter_expression) | Keeps the rows for which a written yes-or-no condition is true, for rules the other filters cannot state, such as two fields together. | Which customers spent over 500 and joined this year? | intermediate | [`FILTER_INCLUDE`](#op-filter_include) when you only need to keep a list of values.<br>[`FILTER_RANGE`](#op-filter_range) when you only need a low and a high limit.<br>[`FILTER_NULL`](#op-filter_null) when you only need rows where a field is or is not missing. |
| [`FILTER_FALSE`](#op-filter_false) | Keeps only the rows where a yes/no field is no, such as lapsed accounts or unfinished surveys. | What do the customers who did not renew have in common? | basic | [`FILTER_TRUE`](#op-filter_true) when you want the rows where the field is yes.<br>[`FILTER_NULL`](#op-filter_null) when you want only the rows where the field is missing. |
| [`FILTER_INCLUDE`](#op-filter_include) | Keeps only the rows whose field holds one of the listed values, such as two regions or three product codes. | What do the figures look like for the North and South regions only? | basic | [`FILTER_EXCLUDE`](#op-filter_exclude) when you want to drop the listed values and keep everything else.<br>[`FILTER_RANGE`](#op-filter_range) when the field is numeric and you want everything between two limits.<br>[`FILTER_SET_CONTAINS_ANY`](#op-filter_set_contains_any) when the field is a multi-select answer.<br>[`AGG_FREQUENCY`](aggregator.md#op-agg_frequency) when you only want how many rows hold one value, not the rows themselves. |
| [`FILTER_NULL`](#op-filter_null) | Keeps either the rows where a field is missing or the rows where it is present, to find gaps or set them aside. | Who skipped the income question, so their other answers can be compared with those who answered? | basic | [`AGG_NULL_COUNT`](aggregator.md#op-agg_null_count) when you only want to count the missing values, not filter on them.<br>[`FILTER_EXCLUDE`](#op-filter_exclude) when you want to drop particular values, not missing ones. |
| [`FILTER_RANGE`](#op-filter_range) | Keeps only the rows whose number lies between a low and a high limit, both limits included. | What do adults aged 18 to 34 say? | basic | [`FILTER_EXPRESSION`](#op-filter_expression) when you need an open limit, such as strictly above a value.<br>[`FILTER_DATE_RANGES`](#op-filter_date_ranges) when the field is a date and you want named periods.<br>[`GROUP_RANGE`](grouper.md#op-group_range) when you want to split the values into bands rather than keep one. |
| [`FILTER_SET_CONTAINS_ALL`](#op-filter_set_contains_all) | Keeps the rows whose multi-select answer includes every listed option, other options allowed, such as people who use both A and B. | How do people who use both our app and our website rate us? | basic | [`FILTER_SET_CONTAINS_ANY`](#op-filter_set_contains_any) when one of the listed options is enough.<br>[`FILTER_SET_EQUALS`](#op-filter_set_equals) when the row must hold exactly the listed options and nothing else. |
| [`FILTER_SET_CONTAINS_ANY`](#op-filter_set_contains_any) | Keeps the rows whose multi-select answer includes at least one of the listed options, such as anyone who uses brand A or B. | What do people who use either of our two apps think of the service? | basic | [`FILTER_SET_CONTAINS_ALL`](#op-filter_set_contains_all) when the row must include every listed option.<br>[`FILTER_SET_CONTAINS_NONE`](#op-filter_set_contains_none) when the row must include none of the listed options.<br>[`FILTER_INCLUDE`](#op-filter_include) when the field holds one value per row, not several. |
| [`FILTER_SET_CONTAINS_NONE`](#op-filter_set_contains_none) | Keeps the rows whose multi-select answer includes none of the listed options, such as people who use neither A nor B. | What do people who use none of the competitor apps think? | basic | [`FILTER_SET_CONTAINS_ANY`](#op-filter_set_contains_any) when you want rows that include one of the options.<br>[`FILTER_EXCLUDE`](#op-filter_exclude) when the field holds one value per row, not several. |
| [`FILTER_SET_EQUALS`](#op-filter_set_equals) | Keeps the rows whose multi-select answer is exactly the listed options, no more and no fewer, such as people who use only A. | How many customers use our app and nothing else? | basic | [`FILTER_SET_CONTAINS_ALL`](#op-filter_set_contains_all) when other options are allowed alongside the listed ones.<br>[`GROUP_SET_VALUE`](grouper.md#op-group_set_value) when you want to see every combination with its count. |
| [`FILTER_TRUE`](#op-filter_true) | Keeps only the rows where a yes/no field is yes, such as active accounts or completed surveys. | What do only the completed interviews say? | basic | [`FILTER_FALSE`](#op-filter_false) when you want the rows where the field is no.<br>[`FILTER_EXPRESSION`](#op-filter_expression) when the condition involves more than one field.<br>[`GROUP_CATEGORY`](grouper.md#op-group_category) when you want yes and no side by side as groups, not one of them. |

## Operators

<a id="op-filter_date_ranges"></a>

### `FILTER_DATE_RANGES`

Keeps only the rows whose date falls inside one of a set of named date ranges, such as custom fiscal quarters.

**Level:** basic

**Questions it answers:**

- What happened during the two campaign periods only?
- What are the figures for the first half of our fiscal year?

**Use cases by domain:**

- *survey:* Keep interviews from the two fieldwork windows of this wave.
- *ops:* Keep orders placed during the holiday trading periods.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- Both ends of each range are inclusive; a range with no start or no end is open on that side.
- The ranges may not overlap; overlapping or duplicate ranges are refused, not merged.
- A date-and-time field is matched by the day it falls on.
- A row whose field is missing is dropped.

**Use something else:**

- [`GROUP_DATE_RANGES`](grouper.md#op-group_date_ranges) when you want each row labelled with its range instead of dropping the rest.
- [`GROUP_DATE`](grouper.md#op-group_date) when you want plain calendar periods such as months.
- [`FILTER_RANGE`](#op-filter_range) when the field is a number rather than a date.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-date-ranges`](../skills/op-filter-date-ranges.md)

<a id="op-filter_exclude"></a>

### `FILTER_EXCLUDE`

Drops the rows whose field holds one of the listed values and keeps the rest, such as removing test accounts.

**Level:** basic

**Questions it answers:**

- What are the totals once internal test accounts are taken out?
- How do the results change without the store that was closed for refit?

**Use cases by domain:**

- *survey:* Drop the "Don't know" answers before working out shares.
- *ops:* Drop orders flagged as internal or test.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- Several filters in one request all have to keep a row for it to stay: they combine as AND, never OR.
- A row whose field is missing is kept, since it does not hold a listed value.
- On a category field each listed value must be a known label, and an unknown label is an error. On a number or date field the values are read as numbers, and one that no row holds simply matches nothing.

**Use something else:**

- [`FILTER_INCLUDE`](#op-filter_include) when you want to keep only the listed values.
- [`FILTER_NULL`](#op-filter_null) when you want to drop the rows where the field is missing.
- [`FILTER_SET_CONTAINS_NONE`](#op-filter_set_contains_none) when the field is a multi-select answer.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-exclude`](../skills/op-filter-exclude.md)

<a id="op-filter_expression"></a>

### `FILTER_EXPRESSION`

Keeps the rows for which a written yes-or-no condition is true, for rules the other filters cannot state, such as two fields together.

**Level:** intermediate

**Questions it answers:**

- Which customers spent over 500 and joined this year?
- Which records have an end date before their start date?

**Use cases by domain:**

- *survey:* Keep respondents who rated the brand 9 or 10 and bought in the last month.
- *ops:* Keep orders where the shipped quantity is below the ordered quantity.
- *harness:* Express a user's free-form condition as one filter.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- The condition must give true or false for every row; any other result is an error.
- When a missing value makes the condition unknown, such as a missing number compared with 5, the row is dropped.
- A missing value compared with == is false and with != is true; write x ?? 0 to fill it first.

**Use something else:**

- [`FILTER_INCLUDE`](#op-filter_include) when you only need to keep a list of values.
- [`FILTER_RANGE`](#op-filter_range) when you only need a low and a high limit.
- [`FILTER_NULL`](#op-filter_null) when you only need rows where a field is or is not missing.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-expression`](../skills/op-filter-expression.md)

<a id="op-filter_false"></a>

### `FILTER_FALSE`

Keeps only the rows where a yes/no field is no, such as lapsed accounts or unfinished surveys.

**Level:** basic

**Questions it answers:**

- What do the customers who did not renew have in common?
- How many interviews were left unfinished, by region?

**Use cases by domain:**

- *survey:* Keep respondents who did not complete the survey.
- *ops:* Keep orders not yet shipped.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- By default the field must be a yes/no field; a truthy option treats 0, an empty text and a missing value as no. On a category field it tests the label text, so a label such as "No" is not no; use FILTER_INCLUDE on the no label instead.
- A missing value is dropped by default but kept under the truthy option, where it counts as no.

**Use something else:**

- [`FILTER_TRUE`](#op-filter_true) when you want the rows where the field is yes.
- [`FILTER_NULL`](#op-filter_null) when you want only the rows where the field is missing.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-false`](../skills/op-filter-false.md)

<a id="op-filter_include"></a>

### `FILTER_INCLUDE`

Keeps only the rows whose field holds one of the listed values, such as two regions or three product codes.

**Level:** basic

**Questions it answers:**

- What do the figures look like for the North and South regions only?
- How did customers on the two premium plans answer?

**Use cases by domain:**

- *survey:* Keep only respondents from the three markets in this wave's report.
- *ops:* Keep only orders from the stores in one district.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- Several filters in one request all have to keep a row for it to stay: they combine as AND, never OR.
- A row whose field is missing is dropped.
- On a category field each listed value must be a known label, and an unknown label is an error. On a number or date field the values are read as numbers, and one that no row holds simply matches nothing.

**Use something else:**

- [`FILTER_EXCLUDE`](#op-filter_exclude) when you want to drop the listed values and keep everything else.
- [`FILTER_RANGE`](#op-filter_range) when the field is numeric and you want everything between two limits.
- [`FILTER_SET_CONTAINS_ANY`](#op-filter_set_contains_any) when the field is a multi-select answer.
- [`AGG_FREQUENCY`](aggregator.md#op-agg_frequency) when you only want how many rows hold one value, not the rows themselves.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-include`](../skills/op-filter-include.md)

<a id="op-filter_null"></a>

### `FILTER_NULL`

Keeps either the rows where a field is missing or the rows where it is present, to find gaps or set them aside.

**Level:** basic

**Questions it answers:**

- Who skipped the income question, so their other answers can be compared with those who answered?
- What are the averages over only the rows that have a value?

**Use cases by domain:**

- *survey:* Look at who skipped a question to see whether they differ from those who answered.
- *ops:* Find orders with no delivery date recorded.
- *science:* Set aside samples with no measurement before comparing groups.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- Missing means no recorded value; a 0, an empty text or an empty multi-select answer is a value, not missing.
- Dropping rows with gaps can bias the result when the gaps are not random.

**Use something else:**

- [`AGG_NULL_COUNT`](aggregator.md#op-agg_null_count) when you only want to count the missing values, not filter on them.
- [`FILTER_EXCLUDE`](#op-filter_exclude) when you want to drop particular values, not missing ones.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-filter-null`](../skills/op-filter-null.md)

<a id="op-filter_range"></a>

### `FILTER_RANGE`

Keeps only the rows whose number lies between a low and a high limit, both limits included.

**Level:** basic

**Questions it answers:**

- What do adults aged 18 to 34 say?
- What is the average order once impossible values above 10,000 are left out?

**Use cases by domain:**

- *survey:* Keep respondents aged 18 to 34.
- *ops:* Keep delivery times between 0 and 72 hours, dropping clearly wrong entries.
- *science:* Keep readings inside the instrument's working range.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- Several filters in one request all have to keep a row for it to stay: they combine as AND, never OR.
- Both limits are inclusive: a value equal to the low or the high limit is kept.
- On a date field the limits are day numbers counted from 1 January 1970, not written dates.
- A row whose field is missing is dropped.

**Use something else:**

- [`FILTER_EXPRESSION`](#op-filter_expression) when you need an open limit, such as strictly above a value.
- [`FILTER_DATE_RANGES`](#op-filter_date_ranges) when the field is a date and you want named periods.
- [`GROUP_RANGE`](grouper.md#op-group_range) when you want to split the values into bands rather than keep one.

**Glossary:** [`outlier`](../glossary.md#term-outlier), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-range`](../skills/op-filter-range.md)

<a id="op-filter_set_contains_all"></a>

### `FILTER_SET_CONTAINS_ALL`

Keeps the rows whose multi-select answer includes every listed option, other options allowed, such as people who use both A and B.

**Level:** basic

**Questions it answers:**

- How do people who use both our app and our website rate us?
- Which tickets carry both the urgent and the billing tags?

**Use cases by domain:**

- *survey:* Keep respondents who bought both brands in the last month.
- *ops:* Keep orders that included both a phone and a case.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- Each listed value must be one of the field's known labels; an unknown label is an error, not an empty match.
- An empty list of options keeps every row that has an answer.
- A row whose field is missing is dropped.

**Use something else:**

- [`FILTER_SET_CONTAINS_ANY`](#op-filter_set_contains_any) when one of the listed options is enough.
- [`FILTER_SET_EQUALS`](#op-filter_set_equals) when the row must hold exactly the listed options and nothing else.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-set-contains-all`](../skills/op-filter-set-contains-all.md)

<a id="op-filter_set_contains_any"></a>

### `FILTER_SET_CONTAINS_ANY`

Keeps the rows whose multi-select answer includes at least one of the listed options, such as anyone who uses brand A or B.

**Level:** basic

**Questions it answers:**

- What do people who use either of our two apps think of the service?
- Which customers paid by any card at least once?

**Use cases by domain:**

- *survey:* Keep respondents aware of at least one of the two new brands.
- *ops:* Keep tickets tagged with any of the billing tags.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- Each listed value must be one of the field's known labels; an unknown label is an error, not an empty match.
- An empty list of options keeps no rows.
- A row whose field is missing is dropped.

**Use something else:**

- [`FILTER_SET_CONTAINS_ALL`](#op-filter_set_contains_all) when the row must include every listed option.
- [`FILTER_SET_CONTAINS_NONE`](#op-filter_set_contains_none) when the row must include none of the listed options.
- [`FILTER_INCLUDE`](#op-filter_include) when the field holds one value per row, not several.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-set-contains-any`](../skills/op-filter-set-contains-any.md)

<a id="op-filter_set_contains_none"></a>

### `FILTER_SET_CONTAINS_NONE`

Keeps the rows whose multi-select answer includes none of the listed options, such as people who use neither A nor B.

**Level:** basic

**Questions it answers:**

- What do people who use none of the competitor apps think?
- Which orders had no discount code applied?

**Use cases by domain:**

- *survey:* Keep respondents unaware of every competitor brand.
- *ops:* Keep tickets with none of the escalation tags.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- Each listed value must be one of the field's known labels; an unknown label is an error, not an empty match.
- A row whose field is missing is kept, since it does not hold a listed value.

**Use something else:**

- [`FILTER_SET_CONTAINS_ANY`](#op-filter_set_contains_any) when you want rows that include one of the options.
- [`FILTER_EXCLUDE`](#op-filter_exclude) when the field holds one value per row, not several.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-set-contains-none`](../skills/op-filter-set-contains-none.md)

<a id="op-filter_set_equals"></a>

### `FILTER_SET_EQUALS`

Keeps the rows whose multi-select answer is exactly the listed options, no more and no fewer, such as people who use only A.

**Level:** basic

**Questions it answers:**

- How many customers use our app and nothing else?
- Which respondents picked exactly these two reasons?

**Use cases by domain:**

- *survey:* Keep the respondents loyal to one brand only.
- *ops:* Keep orders paid with exactly one card and no other method.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- Each listed value must be one of the field's known labels; an unknown label is an error, not an empty match.
- An empty list keeps the rows that picked nothing, which differs from a missing answer.
- A row whose field is missing is dropped.

**Use something else:**

- [`FILTER_SET_CONTAINS_ALL`](#op-filter_set_contains_all) when other options are allowed alongside the listed ones.
- [`GROUP_SET_VALUE`](grouper.md#op-group_set_value) when you want to see every combination with its count.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-set-equals`](../skills/op-filter-set-equals.md)

<a id="op-filter_true"></a>

### `FILTER_TRUE`

Keeps only the rows where a yes/no field is yes, such as active accounts or completed surveys.

**Level:** basic

**Questions it answers:**

- What do only the completed interviews say?
- What are the figures for active subscribers?

**Use cases by domain:**

- *survey:* Keep respondents who passed the attention check.
- *ops:* Keep orders flagged as delivered.

**Assumptions:**

- Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps.
- By default the field must be a yes/no field; a truthy option treats any non-zero number and any non-empty text as yes. On a category field it tests the label text, so a label such as "No" counts as yes; filter a Yes/No category with FILTER_INCLUDE on the yes label instead.
- A row whose field is missing is dropped.

**Use something else:**

- [`FILTER_FALSE`](#op-filter_false) when you want the rows where the field is no.
- [`FILTER_EXPRESSION`](#op-filter_expression) when the condition involves more than one field.
- [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want yes and no side by side as groups, not one of them.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-filter-true`](../skills/op-filter-true.md)

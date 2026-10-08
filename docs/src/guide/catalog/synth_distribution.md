# Synth distributions

The synth distributions this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`bernoulli`](#op-bernoulli) | Draws a yes/no value per row: 1 with a chosen chance p and 0 otherwise, such as whether a customer churned. | How do I mark 12% of synthetic customers as churned? | basic | [`weighted_categorical`](#op-weighted_categorical) when there are more than two outcomes.<br>[`set_bernoulli`](#op-set_bernoulli) when each row can tick several options.<br>[`constant`](#op-constant) when every row should get the same value. |
| [`constant`](#op-constant) | Puts the same value on every row, for a placeholder field or a fixed flag in a test fixture. | How do I fill a column with the same country code? | basic | [`monotonic_from`](#op-monotonic_from) when each row needs a distinct ID.<br>[`weighted_categorical`](#op-weighted_categorical) when the value should vary over a few labels.<br>[`bernoulli`](#op-bernoulli) when a flag should be set on only some rows. |
| [`discrete`](#op-discrete) | Draws whole-number levels at set shares, such as a 1 to 7 rating scale, so each level appears about as often as declared. | How do I generate answers on a 1 to 5 scale where most people choose 4? | intermediate | [`normal`](#op-normal) when the field is a continuous measure.<br>[`weighted_categorical`](#op-weighted_categorical) when the levels are text labels.<br>[`poisson`](#op-poisson) when the counts follow an average rate rather than fixed shares. |
| [`exponential`](#op-exponential) | Draws positive waiting times where short waits are common and long ones rare, set by how often events happen. | How do I simulate the time between customer arrivals? | intermediate | [`poisson`](#op-poisson) when you want whole-number counts of events per period.<br>[`pareto`](#op-pareto) when a few values should be extremely large.<br>[`lognormal`](#op-lognormal) when values bunch around a typical size rather than near zero. |
| [`lognormal`](#op-lognormal) | Draws positive numbers with a long right tail, like incomes or order values: most are modest and a few are very large. | How do I simulate skewed order values? | intermediate | [`normal`](#op-normal) when values are spread evenly around a centre.<br>[`pareto`](#op-pareto) when the tail should follow a power law, as with wealth or city sizes.<br>[`exponential`](#op-exponential) when the values are waiting times between events. |
| [`mixture`](#op-mixture) | Draws from two or more bell curves blended by weight, for data with several peaks, like weekday and weekend order sizes. | How do I simulate a column with two separate peaks? | advanced | [`normal`](#op-normal) when one peak is enough.<br>[`lognormal`](#op-lognormal) when there is one peak with a long right tail.<br>[`discrete`](#op-discrete) when the field is a coded scale with a few fixed levels. |
| [`monotonic_from`](#op-monotonic_from) | Counts up (or down) by a fixed step from a start value, one step per row, to make unique IDs or row numbers. | How do I give each synthetic row a unique customer ID? | basic | [`constant`](#op-constant) when every row should get the same value.<br>[`uniform`](#op-uniform) when the numbers should be random, not in order.<br>[`regex`](#op-regex) when IDs should be text codes like AB-1234. |
| [`normal`](#op-normal) | Draws numbers from a bell curve around a chosen centre and spread, optionally clipped to a low and a high bound. | How do I generate realistic test scores that cluster around 70? | basic | [`lognormal`](#op-lognormal) when values are always positive with a long right tail, like income.<br>[`mixture`](#op-mixture) when the data has two or more peaks.<br>[`discrete`](#op-discrete) when the field is a coded scale with a few levels, like 1 to 7. |
| [`pareto`](#op-pareto) | Draws heavy-tailed values above a minimum, where a small share of rows holds most of the total, like wealth or file sizes. | How do I simulate customers where the top fifth spend most of the money? | advanced | [`lognormal`](#op-lognormal) when the tail is long but values have a typical size.<br>[`exponential`](#op-exponential) when the values are waiting times between events.<br>[`uniform`](#op-uniform) when every value in a range should be equally likely. |
| [`poisson`](#op-poisson) | Draws whole-number counts of events per period, such as visits per day, around a chosen average. | How many orders might a store get per hour? | intermediate | [`exponential`](#op-exponential) when you want the time between events, not how many.<br>[`bernoulli`](#op-bernoulli) when each row is a yes/no outcome.<br>[`discrete`](#op-discrete) when you know the exact share of each count. |
| [`regex`](#op-regex) | Generates text matching a pattern, such as order codes like AB-1234, for realistic-looking IDs and labels. | How do I generate postcodes that look real? | intermediate | [`weighted_categorical`](#op-weighted_categorical) when the values come from a short known list.<br>[`monotonic_from`](#op-monotonic_from) when IDs must be unique.<br>[`constant`](#op-constant) when every row should carry the same text. |
| [`set_bernoulli`](#op-set_bernoulli) | Fills a multi-select answer: each option is ticked on its own at its own rate, such as 40% picking email and 25% SMS. | How do I simulate a tick-all-that-apply question? | intermediate | [`weighted_categorical`](#op-weighted_categorical) when each row picks exactly one answer.<br>[`bernoulli`](#op-bernoulli) when there is only one yes/no flag. |
| [`uniform`](#op-uniform) | Draws numbers between a low and a high bound, every value equally likely, for flat filler columns or random noise. | How do I fill a test column with random numbers between 0 and 100? | basic | [`normal`](#op-normal) when values should cluster around a typical value.<br>[`uniform_date`](#op-uniform_date) when the field is a calendar date.<br>[`discrete`](#op-discrete) when the field is a coded scale with a few fixed levels. |
| [`uniform_date`](#op-uniform_date) | Draws calendar dates between a start and an end date, every day equally likely, both ends included. | How do I spread synthetic orders across 2024? | basic | [`uniform`](#op-uniform) when the field is a plain number, not a date.<br>[`constant`](#op-constant) when every row shares one date. |
| [`weighted_categorical`](#op-weighted_categorical) | Picks one label per row from a list, each at its own share, such as 50% North, 30% South and 20% West. | How do I give synthetic respondents a realistic region mix? | basic | [`bernoulli`](#op-bernoulli) when there are just two outcomes stored as 1 and 0 or true and false.<br>[`set_bernoulli`](#op-set_bernoulli) when each row can pick several options.<br>[`discrete`](#op-discrete) when the levels are numbers on a scale. |

## Operators

<a id="op-bernoulli"></a>

### `bernoulli`

Draws a yes/no value per row: 1 with a chosen chance p and 0 otherwise, such as whether a customer churned.

**Level:** basic

**Questions it answers:**

- How do I mark 12% of synthetic customers as churned?
- How do I generate a random true/false flag?

**Use cases by domain:**

- *survey:* Whether each respondent is a current customer, at the source's 35% share.
- *ops:* A late-delivery flag on about 8% of orders.

**Assumptions:**

- p is between 0 and 1; on a true/false field 1 is true, on a number field it is the number 1.
- The observed share wanders from p in a small cohort and settles as rows grow.
- A synthetic copy rebuilds every true/false field this way from its observed share; a model on it shifts which rows get 1 (a probit), not the share.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`weighted_categorical`](#op-weighted_categorical) when there are more than two outcomes.
- [`set_bernoulli`](#op-set_bernoulli) when each row can tick several options.
- [`constant`](#op-constant) when every row should get the same value.

**Glossary:** [`probit`](../glossary.md#term-probit)

**Skill:** [`op-synth-bernoulli`](../skills/op-synth-bernoulli.md)

<a id="op-constant"></a>

### `constant`

Puts the same value on every row, for a placeholder field or a fixed flag in a test fixture.

**Level:** basic

**Questions it answers:**

- How do I fill a column with the same country code?
- How do I add a fixed version field to test data?

**Use cases by domain:**

- *ops:* A single-site extract where every row carries the same store code.
- *harness:* A schema-version field that is always 3.

**Assumptions:**

- The value is checked against the field type when the spec is read: text on a number field is refused, and a multi-select field takes a list of option names.
- It uses no randomness, so adding or removing this field leaves every other field's draws unchanged.

**Use something else:**

- [`monotonic_from`](#op-monotonic_from) when each row needs a distinct ID.
- [`weighted_categorical`](#op-weighted_categorical) when the value should vary over a few labels.
- [`bernoulli`](#op-bernoulli) when a flag should be set on only some rows.

**Skill:** [`op-synth-constant`](../skills/op-synth-constant.md)

<a id="op-discrete"></a>

### `discrete`

Draws whole-number levels at set shares, such as a 1 to 7 rating scale, so each level appears about as often as declared.

**Level:** intermediate

**Questions it answers:**

- How do I generate answers on a 1 to 5 scale where most people choose 4?
- How do I reproduce the exact spread of a rating question?

**Use cases by domain:**

- *survey:* A 0 to 10 likelihood-to-recommend item with the source's real share at each point.
- *ops:* Items per order, where 1, 2 and 3 cover nearly every order.

**Assumptions:**

- values are listed in strictly ascending order with no repeats; weights may be raw counts and default to equal.
- A synthetic copy rebuilds every whole-number field with up to 64 distinct levels this way; above 64 it falls back to a clipped bell curve.
- The field is drawn on its own unless the spec adds correlations, models or rules that tie it to other fields.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`normal`](#op-normal) when the field is a continuous measure.
- [`weighted_categorical`](#op-weighted_categorical) when the levels are text labels.
- [`poisson`](#op-poisson) when the counts follow an average rate rather than fixed shares.

**Skill:** [`op-synth-discrete`](../skills/op-synth-discrete.md)

<a id="op-exponential"></a>

### `exponential`

Draws positive waiting times where short waits are common and long ones rare, set by how often events happen.

**Level:** intermediate

**Questions it answers:**

- How do I simulate the time between customer arrivals?
- How do I generate realistic call durations for a load test?

**Use cases by domain:**

- *ops:* Minutes between support tickets at one every 4 minutes on average (lambda 0.25).
- *science:* Time to failure of parts that fail at a steady rate.

**Assumptions:**

- lambda is a rate, not the average: the mean is 1/lambda, so lambda 0.5 gives an average of 2.
- There is no bound: a rare extreme value can appear, so cap it with a constraint when the field must stay in range.
- On a u8 to u64 field each draw is rounded to the nearest whole number; a negative draw is stored as 0 and one past the type's top stops there.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`poisson`](#op-poisson) when you want whole-number counts of events per period.
- [`pareto`](#op-pareto) when a few values should be extremely large.
- [`lognormal`](#op-lognormal) when values bunch around a typical size rather than near zero.

**Glossary:** [`mean`](../glossary.md#term-mean)

**Skill:** [`op-synth-exponential`](../skills/op-synth-exponential.md)

<a id="op-lognormal"></a>

### `lognormal`

Draws positive numbers with a long right tail, like incomes or order values: most are modest and a few are very large.

**Level:** intermediate

**Questions it answers:**

- How do I simulate skewed order values?
- How do I generate realistic household incomes?

**Use cases by domain:**

- *ops:* Order values with a typical basket near 40 and occasional orders in the thousands.
- *science:* Particle sizes or concentrations that vary by multiples rather than by fixed amounts.

**Assumptions:**

- mu and sigma describe the LOG of the value, not the value: the median value is e^mu and the average is e^(mu + sigma²/2).
- A larger sigma gives a longer tail; sigma must be above 0.
- There is no bound: a rare extreme value can appear, so cap it with a constraint when the field must stay in range.
- On a u8 to u64 field each draw is rounded to the nearest whole number; a negative draw is stored as 0 and one past the type's top stops there.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`normal`](#op-normal) when values are spread evenly around a centre.
- [`pareto`](#op-pareto) when the tail should follow a power law, as with wealth or city sizes.
- [`exponential`](#op-exponential) when the values are waiting times between events.

**Glossary:** [`skew`](../glossary.md#term-skew), [`median`](../glossary.md#term-median)

**Skill:** [`op-synth-lognormal`](../skills/op-synth-lognormal.md)

<a id="op-mixture"></a>

### `mixture`

Draws from two or more bell curves blended by weight, for data with several peaks, like weekday and weekend order sizes.

**Level:** advanced

**Questions it answers:**

- How do I simulate a column with two separate peaks?
- How do I generate order sizes where small and bulk orders form distinct groups?

**Use cases by domain:**

- *ops:* Basket values from a mix of small top-up orders and large weekly shops.
- *science:* Readings from two instruments with different calibrations pooled into one column.

**Assumptions:**

- Each row first picks a component by weight, then draws from that component's bell curve; without weights each component is equally likely.
- means and stds are listed per component and must be the same length, at least two.
- A synthetic copy fits two components automatically when they describe the data clearly better than one; three or more are written by hand.
- There is no bound: a rare extreme value can appear, so cap it with a constraint when the field must stay in range.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`normal`](#op-normal) when one peak is enough.
- [`lognormal`](#op-lognormal) when there is one peak with a long right tail.
- [`discrete`](#op-discrete) when the field is a coded scale with a few fixed levels.

**Glossary:** [`normal-distribution`](../glossary.md#term-normal-distribution)

**Skill:** [`op-synth-mixture`](../skills/op-synth-mixture.md)

<a id="op-monotonic_from"></a>

### `monotonic_from`

Counts up (or down) by a fixed step from a start value, one step per row, to make unique IDs or row numbers.

**Level:** basic

**Questions it answers:**

- How do I give each synthetic row a unique customer ID?
- How do I number rows from 1000 upward?

**Use cases by domain:**

- *ops:* Sequential invoice numbers starting at 50,000.
- *harness:* A primary-key column for a fixture that a lookup index is built on.

**Assumptions:**

- The first row gets start, the next start plus step, and so on; step must not be 0, and a negative step counts down.
- A row a constraint rejects still uses up a number, so the IDs can have gaps.
- Pick a field wide enough: past the type's top value every row repeats the top, so the IDs stop being unique.
- It uses no randomness, so adding or removing this field leaves every other field's draws unchanged.

**Use something else:**

- [`constant`](#op-constant) when every row should get the same value.
- [`uniform`](#op-uniform) when the numbers should be random, not in order.
- [`regex`](#op-regex) when IDs should be text codes like AB-1234.

**Skill:** [`op-synth-monotonic-from`](../skills/op-synth-monotonic-from.md)

<a id="op-normal"></a>

### `normal`

Draws numbers from a bell curve around a chosen centre and spread, optionally clipped to a low and a high bound.

**Level:** basic

**Questions it answers:**

- How do I generate realistic test scores that cluster around 70?
- How do I make a numeric column with a known average and spread?

**Use cases by domain:**

- *survey:* Synthetic satisfaction scores averaging 7 with a standard deviation of 1.5.
- *science:* Simulated measurement error around a true reading.

**Assumptions:**

- mean sets the centre and std the spread; std must be above 0.
- Bounds clip rather than redraw: a draw past a bound is set to that bound, so tight bounds pile values up at the edges.
- On a u8 to u64 field each draw is rounded to the nearest whole number; a negative draw is stored as 0 and one past the type's top stops there.
- The field is drawn on its own unless the spec adds correlations, models or rules that tie it to other fields.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`lognormal`](#op-lognormal) when values are always positive with a long right tail, like income.
- [`mixture`](#op-mixture) when the data has two or more peaks.
- [`discrete`](#op-discrete) when the field is a coded scale with a few levels, like 1 to 7.

**Glossary:** [`normal-distribution`](../glossary.md#term-normal-distribution), [`standard-deviation`](../glossary.md#term-standard-deviation)

**Skill:** [`op-synth-normal`](../skills/op-synth-normal.md)

<a id="op-pareto"></a>

### `pareto`

Draws heavy-tailed values above a minimum, where a small share of rows holds most of the total, like wealth or file sizes.

**Level:** advanced

**Questions it answers:**

- How do I simulate customers where the top fifth spend most of the money?
- How do I generate file sizes with a few huge files?

**Use cases by domain:**

- *ops:* Account balances with xm 10,000 and alpha 1.16, close to an 80/20 split.
- *science:* Event sizes that follow a power law, such as city populations.

**Assumptions:**

- xm is the smallest value drawn; alpha sets the tail, and a smaller alpha means a heavier tail. Both must be above 0.
- With alpha at or below 1 the mean is infinite and averages over the generated rows never settle. Between 1 and 2 the mean exists but the variance is infinite, so averages settle only slowly and jump when a huge value lands.
- There is no bound: a rare extreme value can appear, so cap it with a constraint when the field must stay in range.
- On a u8 to u64 field each draw is rounded to the nearest whole number; a negative draw is stored as 0 and one past the type's top stops there.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`lognormal`](#op-lognormal) when the tail is long but values have a typical size.
- [`exponential`](#op-exponential) when the values are waiting times between events.
- [`uniform`](#op-uniform) when every value in a range should be equally likely.

**Glossary:** [`mean`](../glossary.md#term-mean), [`variance`](../glossary.md#term-variance)

**Skill:** [`op-synth-pareto`](../skills/op-synth-pareto.md)

<a id="op-poisson"></a>

### `poisson`

Draws whole-number counts of events per period, such as visits per day, around a chosen average.

**Level:** intermediate

**Questions it answers:**

- How many orders might a store get per hour?
- How do I simulate defect counts per batch?

**Use cases by domain:**

- *ops:* Support calls per hour at an average of 6.
- *science:* Particle counts per time window from a steady source.

**Assumptions:**

- lambda is both the average count and its variance; real counts often vary more than that (overdispersion), which this cannot reproduce.
- From an average of 30 up, the draw is a rounded bell-curve approximation instead of the exact method.
- The field is drawn on its own unless the spec adds correlations, models or rules that tie it to other fields.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`exponential`](#op-exponential) when you want the time between events, not how many.
- [`bernoulli`](#op-bernoulli) when each row is a yes/no outcome.
- [`discrete`](#op-discrete) when you know the exact share of each count.

**Glossary:** [`variance`](../glossary.md#term-variance), [`overdispersion`](../glossary.md#term-overdispersion)

**Skill:** [`op-synth-poisson`](../skills/op-synth-poisson.md)

<a id="op-regex"></a>

### `regex`

Generates text matching a pattern, such as order codes like AB-1234, for realistic-looking IDs and labels.

**Level:** intermediate

**Questions it answers:**

- How do I generate postcodes that look real?
- How do I fill a SKU column with codes like SKU-00042?

**Use cases by domain:**

- *ops:* Masked account codes that keep the real shape but none of the real values.
- *harness:* Order references shaped like the production format, for a parser test.

**Assumptions:**

- Open-ended repeats such as * and + are capped at max_repeat copies (default 8); back-references are not supported.
- Generated strings are not guaranteed unique, and each distinct one becomes a dictionary entry, so a wide pattern on many rows can overflow a narrow categorical field.
- The field is drawn on its own unless the spec adds correlations, models or rules that tie it to other fields.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`weighted_categorical`](#op-weighted_categorical) when the values come from a short known list.
- [`monotonic_from`](#op-monotonic_from) when IDs must be unique.
- [`constant`](#op-constant) when every row should carry the same text.

**Skill:** [`op-synth-regex`](../skills/op-synth-regex.md)

<a id="op-set_bernoulli"></a>

### `set_bernoulli`

Fills a multi-select answer: each option is ticked on its own at its own rate, such as 40% picking email and 25% SMS.

**Level:** intermediate

**Questions it answers:**

- How do I simulate a tick-all-that-apply question?
- How do I generate which channels each customer has opted into?

**Use cases by domain:**

- *survey:* Brands each respondent has bought in the last year.
- *ops:* Features enabled on each account.

**Assumptions:**

- options are listed in bit order and become the field's dictionary; frequencies default to 0.5 each.
- Options are drawn independently unless the spec pairs this field with another, in which case a paired option follows the captured joint pattern.
- A row with no option ticked is an empty answer, not a missing one.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`weighted_categorical`](#op-weighted_categorical) when each row picks exactly one answer.
- [`bernoulli`](#op-bernoulli) when there is only one yes/no flag.

**Skill:** [`op-synth-set-bernoulli`](../skills/op-synth-set-bernoulli.md)

<a id="op-uniform"></a>

### `uniform`

Draws numbers between a low and a high bound, every value equally likely, for flat filler columns or random noise.

**Level:** basic

**Questions it answers:**

- How do I fill a test column with random numbers between 0 and 100?
- How do I add flat random noise to a synthetic cohort?

**Use cases by domain:**

- *ops:* Random handling times between 1 and 5 minutes for a load test.
- *harness:* A placeholder score column in a test fixture.

**Assumptions:**

- Before rounding, the low bound can be drawn and the high bound never is, so min 0 and max 1 gives values from 0 up to just under 1.
- On a u8 to u64 field the draws are rounded, so the high bound can appear and the two end values come out about half as often as the rest; for equally likely whole numbers use discrete with equal weights.
- On a u8 to u64 field each draw is rounded to the nearest whole number; a negative draw is stored as 0 and one past the type's top stops there.
- The field is drawn on its own unless the spec adds correlations, models or rules that tie it to other fields.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`normal`](#op-normal) when values should cluster around a typical value.
- [`uniform_date`](#op-uniform_date) when the field is a calendar date.
- [`discrete`](#op-discrete) when the field is a coded scale with a few fixed levels.

**Skill:** [`op-synth-uniform`](../skills/op-synth-uniform.md)

<a id="op-uniform_date"></a>

### `uniform_date`

Draws calendar dates between a start and an end date, every day equally likely, both ends included.

**Level:** basic

**Questions it answers:**

- How do I spread synthetic orders across 2024?
- How do I generate sign-up dates within the last quarter?

**Use cases by domain:**

- *survey:* Interview dates spread over a six-week fieldwork period.
- *ops:* Order dates across a fiscal year for a reporting test.

**Assumptions:**

- start and end are written as YYYY-MM-DD; end may equal start, which puts every row on that day, but may not come before it.
- Dates before 1970-01-01 are allowed.
- The field is drawn on its own unless the spec adds correlations, models or rules that tie it to other fields.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`uniform`](#op-uniform) when the field is a plain number, not a date.
- [`constant`](#op-constant) when every row shares one date.

**Skill:** [`op-synth-uniform-date`](../skills/op-synth-uniform-date.md)

<a id="op-weighted_categorical"></a>

### `weighted_categorical`

Picks one label per row from a list, each at its own share, such as 50% North, 30% South and 20% West.

**Level:** basic

**Questions it answers:**

- How do I give synthetic respondents a realistic region mix?
- How do I assign 70% of test users to plan A?

**Use cases by domain:**

- *survey:* Age band and region with the same mix as the real sample.
- *harness:* Plan tier on test accounts at a fixed 70/20/10 split.

**Assumptions:**

- weights may be raw counts and need not sum to 1; without weights every label is equally likely.
- The labels become the field's dictionary, so the list must fit the categorical width (256 labels for categorical_u8).
- The field is drawn on its own unless the spec adds correlations, models or rules that tie it to other fields.
- The same spec and seed give the same rows every time; change the seed for a fresh draw.

**Use something else:**

- [`bernoulli`](#op-bernoulli) when there are just two outcomes stored as 1 and 0 or true and false.
- [`set_bernoulli`](#op-set_bernoulli) when each row can pick several options.
- [`discrete`](#op-discrete) when the levels are numbers on a scale.

**Skill:** [`op-synth-weighted-categorical`](../skills/op-synth-weighted-categorical.md)

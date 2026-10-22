<!-- section question -->
You have a measure — income, order value, a satisfaction score, a set of ticked options — and you want to know what it looks like across the rows: its typical value, its total, how spread out it is, and how precisely the data pins those figures down. Nothing is being compared yet; this is the summary you read before any comparison.

It sounds like:

- "What's the typical order value?"
- "What did customers spend in total last quarter?"
- "How much do satisfaction scores vary, and how sure are we of the average?"

<!-- section pitfalls -->
- **The average is not always typical.** A few very large values pull the mean up; when the measure is skewed (incomes, order values, durations), read the median beside it and say which one you report.
- **Missing values are left out, not counted as zero.** Every aggregate reports how many rows it used (`n`) and how many were null (`n_null`) in its components; check them before comparing two figures.
- **A total depends on how many rows there are.** Filter the cohort or change the period and the total moves even when nothing about the typical row changed; use an average or a share to compare groups of different sizes.
- **An interval describes the average, not the people.** A narrow confidence interval says the mean is well pinned down; it says nothing about how far individual rows spread, which the standard deviation shows.
- **Survey data may need a weight.** If the rows do not represent the population evenly, an unweighted average describes the sample, not the population; set the request's weight.

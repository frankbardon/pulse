<!-- section question -->
You have one numeric measure — income, order value, a duration, a test score — and you want to know its shape before you summarise or test it: is it roughly a bell curve, lopsided with a long tail, flat or peaked, and are there values far out from the rest?

It sounds like:

- "Is income skewed?"
- "Does this score follow a normal distribution, so a t-test is safe?"
- "Is the spend of the top customers far out from everyone else?"

Start with the figures that describe shape: the spread (standard deviation, range, percentiles), the skewness (lopsidedness) and the kurtosis (how heavy the tails are). Add a histogram-style grouping to see it, and a normality test only when a later step assumes a bell shape. To compare one group's shape with another's, use a two-sample Kolmogorov-Smirnov test.

<!-- section pitfalls -->
- **Skewness and kurtosis move with a few rows.** One extreme value can change both a lot, especially on a small sample; look at the percentiles or a histogram before you rest a decision on them.
- **A normality test answers a narrow question.** On a large sample it flags departures from a bell curve too small to matter; on a small one it misses large ones. Ask whether the departure is big enough to affect the test you plan to run.
- **Most real measures are not bell-shaped.** Money, durations and counts usually have a long upper tail; that is a feature of the data, not an error. Report the median beside the mean, or work on a log scale.
- **An outlier is a lead, not a mistake.** A value far from the rest may be a recording error or your most important customer; check it before you trim or cap it.
- **Kurtosis here is excess kurtosis.** A bell curve scores zero, not three; a positive figure means heavier tails than a bell curve, a negative one lighter.
- **Shape depends on the population you filtered.** Mixing two groups with different typical values can produce two humps or a false skew; check the shape within each group as well.

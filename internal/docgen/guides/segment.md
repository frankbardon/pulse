<!-- section question -->
You have rows described by one or more measures — customers by age and income, products by price and sales, respondents by their answers — and you want to sort them into groups: tiers along one measure, or clusters of rows that look alike across several.

It sounds like:

- "Are there natural groups of customers?"
- "Split income into four equal-sized tiers."
- "Which customers fall into the high-value segment?"

For tiers on one measure, cut it into ranges: equal-sized groups (quantiles), equal-width bands, or breakpoints you choose. For groups across several measures, a clustering grouper such as k-means places every row with the nearest of a set number of centres. A filter keeps one segment you have already defined, for example by a rule on several fields.

<!-- section pitfalls -->
- **Clustering always returns the number of groups you ask for.** It finds groups whether or not the data has them; a clean split can be a convenient cut through one continuous spread. Check how much of the spread the groups explain, and try a different number of groups.
- **A segment is only useful if it means something.** Describe each group in plain terms (younger, high income, recent buyers) and check that the difference matters for a decision before you name it.
- **Units decide what counts as near.** A measure in dollars swamps one in years unless the fields are standardised first; check that the clustering puts the measures on a common scale.
- **Quantile tiers have equal counts, not equal widths.** The top tier of a skewed measure can span a far wider range than the others; read each tier's minimum and maximum.
- **Segments found and tested on the same data look better than they are.** If you then compare the segments on the measures that built them, they will differ by construction; test them on a different outcome or on fresh data.
- **Results can shift with the starting point.** K-means depends on its starting centres; fix the seed so the same data gives the same groups, and treat a segment that moves between runs with care.

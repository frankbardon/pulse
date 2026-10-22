<!-- section question -->
You have two or more groups — treatment and control, regions, customer segments — and one outcome, such as revenue, a score or whether someone converted. You want to know whether the groups really differ on that outcome, by how much, and whether the gap is bigger than the noise you would expect from the rows you happened to collect.

It sounds like:

- "Did the variant arm spend more than control?"
- "Do the four regions convert at different rates?"
- "Is this segment's satisfaction score different from everyone else's?"

Start with the group figures themselves (an average or a share per group), then add a test that says how surprising the gap would be if the groups were alike. Two groups take a two-sample test; three or more take an analysis of variance or its rank-based cousin, followed by pairwise comparisons only if you need to know which pair differs.

<!-- section pitfalls -->
- **A small p-value is not a big difference.** With many rows, a gap too small to matter still clears the bar; read the size of the gap and the effect size (Cohen's d, Cramér's V) beside the p-value.
- **A large p-value is not proof the groups are alike.** With few rows per group a real gap can go undetected; read the interval for the difference, which shows how large a gap the data still allows.
- **Many comparisons inflate false alarms.** Testing every pair of regions, or every cell of a banner, makes some gap clear the bar by luck; use a test across all groups first, a pairwise procedure built for it (Tukey's HSD), or a request's `multiplicity` block to adjust the p-values.
- **Pick the test for the outcome.** Means of a numeric outcome take a t-test or ANOVA; shares of a yes/no outcome take a proportion or chi-square test; a skewed outcome with outliers is safer with a rank-based test (Mann-Whitney, Kruskal-Wallis).
- **Same people measured twice are not two groups.** Before-and-after scores on the same customers need a paired test; treating them as independent groups throws away the pairing and usually hides the change.
- **Groups that differ in make-up confound the comparison.** If one region has more large accounts, its higher average may reflect the mix, not the region; compare like with like or move to the drivers question.
- **Survey data may need a weight.** An unweighted comparison describes the sample; set the request's weight when the rows do not represent the population evenly.

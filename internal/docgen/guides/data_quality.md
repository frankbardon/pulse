<!-- section question -->
Before you trust an analysis, you want to know whether the data behind it is sound: how much is missing, whether any rows are implausible or far out of line with the rest, and whether some respondents answered carelessly.

It sounds like:

- "How much is missing?"
- "Are there customers whose figures look implausible?"
- "Who answered carelessly?"

Count what is missing per field (a null count beside a count of present values), and keep or drop the null rows with a null filter. For rows out of line on several fields at once, score each row's distance from the cohort's centre (Mahalanobis distance) or its leverage in a model. For a rating battery, flag rows that give the same answer to every item (straight-lining) or answer too few items to score.

<!-- section pitfalls -->
- **Missing is not zero.** Aggregates leave null rows out and report how many they skipped (`n_null`); a filter or formula that turns a null into zero changes every figure after it.
- **Check what the missing rows have in common.** If the rows with missing answers come mostly from one group, dropping them shifts the comparison; compare the groups' missing rates first.
- **An unusual row is a lead, not an error.** A customer far from the centre may be a typo or a genuine high earner; check it before you delete or cap it.
- **Distance-based outlier scores assume a bell-shaped cloud.** On a skewed measure such as income the tail rows get extreme scores and tiny p-values even when they are genuine; judge them against the measure's shape.
- **Many rows tested means some flagged by luck.** Scoring every row against a cut-off flags a few ordinary rows on any data set; look at how far beyond the cut-off a row falls.
- **Fix the data, then rerun.** A quality check is worth doing before the analysis it protects; rerun the analysis after you exclude or correct rows and say what changed.

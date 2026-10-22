<!-- section question -->
Before you trust an analysis, you want to know whether the data behind it is sound: how much is missing, whether the gaps fall evenly or pile up in one group, whether any rows are implausible or far out of line with the rest, and whether some respondents answered carelessly.

It sounds like:

- "How much is missing?"
- "Is the missing data concentrated in one channel or group?"
- "Are there customers whose figures look implausible?"
- "Who answered carelessly?"

Count what is missing per field (a null count beside a count of present values), then group the same counts by a field that could explain the gaps, such as the channel a form arrived through. Keep or drop the null rows with a null filter once you know who they are. For rows out of line on several fields at once, score each row's distance from the cohort's centre (Mahalanobis distance) or its leverage in a model. For a rating battery, flag rows that give the same answer to every item (straight-lining) or answer too few items to score.

<!-- section pitfalls -->
- **Missing is not zero.** Aggregates leave null rows out and report how many they skipped (`n_null`); a filter or formula that turns a null into zero changes every figure after it.
- **Check what the missing rows have in common.** If the blanks come mostly from one group, a figure over the present rows leans towards the groups that answered; compare the groups' missing rates before you drop or ignore the blanks.
- **Dropping null rows is a choice, not a cleanup.** A null filter shrinks every group by a different amount when the gaps are uneven; say how many rows each group lost.
- **A small, even gap is usually harmless.** A field blank on a few rows in every group rarely moves a comparison; an uneven gap on the field you are reporting does.
- **An unusual row is a lead, not an error.** A customer far from the centre may be a typo or a genuine high earner; check it before you delete or cap it.
- **Fix the data, then rerun.** A quality check is worth doing before the analysis it protects; rerun the analysis after you exclude or correct rows and say what changed.

<!-- section question -->
You have two measures and want to know whether they go together: when one is high, does the other tend to be high (or low) too, and how tightly? For two numeric fields that is a correlation; for two categorical fields it is a test of whether the categories are independent, read from a crosstab of the two.

It sounds like:

- "Do customers who spent more last year spend more this year?"
- "Is age associated with income?"
- "Does conversion depend on the customer segment?"

A relationship is symmetric: nothing is cast as the cause and nothing as the effect. When you want to say how much one outcome moves with several measures at once, that is the drivers question.

<!-- section pitfalls -->
- **Association is not causation.** Two measures can move together because a third thing drives both, or by the way the rows were selected; a correlation never says which way, if any, the influence runs.
- **Pearson's r sees straight lines only.** A strong curved or step-shaped relationship can show a weak r; plot the data or use a rank correlation (Spearman, Kendall), which also resists a few extreme values.
- **One or two outliers can make or break a correlation.** Check whether the figure survives the rank-based version before reporting it.
- **A significant correlation can be tiny.** With many rows a correlation close to zero still clears the bar; read the size of r and its interval, not only the p-value.
- **Chi-square needs enough rows per cell.** When some expected cell counts are small, use Fisher's exact test instead; a crosstab with many sparse cells is better collapsed into fewer categories first.
- **Group averages can tell a different story from rows.** A relationship between group means (region by region) need not hold for the individual rows inside them; the ecological fallacy example shows the reversal.
- **Many pairs at once inflate false alarms.** A full correlation table across many fields will show some strong-looking pairs by luck; treat them as leads to confirm, not findings.

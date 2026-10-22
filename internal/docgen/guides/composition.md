<!-- section question -->
You have a categorical field — region, plan, answer option, the brands a respondent ticked — and you want to know its mix: how the rows split across the categories, what share each option has, and whether that mix differs from one group to another.

It sounds like:

- "What's our customer mix by region?"
- "What share of each region's customers converted?"
- "Which attributes go with which brand?"

Counts per category answer the first question; shares answer the rest. In a crosstab the share can run along the row (each row sums to one), down the column (each column sums to one) or over the whole table, and the three answer different questions, so choose the direction from the sentence you want to write.

<!-- section pitfalls -->
- **Say what the share is a share of.** The same cell is a different share of its row, of its column and of the whole table; name the base every time you report one.
- **A share hides the count behind it.** A category with a striking share may rest on a handful of rows; keep the margin counts beside the shares and treat small bases with care.
- **A different mix is not yet a tested difference.** Shares describe the data; to say two groups' mixes really differ, add a chi-square test or a cell-level significance overlay.
- **Multi-select answers do not sum to one.** When a respondent can tick several options, option shares add up to more than the whole; read each as the share of rows choosing that option.
- **Missing values change the base.** Rows with no category are left out of the shares by default; check how many there were before comparing mixes.
- **Survey data may need a weight.** An unweighted mix describes the sample; when some groups are over- or under-represented, set the request's weight or rake the sample to known population totals.

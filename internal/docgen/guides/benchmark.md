<!-- section question -->
You have a figure — an average, a share, a total, a score — and something to hold it against: a target you set, the whole population, another group, last wave or a reference request. You want to know whether your figure sits above or below that reference, by how much, and whether the gap is bigger than the noise in the rows you have.

It sounds like:

- "Is our average order value above the target we set?"
- "Which regions over-index on revenue against the total?"
- "How does this wave's mix compare with the last one?"

Decide first what the reference is, because it picks the analysis. A fixed figure you supply takes a one-sample test (a t-test for an average). The total or the population takes an index or z-score overlay, where an index of one hundred means level with the reference. Another group, another wave or another request takes a delta or index overlay against that sibling, stage or reference.

<!-- section pitfalls -->
- **Choose the reference before you look.** A target picked after seeing the data, or the one comparison that happened to look good, makes any gap look convincing; write the reference down first.
- **An index is a ratio, not a test.** An index of one hundred and twenty says the figure is a fifth above the reference; it says nothing about whether that gap could come from the rows you happened to collect. Read the base counts, or use a test or a z-score overlay to judge it.
- **A group is part of its own total.** When a large group is compared with a total it makes up much of, the gap shrinks towards zero; for a fair contrast compare it with everyone else, or with a sibling group.
- **Sums and averages index differently.** Indexing each group's total against the grand total gives its share of the whole, which grows with group size; index an average or a rate when you want to compare how the typical row performs.
- **A small p-value is not a big gap.** With many rows a gap too small to matter still clears the bar; read the size of the gap and the effect size beside the p-value.
- **Many groups against one reference inflate false alarms.** Scoring every region or every cell against the total makes some stand out by luck; use the request's `multiplicity` block to adjust the p-values.
- **Survey data may need a weight.** An unweighted figure describes the sample; set the request's weight when you benchmark against a population figure.

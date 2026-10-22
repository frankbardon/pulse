<!-- section question -->
You have a measure recorded over time — orders per day, revenue per month, the same customers before and after a change — and you want to know whether it is going up or down, how this period compares to the last one, or whether a change after some event is more than noise.

It sounds like:

- "Is revenue going up?"
- "How does this month compare to last month, and to the same month last year?"
- "Did customers spend more after the launch than before it?"

There are three common shapes. Bucket the rows by period (day, week, month) and read the series, adding period-over-period changes or indexes. Test the whole series for a steady upward or downward trend. Or, when the same rows are measured twice, compare each row with itself before and after.

<!-- section pitfalls -->
- **Before and after is not cause and effect.** Anything else that changed in the same window — season, prices, a gradual drift — lands in the difference too; a comparison group that did not get the change is what isolates it.
- **Periods of different length are not comparable.** February has fewer days than March and a partial last month looks like a collapse; compare averages per day, or complete periods only.
- **Seasonality looks like a trend.** A December peak is not growth; compare with the same period a year earlier, or remove the seasonal pattern first.
- **Thin periods swing wildly.** With few rows per day, daily figures jump around and a trend test may miss a real drift; bucket into weeks or months, and read a short noisy series as inconclusive rather than flat.
- **Smoothing before testing overstates a trend.** A moving average makes neighbouring points depend on each other, so a trend test on the smoothed series looks surer than the data allow; test the raw period figures.
- **Paired data need a paired test.** The same customers before and after are not two independent groups; a paired test compares each customer with themselves and is far more sensitive.
- **Time zones move rows between days.** A timestamp near midnight belongs to different days in different zones; set the zone the business reports in before bucketing.

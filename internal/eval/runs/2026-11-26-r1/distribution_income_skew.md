# distribution_income_skew

4 calls: imports_list (empty) → manifest intent=describe → inspect customers.pulse (guessed) → process. The agent says there is no skewness aggregator in the manifest, so it inferred skew from the mean/median gap.

The agent also reports that manifest descriptions are empty and do not say that AGG_PERCENTILE takes 0–100, not a 0–1 fraction. It used 0.9 once and discarded the result.

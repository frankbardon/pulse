# prepare_income_zscore

9 calls: imports_list (only `intake`, left by the parallel import task) → manifest intent=prepare (large, empty descriptions) → inspect imports/intake.pulse → skills_get op-attr-zscore → examples_get op-filter-range (error: that name is a skill, not an example) → skills_get op-filter-range → examples_search category=materialize → materialize (error PULSE_REQUEST_UNKNOWN_FIELD: `values` placed under params) → materialize (succeeded, 193 rows). 2 errors, 1 retry.

**Wrong cohort:** the agent used intake.pulse, which has age and income, instead of customers.pulse, because no tool lists the data-dir cohorts.

Friction:
- The op-filter-range skill's Params table lists `values` as if it went under `params`.
- FILTER_RANGE has no open bound, so the agent used 255 as the upper limit.

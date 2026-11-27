# benchmark_income_target

6 calls: imports_list → examples_search one-sample → examples_get t_test_one_sample (uses experiment.pulse) → inspect experiment.pulse (no income) → process (failed: data_dir ".data") → process (succeeded).

**Wrong cohort:** the agent never found `customers.pulse`, which holds income, and tested revenue against 35,000 instead, saying the comparison was not meaningful.

Root cause: no tool lists the cohorts in the data dir, and the agent anchored on the example's cohort.

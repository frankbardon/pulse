# quality_missing_income

6 calls: imports_list (found `intake`, created by the parallel import task) → manifest intent=compare_groups (no AGG_NULL_COUNT) → inspect → examples_search intent=data_quality → examples_get agg_missing_by_channel → process. No errors. Paper 42%, phone 13%, web 4%.

Friction:
- A plain search for "null missing rate" returned nothing.
- The example cohort `intake.pulse` + `data_dir` did not match the managed path.
- The agent reports that the manifest's format_version is "1.0".

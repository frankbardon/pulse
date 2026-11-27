# quality_missing_income

4 calls: cohorts_list → examples_search "null missing rate" → examples_get agg_missing_by_channel → process intake.pulse. No retries. (r1 FAIL fixed: no imports/intake.pulse in the data dir, agent used intake.pulse.)

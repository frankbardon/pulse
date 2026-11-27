# distribution_income_skew

5 calls: cohorts_list → facet_schema with numeric_percentiles [5,25,50,75,95] (failed: SERVICE_VALIDATION, percentiles must lie in (0,1)) → examples_search "skewness" → examples_get shape_moments → process. 1 retry. (r1 FAIL fixed: AGG_SKEWNESS found.)

Friction: model slip; the facet_schema tool schema states "Strictly in (0, 1)".

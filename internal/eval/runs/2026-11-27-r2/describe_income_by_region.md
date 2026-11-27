# describe_income_by_region

4 calls: cohorts_list → process (failed: PROCESSING_CONFIG unknown aggregation type AGG_MEAN, authored from memory) → examples_search "average" → process (AGG_AVERAGE, ok). 1 retry.

Friction: model slip (guessed AGG_MEAN without reading the catalog; the tool description says not to author from memory). The error does not suggest AGG_AVERAGE.

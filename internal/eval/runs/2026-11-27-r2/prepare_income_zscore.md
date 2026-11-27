# prepare_income_zscore

7 calls: cohorts_list → examples_search zscore → examples_search materialize → examples_get zscore → examples_get filterers_range (failed: SERVICE_VALIDATION example not found, guessed name) → examples_get facet-chisq-vs-pop → materialize (ok, 133 records). 1 retry.

Friction: FILTER_RANGE still has no open upper bound; agent used 255 for "40 or older" (r1 row). Guessed an example name.

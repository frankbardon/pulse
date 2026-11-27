# prepare_income_zscore

7 calls: cohorts_list → materialize predict:true with FILTER_NUMERIC_RANGE (valid:false, PROCESSING_CONFIG unknown filter type) → examples_search "filter numeric age" → examples_search "zscore" → examples_get filter_range_age → examples_get zscore → materialize (ok, 133 records). 1 retry. FILTER_RANGE upper bound 255 stands in for open (age is u8).

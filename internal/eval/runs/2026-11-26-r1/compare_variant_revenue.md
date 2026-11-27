# compare_variant_revenue

6 calls: imports_list (empty) → manifest intent=compare_groups → examples_search tag experiment-analysis → examples_get t_test_two_sample → process (failed: data_dir) → process (succeeded). 1 retry. Welch t=3.16, p=0.0017.

Friction:
- Example `data_dir`.
- No cohort listing.
- The TEST_WELCH `reference` param has no description in the manifest.

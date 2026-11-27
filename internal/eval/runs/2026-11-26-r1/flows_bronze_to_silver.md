# flows_bronze_to_silver

9 calls: manifest intent=change_over_time → imports_list (empty) → manifest intent=flows → label_tables → skills_get op-overlay-markov → examples_search intent=flows → examples_get crosstab-retention → process (failed: data_dir) → process (succeeded). 1 retry. Bronze→silver 17.4%.

Friction: example `data_dir`; no cohort listing.

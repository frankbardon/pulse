# segment_training_clusters

14 calls: imports_list (empty) → examples_search k-means → examples_get group_kmeans → then 10 `pulse_inspect` filename guesses (training, train, customers, housing, sales, orders, training_data ✓, products, cars) plus 1 unscoped manifest call, which overflowed the client output limit → process. 6 file-not-found errors. Clusters of 197/64/39.

Friction: no cohort-listing tool, so the agent spent 10 calls guessing filenames.

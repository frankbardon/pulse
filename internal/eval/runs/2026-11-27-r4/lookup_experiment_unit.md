# lookup_experiment_unit

5 calls: cohorts_list → inspect → process FILTER_INCLUDE id=42 (metadata only, no rows) → inspect → lookup. No errors; 2 detour calls before finding pulse_lookup.

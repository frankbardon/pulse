# lookup_experiment_unit

5 calls: imports_list (saw only `intake`, left by the parallel import task) → inspect imports/intake.pulse (wrong cohort) → inspect experiment.pulse (filename guessed) → label_tables → lookup id=42. No errors.

Friction: no tool lists the cohorts in the data dir, so the agent guessed `experiment.pulse`.

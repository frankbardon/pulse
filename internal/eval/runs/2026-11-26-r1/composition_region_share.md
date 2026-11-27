# composition_region_share

3 calls: imports_list (only `intake`, left by the parallel import task) → inspect imports/intake.pulse (no region) → label_tables (none). The agent concluded that no region data exists and offered an unrun channel-share call, which is recorded here.

**Unanswered in substance:** the agent never found `customers.pulse`.

Root cause: no tool lists data-dir cohorts, and `pulse_imports_list` reads as if it were the full cohort list.

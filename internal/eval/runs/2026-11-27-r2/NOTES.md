# Run 2026-11-27-r2: 19/20 as run, 20/20 rescored

This was the first rerun after the E2-S2 fixes (binary `793d0f685`). All four r1 failures pass. The one failure was `composition_region_share`: counts by region, with no share in the result. It was classified as a tool-description gap, and `222625dff` added a share sentence to the `pulse_process` description. That did not change the agent's behaviour in r3.

Rescored (`score.txt`): under the composition key the owner widened on 2026-11-27 (a count-by-region request is a correct answer), r2 also scores 20/20. r3 is still the green run, by owner ruling: it was the first full run after the last served-text change (`222625dff`), and r2 predates that change.

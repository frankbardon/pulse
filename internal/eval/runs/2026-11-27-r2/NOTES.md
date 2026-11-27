# Run 2026-11-27-r2: 19/20

This was the first rerun after the E2-S2 fixes (binary `793d0f685`). All four r1 failures pass. The one failure was `composition_region_share`: counts by region, with no share in the result. It was classified as a tool-description gap, and `222625dff` added a share sentence to the `pulse_process` description. That did not change the agent's behaviour in r3.

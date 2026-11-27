# Run 2026-11-27-r3: the green run (20/20)

- Binary: `v1.0.0-alpha.48-125-g222625dff`, with a fresh fixture data dir. One fresh Sonnet `pulse-eval-agent` per task. The import task ran last and alone.
- Scored 19/20 when run. `composition_region_share` returned counts by region and the agent worked out the shares by hand, as it also did in r2.
- Owner call (2026-11-27): a count-by-region request is a correct answer, since each share is exact from the counts and the reported total. The task's assertions now also accept the south and west counts, scoped to `data` rows. A count-by-region request scores PASS. A wrong request (average income by region) still scores FAIL.
- This changes scoring only; the served text did not change. Rescored: 20/20 (`score.txt`).
- The prompt asked for the tool call list after the JSON, which is the source of each `<task>.md`.
- The `simulate_test_scores` agent named the synth operators and params but did not write a spec document. The orchestrator assembled the synth pseudo-tool spec from them, with seed 42; see its `.md`.

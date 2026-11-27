# simulate_test_scores

3 calls: skills_list unscoped (output too large for the client) → skills_list intent=simulate → skills_get op-synth-monotonic-from.

Not runnable by the agent: no MCP tool runs synth, which the skill states. The agent named the operators and params (monotonic_from start 1 step 1 for id; uniform min 0 max 100 for score, 500 rows, scores.pulse) but did not write a full spec document. The orchestrator assembled the synth pseudo-tool spec from exactly those named operators and params, with seed 42 chosen by the orchestrator.

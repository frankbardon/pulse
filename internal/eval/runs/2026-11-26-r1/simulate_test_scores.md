# simulate_test_scores

5 calls: skills_list unscoped (about 158K chars, overflowed) → skills_list intent=simulate → examples_search synth → skills_get op-synth-monotonic-from → skills_get synthetic-data. The agent answered `{"tool": null}` because no MCP tool runs synth, then gave the spec it would use. The orchestrator recorded that spec as the `synth` pseudo-tool submission, per the procedure (no seed given).

Friction: the simulate intent's skills do not say up front that synth is CLI-only; the unscoped skills list overflows; the synthetic-data skill gives no CLI flags.

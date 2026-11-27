# simulate_test_scores

3 calls: skills_list unscoped (failed: client output limit, 158,423 chars) → skills_list intent=prepare → skills_get synthetic-data.

Not runnable by the agent: no MCP tool runs synth (the skill now says so up front). The agent wrote the spec and the CLI command `pulse synth from-schema -s spec.json -o scores.pulse --seed 42`; the orchestrator recorded it as the synth pseudo-tool.

Friction: the unscoped skills_list still overflows the client limit (r1 row, "fixed" for the manifest; skills_list unscoped is still ~158K).

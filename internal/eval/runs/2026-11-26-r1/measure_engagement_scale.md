# measure_engagement_scale

5 calls: imports_list → examples_search ("cronbach alpha reliability") → examples_get scale_reliability → process (failed SERVICE_RESOURCE on example `data_dir: ".data"`) → process without data_dir (succeeded). 1 retry.

Result: alpha 0.824, omega 0.827, no item worth dropping.

Friction: the served example body's `data_dir` does not resolve over MCP.

# change_repeated_conditions

Tool calls (5):

1. `pulse_imports_list`: found no managed imports.
2. `pulse_examples_search` ("repeated measures"): found `rm_anova_three_conditions`.
3. `pulse_examples_get`: fetched the example body.
4. `pulse_process`: failed with `SERVICE_RESOURCE`. The example body sets the cohort `data_dir` to `".data"`, and that path does not resolve on the MCP server.
5. `pulse_process`: succeeded after dropping `data_dir`.

Retries: 1.

The answer was a repeated-measures ANOVA: F=545.15, p≈1.4e-46, partial η²=0.93, n=40 subjects.

Friction: the example bodies served over MCP carry a `data_dir` that the server cannot open, so the agent used one tool call recovering from it.

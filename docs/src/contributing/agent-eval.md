# Agent Evaluation

The agent evaluation checks that an agent can answer plain-language
questions with Pulse using only what the MCP server serves: the
manifest, skills, examples and tool descriptions. Each task is a
question over the example fixture cohorts. An agent's answer passes
when the request it submits produces a result that meets the task's
assertions. The wording and labels of the request do not matter.

The task set lives in `internal/eval/tasks/`, one `<task-id>.json` per
task. Each file holds the question, its intent, the fixture cohorts, a
reference submission and the assertions. The file format and the
assertion kinds are in the package documentation
(`go doc ./internal/eval`). The tasks are the answer key, so they are
never served: `TestEvalNoAnswerKeyLeak` keeps them out of the example
library, the skill pack and every shipped binary. CI runs each
reference against its own assertions (`TestEvalReferences`) and needs a
task for every intent (`TestEvalIntentCoverage`).

## Running the evaluation

Rerun the whole set before a release and after a change to served
text. Run the agent outside the repository so it can see only what the
server serves.

1. Build the binary and a fresh fixture data directory outside the
   repository. Copy in the raw files the import task reads (each
   task's `files` list):

   ```bash
   make build
   export PULSE_DATA_DIR="$(mktemp -d)"
   internal/examples/fixtures/build.sh
   cp internal/examples/fixtures/intake.csv "$PULSE_DATA_DIR"/
   ```

2. Register `bin/pulse mcp` as a stdio MCP server in the agent client,
   with `PULSE_DATA_DIR` set to that directory.
3. Run one fresh agent session per task. Give the agent only the pulse
   MCP tools (`pulse_*`): no shell, no file access, no web. Run the
   import task (`prepare_import_intake`) last and alone, after every
   other task has finished. It writes `imports/intake.pulse` into the
   shared data directory; a task run beside it can see that cohort in
   the import list and use it, and the scorer's fresh environment has
   no managed imports, so the answer fails. Rebuild the data directory
   (step 1) before the next full run.
4. Send the task's `question` in this prompt, with nothing else:

   ```text
   Answer this question about the data in the connected Pulse server.
   Use only the pulse MCP tools.

   <question>

   When you are done, give the tool call that answers the question as
   JSON: {"tool": "<tool name>", "arguments": {<its arguments>}}.
   ```

5. Save the run under `internal/eval/runs/<date>/`, two files per task:
   - `<task-id>.json`: the final submission, `{"tool", "arguments"}`.
     The simulate task has no MCP tool, so the agent cannot run its
     answer. The orchestrator records the synth spec the agent wrote
     as `{"tool": "synth", "arguments": {"spec": …, "out": …,
     "seed": …}}` (the synth pseudo-tool) and notes in the summary that
     the answer was not runnable by the agent.
   - `<task-id>.md`: a short transcript summary. Record the tool calls in
     order with a count, any retries, and where the agent went wrong.
6. Score the run from the repository root:

   ```bash
   go run ./internal/tools/evalscore internal/eval/runs/<date>
   ```

   It runs every submission on freshly built fixtures, prints `PASS` or
   `FAIL` per task with the failing assertions, and exits 1 if any task
   fails. A task with no submission fails as unanswered. `-json`
   prints the reports as JSON.

## Pass rule

A task passes when its final submission passes the checker. Retries
within the session are allowed. Tool calls are counted in the summary,
not graded. The run passes when every task passes.

## Classifying a failure

Give every failing task one cause and fix the surface the agent should
have read:

| Cause | Fix |
|---|---|
| Doc surface missing or wrong | The skill, example or guide page the agent read (or should have found) |
| Tool description | The tool's served description |
| Purpose | The operator's `Purpose` or `Interpretation` |
| Code gap | The engine, then the docs that describe it |
| Model slip | Nothing, if the served text was right and clear. Record it |

After the fixes, rerun the whole set, not only the failed tasks, until
every task passes.

## Adding a task

Add `internal/eval/tasks/<task-id>.json`. Write the question as a
non-statistician would ask it, without operator names. Write
assertions that any correct request meets, not only the reference.
Then run `go test -count=1 ./internal/eval/`. A new intent needs a task
in the same change.

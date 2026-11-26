// Package eval is the agent task evaluation: a set of plain-language
// questions over the example fixture cohorts, each with a reference
// submission and a list of deterministic assertions about the answer.
//
// The package scores a submission, never its wording: a submission
// passes when every assertion of its task holds over the result the
// submission produces, so any request that answers the question passes,
// whatever labels, presets or extra slots it carries.
//
// The task set is the answer key. It lives here, outside
// internal/examples and internal/skills, so it is never served to the
// agent being evaluated; TestEvalNoAnswerKeyLeak keeps it that way.
//
// # Task files
//
// One JSON object per file under tasks/, named <id>.json:
//
//	{
//	  "id":         "describe_income_by_region",  // the file stem
//	  "intent":     "describe",                   // an intent-taxonomy ID
//	  "question":   "What is ...?",               // what the agent is asked
//	  "cohorts":    ["customers"],                // fixture cohorts the run needs
//	  "files":      ["intake.csv"],               // optional: raw fixture files copied into the data dir
//	  "indexes":    [{"cohort": "experiment", "fields": ["id"]}], // optional point-lookup indexes
//	  "reference":  {"tool": "pulse_process", "arguments": {...}},
//	  "assertions": [{"kind": "value", ...}, ...]
//	}
//
// A submission (the reference, or an agent's final answer) names the
// tool it calls and that tool's arguments: any pulse MCP tool name, or
// "synth" for a synthetic-cohort spec ({"spec", "out", "seed"}), the one
// route with no MCP tool. Every data_dir key in the arguments is
// dropped, so a cohort resolves against the run's fresh data directory.
//
// # Assertions
//
// Each assertion has a kind. Scope keys pick the numbers a value-like
// assertion reads from the result (the tool's output as JSON):
//
//   - section: a top-level result key ("data", "tests", "regressions",
//     "rows", ...); empty reads the whole result.
//   - types: keep only section entries whose "type" is listed.
//   - in_row: keep only section entries holding every listed string as
//     a top-level value (a group key such as "north").
//   - key: a dotted key path a number's path must end with ("p_value",
//     "coefficients.income").
//   - metric: {"types": [...], "field": "..."}: a number's last key must
//     be the output label of a request operator of one of those types
//     over that field (its label, else TYPE_field).
//
// Kinds:
//
//   - operator: the submission uses an operator in any_of.
//   - value: some in-scope number lies within tol of near, and/or in
//     [min, max].
//   - sign: some in-scope number has the given sign ("positive" or
//     "negative").
//   - order: for some in-scope key the entry holding high exceeds the
//     entry holding low.
//   - top: for some in-scope key the entry holding value is the largest
//     (smallest with lowest), over at least two entries.
//   - rows: every string in present is a top-level value of some section
//     entry, and the entry count is count / within [min_count, max_count].
//   - row: some section entry equals match on every key (numbers within
//     tol).
//   - warning: a warning (any "warnings" entry, at any depth) carries code.
//   - error: the run fails with code; without one, a failed run fails
//     the task.
//   - cohort: the cohort the submission wrote has rows records and
//     carries fields (field_types pins their types).
//   - any: at least one of the nested assertions holds.
package eval

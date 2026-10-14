package skills

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
)

// fieldClauseEnds are the markers that close an atomic skill's `Field`
// clause: the next slot it names, a newline, or the `;` that opens the
// refusal prose (which names the types it does NOT accept).
var fieldClauseEnds = []string{"\n", ";", "`SplitBy`", "`Field2`", "`SubjectField`"}

var backticked = regexp.MustCompile("`([a-z0-9_]+)`")

// skillFieldTypes returns the registered field-type names an atomic
// skill's `## Inputs` section lists for its `Field`: the backticked type
// names after the clause's first `:` (before it, a qualifier such as
// "numeric (no `decimal128`)" may name an excluded type).
func skillFieldTypes(t *testing.T, stem string) []string {
	t.Helper()
	raw, ok := Get(stem)
	if !ok {
		t.Fatalf("%s: no such skill", stem)
	}
	_, inputs, ok := strings.Cut(raw, "## Inputs")
	if !ok {
		t.Fatalf("%s: no ## Inputs section", stem)
	}
	inputs, _, _ = strings.Cut(inputs, "\n## ")
	_, clause, ok := strings.Cut(inputs, "`Field`")
	if !ok {
		t.Fatalf("%s: ## Inputs names no `Field`", stem)
	}
	for _, end := range fieldClauseEnds {
		clause, _, _ = strings.Cut(clause, end)
	}
	if _, after, ok := strings.Cut(clause, ":"); ok {
		clause = after
	}
	seen := map[string]bool{}
	for _, m := range backticked.FindAllStringSubmatch(clause, -1) {
		if ft, ok := encoding.ParseFieldType(m[1]); ok && ft.IsKnown() {
			seen[m[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// tableJudgedSkillOps are the operators whose atomic skill must list
// exactly the accepted-types table's Field types: every tier-1 numeric
// test, and the numeric readers that refuse a categorical (E2-S4).
func tableJudgedSkillOps() []string {
	var out []string
	for _, op := range fieldaccept.Operators() {
		if r, ok := fieldaccept.Lookup(op, fieldaccept.SlotField); ok && r.Code == errors.PULSE_TEST_FIELD_NOT_NUMERIC {
			out = append(out, op)
		}
	}
	return append(out,
		"AGG_SUM", "AGG_AVERAGE", "AGG_MIN", "AGG_MAX", "AGG_STDDEV", "AGG_VARIANCE", "AGG_RANGE",
		"AGG_ZSCORE", "AGG_MEDIAN", "AGG_PERCENTILE", "AGG_SKEWNESS", "AGG_KURTOSIS",
		"AGG_WEIGHTED_MEAN", "AGG_DISTINCT_SUM", "AGG_CI_LOWER", "AGG_CI_UPPER",
		"FEAT_LOG", "FEAT_SQRT", "FEAT_BUCKETIZE")
}

// TestAtomicSkillFieldTypesMatchFieldAcceptTable holds the Field types
// an atomic skill lists to the accepted-types table (internal/
// fieldaccept) — the rule predict and the runtime enforce — so a skill
// can neither omit a type the operator reads nor promise one it
// refuses.
func TestAtomicSkillFieldTypesMatchFieldAcceptTable(t *testing.T) {
	ops := tableJudgedSkillOps()
	if len(ops) != 16+19 {
		t.Fatalf("judged %d operators, want 35 (16 tier-1 numeric tests, 19 numeric readers)", len(ops))
	}
	for _, op := range ops {
		category, name, _ := strings.Cut(op, "_")
		stem := "op-" + strings.ToLower(category) + "-" + strings.ReplaceAll(strings.ToLower(name), "_", "-")
		got := skillFieldTypes(t, stem)
		want := fieldaccept.TypeNames(op, fieldaccept.SlotField)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s lists Field types %v, the table accepts %v", stem, got, want)
		}
	}
}

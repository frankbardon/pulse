package eval

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// outcome builds an Outcome from JSON literals for the args and result.
func outcome(t *testing.T, args, result string) *Outcome {
	t.Helper()
	o := &Outcome{}
	for _, p := range []struct {
		src string
		dst *any
	}{{args, &o.args}, {result, &o.Result}} {
		if p.src == "" {
			continue
		}
		if err := json.Unmarshal([]byte(p.src), p.dst); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func f(v float64) *float64 { return &v }
func n(v int) *int         { return &v }

// TestCheck_AssertionKinds pins each assertion kind both ways: the
// holding case returns no message, the violated one names why.
func TestCheck_AssertionKinds(t *testing.T) {
	t.Parallel()
	const args = `{"groups":[{"type":"GROUP_CATEGORY","field":"region"}],
		"aggregations":[{"type":"AGG_AVERAGE","field":"income","label":"avg"},{"type":"AGG_COUNT","field":"id"}],
		"tests":[{"type":"TEST_T","field":"income","split_by":"arm"}]}`
	const result = `{"data":[
			{"region":"north","avg":50,"AGG_COUNT_id":10},
			{"region":"south","avg":40,"AGG_COUNT_id":30}],
		"tests":[{"type":"TEST_T","p_value":0.01,"details":{"diff":-3}}],
		"regressions":[{"type":"REG_OLS","coefficients":{"income":2.5}}],
		"warnings":[{"code":"PULSE_X"}]}`
	avg := &Metric{Types: []string{"AGG_AVERAGE"}, Field: "income"}
	cases := []struct {
		name string
		a    Assertion
		pass bool
	}{
		{"operator holds", Assertion{Kind: "operator", AnyOf: []string{"TEST_WELCH", "TEST_T"}}, true},
		{"operator missing", Assertion{Kind: "operator", AnyOf: []string{"TEST_WELCH"}}, false},
		{"value near", Assertion{Kind: "value", Section: "data", InRow: []string{"north"}, Near: f(50), Tol: 0.5}, true},
		{"value off", Assertion{Kind: "value", Section: "data", InRow: []string{"north"}, Near: f(45), Tol: 0.5}, false},
		{"value key max", Assertion{Kind: "value", Section: "tests", Key: "p_value", Max: f(0.05)}, true},
		{"value key max violated", Assertion{Kind: "value", Section: "tests", Key: "p_value", Max: f(0.001)}, false},
		{"value dotted key min", Assertion{Kind: "value", Key: "coefficients.income", Min: f(2)}, true},
		{"value types filter", Assertion{Kind: "value", Section: "tests", Types: []string{"TEST_WELCH"}, Key: "p_value", Max: f(1)}, false},
		{"sign negative", Assertion{Kind: "sign", Key: "diff", Sign: "negative"}, true},
		{"sign positive", Assertion{Kind: "sign", Key: "diff", Sign: "positive"}, false},
		{"order holds", Assertion{Kind: "order", Section: "data", Metric: avg, High: "north", Low: "south"}, true},
		{"order reversed", Assertion{Kind: "order", Section: "data", Metric: avg, High: "south", Low: "north"}, false},
		{"top metric", Assertion{Kind: "top", Section: "data", Metric: avg, Value: "north"}, true},
		{"top metric wrong", Assertion{Kind: "top", Section: "data", Metric: avg, Value: "south"}, false},
		{"top lowest", Assertion{Kind: "top", Section: "data", Metric: avg, Value: "south", Lowest: true}, true},
		{"rows present count", Assertion{Kind: "rows", Section: "data", Present: []string{"north", "south"}, Count: n(2)}, true},
		{"rows absent", Assertion{Kind: "rows", Section: "data", Present: []string{"east"}}, false},
		{"rows count off", Assertion{Kind: "rows", Section: "data", Count: n(3)}, false},
		{"rows min count", Assertion{Kind: "rows", Section: "data", MinCount: n(3)}, false},
		{"row match", Assertion{Kind: "row", Section: "data", Match: map[string]any{"region": "south", "avg": 40.0}}, true},
		{"row mismatch", Assertion{Kind: "row", Section: "data", Match: map[string]any{"region": "south", "avg": 50.0}}, false},
		{"warning present", Assertion{Kind: "warning", Code: "PULSE_X"}, true},
		{"warning absent", Assertion{Kind: "warning", Code: "PULSE_Y"}, false},
		{"error on success", Assertion{Kind: "error", Code: "PULSE_X"}, false},
		{"any one holds", Assertion{Kind: "any", Assertions: []Assertion{
			{Kind: "operator", AnyOf: []string{"REG_GLM"}},
			{Kind: "sign", Key: "diff", Sign: "negative"},
		}}, true},
		{"any none holds", Assertion{Kind: "any", Assertions: []Assertion{
			{Kind: "operator", AnyOf: []string{"REG_GLM"}},
		}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := check(c.a, outcome(t, args, result))
			if got := msg == ""; got != c.pass {
				t.Fatalf("pass = %v, want %v (message %q)", got, c.pass, msg)
			}
			if !c.pass && msg == "" {
				t.Fatal("a failing assertion must say why")
			}
		})
	}
}

// TestCheck_RunErrors: a failed run fails every task unless an error
// assertion expects its code.
func TestCheck_RunErrors(t *testing.T) {
	t.Parallel()
	failed := &Outcome{Err: perr.NewCodedError(perr.SERVICE_VALIDATION, "boom")}
	expect := &Task{Assertions: []Assertion{{Kind: "error", Code: string(perr.SERVICE_VALIDATION)}}}
	if fails := Check(expect, failed); len(fails) != 0 {
		t.Fatalf("expected error: %v", fails)
	}
	wrong := &Task{Assertions: []Assertion{{Kind: "error", Code: "PULSE_OTHER"}}}
	if fails := Check(wrong, failed); len(fails) != 1 {
		t.Fatalf("wrong code: %v", fails)
	}
	plain := &Task{Assertions: []Assertion{{Kind: "operator", AnyOf: []string{"TEST_T"}}}}
	if fails := Check(plain, failed); len(fails) != 1 || !strings.Contains(fails[0], "run failed") {
		t.Fatalf("unexpected error: %v", fails)
	}
	if fails := Check(plain, &Outcome{Err: errors.New("x")}); len(fails) != 1 {
		t.Fatalf("plain error: %v", fails)
	}
}

// TestParseTask_Refusals: a task file with an unknown key, an unknown
// intent or an unknown assertion kind is refused.
func TestParseTask_Refusals(t *testing.T) {
	t.Parallel()
	const ok = `{"id":"x","intent":"describe","question":"q?","cohorts":["customers"],
		"reference":{"tool":"pulse_process","arguments":{}},"assertions":[{"kind":"rows"}]}`
	if _, err := ParseTask([]byte(ok)); err != nil {
		t.Fatalf("valid task refused: %v", err)
	}
	for name, bad := range map[string]string{
		"unknown key":    strings.Replace(ok, `"id"`, `"idd":1,"id"`, 1),
		"unknown intent": strings.Replace(ok, `"describe"`, `"guess"`, 1),
		"unknown kind":   strings.Replace(ok, `"rows"`, `"rowz"`, 1),
		"empty any":      strings.Replace(ok, `{"kind":"rows"}`, `{"kind":"any"}`, 1),
		"no tool":        strings.Replace(ok, `"pulse_process"`, `""`, 1),
	} {
		if _, err := ParseTask([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

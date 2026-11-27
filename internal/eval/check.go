package eval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Report is a scored submission: Pass when no assertion failed.
type Report struct {
	Task     string   `json:"task"`
	Pass     bool     `json:"pass"`
	Failures []string `json:"failures,omitempty"`
}

// Score runs sub for task on a fresh data directory at dataDir (built
// from fixtureDir) and checks the outcome against the task.
func Score(ctx context.Context, fixtureDir, dataDir string, t *Task, sub Submission) Report {
	env, err := NewEnv(ctx, fixtureDir, dataDir, t)
	if err != nil {
		return Report{Task: t.ID, Failures: []string{"setup: " + err.Error()}}
	}
	return ScoreIn(ctx, env, t, sub)
}

// ScoreIn runs sub for task against an existing env and checks it.
func ScoreIn(ctx context.Context, env *Env, t *Task, sub Submission) Report {
	fails := Check(t, Run(ctx, env, sub))
	return Report{Task: t.ID, Pass: len(fails) == 0, Failures: fails}
}

// Check evaluates every assertion of t over o and returns one message
// per failing assertion (empty: pass). A failed run fails the task
// unless an error assertion expects its code.
func Check(t *Task, o *Outcome) []string {
	var fails []string
	expectsError := false
	for _, a := range t.Assertions {
		if a.Kind == "error" {
			expectsError = true
		}
	}
	if o.Err != nil && !expectsError {
		return []string{"run failed: " + o.Err.Error()}
	}
	for i, a := range t.Assertions {
		if msg := check(a, o); msg != "" {
			fails = append(fails, fmt.Sprintf("assertions[%d] %s: %s", i, a.Kind, msg))
		}
	}
	return fails
}

// check returns "" when a holds over o, else why not.
func check(a Assertion, o *Outcome) string {
	if o.Err != nil && a.Kind != "error" {
		return "run failed: " + o.Err.Error()
	}
	switch a.Kind {
	case "operator":
		used := operatorsIn(o.args)
		for _, op := range a.AnyOf {
			if used[op] {
				return ""
			}
		}
		return fmt.Sprintf("uses none of %v (uses %v)", a.AnyOf, sortedKeys(used))
	case "value":
		nums := numbers(a, o)
		for _, n := range nums {
			if inRange(a, n.v) {
				return ""
			}
		}
		return fmt.Sprintf("no in-scope number satisfies %s (saw %s)", describeRange(a), describeNums(nums))
	case "sign":
		nums := numbers(a, o)
		for _, n := range nums {
			if (a.Sign == "positive" && n.v > 0) || (a.Sign == "negative" && n.v < 0) {
				return ""
			}
		}
		return fmt.Sprintf("no in-scope number is %s (saw %s)", a.Sign, describeNums(nums))
	case "order":
		hi, lo := entryWith(a, o, a.High), entryWith(a, o, a.Low)
		if hi == nil || lo == nil {
			return fmt.Sprintf("no entry holds %q and one holds %q", a.High, a.Low)
		}
		hn, ln := scopedNums(a, o, hi), scopedNums(a, o, lo)
		for k, hv := range hn {
			if lv, ok := ln[k]; ok && hv > lv {
				return ""
			}
		}
		return fmt.Sprintf("no in-scope key puts %q above %q", a.High, a.Low)
	case "top":
		return checkTop(a, o)
	case "rows":
		return checkRows(a, o)
	case "row":
		for _, e := range entries(a, o) {
			if matches(e, a.Match, a.Tol) {
				return ""
			}
		}
		return fmt.Sprintf("no %s entry matches %v", sectionName(a), a.Match)
	case "warning":
		if hasWarning(o.Result, a.Code) {
			return ""
		}
		return "no warning carries " + a.Code
	case "error":
		if o.Err == nil {
			return "the run succeeded; want error " + a.Code
		}
		if got := o.Code(); got != a.Code {
			return fmt.Sprintf("the run failed with %q, want %s", got, a.Code)
		}
		return ""
	case "cohort":
		return checkCohort(a, o)
	case "any":
		var why []string
		for _, sub := range a.Assertions {
			msg := check(sub, o)
			if msg == "" {
				return ""
			}
			why = append(why, sub.Kind+": "+msg)
		}
		return "none holds: " + strings.Join(why, "; ")
	}
	return "unknown kind " + a.Kind
}

// num is one number in the result and the object-key path to it.
type num struct {
	path []string
	v    float64
}

// entries returns the section's entries after the types and in_row
// filters: the elements of an array section, or the section itself.
func entries(a Assertion, o *Outcome) []any {
	root := o.Result
	if a.Section != "" {
		m, _ := root.(map[string]any)
		root = m[a.Section]
	}
	var es []any
	if arr, ok := root.([]any); ok {
		es = arr
	} else if root != nil {
		es = []any{root}
	}
	var out []any
	for _, e := range es {
		m, isObj := e.(map[string]any)
		if len(a.Types) > 0 && (!isObj || !contains(a.Types, fmt.Sprint(m["type"]))) {
			continue
		}
		if len(a.InRow) > 0 && (!isObj || !holdsAll(m, a.InRow)) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// numbers collects every in-scope number.
func numbers(a Assertion, o *Outcome) []num {
	keys := metricKeys(a.Metric, o.args)
	var out []num
	for _, e := range entries(a, o) {
		walk(e, nil, func(path []string, v float64) {
			if keep(a, keys, path) {
				out = append(out, num{path: append([]string(nil), path...), v: v})
			}
		})
	}
	return out
}

// scopedNums is numbers over one entry, keyed by path.
func scopedNums(a Assertion, o *Outcome, e any) map[string]float64 {
	keys := metricKeys(a.Metric, o.args)
	out := map[string]float64{}
	walk(e, nil, func(path []string, v float64) {
		if keep(a, keys, path) {
			out[strings.Join(path, ".")] = v
		}
	})
	return out
}

func keep(a Assertion, metric map[string]bool, path []string) bool {
	if a.Key != "" && !hasSuffix(path, strings.Split(a.Key, ".")) {
		return false
	}
	if a.Metric != nil && (len(path) == 0 || !metric[path[len(path)-1]]) {
		return false
	}
	return true
}

// walk calls f for every number under v with its object-key path
// (array indices are not path segments).
func walk(v any, path []string, f func([]string, float64)) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			walk(e, append(path, k), f)
		}
	case []any:
		for _, e := range x {
			walk(e, path, f)
		}
	case float64:
		f(path, x)
	}
}

func hasSuffix(path, suffix []string) bool {
	if len(suffix) > len(path) {
		return false
	}
	off := len(path) - len(suffix)
	for i, s := range suffix {
		if path[off+i] != s {
			return false
		}
	}
	return true
}

// metricKeys resolves m to the output keys of matching request
// operators: each operator object (one with a "type") of a listed type
// whose "field" is m.Field contributes its label, else TYPE_field.
func metricKeys(m *Metric, args any) map[string]bool {
	keys := map[string]bool{}
	if m == nil {
		return keys
	}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if t, ok := x["type"].(string); ok && contains(m.Types, t) && x["field"] == m.Field {
				if l, ok := x["label"].(string); ok && l != "" {
					keys[l] = true
				} else {
					keys[t+"_"+m.Field] = true
				}
			}
			for _, e := range x {
				visit(e)
			}
		case []any:
			for _, e := range x {
				visit(e)
			}
		}
	}
	visit(args)
	return keys
}

// operatorsIn returns every operator the submission arguments name: each
// "type" string, plus each overlay "kind".
func operatorsIn(args any) map[string]bool {
	used := map[string]bool{}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, k := range []string{"type", "kind"} {
				if t, ok := x[k].(string); ok {
					used[t] = true
				}
			}
			for _, e := range x {
				visit(e)
			}
		case []any:
			for _, e := range x {
				visit(e)
			}
		}
	}
	visit(args)
	return used
}

func inRange(a Assertion, v float64) bool {
	if math.IsNaN(v) {
		return false
	}
	if a.Near != nil && math.Abs(v-*a.Near) > a.Tol {
		return false
	}
	if a.Min != nil && v < *a.Min {
		return false
	}
	if a.Max != nil && v > *a.Max {
		return false
	}
	return true
}

func describeRange(a Assertion) string {
	var parts []string
	if a.Near != nil {
		parts = append(parts, fmt.Sprintf("%g±%g", *a.Near, a.Tol))
	}
	if a.Min != nil {
		parts = append(parts, fmt.Sprintf(">=%g", *a.Min))
	}
	if a.Max != nil {
		parts = append(parts, fmt.Sprintf("<=%g", *a.Max))
	}
	return strings.Join(parts, " and ")
}

func describeNums(nums []num) string {
	if len(nums) == 0 {
		return "no number in scope"
	}
	const most = 8
	var parts []string
	for i, n := range nums {
		if i == most {
			parts = append(parts, fmt.Sprintf("… %d more", len(nums)-most))
			break
		}
		parts = append(parts, fmt.Sprintf("%s=%g", strings.Join(n.path, "."), n.v))
	}
	return strings.Join(parts, ", ")
}

// entryWith is the first in-scope entry holding s as a top-level value.
func entryWith(a Assertion, o *Outcome, s string) any {
	for _, e := range entries(a, o) {
		if m, ok := e.(map[string]any); ok && holdsAll(m, []string{s}) {
			return e
		}
	}
	return nil
}

func holdsAll(m map[string]any, vals []string) bool {
	for _, want := range vals {
		found := false
		for _, v := range m {
			if s, ok := v.(string); ok && s == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func checkTop(a Assertion, o *Outcome) string {
	es := entries(a, o)
	if len(es) < 2 {
		return fmt.Sprintf("%d %s entries; top needs at least 2", len(es), sectionName(a))
	}
	per := make([]map[string]float64, len(es))
	for i, e := range es {
		per[i] = scopedNums(a, o, e)
	}
	for k := range per[0] {
		best, bestIdx := 0.0, -1
		for i, p := range per {
			v, ok := p[k]
			if !ok || math.IsNaN(v) {
				continue
			}
			if bestIdx < 0 || (!a.Lowest && v > best) || (a.Lowest && v < best) {
				best, bestIdx = v, i
			}
		}
		if bestIdx >= 0 {
			if m, ok := es[bestIdx].(map[string]any); ok && holdsAll(m, []string{a.Value}) {
				return ""
			}
		}
	}
	word := "largest"
	if a.Lowest {
		word = "smallest"
	}
	return fmt.Sprintf("the entry holding %q is not the %s on any in-scope key", a.Value, word)
}

func checkRows(a Assertion, o *Outcome) string {
	es := entries(a, o)
	for _, want := range a.Present {
		found := false
		for _, e := range es {
			if m, ok := e.(map[string]any); ok && holdsAll(m, []string{want}) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Sprintf("no %s entry holds %q", sectionName(a), want)
		}
	}
	n := len(es)
	if a.Count != nil && n != *a.Count {
		return fmt.Sprintf("%d %s entries, want %d", n, sectionName(a), *a.Count)
	}
	if a.MinCount != nil && n < *a.MinCount {
		return fmt.Sprintf("%d %s entries, want at least %d", n, sectionName(a), *a.MinCount)
	}
	if a.MaxCount != nil && n > *a.MaxCount {
		return fmt.Sprintf("%d %s entries, want at most %d", n, sectionName(a), *a.MaxCount)
	}
	return ""
}

// matches reports whether e carries every key of want with an equal
// value (numbers within tol).
func matches(e any, want map[string]any, tol float64) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	for k, w := range want {
		got, ok := m[k]
		if !ok {
			return false
		}
		wf, wNum := w.(float64)
		gf, gNum := got.(float64)
		switch {
		case wNum && gNum:
			if math.Abs(wf-gf) > tol {
				return false
			}
		case fmt.Sprint(w) != fmt.Sprint(got):
			return false
		}
	}
	return true
}

func hasWarning(v any, code string) bool {
	switch x := v.(type) {
	case map[string]any:
		if ws, ok := x["warnings"].([]any); ok {
			for _, w := range ws {
				if m, ok := w.(map[string]any); ok && m["code"] == code {
					return true
				}
			}
		}
		for _, e := range x {
			if hasWarning(e, code) {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if hasWarning(e, code) {
				return true
			}
		}
	}
	return false
}

func checkCohort(a Assertion, o *Outcome) string {
	c, ok := o.Cohort.(map[string]any)
	if !ok {
		return "the submission wrote no cohort"
	}
	if a.Rows != nil {
		if got, _ := c["record_count"].(float64); int64(got) != *a.Rows {
			return fmt.Sprintf("the written cohort has %v records, want %d", c["record_count"], *a.Rows)
		}
	}
	types := map[string]string{}
	fields, _ := c["fields"].([]any)
	for _, f := range fields {
		if m, ok := f.(map[string]any); ok {
			types[fmt.Sprint(m["name"])] = fmt.Sprint(m["type"])
		}
	}
	for _, name := range a.Fields {
		if _, ok := types[name]; !ok {
			return fmt.Sprintf("the written cohort has no field %q (fields %v)", name, sortedKeys(types))
		}
	}
	for name, want := range a.FieldTypes {
		if got, ok := types[name]; !ok || got != want {
			return fmt.Sprintf("the written cohort's field %q is %q, want %q", name, got, want)
		}
	}
	return ""
}

func sectionName(a Assertion) string {
	if a.Section == "" {
		return "result"
	}
	return a.Section
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

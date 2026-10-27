package descriptor

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// skill_params_test.go holds every op skill's `## Params` table to the
// manifest declaration of the operator it documents (U38 FR-18 / FR-19).
//
// The one table shape is
//
//	| Name | Type | Default | Description |
//	|---|---|---|---|
//	| `name` | type | default | description |
//
// one row per param, the name alone in backticks. The Default cell is
// `(required)` exactly when the declaration says Required; `—` when it
// declares no default; else the declared default (numbers compared by
// value, a string bare or JSON-quoted, a list / object as compact JSON;
// backticks optional). A skill whose operator declares no params needs
// no table. The Type column is prose and not checked.

// paramsTableHeader is the one accepted header row.
const paramsTableHeader = "| Name | Type | Default | Description |"

// skillParamsSlotKeys are names a Params table may list that are NOT
// operator params: request-level slots (a slot / request `weight`, a
// `multiplicity` block), window spec slots (`partition_by`, `order_by`,
// `frame`), slot keys documented beside the params (`tz`, `tz_field`)
// the slot fields a filterer / ATTR_FORMULA reads instead of params
// (`values`, `expression`) and a matrix slot's `encoding`. They are allowed, never required, and their
// Default cell is not checked.
var skillParamsSlotKeys = map[string]bool{
	"weight": true, "multiplicity": true,
	"partition_by": true, "order_by": true, "frame": true,
	"tz": true, "tz_field": true,
	"values": true, "expression": true,
	"encoding": true,
}

// regressionModifierParams names, per modifier skill operator, the
// REG_OLS slot params it documents (REG_GLM declares the same knobs).
var regressionModifierParams = map[string][]string{
	"REG_RESAMPLE":  {"resample", "jackknife_groups", "jackknife_by", "bootstrap_iters", "rng_seed"},
	"REG_SELECTION": {"selection", "criterion"},
}

// skillParamRow is one parsed table row.
type skillParamRow struct {
	name, typ, def, desc string
}

var skillParamNameCell = regexp.MustCompile("^`([A-Za-z_][A-Za-z0-9_]*)`$")

// skillParamsSection returns the body of a served skill's `## Params`
// section (heading excluded); false when it has none.
func skillParamsSection(body string) (string, bool) {
	i := strings.Index(body, "\n## Params\n")
	if i < 0 {
		return "", false
	}
	rest := body[i+len("\n## Params\n"):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest, true
}

// splitTableRow splits one markdown table row on its unescaped pipes,
// trimming each cell; `\|` stays inside a cell.
func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = line[:len(line)-1]
	}
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cur.WriteString(`\|`)
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

// parseSkillParams parses a Params section: the rows of its one
// normalized table (nil when it has none) and every shape problem.
func parseSkillParams(section string) (rows []skillParamRow, problems []string) {
	lines := strings.Split(section, "\n")
	tables := 0
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") {
			continue
		}
		// A table starts here: its header, then its separator.
		header := strings.Join(strings.Fields(line), " ")
		start := i
		for i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "|") {
			i++
		}
		if header != paramsTableHeader {
			if strings.HasPrefix(header, "| Name ") {
				problems = append(problems, "params table header "+strconv.Quote(header)+", want "+strconv.Quote(paramsTableHeader))
			}
			continue
		}
		tables++
		for k := start + 2; k <= i; k++ {
			cells := splitTableRow(lines[k])
			if len(cells) != 4 {
				problems = append(problems, "row "+strconv.Quote(strings.TrimSpace(lines[k]))+" has "+strconv.Itoa(len(cells))+" cells, want 4")
				continue
			}
			m := skillParamNameCell.FindStringSubmatch(cells[0])
			if m == nil {
				problems = append(problems, "row name cell "+strconv.Quote(cells[0])+" is not one backticked name")
				continue
			}
			rows = append(rows, skillParamRow{name: m[1], typ: cells[1], def: cells[2], desc: cells[3]})
		}
	}
	if tables > 1 {
		problems = append(problems, "more than one params table")
	}
	return rows, problems
}

// skillParamDefaultCell is the Default cell a declaration owes: the
// canonical form (for the error message) and a matcher.
func skillParamDefaultMatches(cell string, p descriptor.Param) (bool, string) {
	switch {
	case p.Required:
		return cell == "(required)", "(required)"
	case p.Default == nil:
		return cell == "—", "—"
	}
	want, _ := json.Marshal(p.Default)
	v := strings.TrimSpace(cell)
	if len(v) >= 2 && strings.HasPrefix(v, "`") && strings.HasSuffix(v, "`") {
		v = v[1 : len(v)-1]
	}
	switch d := p.Default.(type) {
	case string:
		return v == d || v == string(want), "`" + string(want) + "`"
	case float64, float32, int, int64, int32, uint, uint64, uint32:
		got, err := strconv.ParseFloat(v, 64)
		var dv float64
		_ = json.Unmarshal(want, &dv)
		return err == nil && got == dv, "`" + string(want) + "`"
	}
	return v == string(want), "`" + string(want) + "`"
}

// declaredOpParams is every built-in operator's declared params, keyed
// by the name an op skill's frontmatter `operator:` carries: the six
// component categories, tests (family entry first, then each variant's
// params it lacks), post-tests, regressions, matrices, overlay kinds
// and synth distributions.
func declaredOpParams() map[string][]descriptor.Param {
	out := map[string][]descriptor.Param{}
	merge := func(name string, ps []descriptor.Param) {
		have := map[string]bool{}
		for _, p := range out[name] {
			have[p.Name] = true
		}
		if _, ok := out[name]; !ok {
			out[name] = []descriptor.Param{}
		}
		for _, p := range ps {
			if !have[p.Name] {
				out[name] = append(out[name], p)
				have[p.Name] = true
			}
		}
	}
	for _, fam := range [][]descriptor.Operator{
		aggregatorCapabilities(), attributeCapabilities(), filtererCapabilities(),
		grouperCapabilities(), windowCapabilities(), featureCapabilities(),
	} {
		for _, o := range fam {
			merge(o.Name, o.Params)
		}
	}
	tests := append(testCapabilities(), postTestCapabilities()...)
	for _, t := range tests {
		if t.Name == t.Family {
			merge(t.Family, t.Params)
		}
	}
	for _, t := range tests {
		merge(t.Family, t.Params)
	}
	for _, r := range regressionCapabilities() {
		merge(r.Name, r.Params)
	}
	// The two regression modifiers declare no params of their own
	// (descriptor.RegressionModifier carries a name and enum values);
	// their knobs are the host regressions' slot params, so the modifier
	// skills are held to those by name.
	for mod, names := range regressionModifierParams {
		for _, r := range regressionCapabilities() {
			if r.Name != "REG_OLS" {
				continue
			}
			for _, p := range r.Params {
				for _, n := range names {
					if p.Name == n {
						merge(mod, []descriptor.Param{p})
					}
				}
			}
		}
	}
	for _, m := range matrixCapabilities() {
		merge(m.Name, m.Params)
	}
	for _, o := range OverlayCapabilities() {
		merge(string(o.Kind), o.Params)
	}
	for _, d := range distributionCapabilities() {
		merge(d.Name, d.Params)
	}
	return out
}

// skillParamsProblems is every drift between one served skill body's
// Params table and its operator's declaration.
func skillParamsProblems(body string, declared []descriptor.Param) []string {
	section, ok := skillParamsSection(body)
	if !ok {
		return []string{"no ## Params section"}
	}
	rows, problems := parseSkillParams(section)
	byName := map[string]descriptor.Param{}
	for _, p := range declared {
		byName[p.Name] = p
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.name] {
			problems = append(problems, "param `"+r.name+"` listed twice")
		}
		seen[r.name] = true
		p, ok := byName[r.name]
		if !ok {
			if !skillParamsSlotKeys[strings.ToLower(r.name)] {
				problems = append(problems, "lists `"+r.name+"`, which the manifest does not declare")
			}
			continue
		}
		if ok, want := skillParamDefaultMatches(r.def, p); !ok {
			problems = append(problems, "`"+r.name+"` Default "+strconv.Quote(r.def)+", manifest says "+want)
		}
	}
	for _, p := range declared {
		if !seen[p.Name] {
			problems = append(problems, "omits manifest param `"+p.Name+"`")
		}
	}
	return problems
}

// TestSkillParamsMatchManifest: every op skill documents exactly its operator's declared params in
// the one normalized table — names in both directions (request-level
// and spec slots allowed beside them), `(required)` iff Required, the
// default as declared.
func TestSkillParamsMatchManifest(t *testing.T) {
	t.Parallel()

	declared := declaredOpParams()
	walked, ops := 0, 0
	for _, stem := range skills.Names() {
		if !strings.HasPrefix(stem, "op-") {
			continue
		}
		ops++
		raw, _ := skills.Raw(stem)
		op := skills.ParseFrontmatter(raw)["operator"]
		ps, ok := declared[op]
		if !ok {
			t.Errorf("%s: operator %q has no manifest declaration", stem, op)
			continue
		}
		walked++
		body, _ := skills.Get(stem)
		for _, p := range skillParamsProblems(body, ps) {
			t.Errorf("%s: %s", stem, p)
		}
	}
	if walked == 0 || walked != ops {
		t.Fatalf("walked %d of %d op skills; every one must be checked", walked, ops)
	}
}

// TestSkillParamsRegressionModifiers: the two regression modifiers
// declare no params of their own, so their skills are held to the host
// regression's slot params of the same names — and every name resolves.
func TestSkillParamsRegressionModifiers(t *testing.T) {
	t.Parallel()

	declared := declaredOpParams()
	for mod, names := range regressionModifierParams {
		got := map[string]bool{}
		for _, p := range declared[mod] {
			got[p.Name] = true
		}
		for _, n := range names {
			if !got[n] {
				t.Errorf("%s: no REG_OLS slot param %q to hold its skill to", mod, n)
			}
		}
		if len(got) != len(names) {
			t.Errorf("%s declares %d params, want %d", mod, len(got), len(names))
		}
	}
}

// TestSkillParamsParser pins the table parser and the default matcher.
func TestSkillParamsParser(t *testing.T) {
	t.Parallel()

	section := "\n| Name | Type | Default | Description |\n|---|---|---|---|\n" +
		"| `kernel` | enum | (required) | `geometric` \\| `weibull_cdf` |\n" +
		"| `decay` | float | — | geometric only |\n" +
		"| `normalize` | bool | `true` | divide |\n\nProse after.\n"
	rows, problems := parseSkillParams(section)
	if len(problems) != 0 || len(rows) != 3 || rows[0].name != "kernel" || rows[0].desc != "`geometric` \\| `weibull_cdf`" {
		t.Fatalf("rows %+v problems %v", rows, problems)
	}
	if _, problems := parseSkillParams("\n| Name | Type | Description |\n|---|---|---|\n| `a` | int | x |\n"); len(problems) != 1 {
		t.Errorf("a three-column params table must be flagged, got %v", problems)
	}
	if _, problems := parseSkillParams("\n| Name | Type | Default | Description |\n|---|---|---|---|\n| `a` / `b` | int | — | x |\n"); len(problems) != 1 {
		t.Errorf("a two-name row must be flagged, got %v", problems)
	}
	cases := []struct {
		cell string
		p    descriptor.Param
		ok   bool
	}{
		{"(required)", descriptor.Param{Required: true}, true},
		{"—", descriptor.Param{Required: true}, false},
		{"—", descriptor.Param{}, true},
		{"`0`", descriptor.Param{}, false},
		{"`1.0`", descriptor.Param{Default: 1}, true},
		{"`0.95`", descriptor.Param{Default: 0.95}, true},
		{"`2`", descriptor.Param{Default: 1}, false},
		{"`normal`", descriptor.Param{Default: "normal"}, true},
		{"`\"normal\"`", descriptor.Param{Default: "normal"}, true},
		{"`\"\"`", descriptor.Param{Default: ""}, true},
		{"`true`", descriptor.Param{Default: true}, true},
		{"`false`", descriptor.Param{Default: true}, false},
	}
	for _, c := range cases {
		if ok, _ := skillParamDefaultMatches(c.cell, c.p); ok != c.ok {
			t.Errorf("cell %q vs %+v: match %v, want %v", c.cell, c.p, ok, c.ok)
		}
	}
	// Names both ways, slot keys allowed.
	got := skillParamsProblems("\n## Params\n\n| Name | Type | Default | Description |\n|---|---|---|---|\n| `a` | int | `1` | x |\n| `weight` | slot | inherited | x |\n| `z` | int | — | x |\n\n## Inputs\n",
		[]descriptor.Param{{Name: "a", Default: 1}, {Name: "b"}})
	if len(got) != 2 || !strings.Contains(got[0], "`z`") || !strings.Contains(got[1], "`b`") {
		t.Errorf("problems %v, want an undeclared z and an omitted b", got)
	}
}

package processing

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// TestExprDocExamples_Compile compiles every FILTER_EXPRESSION /
// ATTR_FORMULA expression example the skill pack and the mdBook show,
// through the real build path (newExprProgram with the runtime's
// ExprOptions), against a synthetic schema declaring every field the
// examples name. It exists because the pack once documented
// `contains(tags, "x")` as a set helper: `contains` is an expr-lang
// operator keyword, so that call can never parse.
//
// Harvested examples:
//   - JSON `"expression": "..."` string values;
//   - Go `Expression: "..."` string literals (a non-literal such as
//     fmt.Sprintf is skipped);
//   - inline (single-backtick, outside fenced blocks) spans that call a
//     set helper, `contains(` or a reserved vector / scalar-maths
//     built-in (exprenv.Names; expression-host pages only), test `"label" in field`, or show a
//     `field % n` modulo.
//
// An example naming a field the synthetic schema lacks fails rather
// than silently deferring its compile: add the field to docExprSchema.
//
// An example reading `params.<name>` compiles with the params its own
// fenced block declares (every `"params": {...}` object in the fence —
// a request's, a Compose batch's or a chain's), folded as the runtime
// folds them (ExtensionRegistry.WithParams); a name the fence does not
// declare fails (exprenv.ParamsRefusal), never a fixture-wide default.
func TestExprDocExamples_Compile(t *testing.T) {
	t.Parallel()

	exprs := harvestDocExpressions(t)
	if len(exprs) < 6 {
		t.Fatalf("harvested only %d expression examples; the extractor is broken", len(exprs))
	}
	schema := docExprSchema()
	reg := &ExtensionRegistry{LookupTables: map[string]LookupTable{
		"adjustments": {Rows: map[string]float64{"x": 1}},
	}}
	// A documented named vector (Request.Vectors) binds by name.
	vecs, verr := vectors.Resolve([]types.VectorSpec{{Name: "battery", Fields: []string{"q1", "q2", "q3"}}}, schema)
	if verr != nil {
		t.Fatal(verr)
	}
	for _, ex := range exprs {
		exReg := reg
		if uses, _ := exprenv.ReadParams(ex.src); len(uses.Names) > 0 {
			params, ce := exprenv.NormalizeParams(ex.params)
			if ce != nil {
				t.Errorf("%s: the params declared beside %q are invalid: %v", ex.where, ex.src, ce)
				continue
			}
			if ce := exprenv.ParamsRefusal(ex.src, params, schema.Field(exprenv.ParamsName) != nil); ce != nil {
				t.Errorf("%s: documented expression %q reads a param its example does not declare: %v", ex.where, ex.src, ce)
				continue
			}
			exReg = reg.WithParams(params)
		}
		// Compiled at the formula site: the harvest mixes filter and
		// formula examples, and only a filter must yield a bool
		// (exprenv.ResultRefusal).
		// coef and sweep_axis are request-bound (a compose binds them); the
		// declaration-only placeholders type-check exactly like the real ones.
		bound := exReg.exprBound()
		bound.Coef, bound.SweepAxis = exprenv.Placeholder, exprenv.Placeholder
		p, err := newExprProgram(ex.src, exprenv.SiteFormula, schema, vecs, exReg.ReferenceVectors, bound.Options())
		if err != nil {
			t.Errorf("%s: documented expression %q does not compile: %v", ex.where, ex.src, err)
			continue
		}
		if p.typed.Load() == nil {
			t.Errorf("%s: documented expression %q names a field docExprSchema lacks (refs %v); add it",
				ex.where, ex.src, p.refs)
		}
	}
}

// docExprSchema declares every field name the documented expressions use.
func docExprSchema() *encoding.Schema {
	cat := func(name string, labels ...string) encoding.Field {
		d := encoding.NewDictionary()
		for _, l := range labels {
			_, _ = d.Add(l)
		}
		return encoding.Field{Name: name, Type: encoding.FieldTypeCategoricalU8, Dictionary: d, Nullable: true}
	}
	set := func(name string, labels ...string) encoding.Field {
		f := cat(name, labels...)
		f.Type = encoding.FieldTypeSetU8
		return f
	}
	num := func(name string) encoding.Field {
		return encoding.Field{Name: name, Type: encoding.FieldTypeF64, Nullable: true}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "age", Type: encoding.FieldTypeU8, Nullable: true},
		cat("state", "CA", "NY", "TX"),
		cat("region", "NA", "EU", "APAC"),
		cat("brand", "Apple", "Samsung", "Other"),
		cat("study", "s1", "s2"),
		cat("panel", "a", "b"),
		{Name: "wave_date", Type: encoding.FieldTypeDate, Nullable: true},
		{Name: "order_day", Type: encoding.FieldTypeDate, Nullable: true},
		{Name: "signup_day", Type: encoding.FieldTypeDate, Nullable: true},
		{Name: "created_at", Type: encoding.FieldTypeDateTime, Nullable: true},
		num("spend"),
		num("weight_kg"), num("height_m"), num("score"),
		num("q1"), num("q2"), num("q3"),
		num("week"), num("wave"), num("x"), num("y"),
		// the one-call MMM recipe (mmm.md): a channel, its lag columns, its adstock.
		num("tv"), num("tv_ad"),
		num("tv_l1"),
		num("tv_l2"),
		num("tv_l3"),
		num("tv_l4"),
		num("tv_l5"),
		num("tv_l6"),
		num("tv_l7"),
		num("tv_l8"),
		num("tv_l9"),
		num("tv_l10"),
		num("tv_l11"),
		num("tv_l12"),
		num("tv_l13"),
		set("tags", "a", "b", "c"),
		set("issuers", "VISA", "MC", "AMEX"),
	}}
}

type docExpr struct {
	where, src string
	// params are the `params` objects declared in the expression's own
	// fenced block, merged (nil outside a fence or with none).
	params map[string]any
}

// docParamsRe is a one-line `"params": {...}` object, the fallback for
// a fence that is not a whole JSON document.
var docParamsRe = regexp.MustCompile(`"params"\s*:\s*(\{[^{}]*\})`)

// fenceParams merges every request-level `params` object in one fenced
// block: each `"params"` key whose value is an object of scalars, lists
// and `{"$var": name}` markers (an operator's own params object holding
// any other nested object is not request params). A marker resolves to
// a sample of what fills it — the first value of a sweep axis of that
// name, else a zero of the template variable's declared type, else 0.
// A fence that does not decode as JSON falls back to its one-line
// `"params": {...}` objects.
func fenceParams(body string) map[string]any {
	out := map[string]any{}
	samples := map[string]any{}
	marker := func(v any) (string, bool) {
		m, ok := v.(map[string]any)
		if !ok || len(m) != 1 {
			return "", false
		}
		name, ok := m["$var"].(string)
		return name, ok
	}
	merge := func(m map[string]any) {
		for _, v := range m {
			if _, nested := v.(map[string]any); nested {
				if _, ok := marker(v); !ok {
					return
				}
			}
		}
		for k, v := range m {
			if name, ok := marker(v); ok {
				if s, found := samples[name]; found {
					v = s
				} else {
					v = json.Number("0")
				}
			}
			out[k] = v
		}
	}
	// collect records each sweep axis's first value and each template
	// variable's zero, so a marker binds a value of the right type.
	var collect func(any)
	collect = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if name, ok := x["name"].(string); ok {
				if vals, ok := x["values"].([]any); ok && len(vals) > 0 {
					samples[name] = vals[0]
				} else if typ, ok := x["type"].(string); ok {
					switch typ {
					case "string":
						samples[name] = ""
					case "bool":
						samples[name] = false
					case "number", "integer":
						samples[name] = json.Number("0")
					}
				}
			}
			for _, e := range x {
				collect(e)
			}
		case []any:
			for _, e := range x {
				collect(e)
			}
		}
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				if m, ok := e.(map[string]any); ok && k == "params" {
					merge(m)
				}
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	// A fence is either a whole JSON document or a `"key": {...}`
	// fragment of one; try both before the one-line fallback.
	var doc any
	decoded := false
	for _, src := range []string{body, "{" + body + "}"} {
		dec := json.NewDecoder(strings.NewReader(src))
		dec.UseNumber()
		// A fragment's leading `"key"` decodes as a bare string; only an
		// object or array is a whole document.
		if dec.Decode(&doc) == nil {
			switch doc.(type) {
			case map[string]any, []any:
				decoded = true
			}
			if decoded {
				break
			}
		}
	}
	if decoded {
		collect(doc)
		walk(doc)
	} else {
		for _, m := range docParamsRe.FindAllStringSubmatch(body, -1) {
			d := json.NewDecoder(strings.NewReader(m[1]))
			d.UseNumber()
			var obj map[string]any
			if d.Decode(&obj) == nil {
				merge(obj)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var (
	docJSONExprRe   = regexp.MustCompile(`"expression"\s*:\s*("(?:[^"\\]|\\.)*")`)
	docGoExprRe     = regexp.MustCompile(`Expression:\s*("(?:[^"\\]|\\.)*")\s*,?\s*$`)
	docFenceRe      = regexp.MustCompile("(?ms)^\\s*```.*?^\\s*```")
	docInlineCodeRe = regexp.MustCompile("`([^`\n]+)`")
	docSetCallRe    = regexp.MustCompile(`^(contains|has_any|has_all|has_none|popcount|set_union|set_intersect|set_diff|set_xor)\(.*\)$`)
	// docBuiltinCallRe: an inline call of a reserved vector /
	// scalar-maths built-in (vmean(...), pow(...), ...).
	docBuiltinCallRe = regexp.MustCompile(`^(` + strings.Join(exprenv.Names(), "|") + `)\(.*\)$`)
	docExprHostPages = map[string]bool{
		"expression-language.md": true, "op-attr-formula.md": true, "op-filter-expression.md": true,
	}
	docInLabelRe = regexp.MustCompile(`^"[^"]+" in [A-Za-z_][A-Za-z0-9_]*$`)
	// docModRe: an inline `field % n` example (float modulo, internal/exprenv mod.go).
	docModRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]* % [0-9.]+( == [0-9.]+)?$`)
)

func harvestDocExpressions(t *testing.T) []docExpr {
	t.Helper()
	var files []string
	skills, err := filepath.Glob(filepath.Join("..", "..", "internal", "skills", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, skills...)
	err = filepath.WalkDir(filepath.Join("..", "..", "docs", "src"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)

	seen := map[string]bool{}
	var out []docExpr
	add := func(where, src string, params map[string]any) {
		if src == "" || seen[src] {
			return
		}
		seen[src] = true
		out = append(out, docExpr{where: where, src: src, params: params})
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		lines := strings.Split(text, "\n")
		// lineParams[i]: the params declared in line i's fenced block.
		lineParams := make([]map[string]any, len(lines))
		for i := 0; i < len(lines); i++ {
			if !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				continue
			}
			end := i + 1
			for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), "```") {
				end++
			}
			if params := fenceParams(strings.Join(lines[i+1:min(end, len(lines))], "\n")); params != nil {
				for j := i + 1; j < end; j++ {
					lineParams[j] = params
				}
			}
			i = end
		}
		for i, line := range lines {
			where := path + ":" + strconv.Itoa(i+1)
			for _, m := range docJSONExprRe.FindAllStringSubmatch(line, -1) {
				var s string
				if err := json.Unmarshal([]byte(m[1]), &s); err != nil {
					t.Errorf("%s: undecodable JSON expression %s: %v", where, m[1], err)
					continue
				}
				add(where, s, lineParams[i])
			}
			if m := docGoExprRe.FindStringSubmatch(line); m != nil {
				s, err := strconv.Unquote(m[1])
				if err != nil {
					t.Errorf("%s: undecodable Go expression %s: %v", where, m[1], err)
					continue
				}
				add(where, s, lineParams[i])
			}
		}
		// Built-in calls are harvested only from the expression hosts'
		// pages: elsewhere `sqrt(...)` / `exp(...)` spans are maths
		// notation (overlay and synth formulas), not expressions.
		exprHost := docExprHostPages[filepath.Base(path)]
		for _, m := range docInlineCodeRe.FindAllStringSubmatch(docFenceRe.ReplaceAllString(text, ""), -1) {
			span := strings.TrimSpace(m[1])
			if docSetCallRe.MatchString(span) || (exprHost && docBuiltinCallRe.MatchString(span)) || docInLabelRe.MatchString(span) || docModRe.MatchString(span) {
				add(path, span, nil)
			}
		}
	}
	return out
}

// TestExprBuiltinsDocumented pins the inventory: every reserved
// vector / scalar-maths built-in is named, backticked, in the
// expression-language skill, so a function added to exprenv
// without a documented row fails here.
func TestExprBuiltinsDocumented(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "skills", "expression-language.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range exprenv.Names() {
		re := regexp.MustCompile("`" + regexp.QuoteMeta(name) + "[`(]")
		if !re.Match(raw) {
			t.Errorf("expression-language.md does not document the built-in %q (want a `%s` or `%s(` span)", name, name, name)
		}
	}
}

// TestExprDocExamples_HarvestsBuiltinCalls guards the harvest itself:
// the inventory's inline built-in calls must reach the compile check.
func TestExprDocExamples_HarvestsBuiltinCalls(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"vmax(vzscore([q1, q2, q3]))": false, "pow(score, 2)": false,
		// attribute-composition's named-vector worked example.
		"vcount(battery) >= 6 ? vmean(battery) : nil": false, "vsd(battery) == 0": false,
	}
	for _, ex := range harvestDocExpressions(t) {
		if _, ok := want[ex.src]; ok {
			want[ex.src] = true
		}
	}
	for src, got := range want {
		if !got {
			t.Errorf("inline built-in call %q was not harvested", src)
		}
	}
}

// TestExprDocExamples_HarvestsFenceParams guards the params harvest:
// request-params' worked example reaches the compile check carrying
// the params its fence declares.
func TestExprDocExamples_HarvestsFenceParams(t *testing.T) {
	t.Parallel()

	const src = "panel == params.from_wave && age >= params.min_age"
	for _, ex := range harvestDocExpressions(t) {
		if ex.src != src {
			continue
		}
		if _, ok := ex.params["from_wave"]; !ok {
			t.Fatalf("%s: harvested params %v lack from_wave", ex.where, ex.params)
		}
		if _, ok := ex.params["min_age"]; !ok {
			t.Fatalf("%s: harvested params %v lack min_age", ex.where, ex.params)
		}
		return
	}
	t.Fatalf("request-params example %q was not harvested", src)
}

// TestExprDocExamples_FenceParamsResolveMarkers guards the marker arm
// of fenceParams: a `{"$var": name}` params entry binds a sample of the
// value that fills it — a sweep axis's first value, else a zero of the
// template variable's declared type — and a `"key": {...}` fragment
// fence decodes as the object it belongs to, never as its bare key.
func TestExprDocExamples_FenceParamsResolveMarkers(t *testing.T) {
	t.Parallel()

	sweep := `"sweep": {
  "axes": [{"name": "w", "values": [2, 3]}],
  "request": {"params": {"w": {"$var": "w"}}}
}`
	if got := fenceParams(sweep); got["w"] != json.Number("2") {
		t.Fatalf("sweep fragment: params %v, want w = the axis's first value 2", got)
	}
	tmpl := `{"variables": [{"name": "label", "type": "string"}],
 "body": {"params": {"label": {"$var": "label"}}}}`
	if got := fenceParams(tmpl); got["label"] != "" {
		t.Fatalf("template: params %v, want label = the string zero", got)
	}
	op := `{"params": {"window": {"size": 3}}}`
	if got := fenceParams(op); got != nil {
		t.Fatalf("operator params object: harvested %v, want none", got)
	}
}

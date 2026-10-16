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
func TestExprDocExamples_Compile(t *testing.T) {
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
		p, err := newExprProgram(ex.src, "filter", schema, vecs, reg.ReferenceVectors, reg.ExprOptions())
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
		{Name: "wave_date", Type: encoding.FieldTypeDate, Nullable: true},
		{Name: "order_day", Type: encoding.FieldTypeDate, Nullable: true},
		{Name: "signup_day", Type: encoding.FieldTypeDate, Nullable: true},
		{Name: "created_at", Type: encoding.FieldTypeDateTime, Nullable: true},
		num("spend"),
		num("weight_kg"), num("height_m"), num("score"),
		num("q1"), num("q2"), num("q3"),
		set("tags", "a", "b", "c"),
		set("issuers", "VISA", "MC", "AMEX"),
	}}
}

type docExpr struct{ where, src string }

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
	add := func(where, src string) {
		if src == "" || seen[src] {
			return
		}
		seen[src] = true
		out = append(out, docExpr{where: where, src: src})
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for i, line := range strings.Split(text, "\n") {
			where := path + ":" + strconv.Itoa(i+1)
			for _, m := range docJSONExprRe.FindAllStringSubmatch(line, -1) {
				var s string
				if err := json.Unmarshal([]byte(m[1]), &s); err != nil {
					t.Errorf("%s: undecodable JSON expression %s: %v", where, m[1], err)
					continue
				}
				add(where, s)
			}
			if m := docGoExprRe.FindStringSubmatch(line); m != nil {
				s, err := strconv.Unquote(m[1])
				if err != nil {
					t.Errorf("%s: undecodable Go expression %s: %v", where, m[1], err)
					continue
				}
				add(where, s)
			}
		}
		// Built-in calls are harvested only from the expression hosts'
		// pages: elsewhere `sqrt(...)` / `exp(...)` spans are maths
		// notation (overlay and synth formulas), not expressions.
		exprHost := docExprHostPages[filepath.Base(path)]
		for _, m := range docInlineCodeRe.FindAllStringSubmatch(docFenceRe.ReplaceAllString(text, ""), -1) {
			span := strings.TrimSpace(m[1])
			if docSetCallRe.MatchString(span) || (exprHost && docBuiltinCallRe.MatchString(span)) || docInLabelRe.MatchString(span) || docModRe.MatchString(span) {
				add(path, span)
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

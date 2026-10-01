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
//     set helper or `contains(`, test `"label" in field`, or show a
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
	for _, ex := range exprs {
		p, err := newExprProgram(ex.src, "filter", schema, reg.ExprOptions())
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
		num("weight_kg"), num("height_m"), num("score"),
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
	docInLabelRe    = regexp.MustCompile(`^"[^"]+" in [A-Za-z_][A-Za-z0-9_]*$`)
	// docModRe: an inline `field % n` example (float modulo, expr_mod.go).
	docModRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]* % [0-9.]+( == [0-9.]+)?$`)
)

func harvestDocExpressions(t *testing.T) []docExpr {
	t.Helper()
	var files []string
	skills, err := filepath.Glob(filepath.Join("..", "internal", "skills", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, skills...)
	err = filepath.WalkDir(filepath.Join("..", "docs", "src"), func(path string, d fs.DirEntry, err error) error {
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
		for _, m := range docInlineCodeRe.FindAllStringSubmatch(docFenceRe.ReplaceAllString(text, ""), -1) {
			span := strings.TrimSpace(m[1])
			if docSetCallRe.MatchString(span) || docInLabelRe.MatchString(span) || docModRe.MatchString(span) {
				add(path, span)
			}
		}
	}
	return out
}

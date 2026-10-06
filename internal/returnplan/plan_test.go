package returnplan

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func mustParse(t *testing.T, ss ...string) []Path {
	t.Helper()
	out := make([]Path, len(ss))
	for i, s := range ss {
		p, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		out[i] = p
	}
	return out
}

// TestParse_Grammar: well-formed paths round-trip to their canonical
// spelling; malformed ones are SyntaxErrors naming the rule.
func TestParse_Grammar(t *testing.T) {
	for _, c := range []struct {
		in   string
		segs []Segment
	}{
		{"data", []Segment{Key("data")}},
		{"data[*].region", []Segment{Key("data"), Elem(), Key("region")}},
		{"matrices[*].primary.values[*][*]", []Segment{Key("matrices"), Elem(), Key("primary"), Key("values"), Elem(), Elem()}},
		{"tests[*].details.effect_*", []Segment{Key("tests"), Elem(), Key("details"), {Name: "effect_", Glob: true}}},
		{"data[*].*", []Segment{Key("data"), Elem(), {Glob: true}}},
		{"data[*].first name", []Segment{Key("data"), Elem(), Key("first name")}},
		{"components.aggregations[*].groups[*].operator.mean", []Segment{Key("components"), Key("aggregations"), Elem(), Key("groups"), Elem(), Key("operator"), Key("mean")}},
	} {
		p, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if !slices.Equal(p.Segments, c.segs) {
			t.Errorf("Parse(%q) = %+v, want %+v", c.in, p.Segments, c.segs)
		}
		if p.String() != c.in {
			t.Errorf("Parse(%q).String() = %q", c.in, p.String())
		}
	}
	for _, in := range []string{"", ".data", "data.", "data..x", "[*]", "data[0]", "data[*", "da*ta", "data]x", "data[*]x", "a.*b"} {
		_, err := Parse(in)
		var se *SyntaxError
		if err == nil {
			t.Errorf("Parse(%q) accepted a malformed path", in)
			continue
		}
		if se, _ = err.(*SyntaxError); se == nil || se.Reason == "" {
			t.Errorf("Parse(%q) error %v is not a SyntaxError with a reason", in, err)
		}
	}
}

func verdict(p *Plan, path ...string) Verdict {
	var segs []Segment
	for _, s := range path {
		if s == "[*]" {
			segs = append(segs, Elem())
		} else {
			segs = append(segs, Key(s))
		}
	}
	return p.Visit(segs)
}

var rootKeys = []string{"data", "metadata", "tests", "warnings", "components"}

// TestVisit_IncludeOnlyAllowlist: an include-only plan emits the
// ancestors of a selected path as containers and drops siblings.
func TestVisit_IncludeOnlyAllowlist(t *testing.T) {
	p := New(PresetCustom, mustParse(t, "data[*].region", "warnings"), nil, nil, 0, rootKeys)
	for _, c := range []struct {
		path []string
		want Verdict
	}{
		{nil, Verdict{Keep: true}},
		{[]string{"data"}, Verdict{Keep: true}},
		{[]string{"data", "[*]"}, Verdict{Keep: true}},
		{[]string{"data", "[*]", "region"}, Verdict{Keep: true, Whole: true}},
		{[]string{"data", "[*]", "revenue"}, Verdict{}},
		{[]string{"tests"}, Verdict{}},
		{[]string{"warnings"}, Verdict{Keep: true, Whole: true}},
	} {
		if got := verdict(p, c.path...); got != c.want {
			t.Errorf("Visit(%v) = %+v, want %+v", c.path, got, c.want)
		}
	}
	if p.SelectsAll || p.Identity() {
		t.Error("an allowlist must not select the whole root")
	}
}

// TestVisit_ExcludeWins: an exclude removes a node even beneath an
// include, and makes its ancestor non-whole.
func TestVisit_ExcludeWins(t *testing.T) {
	p := New("full", mustParse(t, "data", "metadata", "tests", "warnings", "components", "tests[*].p_value"),
		mustParse(t, "tests[*].details", "components"), nil, 0, rootKeys)
	if got := verdict(p, "tests", "[*]", "details"); got.Keep {
		t.Errorf("excluded node kept: %+v", got)
	}
	if got := verdict(p, "components"); got.Keep {
		t.Errorf("excluded top-level slot kept: %+v", got)
	}
	if got := verdict(p, "tests"); !got.Keep || got.Whole {
		t.Errorf("ancestor of an exclusion = %+v, want keep, not whole", got)
	}
	if got := verdict(p, "data"); got != (Verdict{Keep: true, Whole: true}) {
		t.Errorf("untouched slot = %+v, want whole", got)
	}
	if got := verdict(p); got.Whole {
		t.Error("a plan with an exclusion must not be whole at the root")
	}
	// tests[*].p_value is covered by tests; components by its exclude.
	if !slices.Equal(Strings(p.Include), []string{"data", "metadata", "tests", "warnings"}) {
		t.Errorf("include = %v", Strings(p.Include))
	}
}

// TestVisit_KeepRetainsNestedWarnings: a keep path is emitted wherever
// its parent is, never conjures its parent, and yields to an exclude.
func TestVisit_KeepRetainsNestedWarnings(t *testing.T) {
	keep := mustParse(t, "tests[*].warnings")
	p := New(PresetCustom, mustParse(t, "tests[*].p_value"), nil, keep, 0, rootKeys)
	if got := verdict(p, "tests", "[*]", "warnings"); got != (Verdict{Keep: true, Whole: true}) {
		t.Errorf("nested warnings under an emitted test = %+v, want kept", got)
	}
	q := New(PresetCustom, mustParse(t, "data"), nil, keep, 0, rootKeys)
	if got := verdict(q, "tests"); got.Keep {
		t.Errorf("a keep path conjured its parent: %+v", got)
	}
	r := New(PresetCustom, mustParse(t, "tests[*].p_value"), mustParse(t, "tests[*].warnings"), keep, 0, rootKeys)
	if got := verdict(r, "tests", "[*]", "warnings"); got.Keep {
		t.Errorf("explicitly excluded warnings kept: %+v", got)
	}
}

// TestVisit_Globs: a suffix glob selects every matching map key.
func TestVisit_Globs(t *testing.T) {
	p := New(PresetCustom, mustParse(t, "tests[*].details.effect_*"), nil, nil, 0, rootKeys)
	if got := verdict(p, "tests", "[*]", "details", "effect_size"); !got.Whole {
		t.Errorf("glob match = %+v", got)
	}
	if got := verdict(p, "tests", "[*]", "details", "n"); got.Keep {
		t.Errorf("glob non-match kept: %+v", got)
	}
}

// TestNew_IdentityAndDigest: the full selection is the identity; the
// digest ignores spelling order, duplicates, covered paths, the preset
// name and irrelevant excludes, and moves with precision.
func TestNew_IdentityAndDigest(t *testing.T) {
	full := New("full", mustParse(t, rootKeys...), nil, mustParse(t, "tests[*].warnings"), 0, rootKeys)
	if !full.Identity() || !full.SelectsAll {
		t.Fatalf("full plan is not the identity: %+v", full)
	}
	if got := verdict(full); got != (Verdict{Keep: true, Whole: true}) {
		t.Errorf("identity root = %+v", got)
	}
	if len(full.Keep) != 0 {
		t.Errorf("keep paths covered by the selection must drop: %v", Strings(full.Keep))
	}
	reordered := New(PresetCustom, mustParse(t, "warnings", "components", "data", "tests", "metadata", "data", "tests[*].p_value"),
		mustParse(t, "nothing_selected.x"), nil, 0, rootKeys)
	if reordered.Digest != full.Digest {
		t.Errorf("equivalent spellings digest differently: %s vs %s", reordered.Digest, full.Digest)
	}
	if !strings.HasPrefix(full.Digest, DigestPrefix) {
		t.Errorf("digest %q lacks %q", full.Digest, DigestPrefix)
	}
	rounded := New("full", mustParse(t, rootKeys...), nil, nil, 4, rootKeys)
	if rounded.Digest == full.Digest || rounded.Identity() {
		t.Error("precision must move the digest and break identity")
	}
	var nilPlan *Plan
	if !nilPlan.Identity() || nilPlan.Visit(nil) != (Verdict{Keep: true, Whole: true}) {
		t.Error("a nil plan is the identity")
	}
}

// TestReturnPlan_ImportBoundary: the leaf imports the standard library
// only, so the public types package can carry a plan without a new
// dependency.
func TestReturnPlan_ImportBoundary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(path, ".") {
				t.Errorf("%s imports %s; internal/returnplan must stay stdlib-only", f, path)
			}
		}
	}
}

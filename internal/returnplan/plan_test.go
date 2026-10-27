package returnplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	t.Parallel()

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
	for _, in := range []string{"", ".data", "data.", "data..x", "[*]", "data[0]", "data[*", "da*ta", "data]x", "data[*]x", "a.*b",
		`data[*]."m[a]`, `data[*].""`, `data[*]."m"x`, `data[*]."m\x"`, `data[*]."m\"`, `data[*]."m"]`, `"a"*b`} {
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

// TestParse_QuotedSegment: a quoted name spells any key — an expanded
// vector element column's brackets, a dot, a star, a quote — and the
// canonical form quotes exactly the names the bare form cannot spell,
// so every bare-spellable path keeps its spelling.
func TestParse_QuotedSegment(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		in, canon string
		segs      []Segment
	}{
		{`data[*]."emb_mean[q3]"`, `data[*]."emb_mean[q3]"`, []Segment{Key("data"), Elem(), Key("emb_mean[q3]")}},
		{`data[*]."emb_mean["*`, `data[*]."emb_mean["*`, []Segment{Key("data"), Elem(), {Name: "emb_mean[", Glob: true}}},
		{`data[*]."a.b*c"`, `data[*]."a.b*c"`, []Segment{Key("data"), Elem(), Key("a.b*c")}},
		{`data[*]."say \"hi\" \\ [x]"`, `data[*]."say \"hi\" \\ [x]"`, []Segment{Key("data"), Elem(), Key(`say "hi" \ [x]`)}},
		{`data[*]."\"lead"`, `data[*]."\"lead"`, []Segment{Key("data"), Elem(), Key(`"lead`)}},
		// Quoting a name the bare form can spell is accepted and
		// canonicalizes bare.
		{`"data"[*]."region"`, `data[*].region`, []Segment{Key("data"), Elem(), Key("region")}},
		{`"tests"[*]."effect_"*`, `tests[*].effect_*`, []Segment{Key("tests"), Elem(), {Name: "effect_", Glob: true}}},
		// A quote inside a bare name stays literal.
		{`data[*].a"b\c`, `data[*].a"b\c`, []Segment{Key("data"), Elem(), Key(`a"b\c`)}},
	} {
		p, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if !slices.Equal(p.Segments, c.segs) {
			t.Errorf("Parse(%q) = %+v, want %+v", c.in, p.Segments, c.segs)
		}
		if got := p.String(); got != c.canon {
			t.Errorf("Parse(%q).String() = %q, want %q", c.in, got, c.canon)
		}
		if back, err := Parse(p.String()); err != nil || !slices.Equal(back.Segments, p.Segments) {
			t.Errorf("canonical %q does not round-trip: %+v, %v", p.String(), back.Segments, err)
		}
	}
	// A quoted element path matches the concrete key the walker visits.
	p := mustParse(t, `data[*]."emb_mean[q3]"`)[0]
	if !p.Matches([]Segment{Key("data"), Elem(), Key("emb_mean[q3]")}) || p.Matches([]Segment{Key("data"), Elem(), Key("emb_mean")}) {
		t.Error("a quoted segment must match exactly its unescaped key")
	}
	if q := Quote(`a"b\c`); q != `"a\"b\\c"` {
		t.Errorf("Quote = %s", q)
	}
}

// TestNew_DigestStableAcrossQuoting pins the digest of a plan spelled
// in the bare grammar: adding quoted segments moved no existing
// spelling, so no existing plan's rp1 digest moved either.
func TestNew_DigestStableAcrossQuoting(t *testing.T) {
	t.Parallel()

	p := New(PresetCustom,
		mustParse(t, "data[*].region", "tests[*].details.effect_*", "data[*].first name", "matrices[*].primary.values[*][*]", `data[*].a"b\c`),
		mustParse(t, "metadata.x*", "components.aggregations[*].groups[*].operator.mean"),
		mustParse(t, "tests[*].warnings"), 3, rootKeys)
	const want = "rp1:2b71f5b7ddb0fc2a59e7c54a9dd3cd595a39fdb130ab75d21472d81b2650c306"
	if p.Digest != want {
		t.Errorf("digest = %s, want the pre-quoting %s", p.Digest, want)
	}
	// The same selection spelled with redundant quotes digests the same.
	q := New(PresetCustom,
		mustParse(t, `"data"[*]."region"`, "tests[*].details.effect_*", `data[*]."first name"`, "matrices[*].primary.values[*][*]", `data[*].a"b\c`),
		mustParse(t, `metadata."x"*`, "components.aggregations[*].groups[*].operator.mean"),
		mustParse(t, "tests[*].warnings"), 3, rootKeys)
	if q.Digest != want {
		t.Errorf("redundantly quoted digest = %s, want %s", q.Digest, want)
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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

func TestPath_Matches(t *testing.T) {
	t.Parallel()

	mustParse := func(s string) Path {
		p, err := Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	c := []Segment{Key("tests"), Elem(), Key("details"), Key("effect_size")}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"tests[*].details.effect_size", true},
		{"tests[*].details.effect_*", true},
		{"tests[*].details.*", true},
		{"tests[*].details", false}, // an ancestor, not this node
		{"tests[*].details.effect_size.x", false},
		{"tests[*].details.p_*", false},
	} {
		if got := mustParse(tc.path).Matches(c); got != tc.want {
			t.Errorf("%s.Matches(%v) = %v, want %v", tc.path, c, got, tc.want)
		}
	}
}

// TestVisitIn_Companions (U28 E4-G1): a companion key is kept whole only
// beside a sibling the parent holds AND the plan keeps; Visit alone
// never keeps it; an exclude covering it (or its sibling) wins, an
// include covering it makes it unconditional, and a plan without
// companions digests as New always did.
func TestVisitIn_Companions(t *testing.T) {
	t.Parallel()

	comp := []Companion{{Path: mustParse(t, "m[*].aux.n")[0], With: "p"}}
	n := []Segment{Key("m"), Elem(), Key("aux"), Key("n")}
	has := func(keys ...string) func(string) bool {
		return func(k string) bool { return slices.Contains(keys, k) }
	}
	for _, c := range []struct {
		name     string
		include  []string
		exclude  []string
		siblings []string
		keep     bool
		comps    int
	}{
		{"beside_kept_p", []string{"m[*].aux.p"}, nil, []string{"n", "p"}, true, 1},
		{"no_p_in_parent", []string{"m[*].aux.p"}, nil, []string{"n"}, false, 1},
		{"p_not_kept", []string{"m[*].name"}, nil, []string{"n", "p"}, false, 1},
		{"p_excluded", []string{"m[*].aux"}, []string{"m[*].aux.p"}, []string{"n", "p"}, true, 0},
		{"p_excluded_partial", []string{"m[*].aux.p", "m[*].aux.ci"}, []string{"m[*].aux.p"}, []string{"n", "p", "ci"}, false, 1},
		{"n_excluded", []string{"m[*].aux.p"}, []string{"m[*].aux.n"}, []string{"n", "p"}, false, 0},
		{"n_included", []string{"m[*].aux.p", "m[*].aux.n"}, nil, []string{"n"}, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := NewWithCompanions("standard", mustParse(t, c.include...), mustParse(t, c.exclude...), nil, comp, 0, []string{"m"})
			if len(p.Companions) != c.comps {
				t.Errorf("companions = %v, want %d", CompanionStrings(p.Companions), c.comps)
			}
			if v := p.VisitIn(n, has(c.siblings...)); v.Keep != c.keep {
				t.Errorf("VisitIn(n) = %+v, want keep %v", v, c.keep)
			}
		})
	}
	with := NewWithCompanions("standard", mustParse(t, "m[*].aux.p"), nil, nil, comp, 0, []string{"m"})
	if v := with.Visit(n); v.Keep {
		t.Errorf("Visit kept a companion it cannot judge: %+v", v)
	}
	without := New("standard", mustParse(t, "m[*].aux.p"), nil, nil, 0, []string{"m"})
	if with.Digest == without.Digest {
		t.Error("a companion does not enter the digest")
	}
	if nilComp := NewWithCompanions("standard", mustParse(t, "m[*].aux.p"), nil, nil, nil, 0, []string{"m"}); nilComp.Digest != without.Digest {
		t.Error("a plan without companions digests differently from New")
	}
	// The pre-companion digest body: no "companions" key at all.
	body, _ := json.Marshal(struct {
		Include   []string `json:"include"`
		Exclude   []string `json:"exclude"`
		Keep      []string `json:"keep"`
		Precision int      `json:"precision"`
	}{[]string{"m[*].aux.p"}, []string{}, []string{}, 0})
	sum := sha256.Sum256(body)
	if want := DigestPrefix + hex.EncodeToString(sum[:]); without.Digest != want {
		t.Errorf("companion-free digest %s, want the pre-companion %s", without.Digest, want)
	}
}

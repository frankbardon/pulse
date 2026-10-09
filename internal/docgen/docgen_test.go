package docgen_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/docgen"
	"github.com/frankbardon/pulse/internal/skills"
)

// profileSnapshot builds the snapshot pulse.New installs for the
// published example feature profile name.
func profileSnapshot(t *testing.T, name string) *descx.InstanceSnapshot {
	t.Helper()
	fp, err := pulse.ExampleFeatureProfile(name)
	if err != nil {
		t.Fatalf("example profile %s: %v", name, err)
	}
	return snapshotEnabling(fp.Features)
}

func snapshotEnabling(enabled []string) *descx.InstanceSnapshot {
	var hidden []string
	for _, n := range descx.FeatureNames() {
		if !slices.Contains(enabled, n) {
			hidden = append(hidden, n)
		}
	}
	return descx.NewInstanceSnapshot(nil, descx.FeatureSet{Enabled: enabled, Hidden: hidden})
}

func defaultSnapshot() *descx.InstanceSnapshot {
	return descx.NewInstanceSnapshot(nil, descx.FeatureSet{Enabled: descx.FeatureNames()})
}

func tree(files []docgen.File) map[string]string {
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = string(f.Body)
	}
	return out
}

// instances is every instance shape the tree tests run over: the nil
// (unscoped) snapshot, the default pulse.New snapshot and every
// published example profile.
func instances(t *testing.T) map[string]*descx.InstanceSnapshot {
	out := map[string]*descx.InstanceSnapshot{"unscoped": nil, "default": defaultSnapshot()}
	for _, name := range pulse.ExampleFeatureProfiles() {
		out["profile/"+name] = profileSnapshot(t, name)
	}
	return out
}

func TestRender_Deterministic(t *testing.T) {
	for key, inst := range instances(t) {
		a := docgen.Render(inst, docgen.Options{})
		b := docgen.Render(inst, docgen.Options{})
		if len(a) != len(b) {
			t.Fatalf("%s: %d vs %d files", key, len(a), len(b))
		}
		for i := range a {
			if a[i].Path != b[i].Path || !bytes.Equal(a[i].Body, b[i].Body) {
				t.Fatalf("%s: renders differ at %s", key, a[i].Path)
			}
			if i > 0 && a[i-1].Path >= a[i].Path {
				t.Fatalf("%s: files not sorted at %s", key, a[i].Path)
			}
		}
	}
}

func TestRender_DefaultTree(t *testing.T) {
	for _, inst := range []*descx.InstanceSnapshot{nil, defaultSnapshot()} {
		files := tree(docgen.Render(inst, docgen.Options{}))
		var catalog []string
		for p := range files {
			if strings.HasPrefix(p, "catalog/") {
				catalog = append(catalog, p)
			}
		}
		surfaces := descx.PurposeSurfaces()
		if len(catalog) != len(surfaces) {
			t.Errorf("catalog pages = %d, want %d (one per PurposeSurfaces category)", len(catalog), len(surfaces))
		}
		for _, s := range surfaces {
			page, ok := files["catalog/"+s.Category+".md"]
			if !ok {
				t.Errorf("no catalog page for %s", s.Category)
				continue
			}
			for _, name := range s.Names {
				if !strings.Contains(page, "| [`"+name+"`](#op-"+strings.ToLower(name)+") |") {
					t.Errorf("catalog/%s.md: no summary row for %s", s.Category, name)
				}
				if !strings.Contains(page, `<a id="op-`+strings.ToLower(name)+`"></a>`) {
					t.Errorf("catalog/%s.md: no detail block for %s", s.Category, name)
				}
			}
		}
		glossary := files["glossary.md"]
		for _, term := range descx.Glossary() {
			if !strings.Contains(glossary, `<a id="term-`+term.ID+`"></a>`) {
				t.Errorf("glossary.md: no entry for %s", term.ID)
			}
		}
		all := skills.List()
		n := 0
		for p := range files {
			if strings.HasPrefix(p, "skills/") {
				n++
			}
		}
		if n != len(all) {
			t.Errorf("skill pages = %d, want every skill (%d)", n, len(all))
		}
		summary := files["SUMMARY.md"]
		for p := range files {
			if p != "SUMMARY.md" && !strings.Contains(summary, "]("+p+")") {
				t.Errorf("SUMMARY.md does not list %s", p)
			}
		}
	}
}

// hiddenOperators returns the operator names inst hides.
func hiddenOperators(inst *descx.InstanceSnapshot) []string {
	var out []string
	for _, n := range inst.HiddenNames() {
		if !strings.Contains(n, ":") {
			out = append(out, n)
		}
	}
	return out
}

func namesToken(text, tok string) bool {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(tok) + `($|[^A-Za-z0-9_])`).MatchString(text)
}

// TestRender_ProfiledHidesOperators: on every example profile a hidden
// operator has no catalog row, no detail block, no mention on any
// catalog / index / glossary / SUMMARY page, no skill page and no link
// to its skill. Non-vacuous: some hidden operator is catalogued, and
// some visible operator's not-for list names one, on the default
// instance.
func TestRender_ProfiledHidesOperators(t *testing.T) {
	full := tree(docgen.Render(defaultSnapshot(), docgen.Options{}))
	fullCatalog := ""
	for p, body := range full {
		if strings.HasPrefix(p, "catalog/") {
			fullCatalog += body
		}
	}
	documents := map[string][]string{}
	for _, md := range skills.List() {
		if md.Operator != "" {
			documents[md.Operator] = append(documents[md.Operator], md.Name)
		}
	}
	hiddenCatalogued, notForScrubbed := 0, 0
	for _, name := range pulse.ExampleFeatureProfiles() {
		inst := profileSnapshot(t, name)
		files := tree(docgen.Render(inst, docgen.Options{}))
		for _, op := range hiddenOperators(inst) {
			if strings.Contains(fullCatalog, `<a id="op-`+strings.ToLower(op)+`"></a>`) {
				hiddenCatalogued++
			}
			if strings.Contains(fullCatalog, " when ") && namesToken(fullCatalog, op) {
				notForScrubbed++
			}
			for p, body := range files {
				if strings.HasPrefix(p, "skills/") {
					continue
				}
				if namesToken(body, op) {
					t.Errorf("profile %s: %s names hidden operator %s", name, p, op)
				}
			}
			for _, stem := range documents[op] {
				if _, ok := files["skills/"+stem+".md"]; ok {
					t.Errorf("profile %s: skill page for hidden operator %s (%s)", name, op, stem)
				}
				for p, body := range files {
					if strings.Contains(body, stem+".md)") {
						t.Errorf("profile %s: %s links hidden operator %s's skill %s", name, p, op, stem)
					}
				}
			}
		}
	}
	if hiddenCatalogued == 0 || notForScrubbed == 0 {
		t.Fatalf("vacuous: hidden operators catalogued by default = %d, named by default = %d", hiddenCatalogued, notForScrubbed)
	}
}

// TestRender_NotForDropsHiddenTarget hides one not-for target — an
// operator, then a capability — and checks the alternative is gone from
// the citing operator's catalog page (row and detail block) while the
// default render carries it.
func TestRender_NotForDropsHiddenTarget(t *testing.T) {
	full := tree(docgen.Render(defaultSnapshot(), docgen.Options{}))
	for _, target := range []string{"TEST_WELCH", "capability:facet"} {
		page, from := citingPage(target)
		if page == "" {
			t.Fatalf("fixture drift: no catalogued operator lists %s as an alternative", target)
		}
		code := "`" + target + "`"
		if !strings.Contains(full[page], code+" when ") && !strings.Contains(full[page], code+"](") {
			t.Fatalf("default %s does not render the %s alternative", page, target)
		}
		var enabled []string
		for _, n := range descx.FeatureNames() {
			if n != target {
				enabled = append(enabled, n)
			}
		}
		got := tree(docgen.Render(snapshotEnabling(enabled), docgen.Options{}))[page]
		if strings.Contains(got, code) {
			t.Errorf("%s names hidden %s", page, target)
		}
		if !strings.Contains(got, `<a id="op-`+strings.ToLower(from)+`"></a>`) {
			t.Errorf("%s lost %s", page, from)
		}
	}
}

// TestRender_FollowUpsListedAndPruned: an operator's FollowUps render
// as a "Follow up with" list in its detail block, and an entry whose
// target the instance hides is dropped while the block stays.
func TestRender_FollowUpsListedAndPruned(t *testing.T) {
	const page, from, target = "catalog/test.md", "TEST_PEARSON_R", "REG_OLS"
	block := func(body string) string {
		start := strings.Index(body, `<a id="op-`+strings.ToLower(from)+`"></a>`)
		if start < 0 {
			return ""
		}
		rest := body[start+1:]
		if end := strings.Index(rest, `<a id="`); end >= 0 {
			rest = rest[:end]
		}
		return rest
	}
	full := block(tree(docgen.Render(defaultSnapshot(), docgen.Options{}))[page])
	if !strings.Contains(full, "**Follow up with:**") || !strings.Contains(full, "`"+target+"`") {
		t.Fatalf("default %s block lacks its %s follow-up:\n%s", from, target, full)
	}
	var enabled []string
	for _, n := range descx.FeatureNames() {
		if n != target {
			enabled = append(enabled, n)
		}
	}
	got := block(tree(docgen.Render(snapshotEnabling(enabled), docgen.Options{}))[page])
	if got == "" {
		t.Fatalf("%s lost %s", page, from)
	}
	if strings.Contains(got, "**Follow up with:**") || strings.Contains(got, "`"+target+"`") {
		t.Errorf("%s block still follows up with hidden %s:\n%s", from, target, got)
	}
}

// citingPage returns the catalog page and name of the first catalogued
// built-in operator whose Purpose lists use as an alternative.
func citingPage(use string) (page, from string) {
	for _, s := range descx.PurposeSurfaces() {
		for _, name := range s.Names {
			p, ok := descx.PurposeOf(name)
			if ok && slices.ContainsFunc(p.NotFor, func(a descriptor.Alternative) bool { return a.Use == use }) {
				return "catalog/" + s.Category + ".md", name
			}
		}
	}
	return "", ""
}

var (
	linkRe   = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	anchorRe = regexp.MustCompile(`<a id="([^"]+)"></a>`)
)

// outsideCode returns body without its fenced code blocks.
func outsideCode(body string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			in = !in
			continue
		}
		if !in {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// TestRender_LinksResolve: every relative link of every file, on every
// instance shape (with and without skills), points at a file of the
// tree and, with a fragment, at an anchor that file declares.
func TestRender_LinksResolve(t *testing.T) {
	for key, inst := range instances(t) {
		for _, opts := range []docgen.Options{{}, {OmitSkills: true}} {
			files := tree(docgen.Render(inst, opts))
			anchors := map[string]map[string]bool{}
			for p, body := range files {
				anchors[p] = map[string]bool{}
				for _, m := range anchorRe.FindAllStringSubmatch(body, -1) {
					anchors[p][m[1]] = true
				}
			}
			links := 0
			for p, body := range files {
				for _, m := range linkRe.FindAllStringSubmatch(outsideCode(body), -1) {
					target := m[1]
					if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
						continue
					}
					links++
					file, frag, _ := strings.Cut(target, "#")
					resolved := p
					if file != "" {
						resolved = path.Clean(path.Join(path.Dir(p), file))
					}
					if _, ok := files[resolved]; !ok {
						t.Errorf("%s (skills omitted %v): %s links missing file %s", key, opts.OmitSkills, p, target)
						continue
					}
					if frag != "" && !anchors[resolved][frag] {
						t.Errorf("%s (skills omitted %v): %s links missing anchor %s", key, opts.OmitSkills, p, target)
					}
				}
			}
			if links == 0 {
				t.Fatalf("%s: no links checked: vacuous", key)
			}
			if opts.OmitSkills {
				for p := range files {
					if strings.HasPrefix(p, "skills") {
						t.Errorf("%s: OmitSkills still wrote %s", key, p)
					}
				}
			}
		}
	}
}

func TestRender_ExtensionWithoutPurpose(t *testing.T) {
	const bare, guided = "AGG_ACME_BARE", "AGG_ACME_GUIDED"
	purpose, _ := descx.PurposeOf("AGG_COUNT")
	ext := &descx.ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{
			{Name: bare, Namespace: "ACME", Description: "Sums the acme column."},
			{Name: guided, Namespace: "ACME", Description: "Guided."},
		},
		Purposes: map[string]descriptor.Purpose{guided: purpose},
	}
	for key, inst := range map[string]*descx.InstanceSnapshot{
		"unscoped": descx.UnscopedInstanceSnapshot(ext),
		"scoped":   descx.NewInstanceSnapshot(ext, descx.FeatureSet{Enabled: append(descx.FeatureNames(), bare, guided)}),
	} {
		page := tree(docgen.Render(inst, docgen.Options{}))["catalog/aggregator.md"]
		row := "| [`" + bare + "`](#op-agg_acme_bare) | No guidance provided. Sums the acme column. | — | — | — |"
		if !strings.Contains(page, row) {
			t.Errorf("%s: no 'No guidance provided' row for %s:\n%s", key, bare, page)
		}
		if !strings.Contains(page, `<a id="op-agg_acme_bare"></a>`) {
			t.Errorf("%s: no detail block for %s", key, bare)
		}
		guidedRow := "| [`" + guided + "`](#op-agg_acme_guided) | " + purpose.Plain + " |"
		if !strings.Contains(page, guidedRow) {
			t.Errorf("%s: extension Purpose not rendered for %s", key, guided)
		}
	}
}

func TestWrite_ThroughAfero(t *testing.T) {
	fsys := afero.NewMemMapFs()
	inst := profileSnapshot(t, pulse.ExampleFeatureProfiles()[0])
	if err := docgen.Write(fsys, "/out/ref", inst, docgen.Options{}); err != nil {
		t.Fatal(err)
	}
	want := docgen.Render(inst, docgen.Options{})
	n := 0
	err := afero.Walk(fsys, "/out/ref", func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want) {
		t.Fatalf("wrote %d files, want %d", n, len(want))
	}
	for _, f := range want {
		got, err := afero.ReadFile(fsys, path.Join("/out/ref", f.Path))
		if err != nil {
			t.Fatalf("read %s: %v", f.Path, err)
		}
		if !bytes.Equal(got, f.Body) {
			t.Errorf("%s: written bytes differ from Render", f.Path)
		}
	}
	if err := docgen.Write(afero.NewReadOnlyFs(afero.NewMemMapFs()), "/x", nil, docgen.Options{}); err == nil {
		t.Error("Write on a read-only fs succeeded")
	}
}

// TestDocgenImportBoundary: docgen is no-execute — it imports the
// standard library, afero, the public descriptor package and the
// internal descriptor and skills packages only (never internal/service
// or internal/processing), and builds no JSON by hand.
func TestDocgenImportBoundary(t *testing.T) {
	allowed := map[string]bool{
		"github.com/spf13/afero":                           true,
		"github.com/frankbardon/pulse/descriptor":          true,
		"github.com/frankbardon/pulse/internal/descriptor": true,
		"github.com/frankbardon/pulse/internal/skills":     true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen++
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if first, _, _ := strings.Cut(p, "/"); !strings.Contains(first, ".") {
				continue
			}
			if !allowed[p] {
				t.Errorf("%s imports %q: docgen may import only stdlib, afero, descriptor, internal/descriptor and internal/skills", name, p)
			}
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(src, []byte("Sprintf")) {
			t.Errorf("%s uses Sprintf: build Markdown with a strings.Builder", name)
		}
	}
	if seen == 0 {
		t.Fatal("no non-test Go files found")
	}
}

var headingRe = regexp.MustCompile(`(?m)^## (\S+)$`)

// TestRender_GlossaryMatchesVirtualSkill: glossary.md keeps exactly the
// terms the instance's virtual glossary skill renders (the same
// keep-function: orphans stay, a term every operator user of which is
// hidden goes). Non-vacuous: some example profile drops a term.
func TestRender_GlossaryMatchesVirtualSkill(t *testing.T) {
	dropped := 0
	for key, inst := range instances(t) {
		served, ok := inst.Discovery().Skill(skills.VirtualGlossary)
		if !ok {
			t.Fatalf("%s: glossary skill not served", key)
		}
		want := headingRe.FindAllStringSubmatch(served, -1)
		got := headingRe.FindAllStringSubmatch(tree(docgen.Render(inst, docgen.Options{}))["glossary.md"], -1)
		if len(want) == 0 || len(got) != len(want) {
			t.Fatalf("%s: glossary.md has %d terms, the glossary skill %d", key, len(got), len(want))
		}
		for i := range want {
			if got[i][1] != want[i][1] {
				t.Fatalf("%s: term %d is %s, the glossary skill's %s", key, i, got[i][1], want[i][1])
			}
		}
		dropped += len(descx.Glossary()) - len(got)
	}
	if dropped == 0 {
		t.Fatal("no instance drops a glossary term: vacuous")
	}
}

// bareTag is a tag-shaped `<word>` token with no attributes; `<br>` is
// the one such tag docgen emits as real HTML.
var bareTag = regexp.MustCompile(`<([A-Za-z_][A-Za-z0-9_-]*)>`)

// inlineCode is a single- or double-backtick inline code span.
var inlineCode = regexp.MustCompile("``[^`].*?``|`[^`\n]+`")

// TestRender_NoBarePlaceholderTags: outside code fences and inline code,
// no rendered page carries a bare `<word>` placeholder — mdBook would
// swallow it as an unclosed HTML tag and drop the text.
func TestRender_NoBarePlaceholderTags(t *testing.T) {
	for key, inst := range instances(t) {
		for _, f := range docgen.Render(inst, docgen.Options{}) {
			inFence := false
			for n, line := range strings.Split(string(f.Body), "\n") {
				trim := strings.TrimSpace(line)
				if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
					inFence = !inFence
					continue
				}
				if inFence {
					continue
				}
				for _, m := range bareTag.FindAllStringSubmatch(inlineCode.ReplaceAllString(line, ""), -1) {
					if strings.EqualFold(m[1], "br") {
						continue
					}
					t.Errorf("%s: %s:%d: bare placeholder %s outside code: %q", key, f.Path, n+1, m[0], line)
				}
			}
		}
	}
}

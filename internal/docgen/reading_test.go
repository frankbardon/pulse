package docgen_test

import (
	"bytes"
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/docgen"
)

// readingFamilyPages are the five Interpretation pages; with the
// Components page they make the six reading pages.
var readingFamilyPages = map[string][]string{
	"reading/test.md":        {"test"},
	"reading/regression.md":  {"regression"},
	"reading/matrix.md":      {"matrix"},
	"reading/overlay.md":     {"overlay"},
	"reading/descriptive.md": {"aggregator", "attribute", "filterer", "grouper", "window", "feature", "synth_distribution"},
}

const componentsPage = "reading/components.md"

// readingPages returns the reading pages (reading.md and reading/*) of
// a rendered tree.
func readingPages(files map[string]string) map[string]string {
	out := map[string]string{}
	for p, body := range files {
		if p == "reading.md" || strings.HasPrefix(p, "reading/") {
			out[p] = body
		}
	}
	return out
}

// TestRender_ReadingPagesDefault: the default (and unscoped) instance
// renders all six reading pages, and every built-in operator declaring
// Interpretations has an entry, with a block per declared field, on its
// family's page.
func TestRender_ReadingPagesDefault(t *testing.T) {
	for key, inst := range map[string]*descx.InstanceSnapshot{"unscoped": nil, "default": defaultSnapshot()} {
		files := tree(docgen.Render(inst, docgen.Options{}))
		pages := readingPages(files)
		for p := range readingFamilyPages {
			if _, ok := pages[p]; !ok {
				t.Errorf("%s: no %s", key, p)
			}
		}
		if _, ok := pages[componentsPage]; !ok {
			t.Errorf("%s: no %s", key, componentsPage)
		}
		if got := len(pages) - 1; got != 6 { // minus reading.md
			t.Errorf("%s: %d reading pages, want 6", key, got)
		}
		entries := 0
		for p, cats := range readingFamilyPages {
			for _, s := range descx.PurposeSurfaces() {
				if !slices.Contains(cats, s.Category) {
					continue
				}
				for _, name := range s.Names {
					ins, ok := descx.InterpretationsOf(name)
					if !ok {
						continue
					}
					entries++
					if !strings.Contains(pages[p], `<a id="op-`+strings.ToLower(name)+`"></a>`) {
						t.Errorf("%s: %s has no entry for %s", key, p, name)
					}
					for _, in := range ins {
						if !strings.Contains(pages[p], "#### `"+in.Field+"`") {
							t.Errorf("%s: %s: %s has no %s block", key, p, name, in.Field)
						}
					}
				}
			}
		}
		if entries == 0 {
			t.Fatal("no built-in Interpretation: vacuous")
		}
	}
}

// TestRender_PValueRuleOnce: the shared p-value rule renders exactly
// once across the whole tree, on the Test page, and every p-value
// field on every reading page links there.
func TestRender_PValueRuleOnce(t *testing.T) {
	rule, ok := descx.SharedInterpretation(descx.SharedPValue)
	if !ok {
		t.Fatal("no shared p-value rule")
	}
	files := tree(docgen.Render(defaultSnapshot(), docgen.Options{}))
	total := 0
	for p, body := range files {
		n := strings.Count(body, rule.Means)
		total += n
		if n > 0 && p != "reading/test.md" {
			t.Errorf("%s repeats the p-value rule", p)
		}
	}
	if total != 1 {
		t.Errorf("p-value rule rendered %d times, want exactly once", total)
	}
	tests := files["reading/test.md"]
	if strings.Count(tests, `<a id="shared-p-value"></a>`) != 1 {
		t.Error("test.md: no single shared-p-value anchor")
	}
	// Every p_value field block on a reading page links the rule.
	for p, body := range readingPages(files) {
		want := "(test.md#shared-p-value)"
		if p == "reading/test.md" {
			want = "(#shared-p-value)"
		}
		blocks := strings.Count(body, "#### `p_value`")
		if blocks > 0 && strings.Count(body, want) < blocks {
			t.Errorf("%s: %d p_value blocks but %d links to the rule", p, blocks, strings.Count(body, want))
		}
	}
	if !strings.Contains(tests, "#### `p_value`\n\nFollows the shared reading: see [Reading a p-value](#shared-p-value).") {
		t.Error("test.md: a p_value block does not link the rule")
	}
}

// TestRender_ReadingProfiledOmits: an instance hiding every regression
// has no regression page (nor SUMMARY / index line for it), and no
// reading page names a hidden operator; the default instance carries
// the page, so the check is not vacuous.
func TestRender_ReadingProfiledOmits(t *testing.T) {
	var regs []string
	for _, s := range descx.PurposeSurfaces() {
		if s.Category == "regression" {
			regs = s.Names
		}
	}
	if len(regs) == 0 {
		t.Fatal("no regressions: vacuous")
	}
	var enabled []string
	for _, n := range descx.FeatureNames() {
		if !slices.Contains(regs, n) {
			enabled = append(enabled, n)
		}
	}
	files := tree(docgen.Render(snapshotEnabling(enabled), docgen.Options{}))
	if _, ok := files["reading/regression.md"]; ok {
		t.Error("regressions hidden but reading/regression.md rendered")
	}
	for _, p := range []string{"SUMMARY.md", "reading.md"} {
		if strings.Contains(files[p], "reading/regression.md") {
			t.Errorf("%s lists the omitted regression page", p)
		}
	}
	for p, body := range readingPages(files) {
		for _, op := range regs {
			if namesToken(body, op) {
				t.Errorf("%s names hidden %s", p, op)
			}
		}
	}
	if _, ok := files["reading/test.md"]; !ok {
		t.Error("hiding regressions dropped the test page")
	}

	// Example profiles: a hidden operator with an entry by default has
	// none on the profile.
	full := readingPages(tree(docgen.Render(defaultSnapshot(), docgen.Options{})))
	dropped := 0
	for key, inst := range instances(t) {
		if !strings.HasPrefix(key, "profile/") {
			continue
		}
		pages := readingPages(tree(docgen.Render(inst, docgen.Options{})))
		for _, op := range hiddenOperators(inst) {
			anchor := `<a id="op-` + strings.ToLower(op) + `"></a>`
			for p, body := range full {
				if strings.Contains(body, anchor) {
					dropped++
					if strings.Contains(pages[p], anchor) {
						t.Errorf("%s: %s keeps hidden %s", key, p, op)
					}
				}
			}
		}
	}
	if dropped == 0 {
		t.Fatal("no example profile hides an operator with a reading entry: vacuous")
	}
}

// TestRender_MultiplicityHiddenNoPAdjusted: hiding capability:multiplicity
// leaves no multiplicity slot token on any reading page, while the
// default Test page names p_adjusted.
func TestRender_MultiplicityHiddenNoPAdjusted(t *testing.T) {
	tokens, ok := descx.SlotTokensOf(descx.FeatureMultiplicity)
	if !ok || !slices.Contains(tokens, "p_adjusted") {
		t.Fatalf("multiplicity slot tokens = %v", tokens)
	}
	full := tree(docgen.Render(defaultSnapshot(), docgen.Options{}))
	if !namesToken(full["reading/test.md"], "p_adjusted") {
		t.Fatal("default test.md names no p_adjusted: vacuous")
	}
	var enabled []string
	for _, n := range descx.FeatureNames() {
		if n != descx.FeatureMultiplicity {
			enabled = append(enabled, n)
		}
	}
	pages := readingPages(tree(docgen.Render(snapshotEnabling(enabled), docgen.Options{})))
	if _, ok := pages["reading/test.md"]; !ok {
		t.Fatal("no test.md with multiplicity hidden")
	}
	for p, body := range pages {
		for _, tok := range tokens {
			if namesToken(body, tok) {
				t.Errorf("%s names hidden slot token %s", p, tok)
			}
		}
	}
}

// TestRender_ComponentsPage: the floor of every slot, the weighted
// floor keys (only when the instance offers weighting) and each
// visible operator's own keys with its mergeability.
func TestRender_ComponentsPage(t *testing.T) {
	page := tree(docgen.Render(defaultSnapshot(), docgen.Options{}))[componentsPage]
	for _, f := range descx.ComponentFloors() {
		for _, k := range f.Keys {
			if !strings.Contains(page, "| `"+k.Name+"` | "+k.Type+" |") {
				t.Errorf("components.md: %s floor key %s missing", f.Slot, k.Name)
			}
		}
	}
	for _, k := range descx.WeightFloorKeys() {
		if !strings.Contains(page, "| `"+k.Name+"` | "+k.Type+" |") {
			t.Errorf("components.md: weighted floor key %s missing", k.Name)
		}
	}
	m := descx.BuildManifestForInstance(defaultSnapshot()).ComponentsSchemas
	n := 0
	for name, s := range m.Aggregators {
		n++
		block := `<a id="op-` + strings.ToLower(name) + `"></a>`
		i := strings.Index(page, block)
		if i < 0 {
			t.Errorf("components.md: no block for %s", name)
			continue
		}
		rest := page[i:]
		if !strings.HasPrefix(rest[len(block):], "\n\n#### `"+name+"`\n\n**Mergeability:** "+string(s.Mergeability)+".") {
			t.Errorf("components.md: %s block lacks its mergeability", name)
		}
	}
	if n == 0 || !strings.Contains(page, "| `mean` | float64 | always |") {
		t.Fatal("components.md: no operator keys: vacuous")
	}

	var enabled []string
	for _, f := range descx.FeatureNames() {
		if f != descx.FeatureWeighting {
			enabled = append(enabled, f)
		}
	}
	unweighted := tree(docgen.Render(snapshotEnabling(enabled), docgen.Options{}))[componentsPage]
	if strings.Contains(unweighted, "weighted-floor") || namesToken(unweighted, "n_weight_invalid") {
		t.Error("weighting hidden but components.md documents the weighted floor keys")
	}
}

const (
	preserveBegin = "<!-- docgen:preserve begin components-intro -->\n"
	preserveEnd   = "<!-- docgen:preserve end components-intro -->\n"
)

// preservedSpan returns the text between the Components intro markers.
func preservedSpan(t *testing.T, body string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, preserveBegin)
	if !ok {
		t.Fatal("components.md: no preserve-begin marker")
	}
	span, _, ok := strings.Cut(rest, preserveEnd)
	if !ok {
		t.Fatal("components.md: no preserve-end marker")
	}
	return span
}

// TestRender_ComponentsIntroPreserved: the hand-written intro between
// the preserve markers is the embedded components_intro.md byte for
// byte, on every instance shape, and a regeneration over an existing
// export reproduces it unchanged.
func TestRender_ComponentsIntroPreserved(t *testing.T) {
	src, err := os.ReadFile("components_intro.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(src, []byte("`n_null`")) {
		t.Fatal("components_intro.md does not discuss n_null")
	}
	for key, inst := range instances(t) {
		page := tree(docgen.Render(inst, docgen.Options{}))[componentsPage]
		if got := preservedSpan(t, page); got != string(src) {
			t.Errorf("%s: preserved intro differs from components_intro.md", key)
		}
	}
	fsys := afero.NewMemMapFs()
	inst := defaultSnapshot()
	if err := docgen.Write(fsys, "/guide", inst, docgen.Options{}); err != nil {
		t.Fatal(err)
	}
	first, _ := afero.ReadFile(fsys, path.Join("/guide", componentsPage))
	if err := docgen.Write(fsys, "/guide", inst, docgen.Options{}); err != nil {
		t.Fatal(err)
	}
	second, _ := afero.ReadFile(fsys, path.Join("/guide", componentsPage))
	if preservedSpan(t, string(first)) != preservedSpan(t, string(second)) || !bytes.Equal(first, second) {
		t.Error("regeneration changed the Components page")
	}
}

// lintExempt mirrors the guidance lint's allowlist for the shared
// p-value rule set (its null-hypothesis statement and its
// absence-of-evidence caveat): a rendered line carrying one of those
// strings is exempt from ASA-NODIFF, and only from it.
func lintExempt() []string {
	rule, _ := descx.SharedInterpretation(descx.SharedPValue)
	return []string{rule.Means, rule.Caveats[1]}
}

// lintPage runs the guidance lint's text rules over a rendered page,
// line by line (each line carries one prose string), and returns the
// hits outside the exemptions.
func lintPage(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		// A table row's first cell is a component key or band range,
		// an identifier rather than prose: the scrub drops the whole
		// slot table with the capability owning the key, so SLOT-TOKEN
		// (which guards against a sentence the scrub would over-drop)
		// reads the prose cells only.
		if strings.HasPrefix(line, "| `") {
			if _, rest, ok := strings.Cut(line[1:], "|"); ok {
				line = "|" + rest
			}
		}
		for _, h := range descx.LintGuidanceText(line) {
			exempt := false
			if h.Rule == "ASA-NODIFF" {
				for _, e := range lintExempt() {
					if strings.Contains(line, e) {
						exempt = true
					}
				}
			}
			if !exempt {
				out = append(out, h.Rule+": "+h.Span)
			}
		}
	}
	return out
}

// TestRender_ReadingPagesLint: every reading page of every instance
// shape passes the guidance lint's text rules; a synthetic violation
// is caught, so the lint is live.
func TestRender_ReadingPagesLint(t *testing.T) {
	seen := 0
	for key, inst := range instances(t) {
		for p, body := range readingPages(tree(docgen.Render(inst, docgen.Options{}))) {
			seen++
			for _, h := range lintPage(body) {
				t.Errorf("%s: %s: lint %s", key, p, h)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no reading pages linted: vacuous")
	}
	page := tree(docgen.Render(defaultSnapshot(), docgen.Options{}))["reading/test.md"]
	if len(lintPage(page)) != 0 {
		t.Fatal("test.md not clean")
	}
	for _, bad := range []string{
		"\nA small p-value proves the hypothesis.\n",
		"\nThe gap is due to chance.\n",
		"\nAdjust the p-values by hand.\n",
	} {
		if len(lintPage(page+bad)) == 0 {
			t.Errorf("synthetic violation %q not caught", strings.TrimSpace(bad))
		}
	}
}

// TestRender_ReadingHidesSlotTokens: hiding any slot-owning capability
// leaves none of its wire tokens on a reading page.
func TestRender_ReadingHidesSlotTokens(t *testing.T) {
	checked := 0
	for _, c := range descx.SlotTokenCapabilities() {
		tokens, _ := descx.SlotTokensOf(c)
		if len(tokens) == 0 {
			continue
		}
		var enabled []string
		for _, n := range descx.FeatureNames() {
			if n != c {
				enabled = append(enabled, n)
			}
		}
		for p, body := range readingPages(tree(docgen.Render(snapshotEnabling(enabled), docgen.Options{}))) {
			for _, tok := range tokens {
				checked++
				if namesToken(body, tok) {
					t.Errorf("%s hidden: %s names %s", c, p, tok)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no slot token checked: vacuous")
	}
}

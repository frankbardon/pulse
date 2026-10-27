//go:build heavy

package docgen_test

import (
	"path"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/docgen"
	"github.com/frankbardon/pulse/internal/skills"
)

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

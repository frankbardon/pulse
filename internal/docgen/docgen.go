// Package docgen renders an instance's analysis reference — the operator
// catalog, the glossary and the skill pack — as a deterministic Markdown
// tree (U21). It is NO-EXECUTE: it reads only the instance's
// InstanceSnapshot (pruned ontology, feature set, extension projection)
// and the guidance registries in internal/descriptor, and writes through
// an afero.Fs. It never imports internal/service or internal/processing
// (TestDocgenImportBoundary).
//
// The tree, paths relative to the export root:
//
//	SUMMARY.md                mdBook summary fragment listing every page
//	index.md                  the landing page
//	catalog.md                the catalog's category index
//	catalog/<category>.md     one page per PurposeSurfaces() category the
//	                          instance offers an operator of
//	reading.md                the "Reading your results" index
//	reading/<family>.md       one Interpretation page per result family
//	                          (tests, regressions, matrices, overlays,
//	                          descriptive) the instance has entries for
//	reading/components.md     the Response.Components floors, weighted
//	                          floor keys, mergeability and every visible
//	                          operator's ComponentSchema keys
//	glossary.md               every glossary term the instance keeps
//	skills.md                 the skill index (omitted with OmitSkills)
//	skills/<stem>.md          every visible skill as the instance serves it
//
// Instance scoping. Every operator, glossary term and skill comes from
// the instance's PRUNED ontology, so a feature-profiled instance's
// export carries no page, row, detail block or link for anything it
// hides. A not-for alternative whose target the prune removed is
// dropped (the whole list when every alternative is), and every prose
// string passes through the instance's ProseScrub. Glossary terms are
// kept or dropped by their owning operators (the same keep-function the
// virtual glossary skill renders with); a kept term's Forms are its
// spellings and are rendered verbatim, never token-scrubbed.
//
// Reading pages. Each Interpretation entry renders its Means, its
// Bands under the named convention, its Sign and its Caveats, scrubbed
// like every other prose string; a field whose path names a hidden
// token (p_adjusted without capability:multiplicity) is dropped whole.
// A shared rule set (the p-value reading) renders ONCE, at the top of
// the first page citing it — the Test page whenever the instance offers
// a test — and every citing field links there. Reading pages never
// link a skill.
//
// Preserved spans. Hand-written prose inside a generated page sits
// between "<!-- docgen:preserve begin <id> -->" and
// "<!-- docgen:preserve end <id> -->" lines and is copied verbatim
// from a file embedded in this package, so regeneration reproduces it
// byte for byte and an export outside the repository carries it. The
// Components intro's source of truth is components_intro.md here.
//
// Determinism: every listing is sorted, every map is walked in a fixed
// order, and Render returns the files sorted by path, so two renders of
// the same instance are byte-identical.
package docgen

import (
	"path"
	"path/filepath"
	"sort"

	"github.com/spf13/afero"

	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// Options tunes one render.
type Options struct {
	// OmitSkills leaves out skills.md and the skills/ tree; no page then
	// links to a skill. The zero value includes the skills.
	OmitSkills bool
}

// File is one rendered file of the tree: its slash-separated path
// relative to the export root, and its bytes.
type File struct {
	Path string
	Body []byte
}

// Render renders inst's reference tree. A nil inst renders the
// unscoped, extension-free instance. The files are sorted by Path.
func Render(inst *descx.InstanceSnapshot, opts Options) []File {
	g := newGen(inst, opts)
	files := map[string]string{}
	cats := g.categories()
	for _, c := range cats {
		files[catalogPath(c.key)] = g.renderCategory(c)
	}
	files["catalog.md"] = renderCatalogIndex(cats)
	files["glossary.md"] = g.renderGlossary()
	pages, homes := g.readingPages(cats)
	for _, p := range pages {
		files[readingPath(p.fam.key)] = g.renderReadingPage(p, homes)
	}
	files[componentsPage] = g.renderComponents()
	files["reading.md"] = renderReadingIndex(pages)
	stems := g.visibleSkills()
	if !opts.OmitSkills {
		for _, md := range stems {
			files[skillPath(md.Name)] = g.renderSkill(md.Name)
		}
		files["skills.md"] = renderSkillIndex(stems)
	}
	files["index.md"] = g.renderIndex()
	files["SUMMARY.md"] = renderSummary(cats, pages, stems, opts.OmitSkills)

	out := make([]File, 0, len(files))
	for p, body := range files {
		out = append(out, File{Path: p, Body: []byte(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Write renders inst's reference tree and writes every file under dir
// on fsys, creating directories as needed. It writes only the rendered
// files: it never removes anything already in dir.
func Write(fsys afero.Fs, dir string, inst *descx.InstanceSnapshot, opts Options) error {
	for _, f := range Render(inst, opts) {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := fsys.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := afero.WriteFile(fsys, p, f.Body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func catalogPath(category string) string { return path.Join("catalog", category+".md") }

func skillPath(stem string) string { return path.Join("skills", stem+".md") }

package gosdk_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/spf13/afero"
)

// Never-existing names, one per surface, shaped like the names they
// stand in for. A hidden name's outcome must equal the never-existing
// one's after the hidden name is substituted out.
const (
	neverSkill   = "op-never-existed"
	neverExample = "example-never-existed"
)

// The two served-body assertions of TestSkillsCoverProfileGet that cannot
// bind until later U10 stories land run REPORT-ONLY (t.Log) while their
// switch is false; flipping the switch is the whole change that makes
// them fail.
//
//   - atomicBodiesNamePrunedFail: a served atomic body names a pruned
//     skill stem or example (today a "## See" line keeps a hidden
//     sibling's stem — only operator / tool tokens are scrubbed). E2-S1
//     renders "## See" by edge and flips it.
//   - topicalBodiesNameHiddenFail: a served topical (kind: design) body
//     names a pruned skill or example or a hidden operator or tool
//     (topical bodies are served unrendered). E4-S3 flips it and deletes
//     invisibilityExemptSkill (feature_parity_mcp_test.go) with it.
const (
	atomicBodiesNamePrunedFail  = false
	topicalBodiesNameHiddenFail = false
)

// reportOrFail logs while the assertion is report-only, fails once its
// switch is flipped.
func reportOrFail(t *testing.T, fail bool, format string, args ...any) {
	t.Helper()
	if fail {
		t.Errorf(format, args...)
		return
	}
	t.Logf("report-only: "+format, args...)
}

// shippedProfiles is every feature profile the repo ships: each
// published example (examples/profiles/*.json) and each private fixture
// (descriptor/testdata/profiles/*.json), keyed "<origin>/<name>".
func shippedProfiles(t *testing.T) map[string]*pulse.FeatureProfile {
	t.Helper()
	out := map[string]*pulse.FeatureProfile{}
	for origin, glob := range map[string]string{
		"published": "../../examples/profiles/*.json",
		"fixture":   "../../descriptor/testdata/profiles/*.json",
	} {
		paths, err := filepath.Glob(glob)
		if err != nil || len(paths) == 0 {
			t.Fatalf("no %s profiles at %s (err=%v)", origin, glob, err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			fp, err := pulse.ParseFeatureProfile(data)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			out[origin+"/"+strings.TrimSuffix(filepath.Base(path), ".json")] = fp
		}
	}
	return out
}

// substEqual fails unless hidden's outcome equals never's once the
// hidden name is replaced by the never-existing one.
func substEqual(t *testing.T, surface, hidden, never, got, want string) {
	t.Helper()
	if subst := strings.ReplaceAll(got, hidden, never); subst != want {
		t.Errorf("%s: hidden %q differs from never-existing %q\n hidden: %s\n never:  %s", surface, hidden, never, got, want)
	}
}

// templateReadRaw serves uri through the pulse-skill://{+name} template
// reader directly (the path a hidden name takes, since it has no exact
// resource) and renders the outcome as one string.
func templateReadRaw(p *pulse.Pulse, uri string) string {
	body, err := gosdk.ReadSkillViaTemplate(p, uri)
	if err != nil {
		return "error: " + err.Error()
	}
	return "ok: " + body
}

// kebabTokens splits text into [a-z0-9_-] runs: the spelling of a skill
// stem or an example name as a whole word.
func kebabTokens(text string) map[string]bool {
	out := map[string]bool{}
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
		return r != '-' && r != '_' && (r < '0' || r > '9') && (r < 'a' || r > 'z')
	}) {
		out[tok] = true
	}
	return out
}

// ontologyNames indexes an ontology's node names by kind.
func ontologyNames(o descriptor.Ontology) map[descriptor.OntologyNodeKind]map[string]bool {
	out := map[descriptor.OntologyNodeKind]map[string]bool{}
	for _, n := range o.Nodes {
		if out[n.Kind] == nil {
			out[n.Kind] = map[string]bool{}
		}
		out[n.Kind][n.Name] = true
	}
	return out
}

func exampleNamesOf(sums []pulse.ExampleSummary) []string {
	out := make([]string, len(sums))
	for i, s := range sums {
		out[i] = s.Name
	}
	return out
}

func skillNamesOf(mds []pulse.SkillMetadata) []string {
	out := make([]string, len(mds))
	for i, m := range mds {
		out[i] = m.Name
	}
	return out
}

// TestSkillsCoverProfileGet is the U10 discovery-invisibility gate: under
// every shipped feature profile, each skill and example the instance
// prunes is indistinguishable from a name that never existed on EVERY
// discovery surface —
//
//   - absent from every list: p.Skills, pulse_skills_list, the
//     pulse-skill:// enumeration and the manifest skills; p.ExamplesSearch,
//     pulse_examples_search and the manifest examples_count;
//   - absent from every search a client could aim at it: by its name, its
//     tags, its category and each operator it uses (facade and MCP);
//   - an exact-name get answers byte-identically, after name
//     substitution, to a never-existing name: facade p.Skill / p.ExampleGet,
//     pulse_skills_get, pulse_examples_get, a pulse-skill:// read and the
//     resource-template reader;
//   - no served body names a pruned skill or example, and no served
//     topical body a hidden operator or tool — report-only until the
//     switches above flip (atomic: E2-S1; topical: E4-S3).
//
// Every shipped profile must prune at least one skill, and the set as a
// whole at least one example, or the gate is vacuous.
func TestSkillsCoverProfileGet(t *testing.T) {
	cfg := gosdk.Config{Version: "9.9.9", DisableCohortScan: true}
	full, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	fullSkills := full.Skills()
	fullExamples := full.ExamplesSearch("", nil, "")
	if slices.Contains(skillNamesOf(fullSkills), neverSkill) || slices.Contains(exampleNamesOf(fullExamples), neverExample) {
		t.Fatal("a never-existing name exists (test premise)")
	}

	prunedExamples := 0
	defer func() {
		if prunedExamples == 0 {
			t.Error("vacuous: no shipped profile prunes an example")
		}
	}()
	profiles := shippedProfiles(t)
	labels := make([]string, 0, len(profiles))
	for l := range profiles {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, label := range labels {
		t.Run(label, func(t *testing.T) {
			ctx := context.Background()
			p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: profiles[label]})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			srv := newServer()
			if err := gosdk.Register(srv, p, cfg); err != nil {
				t.Fatalf("Register: %v", err)
			}
			c, cancel := connect(t, srv)
			defer cancel()

			// The pruned set comes from the instance ontology, independently
			// of the lists under test: a skill / example node the full graph
			// has and this instance's graph lacks.
			kept := ontologyNames(p.Ontology())
			var hiddenSkills []string
			for _, md := range fullSkills {
				if !kept[descriptor.OntologyNodeSkill][md.Name] {
					hiddenSkills = append(hiddenSkills, md.Name)
				}
			}
			var hiddenExamples []pulse.ExampleSummary
			for _, ex := range fullExamples {
				if !kept[descriptor.OntologyNodeExample][ex.Name] {
					hiddenExamples = append(hiddenExamples, ex)
				}
			}
			visibleSkills := skillNamesOf(p.Skills())
			visibleExamples := exampleNamesOf(p.ExamplesSearch("", nil, ""))
			if got, want := len(visibleSkills)+len(hiddenSkills), len(fullSkills); got != want {
				t.Errorf("p.Skills() (%d) + pruned (%d) != full (%d)", len(visibleSkills), len(hiddenSkills), want)
			}
			if got, want := len(visibleExamples)+len(hiddenExamples), len(fullExamples); got != want {
				t.Errorf("p.ExamplesSearch (%d) + pruned (%d) != full (%d)", len(visibleExamples), len(hiddenExamples), want)
			}
			for _, s := range hiddenSkills {
				if slices.Contains(visibleSkills, s) {
					t.Errorf("p.Skills() lists pruned skill %s", s)
				}
			}
			for _, ex := range hiddenExamples {
				if slices.Contains(visibleExamples, ex.Name) {
					t.Errorf("p.ExamplesSearch lists pruned example %s", ex.Name)
				}
			}
			if len(hiddenSkills) == 0 {
				t.Fatal("vacuous: profile prunes no skill")
			}
			prunedExamples += len(hiddenExamples)

			// Every list agrees with the facade, so absence from the facade
			// list is absence everywhere.
			m := p.Manifest(ctx)
			var manifestSkills []string
			for _, s := range m.Skills {
				manifestSkills = append(manifestSkills, s.Name)
			}
			for surface, names := range map[string][]string{
				"pulse_skills_list":       skillsListed(t, c),
				"pulse-skill:// resource": skillResourcesListed(t, c),
				"manifest skills":         manifestSkills,
			} {
				if !slices.Equal(names, visibleSkills) {
					t.Errorf("%s lists %d skills, p.Skills() %d (hidden skill reachable by listing?)", surface, len(names), len(visibleSkills))
				}
			}
			if got := examplesListed(t, c); !slices.Equal(got, visibleExamples) {
				t.Errorf("pulse_examples_search lists %d examples, p.ExamplesSearch %d", len(got), len(visibleExamples))
			}
			if m.ExamplesCount != len(visibleExamples) {
				t.Errorf("manifest examples_count = %d, p.ExamplesSearch %d", m.ExamplesCount, len(visibleExamples))
			}

			// Exact-name get: hidden skill == never-existing skill.
			neverBody, neverOK := p.Skill(neverSkill)
			neverGet := callRaw(t, c, "pulse_skills_get", map[string]any{"name": neverSkill})
			neverRead := readResourceRaw(c, gosdk.SkillURIScheme+neverSkill)
			neverTmpl := templateReadRaw(p, gosdk.SkillURIScheme+neverSkill)
			for _, s := range hiddenSkills {
				if body, ok := p.Skill(s); ok != neverOK || body != neverBody {
					t.Errorf("p.Skill(%s) = (%d bytes, %v), never-existing = (%d bytes, %v)", s, len(body), ok, len(neverBody), neverOK)
				}
				substEqual(t, "pulse_skills_get", s, neverSkill, callRaw(t, c, "pulse_skills_get", map[string]any{"name": s}), neverGet)
				substEqual(t, "read pulse-skill://", s, neverSkill, readResourceRaw(c, gosdk.SkillURIScheme+s), neverRead)
				substEqual(t, "pulse-skill:// template", s, neverSkill, templateReadRaw(p, gosdk.SkillURIScheme+s), neverTmpl)
			}

			// Exact-name get + every search: hidden example == never-existing.
			neverEx, neverExOK := p.ExampleGet(neverExample)
			neverExGet := callRaw(t, c, "pulse_examples_get", map[string]any{"name": neverExample})
			for _, ex := range hiddenExamples {
				if got, ok := p.ExampleGet(ex.Name); ok != neverExOK || got != neverEx {
					t.Errorf("p.ExampleGet(%s) = (%v, %v), never-existing = (%v, %v)", ex.Name, got, ok, neverEx, neverExOK)
				}
				substEqual(t, "pulse_examples_get", ex.Name, neverExample, callRaw(t, c, "pulse_examples_get", map[string]any{"name": ex.Name}), neverExGet)

				searches := []struct {
					query, category string
					tags            []string
				}{{query: ex.Name}, {tags: ex.Tags}, {category: ex.Category}}
				for _, op := range ex.Operators {
					searches = append(searches, struct {
						query, category string
						tags            []string
					}{query: op})
				}
				for _, q := range searches {
					if slices.Contains(exampleNamesOf(p.ExamplesSearch(q.query, q.tags, q.category)), ex.Name) {
						t.Errorf("p.ExamplesSearch(%q, %v, %q) returns pruned example %s", q.query, q.tags, q.category, ex.Name)
					}
					args := map[string]any{"query": q.query, "category": q.category}
					if len(q.tags) > 0 {
						args["tags"] = q.tags
					}
					if hits := callRaw(t, c, "pulse_examples_search", args); strings.Contains(hits, `\"name\":\"`+ex.Name+`\"`) {
						t.Errorf("pulse_examples_search %v returns pruned example %s", args, ex.Name)
					}
				}
			}

			// Served bodies name nothing pruned (report-only, see switches).
			// Example names that are a single word ("logistic") double as
			// taxonomy tags, so only compound ones count as a name.
			hiddenNames := map[string]bool{}
			for _, s := range hiddenSkills {
				hiddenNames[s] = true
			}
			for _, ex := range hiddenExamples {
				if strings.ContainsAny(ex.Name, "-_") {
					hiddenNames[ex.Name] = true
				}
			}
			hiddenTokens := hiddenProseTokens(t, p)
			for _, md := range p.Skills() {
				body, ok := p.Skill(md.Name)
				if !ok {
					t.Errorf("listed skill %s reads as not found", md.Name)
					continue
				}
				var named []string
				for tok := range kebabTokens(body) {
					if hiddenNames[tok] {
						named = append(named, tok)
					}
				}
				sort.Strings(named)
				if md.Kind != "design" {
					if len(named) > 0 {
						reportOrFail(t, atomicBodiesNamePrunedFail, "served atomic skill %s names pruned %v", md.Name, named)
					}
					continue
				}
				if named = append(named, leakedTokens(body, hiddenTokens)...); len(named) > 0 {
					reportOrFail(t, topicalBodiesNameHiddenFail, "served topical skill %s names hidden %v", md.Name, named)
				}
			}
		})
	}
}

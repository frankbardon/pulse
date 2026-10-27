package gosdk_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/mcp/gosdk"
)

// Never-existing names, one per surface, shaped like the names they
// stand in for. A hidden name's outcome must equal the never-existing
// one's after the hidden name is substituted out.
const (
	neverSkill   = "op-never-existed"
	neverExample = "example-never-existed"
)

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

package pulse

import (
	"context"
	"encoding/json"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/doccheck"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// removedNamesAllowlist is TestNoRemovedNamesInDocs's shrink-only baseline
// (internal/doccheck): path = the served source, entry = the denied name.
const removedNamesAllowlist = "removed-names"

// removedNamesSkipDocs are docs/src pages the scan skips: the generated
// coverage matrix names every public identifier verbatim, so a mention
// scan over it is vacuous.
var removedNamesSkipDocs = map[string]bool{
	"docs/src/contract/coverage.md": true,
}

// TestNoRemovedNamesInDocs (U32 E2-S3, PRD FR-14–FR-16): no served text
// names a removed or pre-1.0 spelling from doccheck.RemovedNames (an
// io.<Name> entry also as pio.<Name>), matched as a
// whole word. Served text is docs/src/** (the generated guide included,
// the coverage page skipped), the repo-root README / CONTRIBUTING /
// SECURITY, and what a default instance serves: every skill body and the
// skill metadata, every MCP tool description, the manifest's string values
// (descriptions and guidance), every error code's message and fixup
// hints, and every example. docs/roadmap/**, CLAUDE.md, .claude/reference/
// and tests are history or contributor notes, not served, and are out of
// scope.
//
// Today's hits are baselined in the shrink-only allowlist
// internal/doccheck/allowlists/removed-names.txt (path = source, entry =
// name): a hit it does not list fails, and so does an entry whose hit is
// gone.
func TestNoRemovedNamesInDocs(t *testing.T) {
	t.Parallel()

	osFS := afero.NewOsFs()
	sources := removedNameSources(t, osFS)
	if len(sources) < 500 {
		t.Fatalf("only %d served sources — a surface walk is broken", len(sources))
	}
	seen := map[string]bool{}
	for _, d := range doccheck.RemovedNames {
		if d.Name == "" || d.Reason == "" || seen[d.Name] {
			t.Errorf("denylist entry %+v: name and reason are required, names are unique", d)
		}
		seen[d.Name] = true
	}

	allow, err := doccheck.Load(osFS, doccheck.AllowlistFile(removedNamesAllowlist))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range allow.Reconcile(doccheck.CheckRemovedNames(sources, doccheck.RemovedNames)).Problems(allow.File) {
		t.Error(p)
	}
}

// removedNameSources gathers the served text TestNoRemovedNamesInDocs
// scans, keyed by a stable label: the repo path for a file, else
// "<surface> <name>".
func removedNameSources(t *testing.T, osFS afero.Fs) map[string]string {
	t.Helper()
	out := map[string]string{}

	for _, f := range []string{"README.md", "CONTRIBUTING.md", "SECURITY.md"} {
		b, err := afero.ReadFile(osFS, f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = string(b)
	}
	err := afero.Walk(osFS, "docs/src", func(p string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Slash form for the skip lookup and the allowlist key, as on
		// every other doc walk: on Windows the walk yields backslashes.
		p = filepath.ToSlash(p)
		if info.IsDir() || path.Ext(p) != ".md" || removedNamesSkipDocs[p] {
			return nil
		}
		b, err := afero.ReadFile(osFS, p)
		if err != nil {
			return err
		}
		out[p] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Skills() {
		body, ok := p.Skill(s.Name)
		if !ok {
			t.Fatalf("listed skill %q has no body", s.Name)
		}
		meta, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		out["skill "+s.Name] = body + "\n" + strings.Join(jsonStrings(t, meta), "\n")
	}
	for _, m := range toolmeta.Meta() {
		out["tool "+m.Name] = m.Description
	}

	raw, err := json.Marshal(p.Manifest(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sections); err != nil {
		t.Fatal(err)
	}
	for k, v := range sections {
		out["manifest "+k] = strings.Join(jsonStrings(t, v), "\n")
	}

	for _, c := range perrors.AllCodes() {
		md, ok := perrors.MetadataFor(c)
		if !ok {
			continue // TestCodesHaveFixups owns a missing entry
		}
		text := []string{md.Message}
		for _, f := range md.Fixups {
			text = append(text, f.Hint)
		}
		out["error "+string(c)] = strings.Join(text, "\n")
	}

	for _, e := range p.ExamplesSearch("", nil, "") {
		ex, ok := p.ExampleGet(e.Name)
		if !ok {
			t.Fatalf("listed example %q has no body", e.Name)
		}
		b, err := json.Marshal(ex)
		if err != nil {
			t.Fatal(err)
		}
		out["example "+e.Name] = strings.Join(jsonStrings(t, b), "\n")
	}
	return out
}

// jsonStrings returns every string value of a JSON document (object keys
// excluded), sorted.
func jsonStrings(t *testing.T, raw []byte) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(doc)
	sort.Strings(out)
	return out
}

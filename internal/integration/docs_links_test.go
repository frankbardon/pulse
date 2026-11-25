package integration_test

import (
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/doccheck"
)

// TestDocsLinksResolve is the internal link checker: every relative link and
// #anchor in docs/src/** (the generated guide included) and in the repo-root
// *.md files resolves. Book pages resolve the way the published mdBook site
// serves them — the target must stay inside docs/src, be a SUMMARY.md
// chapter, and carry the anchor under mdBook's heading-slug rules; root
// files resolve the way GitHub renders them. External http(s) links are out
// of scope (`make linkcheck-external` checks them by hand), and
// .claude/reference/ pointers belong to TestReferenceDocPointersResolve.
//
// Known violations live in the shrink-only allowlist
// internal/doccheck/allowlists/links.txt: a broken link it does not name
// fails, and so does an entry whose link has been fixed.
func TestDocsLinksResolve(t *testing.T) {
	t.Parallel()

	fsys := afero.NewOsFs()
	files, err := doccheck.DocSources(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 100 {
		t.Fatalf("found only %d markdown sources — the walk is broken", len(files))
	}
	violations, err := doccheck.CheckLinks(fsys, files, doccheck.LinkOptions{BookRoot: doccheck.BookRoot})
	if err != nil {
		t.Fatal(err)
	}
	allow, err := doccheck.Load(fsys, doccheck.AllowlistFile("links"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range allow.Reconcile(violations).Problems(allow.File) {
		t.Error(p)
	}
}

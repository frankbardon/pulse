package cli

import (
	"bytes"
	"flag"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/docgen"
)

// `make docs` regenerates docs/src/cli/reference.md by running
// TestCLIReferenceCurrent with -update-cli-reference.
var updateCLIReference = flag.Bool("update-cli-reference", false, "rewrite docs/src/cli/reference.md (prefer `make docs`)")

// TestCLIReferenceCurrent keeps the generated per-leaf flags reference
// (docs/src/cli/reference.md) current: it renders the page from the
// command tree the binary mounts and requires the committed page to match
// byte for byte. A new leaf, flag, alias, usage string or default without
// `make docs` fails here. -update-cli-reference rewrites the page instead.
func TestCLIReferenceCurrent(t *testing.T) {
	t.Parallel()
	want := docgen.RenderCLIReference(CLIReference(App()))
	osfs := afero.NewBasePathFs(afero.NewOsFs(), coverageRepoRoot)
	if *updateCLIReference {
		if err := afero.WriteFile(osfs, docgen.CLIReferencePath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := afero.ReadFile(osfs, docgen.CLIReferencePath)
	if err != nil {
		t.Fatalf("%v: run `make docs` and commit %s", err, docgen.CLIReferencePath)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale: run `make docs` and commit it (never hand-edit it; it is rendered from the command tree)", docgen.CLIReferencePath)
	}
}

// TestLeafDocLinksMounted: every leafDocLinks key is a runnable leaf, so a
// renamed or removed command cannot leave a dead entry behind.
func TestLeafDocLinksMounted(t *testing.T) {
	t.Parallel()
	mounted := map[string]bool{}
	for _, l := range Leaves(SetUpTree(App())) {
		mounted[l.Path] = true
	}
	for leaf := range leafDocLinks {
		if !mounted[leaf] {
			t.Errorf("leafDocLinks names %q, which is not a runnable leaf; drop or rename the entry", leaf)
		}
	}
}

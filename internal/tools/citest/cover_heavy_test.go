//go:build heavy

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestHeavyRun_MergesCoverProfileParts runs a real shard (go test, hence
// heavy) that needs two go test invocations — b's heavy TestShared
// collides with a's fast TestShared — and checks the shard's one
// -coverprofile holds both invocations' coverage, with the parts removed.
func TestHeavyRun_MergesCoverProfileParts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/hm\n\ngo 1.26\n")
	writeFile(t, root, ".github/heavy-shards.txt", "1 ./a TestA\n1 ./b TestShared\n")
	writeFile(t, root, ".github/workflows/ci.yml", "jobs:\n  test-heavy:\n    strategy:\n      matrix:\n        shard: [\"1\", \"catchall\"]\n")
	writeFile(t, root, "a/a.go", "package a\n\nfunc F(x int) int {\n\tif x > 0 {\n\t\treturn 1\n\t}\n\treturn 0\n}\n")
	writeFile(t, root, "a/a_heavy_test.go", "//go:build heavy\n\npackage a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { F(1) }\n")
	writeFile(t, root, "a/a_test.go", "package a\n\nimport \"testing\"\n\nfunc TestShared(t *testing.T) {}\n")
	writeFile(t, root, "b/b.go", "package b\n\nfunc G() int { return 2 }\n")
	writeFile(t, root, "b/b_heavy_test.go", "//go:build heavy\n\npackage b\n\nimport \"testing\"\n\nfunc TestShared(t *testing.T) { G() }\n")

	prof := filepath.Join(root, "cover.out")
	code, err := heavyRun(root, "1", []string{"-count=1", "-coverpkg=./...", "-coverprofile=" + prof})
	if err != nil || code != 0 {
		t.Fatalf("heavyRun = %d, %v", code, err)
	}
	data, err := os.ReadFile(prof)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseProfiles([]string{prof}, []io.Reader{bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"example.com/hm/a", "example.com/hm/b"} {
		if c := got[pkg]; c.covered == 0 {
			t.Errorf("%s coverage %+v: want covered statements from its own go test run\n%s", pkg, c, data)
		}
	}
	parts, _ := filepath.Glob(prof + ".part*")
	if len(parts) != 0 {
		t.Errorf("parts left behind: %v", parts)
	}
}

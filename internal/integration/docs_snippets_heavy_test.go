//go:build heavy

package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/doccheck"
)

// TestDocsSnippetsCompile is the compile half of the snippet check: every
// Go block in docs/src whose fence reads `go compile` builds. A block with
// a package clause builds as is; any other block builds with `package
// snippet` prepended, so it must carry its own imports. Each block is its
// own scratch package inside this module, added through go build -overlay
// so nothing is written to the source tree. Heavy tier: it runs the go
// toolchain.
//
// Known failures live in the shrink-only allowlist
// internal/doccheck/allowlists/snippets-go.txt, keyed by block hash.
func TestDocsSnippetsCompile(t *testing.T) {
	t.Parallel()

	osfs := afero.NewOsFs()
	var blocks []docSnippet
	for _, s := range collectDocSnippets(t, osfs) {
		if s.info.Lang == "go" {
			blocks = append(blocks, s)
		}
	}
	violations := buildGoSnippets(t, blocks)
	allow, err := doccheck.Load(osfs, doccheck.AllowlistFile("snippets-go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range allow.Reconcile(violations).Problems(allow.File) {
		t.Error(p)
	}
}

// TestGoSnippetBuildNamesBrokenBlock: of a good block (bare file body with
// its own imports), a full file with a package clause and a block that
// does not type-check, only the broken one is a violation, and it names
// its page and fence line.
func TestGoSnippetBuildNamesBrokenBlock(t *testing.T) {
	t.Parallel()

	mk := func(line int, body string) docSnippet {
		info := doccheck.ParseSnippetInfo("go compile")
		return docSnippet{path: "docs/src/x.md", fence: doccheck.Fence{Info: "go compile", Body: body, Line: line}, info: info}
	}
	good := mk(3, "import \"github.com/frankbardon/pulse\"\n\nvar _ = pulse.Options{}\n")
	full := mk(9, "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(1) }\n")
	broken := mk(17, "import \"github.com/frankbardon/pulse\"\n\nvar _ = pulse.NoSuchSymbol\n")
	vs := buildGoSnippets(t, []docSnippet{good, full, broken})
	if len(vs) != 1 {
		t.Fatalf("got %d violations, want 1 (the broken block): %+v", len(vs), vs)
	}
	v := vs[0]
	if v.Path != "docs/src/x.md" || v.Entry != doccheck.SnippetKey("go", []byte(broken.fence.Body)) ||
		!strings.Contains(v.Message, "line 17") || !strings.Contains(v.Message, "NoSuchSymbol") {
		t.Errorf("violation does not name the broken block: %+v", v)
	}
}

// goSnippetSource is the file a go compile block builds as.
func goSnippetSource(body string) string {
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "//") {
			continue
		}
		if strings.HasPrefix(l, "package ") {
			return body
		}
		break
	}
	return "package snippet\n\n" + body
}

// snippetPkgRe finds the scratch package a go build diagnostic is about.
var snippetPkgRe = regexp.MustCompile(`internal/doccheck/snippetbuild/s(\d+)\b`)

// buildGoSnippets builds every block in one go build -overlay invocation
// and attributes each diagnostic line to its block.
func buildGoSnippets(t *testing.T, snippets []docSnippet) []doccheck.Violation {
	t.Helper()
	if len(snippets) == 0 {
		return nil
	}
	root, err := os.Getwd() // TestMain runs from the module root
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replace := map[string]string{}
	args := []string{"build", "-o", os.DevNull}
	for i, s := range snippets {
		src := filepath.Join(scratch, fmt.Sprintf("s%d.go", i))
		if err := os.WriteFile(src, []byte(goSnippetSource(s.fence.Body)), 0o644); err != nil {
			t.Fatal(err)
		}
		dir := fmt.Sprintf("internal/doccheck/snippetbuild/s%d", i)
		replace[filepath.Join(root, filepath.FromSlash(dir), "snippet.go")] = src
		args = append(args, "./"+dir)
	}
	ov, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	ovPath := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(ovPath, ov, 0o644); err != nil {
		t.Fatal(err)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go toolchain not on PATH (needed for `go compile` blocks): %v", err)
	}
	args = append(args[:1], append([]string{"-overlay", ovPath}, args[1:]...)...)
	cmd := exec.Command(goBin, args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// go build heads each failing package's diagnostics with "# <pkg>";
	// the diagnostics name the overlay's scratch file, so strip its
	// directory.
	diag := map[int][]string{}
	cur := -1
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "# ") {
			cur = -1
			if m := snippetPkgRe.FindStringSubmatch(line); m != nil {
				cur, _ = strconv.Atoi(m[1])
			}
			continue
		}
		if cur >= 0 && strings.TrimSpace(line) != "" {
			diag[cur] = append(diag[cur], strings.TrimSpace(strings.ReplaceAll(line, scratch+string(filepath.Separator), "")))
		}
	}
	if len(diag) == 0 {
		t.Fatalf("go build failed with no diagnostic naming a snippet:\n%s", out)
	}
	var vs []doccheck.Violation
	for i, s := range snippets {
		if d := diag[i]; len(d) > 0 {
			if len(d) > 3 {
				d = d[:3]
			}
			vs = append(vs, s.violation("go compile block does not build: "+strings.Join(d, " | ")))
		}
	}
	return vs
}

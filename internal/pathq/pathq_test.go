package pathq

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestQuote_WindowsPathSingleBackslashes: a backslash is written once
// (not %q's doubled escape); a double quote and a non-printable rune
// keep %q's escaping; a path without a backslash quotes byte-identically
// to %q.
func TestQuote_WindowsPathSingleBackslashes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{`C:\Users\me\cohort.pulse`, `"C:\Users\me\cohort.pulse"`},
		{`\\server\share\a b.pulse`, `"\\server\share\a b.pulse"`},
		{`C:\`, `"C:\"`},
		{"C:\\new\"q\"\n.pulse", `"C:\new\"q\"\n.pulse"`},
		{"", `""`},
	} {
		if got := Quote(tc.in); got != tc.want {
			t.Errorf("Quote(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
	for _, p := range []string{"/data/cohort.pulse", "rel/dir", `a"b`, "tab\there", "naïve/ü.pulse", "\x00bad"} {
		if got, want := Quote(p), fmt.Sprintf("%q", p); got != want {
			t.Errorf("Quote(%q) = %s, want %%q's %s on a path without a backslash", p, got, want)
		}
	}
}

// TestPathq_ImportBoundary: pathq stays a stdlib-only leaf, so every
// package — the no-execute descriptor and the template firewall's
// allow-list included — can import it.
func TestPathq_ImportBoundary(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(path, ".") {
				t.Errorf("%s imports %s; internal/pathq must stay stdlib-only", f, path)
			}
		}
	}
}

package coverfloors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		"m.go":           "package m\n",
		"a/a.go":         "package a\n",
		"b/c/c.go":       "package c\n",
		"t/only_test.go": "package t\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestParse(t *testing.T) {
	t.Parallel()
	got, err := Parse(strings.NewReader("# header\n\na 80 # trailing\nb/c\t92.5\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Floor{{Dir: "a", Percent: 80, Line: 3}, {Dir: "b/c", Percent: 92.5, Line: 4}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}
	for _, bad := range []string{"a\n", "a 80 x\n", "a eighty\n"} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Errorf("Parse(%q): no error", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	root := tree(t)
	cases := []struct {
		name  string
		table string
		want  string // substring of the joined problems; "" = valid
	}{
		{"valid", "a 80\nb/c 0\n. 100\n", ""},
		{"empty", "# nothing\n", "no floors"},
		{"duplicate", "a 80\nb/c 90\na 85\n", ":3: a duplicates line 1"},
		{"above 100", "a 100.5\n", "outside 0..100"},
		{"below 0", "a -1\n", "outside 0..100"},
		{"nonexistent", "nope 80\n", "nope is not a Go package directory"},
		{"test-only dir", "t 80\n", "t is not a Go package directory"},
		{"unclean", "./a 80\n", "not a clean module-relative package dir"},
		{"escapes root", "../a 80\n", "not a clean module-relative package dir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			floors, err := Parse(strings.NewReader(tc.table))
			if err != nil {
				t.Fatal(err)
			}
			probs := strings.Join(Validate(root, floors), "\n")
			if tc.want == "" && probs != "" {
				t.Fatalf("unexpected problems:\n%s", probs)
			}
			if tc.want != "" && !strings.Contains(probs, tc.want) {
				t.Fatalf("problems %q do not contain %q", probs, tc.want)
			}
		})
	}
}

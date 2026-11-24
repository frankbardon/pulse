package doccheck

import (
	"strings"
	"testing"

	"github.com/spf13/afero"
)

func TestParseAllowlist(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		wantErr string
		want    int
	}{
		{name: "empty", in: "", want: 0},
		{name: "comments and blanks", in: "# header\n\n  \n# more\n", want: 0},
		{name: "two entries", in: "a.md\tx\twhy\nb.md\ty\twhy not\n", want: 2},
		{name: "crlf", in: "a.md\tx\twhy\r\n", want: 1},
		{name: "two fields", in: "a.md\tx\n", wantErr: "want 3 TAB-separated fields"},
		{name: "four fields", in: "a.md\tx\ty\tz\n", wantErr: "want 3 TAB-separated fields"},
		{name: "empty path", in: " \tx\twhy\n", wantErr: "empty path"},
		{name: "empty entry", in: "a.md\t \twhy\n", wantErr: "empty entry"},
		{name: "empty reason", in: "a.md\tx\t \n", wantErr: "empty reason"},
		{name: "duplicate", in: "a.md\tx\tone\na.md\tx\ttwo\n", wantErr: "duplicate entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, err := Parse("list.txt", strings.NewReader(tc.in))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := len(a.Entries()); got != tc.want {
				t.Fatalf("entries = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestLoadAllowlist(t *testing.T) {
	t.Parallel()
	fsys := afero.NewMemMapFs()
	if _, err := Load(fsys, AllowlistFile("missing")); err == nil {
		t.Fatal("a missing allowlist must be an error: every check commits its list")
	}
	if err := afero.WriteFile(fsys, AllowlistFile("x"), []byte("a.md\tt\twhy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Load(fsys, AllowlistFile("x"))
	if err != nil {
		t.Fatal(err)
	}
	if a.File != "internal/doccheck/allowlists/x.txt" || len(a.Entries()) != 1 || a.Entries()[0].Line != 1 {
		t.Fatalf("loaded %+v", a)
	}
}

func TestReconcile(t *testing.T) {
	t.Parallel()
	a, err := Parse("list.txt", strings.NewReader("a.md\tknown\twhy\nb.md\tfixed\twhy\n"))
	if err != nil {
		t.Fatal(err)
	}
	vs := []Violation{
		{Path: "a.md", Entry: "known", Message: "m"},
		{Path: "a.md", Entry: "known", Message: "same key, second line"},
		{Path: "c.md", Entry: "fresh", Message: "broken"},
		{Path: "c.md", Entry: "fresh", Message: "broken again"},
		{Path: "a.md", Entry: "fresh", Message: "same entry, other file"},
	}
	res := a.Reconcile(vs)
	if res.OK() {
		t.Fatal("new and stale present, OK() must be false")
	}
	if res.Allowed != 1 {
		t.Errorf("Allowed = %d, want 1", res.Allowed)
	}
	if len(res.New) != 2 || res.New[0].Path != "a.md" || res.New[1].Path != "c.md" {
		t.Errorf("New = %+v, want a.md/fresh and c.md/fresh, sorted and de-duplicated", res.New)
	}
	if len(res.Stale) != 1 || res.Stale[0].Entry != "fixed" || res.Stale[0].Line != 2 {
		t.Errorf("Stale = %+v, want b.md/fixed at line 2", res.Stale)
	}
	probs := res.Problems(a.File)
	if len(probs) != 3 {
		t.Fatalf("Problems = %q, want 3", probs)
	}
	if !strings.Contains(probs[2], "list.txt:2: stale allowlist entry") || !strings.Contains(probs[2], "delete the line") {
		t.Errorf("stale message = %q", probs[2])
	}

	// Exactly matching: clean.
	if res := a.Reconcile([]Violation{{Path: "a.md", Entry: "known"}, {Path: "b.md", Entry: "fixed"}}); !res.OK() {
		t.Errorf("exact match must pass, got %+v", res)
	}
}

func TestFormatRoundTrips(t *testing.T) {
	t.Parallel()
	vs := []Violation{{Path: "b.md", Entry: "y"}, {Path: "a.md", Entry: "x"}, {Path: "b.md", Entry: "y"}}
	out := Format(vs, "baseline")
	if out != "a.md\tx\tbaseline\nb.md\ty\tbaseline\n" {
		t.Fatalf("Format = %q", out)
	}
	a, err := Parse("f", strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if res := a.Reconcile(vs); !res.OK() {
		t.Fatalf("formatted list must cover its own violations: %+v", res)
	}
}

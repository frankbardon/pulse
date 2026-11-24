package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

func TestRunReportsBrokenExternalLinks(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
		case "/head-refused":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		case "/gone":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	fsys := afero.NewMemMapFs()
	files := map[string]string{
		"docs/src/SUMMARY.md": "- [A](a.md)\n",
		"docs/src/a.md": "[ok](" + srv.URL + "/ok#frag) [refused](" + srv.URL + "/head-refused)\n" +
			"[gone](" + srv.URL + "/gone) [internal](b.md)\n```\n[skip](" + srv.URL + "/fenced)\n```\n",
		"README.md": "<" + srv.URL + "/gone>\n[ok again](" + srv.URL + "/ok)\n",
	}
	for p, c := range files {
		if err := afero.WriteFile(fsys, p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	broken, err := run(fsys, srv.Client(), 3, &out)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if broken != 1 {
		t.Fatalf("broken = %d, want 1 (only /gone)\n%s", broken, got)
	}
	for _, want := range []string{
		"BROKEN " + srv.URL + "/gone: HTTP 404",
		"  README.md:1",
		"  docs/src/a.md:2",
		"checked 3 external URLs in 3 files: 1 broken",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "fenced") {
		t.Errorf("a link inside a code fence was checked:\n%s", got)
	}
}

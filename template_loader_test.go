package pulse

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// tmplDoc builds a minimal well-formed template document carrying the
// supplied description, so the same name written into two roots stays
// distinguishable and precedence is observable from the loaded store.
func tmplDoc(description string) string {
	return `{
  "description": "` + description + `",
  "target": "request",
  "variables": [{"name": "metric", "type": "field", "required": true}],
  "body": {"cohort": {"filename": "sales.pulse"}, "aggregations": [{"$var": "metric"}]}
}`
}

// tmplFS is the in-memory filesystem the template tests keep their
// template directories on. The store reads through Options.FS when one is
// supplied, so these tests stay off the host; each takes its own directory
// from tmplDir, which keeps parallel tests apart on the one Fs.
var tmplFS = afero.NewMemMapFs()

// tmplDirSeq numbers tmplDir directories so every call is a fresh root.
var tmplDirSeq atomic.Uint64

// tmplDir creates and returns a fresh, empty template root on tmplFS.
func tmplDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(string(filepath.Separator)+"templates", strconv.FormatUint(tmplDirSeq.Add(1), 10))
	if err := tmplFS.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	return dir
}

// writeTmpl writes content at dir/rel on tmplFS, creating parent
// directories, and returns the full path.
func writeTmpl(t *testing.T, dir, rel, content string) string {
	t.Helper()
	return writeTmplOn(t, tmplFS, dir, rel, content)
}

// writeTmplOn is writeTmpl onto an explicit filesystem — the engine's own
// Options.FS when a test also keeps cohorts there.
func writeTmplOn(t *testing.T, fsys afero.Fs, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := fsys.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := afero.WriteFile(fsys, path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return path
}

// newPulse builds a Pulse over tmplFS with the supplied template
// configuration, failing the test on error. Template roots are paths on
// that same Fs: the store reads through Options.FS.
func newPulse(t *testing.T, opts Options) *Pulse {
	t.Helper()
	opts.FS = tmplFS
	p, err := New(opts)
	if err != nil {
		t.Fatalf("New(%+v) = %v, want nil error", opts, err)
	}
	return p
}

// TestResolveTemplateDirs is the precedence contract for the directory
// list itself: the explicit option wins outright, the environment variable
// is consulted only when the option is empty, and a multi-root variable
// splits on the host path-list separator.
func TestResolveTemplateDirs(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	sep := string(os.PathListSeparator)

	tests := []struct {
		name string
		opt  []string
		env  string
		want []string
	}{
		{
			name: "neither option nor env is a no-op",
			want: nil,
		},
		{
			name: "option alone",
			opt:  []string{"/a", "/b"},
			want: []string{"/a", "/b"},
		},
		{
			name: "env alone, single root",
			env:  "/a",
			want: []string{"/a"},
		},
		{
			name: "env alone, multiple roots split on the list separator",
			env:  "/a" + sep + "/b" + sep + "/c",
			want: []string{"/a", "/b", "/c"},
		},
		{
			name: "option shadows env entirely",
			opt:  []string{"/opt"},
			env:  "/env",
			want: []string{"/opt"},
		},
		{
			name: "blank env is a no-op",
			env:  "   ",
			want: nil,
		},
		{
			name: "env preserves author order",
			env:  "/z" + sep + "/a",
			want: []string{"/z", "/a"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envTemplatesDir, tc.env)
			got := resolveTemplateDirs(&Options{TemplateDirs: tc.opt})
			if len(got) != len(tc.want) {
				t.Fatalf("resolveTemplateDirs = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("resolveTemplateDirs = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestResolveTemplateDirs_DoesNotAliasCallerSlice guards the option path:
// the resolved list must be the engine's own copy, so a caller mutating the
// slice it handed to Options cannot retarget a live store.
func TestResolveTemplateDirs_DoesNotAliasCallerSlice(t *testing.T) {
	t.Parallel()

	caller := []string{"/a", "/b"}
	got := resolveTemplateDirs(&Options{TemplateDirs: caller})
	caller[0] = "/mutated"
	if got[0] != "/a" {
		t.Fatalf("resolved dirs alias the caller slice: got %v", got)
	}
}

// TestNew_NoTemplateDirsIsSilentNoOp is the "unconfigured is normal"
// promise: New succeeds with no store at all, and a lookup against that
// absent store answers PULSE_TEMPLATE_NOT_FOUND instead of panicking.
func TestNew_NoTemplateDirsIsSilentNoOp(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	t.Setenv(envTemplatesDir, "")

	p := newPulse(t, Options{})
	if p.templates != nil {
		t.Fatalf("expected no template store when nothing is configured, got %v", p.templates.Dirs())
	}
	if got := p.templates.List(); got != nil {
		t.Fatalf("nil store List() = %v, want nil", got)
	}

	_, err := p.templates.Get("anything")
	if err == nil {
		t.Fatal("Get on an unconfigured store = nil error, want PULSE_TEMPLATE_NOT_FOUND")
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("Get error = %v (%T), want *errors.CodedError", err, err)
	}
	if ce.Code != perr.PULSE_TEMPLATE_NOT_FOUND {
		t.Fatalf("Get error code = %s, want %s", ce.Code, perr.PULSE_TEMPLATE_NOT_FOUND)
	}
}

// TestNew_TemplateDirsFromOption wires the explicit option end to end: the
// store New builds indexes the files under the configured roots.
func TestNew_TemplateDirsFromOption(t *testing.T) {
	t.Parallel()

	dir := tmplDir(t)
	writeTmpl(t, dir, "finance/revenue.json", tmplDoc("from option"))

	p := newPulse(t, Options{TemplateDirs: []string{dir}})

	tmpl, err := p.templates.Get("finance/revenue")
	if err != nil {
		t.Fatalf("Get(finance/revenue) = %v, want the loaded template", err)
	}
	if tmpl.Description != "from option" {
		t.Fatalf("Description = %q, want %q", tmpl.Description, "from option")
	}
}

// TestNew_TemplateDirsReadThroughOptionsFS is the embedder contract: with
// Options.FS supplied, template roots are paths on that filesystem. A
// template that exists only in memory loads and lists; its path is absent
// from the host.
func TestNew_TemplateDirsReadThroughOptionsFS(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	dir := filepath.Join(string(filepath.Separator)+"pulse-mem-only", "templates")
	writeTmplOn(t, fsys, dir, "finance/revenue.json", tmplDoc("in memory"))
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("%s exists on the host; the test needs a path only the in-memory Fs has", dir)
	}

	p, err := New(Options{FS: fsys, TemplateDirs: []string{dir}})
	if err != nil {
		t.Fatalf("New = %v, want the in-memory template loaded", err)
	}
	tmpl, err := p.GetTemplate("finance/revenue")
	if err != nil {
		t.Fatalf("GetTemplate(finance/revenue) = %v", err)
	}
	if tmpl.Description != "in memory" {
		t.Fatalf("Description = %q, want %q", tmpl.Description, "in memory")
	}
	if listed := p.ListTemplates(); len(listed) != 1 || listed[0].Path != filepath.Join(dir, "finance", "revenue.json") {
		t.Fatalf("ListTemplates() = %+v, want the one in-memory template", listed)
	}
}

// TestNew_TemplateDirsHostFsWithoutFS is the CLI / MCP shape: with no
// Options.FS the instance Fs is rooted at DataDir, yet template roots stay
// host paths — absolute, outside the data directory — read from the host
// filesystem.
func TestNew_TemplateDirsHostFsWithoutFS(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "host.json"), []byte(tmplDoc("on host")), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := New(Options{DataDir: t.TempDir(), TemplateDirs: []string{dir}})
	if err != nil {
		t.Fatalf("New = %v", err)
	}
	tmpl, err := p.GetTemplate("host")
	if err != nil {
		t.Fatalf("GetTemplate(host) = %v, want the host template", err)
	}
	if tmpl.Description != "on host" {
		t.Fatalf("Description = %q, want %q", tmpl.Description, "on host")
	}
}

// TestNew_TemplateDirsFromEnv covers the environment fallback, including
// the multi-root form: one variable carrying several roots separated by
// the host path-list separator, in precedence order.
func TestNew_TemplateDirsFromEnv(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	first := tmplDir(t)
	second := tmplDir(t)
	writeTmpl(t, first, "a.json", tmplDoc("first root"))
	writeTmpl(t, second, "b.json", tmplDoc("second root"))

	t.Setenv(envTemplatesDir, first+string(os.PathListSeparator)+second)

	p := newPulse(t, Options{})

	dirs := p.templates.Dirs()
	if len(dirs) != 2 || dirs[0] != first || dirs[1] != second {
		t.Fatalf("Dirs() = %v, want [%s %s]", dirs, first, second)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := p.templates.Get(name); err != nil {
			t.Fatalf("Get(%s) = %v, want the loaded template", name, err)
		}
	}
}

// TestNew_TemplateDirsOptionShadowsEnv proves the fallback is a fallback:
// with the option set, the environment variable is never consulted, so a
// template that exists only under the env root stays unreachable.
func TestNew_TemplateDirsOptionShadowsEnv(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	optDir := tmplDir(t)
	envDir := tmplDir(t)
	writeTmpl(t, optDir, "wanted.json", tmplDoc("option root"))
	writeTmpl(t, envDir, "unwanted.json", tmplDoc("env root"))

	t.Setenv(envTemplatesDir, envDir)

	p := newPulse(t, Options{TemplateDirs: []string{optDir}})

	if _, err := p.templates.Get("wanted"); err != nil {
		t.Fatalf("Get(wanted) = %v, want the option root's template", err)
	}
	if _, err := p.templates.Get("unwanted"); err == nil {
		t.Fatal("Get(unwanted) succeeded; the env var must be ignored when TemplateDirs is set")
	}
}

// TestNew_TemplateDirsPrecedenceEndToEnd is the ordering contract measured
// through New rather than through the store alone: the same name in two
// configured roots resolves to the FIRST root's document.
func TestNew_TemplateDirsPrecedenceEndToEnd(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	high := tmplDir(t)
	low := tmplDir(t)
	writeTmpl(t, high, "shared.json", tmplDoc("high precedence"))
	writeTmpl(t, low, "shared.json", tmplDoc("low precedence"))

	t.Run("option order", func(t *testing.T) {
		p := newPulse(t, Options{TemplateDirs: []string{high, low}})
		tmpl, err := p.templates.Get("shared")
		if err != nil {
			t.Fatalf("Get(shared) = %v", err)
		}
		if tmpl.Description != "high precedence" {
			t.Fatalf("Description = %q, want the first root's document", tmpl.Description)
		}
	})

	t.Run("option order reversed", func(t *testing.T) {
		p := newPulse(t, Options{TemplateDirs: []string{low, high}})
		tmpl, err := p.templates.Get("shared")
		if err != nil {
			t.Fatalf("Get(shared) = %v", err)
		}
		if tmpl.Description != "low precedence" {
			t.Fatalf("Description = %q, want the first root's document", tmpl.Description)
		}
	})

	t.Run("env order", func(t *testing.T) {
		t.Setenv(envTemplatesDir, high+string(os.PathListSeparator)+low)
		p := newPulse(t, Options{})
		tmpl, err := p.templates.Get("shared")
		if err != nil {
			t.Fatalf("Get(shared) = %v", err)
		}
		if tmpl.Description != "high precedence" {
			t.Fatalf("Description = %q, want the first root's document", tmpl.Description)
		}
	})
}

// TestNew_TemplateDirShapes covers the directory-shape behaviours the store
// implements, asserted through New so the wiring is what is under test: an
// absent root is an absent layer, a root that is a regular file is a
// misconfiguration.
func TestNew_TemplateDirShapes(t *testing.T) {
	t.Parallel()

	present := tmplDir(t)
	writeTmpl(t, present, "ok.json", tmplDoc("present"))

	fileRoot := filepath.Join(tmplDir(t), "not-a-dir")
	if err := afero.WriteFile(tmplFS, fileRoot, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		dirs    []string
		wantErr bool
		// wantErrSubstr, when set, must appear in the New error.
		wantErrSubstr string
	}{
		{
			name: "missing root is skipped without error",
			dirs: []string{filepath.Join(tmplDir(t), "absent")},
		},
		{
			name: "missing root alongside a present one still loads the present one",
			dirs: []string{filepath.Join(tmplDir(t), "absent"), present},
		},
		{
			name:          "root that is a regular file is an error",
			dirs:          []string{fileRoot},
			wantErr:       true,
			wantErrSubstr: fileRoot,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Options{FS: tmplFS, TemplateDirs: tc.dirs})
			if tc.wantErr {
				if err == nil {
					t.Fatal("New = nil error, want a failure")
				}
				if tc.wantErrSubstr != "" && !strings.Contains(err.Error(), strconv.Quote(tc.wantErrSubstr)) {
					t.Fatalf("New error %q does not name %q", err, tc.wantErrSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New = %v, want nil error", err)
			}
		})
	}
}

// TestNew_MalformedTemplateFailsNew is the fail-fast promise: a broken
// document in a configured directory stops startup, and the error names the
// offending file so the operator knows which one to open.
func TestNew_MalformedTemplateFailsNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rel     string
		content string
	}{
		{
			name:    "unparseable JSON",
			rel:     "broken.json",
			content: `{"target": "request",`,
		},
		{
			name:    "valid JSON, invalid declaration",
			rel:     "nobody.json",
			content: `{"target": "request", "body": {"cohort": {"filename": "x.pulse"}}, "variables": [{"name": "", "type": "string"}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tmplDir(t)
			path := writeTmpl(t, dir, tc.rel, tc.content)

			_, err := New(Options{FS: tmplFS, TemplateDirs: []string{dir}})
			if err == nil {
				t.Fatal("New = nil error, want the malformed template to fail startup")
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("New error %q does not name the offending file %q", err, path)
			}
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("New error = %v (%T), want *errors.CodedError", err, err)
			}
		})
	}
}

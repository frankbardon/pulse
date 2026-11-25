package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/doccheck"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
)

// docSnippet is one fenced block of a docs/src page the snippet check
// looks at.
type docSnippet struct {
	path  string
	fence doccheck.Fence
	info  doccheck.SnippetInfo
	root  doccheck.RootKind // json request roots only
}

func (s docSnippet) violation(msg string) doccheck.Violation {
	return doccheck.Violation{
		Path:    s.path,
		Entry:   doccheck.SnippetKey(s.info.Lang, []byte(s.fence.Body)),
		Message: fmt.Sprintf("block at line %d: %s", s.fence.Line, msg),
	}
}

// snippetFixtureDir is where the fixture cohorts a cohort= tag names are
// imported, on the in-memory layer of the snippet filesystem.
const snippetFixtureDir = "/snippet-fixtures"

// snippetFixtureCohorts lists the cohort names a cohort= tag may use: every
// shared example fixture (internal/examples/fixtures/<name>.csv with its
// schemas/<name>.json), the cohorts fixtures/build.sh imports.
func snippetFixtureCohorts(t *testing.T) map[string]bool {
	t.Helper()
	csvs, err := filepath.Glob(filepath.Join("internal", "examples", "fixtures", "*.csv"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range csvs {
		name := strings.TrimSuffix(filepath.Base(c), ".csv")
		if _, err := os.Stat(filepath.Join("internal", "examples", "fixtures", "schemas", name+".json")); err == nil {
			out[name] = true
		}
	}
	if len(out) < 10 {
		t.Fatalf("found only %d fixture cohorts — the fixture glob is broken", len(out))
	}
	return out
}

// collectDocSnippets scans every docs/src page (the generated guide
// included) for json and go blocks. A json block is kept only when it
// strict-decodes as a request root; a go block only when tagged compile.
func collectDocSnippets(t *testing.T, fsys afero.Fs) []docSnippet {
	t.Helper()
	var out []docSnippet
	err := afero.Walk(fsys, doccheck.BookRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		src, err := afero.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		for _, f := range doccheck.ScanFences(src) {
			s := docSnippet{path: filepath.ToSlash(p), fence: f, info: doccheck.ParseSnippetInfo(f.Info)}
			switch {
			case s.info.Lang == "json":
				root, ok := doccheck.RequestRoot([]byte(f.Body))
				if !ok {
					continue
				}
				s.root = root
			case s.info.Lang == "go" && s.info.Compile:
			default:
				continue
			}
			out = append(out, s)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// TestDocsSnippetsRun runs the book's request examples. Every fenced json
// block in docs/src (the generated guide included) that strict-decodes as
// a request root (Request, ComposedRequest, ChainRequest, FacetRequest)
// runs against the shared example fixture cohorts: its predict must be
// valid and its process must return no error; the output is not
// asserted. Blocks that are not request roots are skipped silently.
// Info-string tags:
//
//   - `json norun` opts a block out;
//   - `json cohort=<name>[,<name>…]` names the fixture cohorts
//     (internal/examples/fixtures/<name>.csv) the block runs on: every
//     cohort reference is pointed at them, and a filename that is not one
//     of them becomes the first. A runnable block without the tag fails.
//
// Go blocks opt in to a compile-only check with `go compile`
// (TestDocsSnippetsCompile, heavy tier: it shells out to go build).
//
// Known failures live in the shrink-only allowlist
// internal/doccheck/allowlists/snippets.txt, keyed by a hash of the block
// body: a new failure fails, and so does an entry whose block now passes
// or has changed. A generated guide page is fixed in its generator
// (internal/docgen) or the skill it renders, never on the page.
func TestDocsSnippetsRun(t *testing.T) {
	t.Parallel()

	osfs := afero.NewOsFs()
	snippets := collectDocSnippets(t, osfs)
	if len(snippets) < 20 {
		t.Fatalf("found only %d request snippets — the scan is broken", len(snippets))
	}
	violations := checkDocSnippets(t, snippets, snippetFixtureCohorts(t))

	allow, err := doccheck.Load(osfs, doccheck.AllowlistFile("snippets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range allow.Reconcile(violations).Problems(allow.File) {
		t.Error(p)
	}
}

// checkDocSnippets runs every runnable json snippet and returns its
// failures.
func checkDocSnippets(t *testing.T, snippets []docSnippet, fixtures map[string]bool) []doccheck.Violation {
	t.Helper()
	var vs []doccheck.Violation
	run := newSnippetRunner(t)
	for _, s := range snippets {
		if s.info.Lang != "json" || s.info.NoRun {
			continue
		}
		if len(s.info.Cohorts) == 0 {
			vs = append(vs, s.violation(fmt.Sprintf("a runnable %s has no cohort= tag — tag the fence `json cohort=<fixture>` (internal/examples/fixtures) or `json norun`", s.root)))
			continue
		}
		unknown := ""
		for _, c := range s.info.Cohorts {
			if !fixtures[c] {
				unknown = c
				break
			}
		}
		if unknown != "" {
			vs = append(vs, s.violation(fmt.Sprintf("cohort=%s names no fixture cohort under internal/examples/fixtures", unknown)))
			continue
		}
		if msg := run.run(s); msg != "" {
			vs = append(vs, s.violation(msg))
		}
	}
	return vs
}

// snippetRunner holds the Pulse instance and the lazily imported fixture
// cohorts the json snippets run on.
type snippetRunner struct {
	t      *testing.T
	fs     afero.Fs
	p      *pulse.Pulse
	mu     sync.Mutex
	loaded map[string]error
}

func newSnippetRunner(t *testing.T) *snippetRunner {
	// Reads of the fixture CSVs come from disk; every write (the imported
	// cohorts, anything a snippet saves) lands in memory.
	fs := afero.NewCopyOnWriteFs(afero.NewReadOnlyFs(afero.NewOsFs()), afero.NewMemMapFs())
	if err := fs.MkdirAll(snippetFixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs, DataDir: snippetFixtureDir})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return &snippetRunner{t: t, fs: fs, p: p, loaded: map[string]error{}}
}

// fixture imports a fixture cohort once.
func (r *snippetRunner) fixture(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, done := r.loaded[name]; done {
		return err
	}
	err := func() error {
		schema, err := loadFixtureSchema(filepath.Join("internal", "examples", "fixtures", "schemas", name+".json"))
		if err != nil {
			return err
		}
		_, err = r.p.Import(context.Background(), &pio.ImportJob{
			Source:     csv.NewReader(r.fs, filepath.Join("internal", "examples", "fixtures", name+".csv")),
			Target:     path.Join(snippetFixtureDir, name+".pulse"),
			Schema:     schema,
			SampleRows: 50,
			FS:         r.fs,
		})
		return err
	}()
	r.loaded[name] = err
	return err
}

// run predicts and processes one json snippet; it returns "" on success,
// else what failed.
func (r *snippetRunner) run(s docSnippet) string {
	for _, c := range s.info.Cohorts {
		if err := r.fixture(c); err != nil {
			r.t.Fatalf("import fixture cohort %s: %v", c, err)
		}
	}
	body, err := doccheck.RetargetCohorts([]byte(s.fence.Body), s.info.Cohorts, snippetFixtureDir)
	if err != nil {
		return "retarget cohorts: " + err.Error()
	}
	ctx := context.Background()
	decode := func(out any) string {
		if err := json.Unmarshal(body, out); err != nil {
			return "decode: " + err.Error()
		}
		return ""
	}
	switch s.root {
	case doccheck.RootComposed:
		var req pulse.ComposedRequest
		if msg := decode(&req); msg != "" {
			return msg
		}
		if msg := envelopeFault(r.p.PredictCompose(ctx, &req)); msg != "" {
			return msg
		}
		if _, err := r.p.Compose(ctx, &req); err != nil {
			return "process: " + err.Error()
		}
	case doccheck.RootChain:
		var req pulse.ChainRequest
		if msg := decode(&req); msg != "" {
			return msg
		}
		if msg := envelopeFault(r.p.PredictChain(ctx, &req)); msg != "" {
			return msg
		}
		if _, err := r.p.ProcessChain(ctx, &req); err != nil {
			return "process: " + err.Error()
		}
	case doccheck.RootFacet:
		var req pulse.FacetRequest
		if msg := decode(&req); msg != "" {
			return msg
		}
		if msg := envelopeFault(r.p.PredictFacet(ctx, &req)); msg != "" {
			return msg
		}
		if _, err := r.p.FacetSchema(ctx, &req); err != nil {
			return "process: " + err.Error()
		}
	default:
		var req pulse.Request
		if msg := decode(&req); msg != "" {
			return msg
		}
		res, err := r.p.Predict(ctx, &req)
		if err != nil {
			return "predict: " + err.Error()
		}
		if !res.Valid {
			return "predict: invalid: " + entriesText(res.Errors)
		}
		if _, err := r.p.Process(ctx, &req); err != nil {
			return "process: " + err.Error()
		}
	}
	return ""
}

func envelopeFault(env *descriptor.Envelope, err error) string {
	if err != nil {
		return "predict: " + err.Error()
	}
	if len(env.Errors) > 0 {
		return "predict: invalid: " + entriesText(env.Errors)
	}
	return ""
}

func entriesText(es []*descriptor.EnvelopeEntry) string {
	var parts []string
	for _, e := range es {
		parts = append(parts, e.Code+" "+e.Message)
	}
	if len(parts) == 0 {
		return "no error entries"
	}
	return strings.Join(parts, "; ")
}

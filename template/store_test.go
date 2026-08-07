package template_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/template"
)

// validTemplate builds a minimal well-formed template document carrying the
// supplied description, so fixtures written to two directories under the
// same name stay distinguishable.
func validTemplate(description string) string {
	return `{
  "description": ` + quote(description) + `,
  "target": "request",
  "variables": [{"name": "metric", "type": "field", "required": true}],
  "body": {"cohort": {"filename": "sales.pulse"}, "aggregations": [{"$var": "metric"}]}
}`
}

// quote is a tiny JSON string quoter for fixture bodies. The descriptions
// the tests use carry no escapes, so a wrapping pair of quotes is enough.
func quote(s string) string { return `"` + s + `"` }

// writeFile writes content at dir/rel, creating parent directories. rel is
// slash-separated for readability and converted to the host separator here,
// which is exactly the round trip the name derivation has to survive.
func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return path
}

// newStore builds a store over dirs and fails the test on error.
func newStore(t *testing.T, dirs ...string) *template.Store {
	t.Helper()
	s, err := template.NewStore(dirs)
	if err != nil {
		t.Fatalf("NewStore(%v) = %v, want nil error", dirs, err)
	}
	return s
}

// listedNames projects a store listing to its names, in listing order.
func listedNames(s *template.Store) []string {
	summaries := s.List()
	out := make([]string, 0, len(summaries))
	for _, sum := range summaries {
		out = append(out, sum.Name)
	}
	return out
}

// TestNewStore_NameIsPathRelative is the naming contract: a template's name
// is its path relative to its OWN directory root, minus the .json
// extension, forward-slash separated. Subdirectories namespace, and the
// root directory's own name never appears in the derived name.
func TestNewStore_NameIsPathRelative(t *testing.T) {
	dir := t.TempDir()
	fixtures := []string{
		"revenue.json",
		"finance/revenue.json",
		"finance/deep/nested/q1.json",
		"a/b/c/d/e.json",
	}
	for _, rel := range fixtures {
		writeFile(t, dir, rel, validTemplate(rel))
	}

	s := newStore(t, dir)

	tests := []struct {
		name     string
		wantDesc string
	}{
		{"revenue", "revenue.json"},
		{"finance/revenue", "finance/revenue.json"},
		{"finance/deep/nested/q1", "finance/deep/nested/q1.json"},
		{"a/b/c/d/e", "a/b/c/d/e.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Get(tt.name)
			if err != nil {
				t.Fatalf("Get(%q) = %v, want the template", tt.name, err)
			}
			if got.Name != tt.name {
				t.Errorf("Template.Name = %q, want %q — the store must stamp the derived name", got.Name, tt.name)
			}
			if got.Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q — wrong file resolved for this name", got.Description, tt.wantDesc)
			}
		})
	}

	if want := len(fixtures); len(s.List()) != want {
		t.Errorf("List() has %d entries, want %d", len(s.List()), want)
	}
}

// TestNewStore_NamesUseForwardSlashes pins separator normalization. The
// fixtures are written through filepath.Join, so on a backslash host the
// on-disk path carries backslashes; the derived name must not.
func TestNewStore_NamesUseForwardSlashes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "finance/quarterly/revenue.json", validTemplate("nested"))

	s := newStore(t, dir)

	names := listedNames(s)
	if len(names) != 1 {
		t.Fatalf("List() = %v, want exactly one entry", names)
	}
	if names[0] != "finance/quarterly/revenue" {
		t.Fatalf("derived name = %q, want %q", names[0], "finance/quarterly/revenue")
	}
	if strings.ContainsRune(names[0], os.PathSeparator) && os.PathSeparator != '/' {
		t.Errorf("derived name %q carries the host path separator; names are always forward-slash separated", names[0])
	}
	if strings.Contains(names[0], `\`) {
		t.Errorf("derived name %q carries a backslash", names[0])
	}
	if _, err := s.Get("finance/quarterly/revenue"); err != nil {
		t.Errorf("Get by the forward-slash name = %v, want the template", err)
	}
}

// TestNewStore_SkipsNonJSONAndDirectories asserts the walk ignores anything
// that is not a *.json file, silently — a README, an editor swap file, and
// an empty subdirectory are all normal contents of a template directory and
// none of them is an error.
func TestNewStore_SkipsNonJSONAndDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "revenue.json", validTemplate("kept"))
	writeFile(t, dir, "README.md", "# not a template")
	writeFile(t, dir, "notes.txt", "definitely not JSON")
	writeFile(t, dir, ".DS_Store", "\x00\x01")
	writeFile(t, dir, "revenue.json.swp", "garbage")
	writeFile(t, dir, "sub/README.md", "# also not a template")
	if err := os.MkdirAll(filepath.Join(dir, "empty", "deeper"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	s := newStore(t, dir)

	got := listedNames(s)
	if len(got) != 1 || got[0] != "revenue" {
		t.Fatalf("List() = %v, want exactly [revenue] — non-JSON files and directories must be skipped silently", got)
	}
}

// TestNewStore_FirstDirectoryWins is the collision contract. Directories
// are an ordered precedence list: the same name in two roots is not an
// error, the first root wins, and the loser is recorded rather than
// discarded.
func TestNewStore_FirstDirectoryWins(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	firstPath := writeFile(t, first, "shared.json", validTemplate("from first"))
	secondPath := writeFile(t, second, "shared.json", validTemplate("from second"))
	writeFile(t, second, "only-second.json", validTemplate("second only"))

	tests := []struct {
		name        string
		dirs        []string
		wantDesc    string
		wantPath    string
		wantShadows []string
	}{
		{
			name:        "first listed root wins",
			dirs:        []string{first, second},
			wantDesc:    "from first",
			wantPath:    firstPath,
			wantShadows: []string{secondPath},
		},
		{
			name:        "reversing the list reverses the winner",
			dirs:        []string{second, first},
			wantDesc:    "from second",
			wantPath:    secondPath,
			wantShadows: []string{firstPath},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t, tt.dirs...)

			got, err := s.Get("shared")
			if err != nil {
				t.Fatalf("Get(shared) = %v, want the winning template (a collision is not an error)", err)
			}
			if got.Description != tt.wantDesc {
				t.Errorf("Get(shared).Description = %q, want %q", got.Description, tt.wantDesc)
			}

			sum, ok := summaryFor(s, "shared")
			if !ok {
				t.Fatalf("List() carries no summary for %q", "shared")
			}
			if sum.Path != tt.wantPath {
				t.Errorf("Summary.Path = %q, want %q", sum.Path, tt.wantPath)
			}
			if len(sum.Shadows) != len(tt.wantShadows) {
				t.Fatalf("Summary.Shadows = %v, want %v", sum.Shadows, tt.wantShadows)
			}
			for i, want := range tt.wantShadows {
				if sum.Shadows[i] != want {
					t.Errorf("Summary.Shadows[%d] = %q, want %q", i, sum.Shadows[i], want)
				}
			}

			// The shadowed entry is retained, not discarded: the name
			// still resolves and the listing still names the loser.
			if _, err := s.Get("only-second"); err != nil {
				t.Errorf("Get(only-second) = %v, want the template — shadowing one name must not drop a whole root", err)
			}
		})
	}
}

// summaryFor finds the listing entry for name.
func summaryFor(s *template.Store, name string) (template.Summary, bool) {
	for _, sum := range s.List() {
		if sum.Name == name {
			return sum, true
		}
	}
	return template.Summary{}, false
}

// TestNewStore_ThreeRootsShadowInOrder asserts precedence is a full ordered
// list rather than a two-way rule: the winner is the earliest root and
// every later root is recorded, in root order.
func TestNewStore_ThreeRootsShadowInOrder(t *testing.T) {
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	pa := writeFile(t, a, "shared.json", validTemplate("a"))
	pb := writeFile(t, b, "shared.json", validTemplate("b"))
	pc := writeFile(t, c, "shared.json", validTemplate("c"))

	s := newStore(t, a, b, c)

	got, err := s.Get("shared")
	if err != nil {
		t.Fatalf("Get(shared) = %v", err)
	}
	if got.Description != "a" {
		t.Errorf("winner = %q, want the first root's entry", got.Description)
	}
	sum, ok := summaryFor(s, "shared")
	if !ok {
		t.Fatal("List() carries no summary for shared")
	}
	want := []string{pb, pc}
	if len(sum.Shadows) != len(want) {
		t.Fatalf("Shadows = %v, want %v", sum.Shadows, want)
	}
	for i := range want {
		if sum.Shadows[i] != want[i] {
			t.Errorf("Shadows[%d] = %q, want %q", i, sum.Shadows[i], want[i])
		}
	}
	if sum.Path != pa {
		t.Errorf("Summary.Path = %q, want %q", sum.Path, pa)
	}
}

// TestNewStore_DuplicateNameWithinOneRootIsImpossible documents WHY the
// collision rule only ever has to arbitrate across roots. Within one root
// the derived name is invertible — appending ".json" to a name recovers the
// relative path exactly — so two distinct files can never produce the same
// name. The near-miss shapes are asserted explicitly: a file and a
// directory of the same stem, and a doubled extension.
func TestNewStore_DuplicateNameWithinOneRootIsImpossible(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", validTemplate("file x"))
	writeFile(t, dir, "x/y.json", validTemplate("nested under x"))
	writeFile(t, dir, "x.json.json", validTemplate("doubled extension"))

	s := newStore(t, dir)

	wantDescs := map[string]string{
		"x":      "file x",
		"x/y":    "nested under x",
		"x.json": "doubled extension",
	}
	summaries := s.List()
	if len(summaries) != len(wantDescs) {
		t.Fatalf("List() = %v, want %d distinct names", listedNames(s), len(wantDescs))
	}
	for _, sum := range summaries {
		if len(sum.Shadows) != 0 {
			t.Errorf("name %q reports shadows %v; within a single root no name can ever collide", sum.Name, sum.Shadows)
		}
		want, ok := wantDescs[sum.Name]
		if !ok {
			t.Fatalf("unexpected name %q in listing %v", sum.Name, listedNames(s))
		}
		got, err := s.Get(sum.Name)
		if err != nil {
			t.Fatalf("Get(%q) = %v", sum.Name, err)
		}
		if got.Description != want {
			t.Errorf("Get(%q).Description = %q, want %q", sum.Name, got.Description, want)
		}
	}
}

// TestNewStore_MalformedFileFailsConstruction is the fail-fast contract:
// one bad document anywhere under any root fails construction, and the
// error names the offending PATH — which Parse alone cannot do, because a
// document whose bytes do not parse has no knowable name.
func TestNewStore_MalformedFileFailsConstruction(t *testing.T) {
	tests := []struct {
		name     string
		rel      string
		content  string
		wantCode perr.Code
	}{
		{
			name:     "malformed JSON",
			rel:      "broken.json",
			content:  `{"target": "request", "body": {`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "unknown wrapper key",
			rel:      "typo.json",
			content:  `{"target":"request","varaibles":[],"body":{"cohort":{"filename":"s.pulse"}}}`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "absent target",
			rel:      "nested/no-target.json",
			content:  `{"body":{"cohort":{"filename":"s.pulse"}}}`,
			wantCode: perr.PULSE_TEMPLATE_TARGET_UNKNOWN,
		},
		{
			name:     "unrecognised target",
			rel:      "bad-target.json",
			content:  `{"target":"Request","body":{"cohort":{"filename":"s.pulse"}}}`,
			wantCode: perr.PULSE_TEMPLATE_TARGET_UNKNOWN,
		},
		{
			name:     "empty body",
			rel:      "empty-body.json",
			content:  `{"target":"request","body":{}}`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
		{
			name:     "marker names an undeclared variable",
			rel:      "deep/nested/typo-marker.json",
			content:  `{"target":"request","variables":[{"name":"metric","type":"field"}],"body":{"aggregations":[{"$var":"metrc"}]}}`,
			wantCode: perr.PULSE_TEMPLATE_INVALID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			// A healthy sibling proves the failure is attributed to the
			// offending file rather than to "something in this directory".
			writeFile(t, dir, "healthy.json", validTemplate("fine"))
			path := writeFile(t, dir, tt.rel, tt.content)

			s, err := template.NewStore([]string{dir})
			if err == nil {
				t.Fatalf("NewStore() = %v, want %s for %s", s, tt.wantCode, path)
			}
			if !perr.HasCode(err, tt.wantCode) {
				t.Fatalf("NewStore() = %v, want code %s", err, tt.wantCode)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name the offending path %q", err.Error(), path)
			}

			ce := codedError(t, err)
			if got := ce.Details["path"]; got != path {
				t.Errorf("details[path] = %v, want %q", got, path)
			}
			wantName := strings.TrimSuffix(tt.rel, ".json")
			if got := ce.Details[perr.DetailTemplate]; got != wantName {
				t.Errorf("details[%q] = %v, want %q — the path names the template even when the bytes do not parse",
					perr.DetailTemplate, got, wantName)
			}
			if strings.TrimSpace(ce.Message) == "" {
				t.Error("coded error carries an empty message")
			}
		})
	}
}

// TestNewStore_EmptyDerivedNameIsInvalid covers the one path shape that
// yields no name at all. Validate deliberately does not require Name —
// naming is the store's job — so the store is the layer that has to reject
// a file that cannot be named.
func TestNewStore_EmptyDerivedNameIsInvalid(t *testing.T) {
	tests := []struct {
		name string
		rel  string
	}{
		{"bare extension at the root", ".json"},
		{"bare extension in a subdirectory", "finance/.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, tt.rel, validTemplate("unnameable"))

			_, err := template.NewStore([]string{dir})
			if err == nil {
				t.Fatal("NewStore() = nil error, want PULSE_TEMPLATE_INVALID for a file with no derivable name")
			}
			if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
				t.Fatalf("NewStore() = %v, want PULSE_TEMPLATE_INVALID", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name the offending path %q", err.Error(), path)
			}
		})
	}
}

// TestNewStore_UnreadableFileIsAFilesystemFault pins the split between a
// filesystem fault and a document fault. An unreadable file is not an
// invalid template: reporting it as one sends an operator to inspect JSON
// when the real fault is a permission bit.
func TestNewStore_UnreadableFileIsAFilesystemFault(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "revenue.json", validTemplate("unreadable"))
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("this user bypasses file permissions; the unreadable-file path is untestable here")
	}

	_, err := template.NewStore([]string{dir})
	if err == nil {
		t.Fatal("NewStore() = nil error, want a filesystem fault for an unreadable template file")
	}
	if !perr.HasCode(err, perr.DATA_FILE) {
		t.Fatalf("NewStore() = %v, want %s", err, perr.DATA_FILE)
	}
	if perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Errorf("NewStore() = %v; an unreadable file must not be reported as an invalid document", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the offending path %q", err.Error(), path)
	}
	if got := codedError(t, err).Details["path"]; got != path {
		t.Errorf("details[path] = %v, want %q", got, path)
	}
}

// TestNewStore_SameRootListedTwice asserts a root repeated in the
// precedence list does not shadow itself. Self-shadowing would report a
// phantom collision on every name in that root.
func TestNewStore_SameRootListedTwice(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "revenue.json", validTemplate("only copy"))

	s := newStore(t, dir, dir)

	sum, ok := summaryFor(s, "revenue")
	if !ok {
		t.Fatalf("List() = %v, want an entry for revenue", listedNames(s))
	}
	if len(sum.Shadows) != 0 {
		t.Errorf("Shadows = %v, want none — a root listed twice is still one copy of each file", sum.Shadows)
	}
	if sum.Path != path {
		t.Errorf("Summary.Path = %q, want %q", sum.Path, path)
	}
	if got, err := s.Get("revenue"); err != nil {
		t.Errorf("Get(revenue) = %v, want the template", err)
	} else if got.Description != "only copy" {
		t.Errorf("Get(revenue).Description = %q, want %q", got.Description, "only copy")
	}
}

// TestNewStore_DocumentNameMustMatchPath asserts the redundancy check. The
// path is authoritative, so a document may omit `name` or repeat it — but a
// document claiming a name its path does not give it is an author error,
// not a silent rename.
func TestNewStore_DocumentNameMustMatchPath(t *testing.T) {
	body := `"body":{"cohort":{"filename":"s.pulse"}}`

	t.Run("agreeing name is accepted", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "finance/revenue.json", `{"name":"finance/revenue","target":"request",`+body+`}`)

		s := newStore(t, dir)
		got, err := s.Get("finance/revenue")
		if err != nil {
			t.Fatalf("Get() = %v", err)
		}
		if got.Name != "finance/revenue" {
			t.Errorf("Name = %q, want %q", got.Name, "finance/revenue")
		}
	})

	t.Run("disagreeing name is rejected", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFile(t, dir, "finance/revenue.json", `{"name":"something-else","target":"request",`+body+`}`)

		_, err := template.NewStore([]string{dir})
		if err == nil {
			t.Fatal("NewStore() = nil error, want PULSE_TEMPLATE_INVALID for a name/path disagreement")
		}
		if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
			t.Fatalf("NewStore() = %v, want PULSE_TEMPLATE_INVALID", err)
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("error %q does not name the offending path %q", err.Error(), path)
		}
		if !strings.Contains(err.Error(), "something-else") {
			t.Errorf("error %q does not quote the declared name", err.Error())
		}
	})
}

// TestStore_GetUnknownName pins the lookup miss.
func TestStore_GetUnknownName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "revenue.json", validTemplate("kept"))
	s := newStore(t, dir)

	tests := []string{
		"missing",
		"revenue.json",    // the file name, not the template name
		"Revenue",         // lookup is case-sensitive
		"finance/revenue", // a namespace that does not exist
		"",
	}
	for _, name := range tests {
		t.Run("name="+name, func(t *testing.T) {
			got, err := s.Get(name)
			if err == nil {
				t.Fatalf("Get(%q) = %v, want PULSE_TEMPLATE_NOT_FOUND", name, got)
			}
			if !perr.HasCode(err, perr.PULSE_TEMPLATE_NOT_FOUND) {
				t.Fatalf("Get(%q) = %v, want PULSE_TEMPLATE_NOT_FOUND", name, err)
			}
			ce := codedError(t, err)
			if got := ce.Details[perr.DetailTemplate]; got != name {
				t.Errorf("details[%q] = %v, want %q", perr.DetailTemplate, got, name)
			}
		})
	}
}

// TestStore_ListIsSortedByName pins deterministic listing order — E2-S3's
// ListTemplates hands this straight to a caller.
func TestStore_ListIsSortedByName(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"zeta.json", "alpha.json", "mid/beta.json", "mid/alpha.json"} {
		writeFile(t, dir, rel, validTemplate(rel))
	}

	s := newStore(t, dir)

	want := []string{"alpha", "mid/alpha", "mid/beta", "zeta"}
	got := listedNames(s)
	if len(got) != len(want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List() = %v, want %v", got, want)
		}
	}

	// Every listed summary must carry the projection a caller builds a
	// form from.
	for _, sum := range s.List() {
		if sum.Target != template.TargetRequest {
			t.Errorf("Summary(%q).Target = %q, want %q", sum.Name, sum.Target, template.TargetRequest)
		}
		if len(sum.Variables) != 1 || sum.Variables[0] != "metric" {
			t.Errorf("Summary(%q).Variables = %v, want [metric]", sum.Name, sum.Variables)
		}
		if sum.Path == "" {
			t.Errorf("Summary(%q).Path is empty; a disk-loaded template must report its source file", sum.Name)
		}
	}
}

// TestNewStore_DirectoryEdgeCases covers the three configured-path shapes:
// a missing directory is skipped, an empty string is skipped, and a path
// that exists but is a regular file is an error. E2-S2 wires these to
// pulse.New(); the behaviour lives here.
func TestNewStore_DirectoryEdgeCases(t *testing.T) {
	present := t.TempDir()
	writeFile(t, present, "revenue.json", validTemplate("kept"))

	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Run("no directories at all", func(t *testing.T) {
		s := newStore(t)
		if got := s.List(); len(got) != 0 {
			t.Errorf("List() = %v, want empty", got)
		}
		if _, err := s.Get("anything"); !perr.HasCode(err, perr.PULSE_TEMPLATE_NOT_FOUND) {
			t.Errorf("Get() = %v, want PULSE_TEMPLATE_NOT_FOUND", err)
		}
	})

	t.Run("missing directory is skipped", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does", "not", "exist")
		s := newStore(t, missing, present)
		if got := listedNames(s); len(got) != 1 || got[0] != "revenue" {
			t.Errorf("List() = %v, want [revenue]", got)
		}
	})

	t.Run("empty string is skipped", func(t *testing.T) {
		s := newStore(t, "", present, "   ")
		if got := listedNames(s); len(got) != 1 || got[0] != "revenue" {
			t.Errorf("List() = %v, want [revenue]", got)
		}
	})

	t.Run("regular file is an error", func(t *testing.T) {
		_, err := template.NewStore([]string{file})
		if err == nil {
			t.Fatal("NewStore() = nil error, want an error for a configured path that is not a directory")
		}
		if !strings.Contains(err.Error(), file) {
			t.Errorf("error %q does not name the offending path %q", err.Error(), file)
		}
	})
}

// TestNewStore_DirsIsDefensiveCopy asserts the configured roots are
// reported back in order and that neither the caller's slice nor the
// store's copy can reach across.
func TestNewStore_DirsIsDefensiveCopy(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	dirs := []string{first, second}

	s, err := template.NewStore(dirs)
	if err != nil {
		t.Fatalf("NewStore() = %v", err)
	}
	dirs[0] = "mutated-by-caller"

	got := s.Dirs()
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("Dirs() = %v, want [%s %s] — the store must hold its own copy", got, first, second)
	}
	got[1] = "mutated-by-reader"
	if again := s.Dirs(); again[1] != second {
		t.Errorf("Dirs() is not a defensive copy: got %q after caller mutation", again[1])
	}
}

// TestStore_NilIsUsable covers the "no store configured" shape E2-S2 and
// E2-S3 rely on: a nil *Store answers rather than panics.
func TestStore_NilIsUsable(t *testing.T) {
	var s *template.Store

	if got := s.List(); len(got) != 0 {
		t.Errorf("(*Store)(nil).List() = %v, want empty", got)
	}
	if got := s.Dirs(); len(got) != 0 {
		t.Errorf("(*Store)(nil).Dirs() = %v, want empty", got)
	}
	tmpl, err := s.Get("anything")
	if err == nil {
		t.Fatalf("(*Store)(nil).Get() = %v, want PULSE_TEMPLATE_NOT_FOUND", tmpl)
	}
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_NOT_FOUND) {
		t.Errorf("(*Store)(nil).Get() = %v, want PULSE_TEMPLATE_NOT_FOUND", err)
	}
}

// TestStore_ConcurrentLookup is the -race guard. The store is mutex-guarded
// from the start because E3 layers a concurrent rescan on top of exactly
// these read paths.
func TestStore_ConcurrentLookup(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeFile(t, first, "shared.json", validTemplate("from first"))
	writeFile(t, second, "shared.json", validTemplate("from second"))
	writeFile(t, second, "finance/other.json", validTemplate("other"))

	s := newStore(t, first, second)

	const goroutines = 16
	const iterations = 50
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iterations)

	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for range iterations {
				switch g % 4 {
				case 0:
					got, err := s.Get("shared")
					if err != nil {
						errs <- err
						return
					}
					if got.Description != "from first" {
						t.Errorf("Get(shared) resolved %q, want the first root's entry", got.Description)
						return
					}
				case 1:
					if _, err := s.Get("finance/other"); err != nil {
						errs <- err
						return
					}
				case 2:
					if len(s.List()) != 2 {
						t.Errorf("List() = %v, want 2 entries", listedNames(s))
						return
					}
				default:
					if _, err := s.Get("absent"); !perr.HasCode(err, perr.PULSE_TEMPLATE_NOT_FOUND) {
						errs <- err
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent lookup failed: %v", err)
	}
}

package embeddersmoke

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// moduleImports is the complete non-stdlib import allowlist for this
// module. Every entry is a surface the smoke flows exist to prove is
// reachable from outside: the extension-author core (pulse, extend,
// types, encoding, errors) plus the public packages the migration
// guide names (descriptor, io, synth, mcp/gosdk, mcpserve, linalg) and the two
// third-party types their signatures expose (afero.Fs on
// pulse.Options.FS, *mcp.Server on gosdk.Register). Adding an import
// outside this set fails the test: either it is a new public surface
// that belongs here deliberately, or it is a leak.
var moduleImports = map[string]bool{
	"github.com/frankbardon/pulse":               true,
	"github.com/frankbardon/pulse/extend":        true,
	"github.com/frankbardon/pulse/types":         true,
	"github.com/frankbardon/pulse/encoding":      true,
	"github.com/frankbardon/pulse/errors":        true,
	"github.com/frankbardon/pulse/descriptor":    true,
	"github.com/frankbardon/pulse/io":            true,
	"github.com/frankbardon/pulse/linalg":        true,
	"github.com/frankbardon/pulse/synth":         true,
	"github.com/frankbardon/pulse/mcp/gosdk":     true,
	"github.com/frankbardon/pulse/mcpserve":      true,
	"github.com/modelcontextprotocol/go-sdk/mcp": true,
	"github.com/spf13/afero":                     true,
}

// extensionAuthorImports is the strict set an extension operator needs:
// files listed in extensionAuthorFiles may import nothing else beyond
// the standard library.
var extensionAuthorImports = map[string]bool{
	"github.com/frankbardon/pulse":          true,
	"github.com/frankbardon/pulse/extend":   true,
	"github.com/frankbardon/pulse/types":    true,
	"github.com/frankbardon/pulse/encoding": true,
	"github.com/frankbardon/pulse/errors":   true,
}

var extensionAuthorFiles = map[string]bool{"extension_test.go": true}

// isStdlib reports whether an import path belongs to the standard
// library: its first element carries no dot.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

func TestSmokeModuleImportAllowlist(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files found; the allowlist would pass vacuously")
	}
	seenAuthor := map[string]bool{}
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		allowed := moduleImports
		if extensionAuthorFiles[name] {
			allowed = extensionAuthorImports
			seenAuthor[name] = true
		}
		var bad []string
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: bad import %s", name, imp.Path.Value)
			}
			if isStdlib(p) || allowed[p] {
				continue
			}
			bad = append(bad, p)
		}
		sort.Strings(bad)
		if len(bad) > 0 {
			t.Errorf("%s imports outside its allowlist: %v", name, bad)
		}
	}
	for name := range extensionAuthorFiles {
		if !seenAuthor[name] {
			t.Errorf("extension-author file %s not found; the strict allowlist checks nothing", name)
		}
	}
}

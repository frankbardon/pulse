package pulse_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// semverLiteral matches a semver-shaped token such as 1.0.0 or v0.39.0. The
// envelope format_version ("1.1") has two components and does not match.
var semverLiteral = regexp.MustCompile(`\bv?\d+\.\d+\.\d+\b`)

// versionLiteralScanRoots are the surfaces that report a server/binary
// version. Every one of them must read the single source (pulse.Version(),
// backed by internal/buildinfo) rather than carry its own literal.
var versionLiteralScanRoots = []string{"mcpserve", "mcp/gosdk", "cmd/pulse"}

// versionLiteralAllowlist holds "<slash path>: <matched token>" entries that
// are permitted. Each entry MUST carry a justification. Keep it narrow: a
// whole-file exemption is never acceptable.
var versionLiteralAllowlist = map[string]string{
	// (empty) — no surface in scope legitimately carries a semver literal.
	// The MCP protocol version is date-shaped (YYYY-MM-DD) and lives in the
	// go-sdk, so it needs no entry here.
}

// TestNoHardcodedVersionLiterals stops a hard-coded version (the historical
// "1.0.0" default in mcpserve and the gosdk doc example) from returning. Every
// non-test .go file under the scan roots is checked, comments included, since
// a doc-comment example is copied verbatim by embedders.
func TestNoHardcodedVersionLiterals(t *testing.T) {
	scanned := 0
	for _, root := range versionLiteralScanRoots {
		for _, f := range findGoFiles(t, root) {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			scanned++
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("reading %s: %v", f, err)
			}
			rel := filepath.ToSlash(f)
			for i, line := range strings.Split(string(data), "\n") {
				for _, tok := range semverLiteral.FindAllString(line, -1) {
					if _, ok := versionLiteralAllowlist[rel+": "+tok]; ok {
						continue
					}
					t.Errorf("%s:%d: hard-coded version literal %q — read pulse.Version() (internal/buildinfo) instead", rel, i+1, tok)
				}
			}
		}
	}
	if scanned == 0 {
		t.Fatalf("scanned no files under %v — scan roots moved?", versionLiteralScanRoots)
	}
}

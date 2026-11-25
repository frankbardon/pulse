package gosdk_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// pinnedProtocolVersions is the MCP revision set Pulse serves, newest
// first: exactly what the pinned go-sdk supports, since no server Pulse
// builds narrows it. docs/src/mcp/protocol.md lists the same set.
var pinnedProtocolVersions = []string{
	"2026-07-28",
	"2025-11-25",
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

// protocolDocPath is the page that states the served revisions, relative
// to this package directory.
const protocolDocPath = "../../docs/src/mcp/protocol.md"

// goSDKRequireRe reads the go-sdk version go.mod pins.
var goSDKRequireRe = regexp.MustCompile(`(?m)^\s*github\.com/modelcontextprotocol/go-sdk (v\S+)`)

// TestSupportedProtocolVersionsPinned makes a go-sdk bump that changes the
// served revision set a visible, deliberate diff: the SDK's set must equal
// the pinned list, and the protocol page must name every pinned revision
// and the go-sdk version go.mod pins. A dropped revision needs a documented
// deprecation first (the page's upgrade policy); an added one updates the
// list and the page.
func TestSupportedProtocolVersionsPinned(t *testing.T) {
	t.Parallel()

	if got := mcpsdk.SupportedProtocolVersions(); !slices.Equal(got, pinnedProtocolVersions) {
		t.Fatalf("go-sdk serves MCP revisions %q, pinned %q: update pinnedProtocolVersions and %s (a dropped revision needs a deprecation first)",
			got, pinnedProtocolVersions, protocolDocPath)
	}

	osfs := afero.NewOsFs()
	doc, err := afero.ReadFile(osfs, protocolDocPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range pinnedProtocolVersions {
		if !strings.Contains(string(doc), "`"+v+"`") {
			t.Errorf("%s does not list the served revision `%s`", protocolDocPath, v)
		}
	}
	mod, err := afero.ReadFile(osfs, "../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := goSDKRequireRe.FindSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod does not require github.com/modelcontextprotocol/go-sdk")
	}
	if !strings.Contains(string(doc), "go-sdk "+string(m[1])) {
		t.Errorf("%s does not name the pinned go-sdk %s", protocolDocPath, m[1])
	}
}

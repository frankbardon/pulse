package errors_test

import (
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// TestErrorsLookup_LimitExceeded_RootLimitFlag: the PULSE_LIMIT_EXCEEDED
// raise fixup names the CLI's ROOT `--limit` flag (every command takes
// it), never `pulse mcp --limit`.
func TestErrorsLookup_LimitExceeded_RootLimitFlag(t *testing.T) {
	t.Parallel()

	r, ok := perr.Lookup(string(perr.PULSE_LIMIT_EXCEEDED))
	if !ok || len(r.Fixups) == 0 {
		t.Fatal("no lookup entry for PULSE_LIMIT_EXCEEDED")
	}
	hint := r.Fixups[0].Hint
	if !strings.Contains(hint, "root `--limit <limit>=<value>`") || strings.Contains(hint, "pulse mcp") {
		t.Fatalf("raise hint %q must name the root --limit flag, not pulse mcp", hint)
	}
}

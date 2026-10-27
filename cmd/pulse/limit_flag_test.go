package main

import (
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	pcli "github.com/frankbardon/pulse/internal/cli"
)

// writeWideCompose writes a ComposedRequest of n AGG_COUNT slots over
// one cohort and returns its path.
func writeWideCompose(t *testing.T, dir, cohort string, n int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"requests": [`)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"label": "s` + strconv.Itoa(i) + `", "cohort": {"filename": "` + cohort + `"}, "aggregations": [{"type": "AGG_COUNT", "field": "age"}]}`)
	}
	b.WriteString(`]}`)
	path := filepath.Join(dir, "wide-compose.json")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLimitFlag_BadValueOnNonMCPLeaf: a bad --limit on any leaf is a
// usage error (exit 2) carrying CLI_INPUT {flag, value, limit}.
func TestLimitFlag_BadValueOnNonMCPLeaf(t *testing.T) {
	t.Parallel()

	_, err := runApp(t, "api", "compose", "--request", "nope.json", "--limit", "max_compose_slots=lots")
	if status, _ := pcli.ExitStatus(err); status != pcli.ExitUsage {
		t.Fatalf("exit status %d (err %v), want %d", status, err, pcli.ExitUsage)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.CLI_INPUT ||
		ce.Details["flag"] != "limit" || ce.Details["value"] != "max_compose_slots=lots" || ce.Details["limit"] != "max_compose_slots" {
		t.Fatalf("err = %#v, want CLI_INPUT {flag: limit, value, limit: max_compose_slots}", err)
	}
}

// TestLimitFlag_NoRequestLevelSlot: limits stay instance-level — the
// payload schema gives Request and ComposedRequest no limit slot.
func TestLimitFlag_NoRequestLevelSlot(t *testing.T) {
	t.Parallel()

	out, err := runApp(t, "schema")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(out), &schema); err != nil {
		t.Fatal(err)
	}
	for _, def := range []string{"Request", "ComposedRequest"} {
		props, ok := schema.Defs[def]
		if !ok {
			t.Fatalf("payload schema has no %s def", def)
		}
		for key := range props.Properties {
			if strings.Contains(key, "limit") {
				t.Errorf("%s carries a request-level limit slot %q", def, key)
			}
			for _, n := range pulse.LimitNames() {
				if key == n {
					t.Errorf("%s carries limit %q", def, key)
				}
			}
		}
	}
}

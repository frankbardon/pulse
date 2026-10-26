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

// TestLimitFlag_RaisesComposeSlots (mmm-followups E1-S2): the root
// --limit reaches a non-mcp leaf — a compose of more slots than the
// built-in max_compose_slots fails with PULSE_LIMIT_EXCEEDED without the
// flag and succeeds with --limit max_compose_slots=N, in either flag
// position.
//
// Falsified by dropping withLimits from withGlobals.
func TestLimitFlag_RaisesComposeSlots(t *testing.T) {
	dir := t.TempDir()
	cohort := createTestPulseFile(t, dir)
	slots := int(pulse.DefaultMaxComposeSlots) + 1
	req := writeWideCompose(t, dir, cohort, slots)

	out, err := runApp(t, "api", "compose", "--request", req, "--json")
	if err == nil || !strings.Contains(out, string(errors.PULSE_LIMIT_EXCEEDED)) {
		t.Fatalf("%d slots without --limit: err %v, want PULSE_LIMIT_EXCEEDED in envelope:\n%.400s", slots, err, out)
	}
	n := strconv.Itoa(slots)
	for _, args := range [][]string{
		{"api", "compose", "--request", req, "--json", "--limit", "max_compose_slots=" + n},
		{"--limit", "max_compose_slots=" + n, "api", "compose", "--request", req, "--json"},
	} {
		out, err := runApp(t, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%.400s", args, err, out)
		}
		var env struct {
			Data struct {
				Responses []json.RawMessage `json:"responses"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Data.Responses) != slots {
			t.Fatalf("%v: %d responses (err %v), want %d", args, len(env.Data.Responses), err, slots)
		}
	}
}

// TestLimitFlag_BadValueOnNonMCPLeaf: a bad --limit on any leaf is a
// usage error (exit 2) carrying CLI_INPUT {flag, value, limit}.
func TestLimitFlag_BadValueOnNonMCPLeaf(t *testing.T) {
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

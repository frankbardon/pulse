//go:build heavy

package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
)

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

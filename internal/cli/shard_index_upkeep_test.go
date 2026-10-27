package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestShardIndexUpkeepCLI_WarningsRideTheEnvelope: on an indexed
// archive, `shard add --json` and `shard remove --json` lift the
// mandatory PULSE_SHARD_INDEXES_UPDATED warning onto the envelope's
// warnings array, with the action in its details.
func TestShardIndexUpkeepCLI_WarningsRideTheEnvelope(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "a.pulse"), filepath.Join(dir, "b.pulse"), filepath.Join(dir, "c.pulse")
	arch := filepath.Join(dir, "arch.pulse")
	writeSetCohortFile(t, a, encoding.FieldTypeSetU8, []string{"x"}, [][2]uint64{{1, 1}})
	writeSetCohortFile(t, b, encoding.FieldTypeSetU8, []string{"x"}, [][2]uint64{{2, 1}})
	writeSetCohortFile(t, c, encoding.FieldTypeSetU8, []string{"x"}, [][2]uint64{{3, 1}})
	var buf bytes.Buffer
	if err := runShardCLI(t, &buf, "create", "--include", a, "--include", b, arch); err != nil {
		t.Fatalf("shard create: %v\n%s", err, buf.String())
	}
	buf.Reset()
	if err := runIndexCLI(t, &buf, "build", arch, "--key", "id"); err != nil {
		t.Fatalf("index build: %v\n%s", err, buf.String())
	}

	type env struct {
		Errors   []any `json:"errors"`
		Warnings []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"warnings"`
	}
	for _, step := range []struct {
		args   []string
		action string
	}{
		{[]string{"add", arch, c, "--json"}, "added"},
		{[]string{"remove", arch, "a.pulse", "--json"}, "removed"},
	} {
		buf.Reset()
		if err := runShardCLI(t, &buf, step.args...); err != nil {
			t.Fatalf("shard %v: %v\n%s", step.args, err, buf.String())
		}
		var e env
		if err := json.Unmarshal(buf.Bytes(), &e); err != nil {
			t.Fatalf("decode %s: %v", buf.String(), err)
		}
		if len(e.Errors) != 0 || len(e.Warnings) != 1 || e.Warnings[0].Code != "PULSE_SHARD_INDEXES_UPDATED" || e.Warnings[0].Details["action"] != step.action {
			t.Errorf("shard %s envelope = %s, want one PULSE_SHARD_INDEXES_UPDATED (%s)", step.args[0], buf.String(), step.action)
		}
	}
}

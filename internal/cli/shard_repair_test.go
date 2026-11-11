package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// TestShardVerifyCLI_RepairRollsBackAnInterruptedAppend: with an append
// journal beside the archive, `shard verify` refuses it with
// PULSE_SHARD_APPEND_INCOMPLETE; `shard verify --repair --json` truncates
// it back to its pre-append bytes, removes the journal, verifies clean
// and reports the rollback under data.repair. A plain verify's envelope
// carries no repair key.
func TestShardVerifyCLI_RepairRollsBackAnInterruptedAppend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a.pulse")
	arch := filepath.Join(dir, "arch.pulse")
	writeSetCohortFile(t, a, encoding.FieldTypeSetU8, []string{"x"}, [][2]uint64{{1, 1}})
	var buf bytes.Buffer
	if err := runShardCLI(t, &buf, "create", "--include", a, arch); err != nil {
		t.Fatalf("shard create: %v\n%s", err, buf.String())
	}
	before, err := os.ReadFile(arch)
	if err != nil {
		t.Fatal(err)
	}
	j, err := encx.NewAppendJournal(bytes.NewReader(before), int64(len(before)), "b.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if err := encx.WriteAppendJournal(afero.NewOsFs(), arch, j); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(arch, append(append([]byte{}, before...), 1, 2, 3, 4), 0o644); err != nil {
		t.Fatal(err)
	}

	type env struct {
		Data   map[string]any `json:"data"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	buf.Reset()
	_ = runShardCLI(t, &buf, "verify", arch, "--json")
	var e env
	if err := json.Unmarshal(buf.Bytes(), &e); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	if len(e.Errors) != 1 || e.Errors[0].Code != "PULSE_SHARD_APPEND_INCOMPLETE" {
		t.Fatalf("plain verify envelope = %s, want PULSE_SHARD_APPEND_INCOMPLETE", buf.String())
	}

	buf.Reset()
	if err := runShardCLI(t, &buf, "verify", arch, "--repair", "--json"); err != nil {
		t.Fatalf("shard verify --repair: %v\n%s", err, buf.String())
	}
	e = env{}
	if err := json.Unmarshal(buf.Bytes(), &e); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	repair, _ := e.Data["repair"].(map[string]any)
	if len(e.Errors) != 0 || e.Data["verified"] != true || repair["repaired"] != true || repair["shard"] != "b.pulse" {
		t.Fatalf("verify --repair envelope = %s", buf.String())
	}
	after, _ := os.ReadFile(arch)
	if !bytes.Equal(after, before) {
		t.Error("the repaired archive differs from its pre-append bytes")
	}
	if _, err := os.Stat(arch + ".append"); !os.IsNotExist(err) {
		t.Errorf("journal still present: %v", err)
	}

	buf.Reset()
	if err := runShardCLI(t, &buf, "verify", arch, "--json"); err != nil {
		t.Fatalf("shard verify: %v\n%s", err, buf.String())
	}
	e = env{}
	if err := json.Unmarshal(buf.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Data["repair"]; ok {
		t.Errorf("a plain verify carries data.repair: %s", buf.String())
	}
}

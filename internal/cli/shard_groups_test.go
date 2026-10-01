package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// TestCliShardGroups_JsonSurfacesRegroupAndHeadroom (E5-S3): a grouped
// shard added to an ungrouped archive surfaces the mandatory
// PULSE_SHARD_GROUPS_REWRITTEN warning on the envelope plus a
// `data.regrouped` entry; `shard verify --json` on a grouped archive
// reports `group_index_headroom`, and on an ungrouped archive emits no
// such key (its envelope is unchanged).
func TestCliShardGroups_JsonSurfacesRegroupAndHeadroom(t *testing.T) {
	dir := t.TempDir()
	flat := filepath.Join(dir, "flat.pulse")
	writeSetCohortFile(t, flat, encoding.FieldTypeSetU8, []string{"a", "b"},
		[][2]uint64{{1, 1}, {2, 1}, {3, 2}, {4, 2}, {5, 3}})
	raw, err := os.ReadFile(flat)
	if err != nil {
		t.Fatal(err)
	}
	var grouped bytes.Buffer
	if _, _, err := encx.DedupCohort(&grouped, bytes.NewReader(raw),
		[]encx.GroupSpec{{Kind: encoding.GroupKindIndexed, Members: []string{"opts"}}}); err != nil {
		t.Fatal(err)
	}
	g1 := filepath.Join(dir, "g1.pulse")
	g2 := filepath.Join(dir, "g2.pulse")
	for _, p := range []string{g1, g2} {
		if err := os.WriteFile(p, grouped.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	archive := filepath.Join(dir, "arch.pulse")
	if err := runShardCLI(t, &buf, "create", "--json", "--include", flat, archive); err != nil {
		t.Fatalf("create: %v\n%s", err, buf.String())
	}
	buf.Reset()
	if err := runShardCLI(t, &buf, "add", "--json", archive, g1); err != nil {
		t.Fatalf("add: %v\n%s", err, buf.String())
	}
	var add struct {
		Data struct {
			Regrouped []struct {
				Reason string `json:"reason"`
			} `json:"regrouped"`
		} `json:"data"`
		Warnings []struct {
			Code string `json:"code"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &add); err != nil {
		t.Fatalf("add envelope: %v\n%s", err, buf.String())
	}
	if len(add.Data.Regrouped) != 1 || add.Data.Regrouped[0].Reason != "incoming_flattened" ||
		len(add.Warnings) != 1 || add.Warnings[0].Code != "PULSE_SHARD_GROUPS_REWRITTEN" {
		t.Fatalf("add envelope = %s", buf.String())
	}

	verify := func(path string) map[string]any {
		t.Helper()
		buf.Reset()
		if err := runShardCLI(t, &buf, "verify", "--json", path); err != nil {
			t.Fatalf("verify %s: %v\n%s", path, err, buf.String())
		}
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		return env.Data
	}
	if _, ok := verify(archive)["group_index_headroom"]; ok {
		t.Fatal("an ungrouped archive's verify envelope grew group_index_headroom")
	}
	garchive := filepath.Join(dir, "garch.pulse")
	buf.Reset()
	if err := runShardCLI(t, &buf, "create", "--json", "--include", g1, "--include", g2, garchive); err != nil {
		t.Fatalf("create grouped: %v\n%s", err, buf.String())
	}
	h, ok := verify(garchive)["group_index_headroom"].([]any)
	if !ok || len(h) != 1 || h[0].(map[string]any)["entries"] != float64(3) {
		t.Fatalf("group_index_headroom = %v", verify(garchive)["group_index_headroom"])
	}
}

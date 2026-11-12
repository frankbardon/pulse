package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestShardCLI_DeadBytesVerifyAndRewrite: two in-place `shard add`s
// leave two dead `_schema.pulse` copies that `shard verify --json`
// reports under data.dead_schema_copies / data.dead_bytes; `shard add
// --rewrite` rewrites the archive instead of appending, leaving none.
func TestShardCLI_DeadBytesVerifyAndRewrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	arch := filepath.Join(dir, "arch.pulse")
	shards := map[string]string{}
	for i, n := range []string{"a", "b", "c", "d"} {
		p := filepath.Join(dir, n+".pulse")
		// Enough records that the dead bytes stay under half the
		// archive: every plain add appends.
		recs := make([][2]uint64, 300)
		for r := range recs {
			recs[r] = [2]uint64{uint64(i*1000 + r), 1}
		}
		writeSetCohortFile(t, p, encoding.FieldTypeSetU8, []string{"x"}, recs)
		shards[n] = p
	}
	var buf bytes.Buffer
	run := func(args ...string) {
		t.Helper()
		buf.Reset()
		if err := runShardCLI(t, &buf, args...); err != nil {
			t.Fatalf("shard %v: %v\n%s", args, err, buf.String())
		}
	}
	verify := func() map[string]any {
		t.Helper()
		run("verify", arch, "--json")
		var e struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(buf.Bytes(), &e); err != nil {
			t.Fatalf("decode %s: %v", buf.String(), err)
		}
		return e.Data
	}

	run("create", "--include", shards["a"], arch)
	run("add", arch, shards["b"])
	run("add", arch, shards["c"])
	d := verify()
	if d["dead_schema_copies"] != float64(2) || d["dead_bytes"].(float64) <= 0 {
		t.Fatalf("after two appends verify data = %v, want 2 dead copies and dead bytes", d)
	}

	before, err := os.ReadFile(arch)
	if err != nil {
		t.Fatal(err)
	}
	run("add", arch, shards["d"], "--rewrite", "--json")
	after, _ := os.ReadFile(arch)
	if bytes.HasPrefix(after, before) {
		t.Error("shard add --rewrite appended instead of rewriting")
	}
	if d := verify(); d["dead_schema_copies"] != float64(0) || d["dead_bytes"] != float64(0) {
		t.Errorf("after --rewrite verify data = %v, want no dead bytes", d)
	}
}

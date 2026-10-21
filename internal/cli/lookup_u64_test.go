package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestAPILookupCLI_U64RowsExact (E4-S4): `pulse api lookup --json` prints
// a u64 cell as its exact integer — 2^53+1 and MaxUint64 keep their own
// digits — on a single file and on a shard archive.
func TestAPILookupCLI_U64RowsExact(t *testing.T) {
	ids := []uint64{1<<53 - 1, 1<<53 + 1, math.MaxUint64, 7}
	dir := t.TempDir()
	write := func(name string, ids []uint64) string {
		t.Helper()
		schema := &encoding.Schema{Fields: []encoding.Field{{Name: "id", Type: encoding.FieldTypeU64}}}
		var buf bytes.Buffer
		if err := encoding.WriteHeader(&buf); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteSchema(&buf, schema); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU64, id); err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	single := write("single.pulse", ids)
	s0, s1 := write("s0.pulse", ids[:2]), write("s1.pulse", ids[2:])
	archive := filepath.Join(dir, "archive.pulse")
	p, err := newPulse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(context.Background(), archive, []string{s0, s1}); err != nil {
		t.Fatal(err)
	}
	for _, cohort := range []string{single, archive} {
		var b bytes.Buffer
		if err := runIndexCLI(t, &b, "build", "--input", cohort, "--key", "id"); err != nil {
			t.Fatalf("index build %s: %v", cohort, err)
		}
		for _, id := range ids {
			var buf bytes.Buffer
			digits := strconv.FormatUint(id, 10)
			if err := runAPICLI(t, &buf, "lookup", "--input", cohort, "--key", "id="+digits, "--json"); err != nil {
				t.Fatalf("lookup %s id=%s: %v", cohort, digits, err)
			}
			var env struct {
				Data struct {
					Rows []map[string]json.Number `json:"rows"`
				} `json:"data"`
			}
			dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
			dec.UseNumber()
			if err := dec.Decode(&env); err != nil {
				t.Fatalf("decode: %v\n%s", err, buf.String())
			}
			if len(env.Data.Rows) != 1 || env.Data.Rows[0]["id"].String() != digits {
				t.Errorf("%s id=%s rows = %v, want the exact id", filepath.Base(cohort), digits, env.Data.Rows)
			}
		}
	}
}

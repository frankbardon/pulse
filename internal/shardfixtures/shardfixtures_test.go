package shardfixtures

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
)

var update = flag.Bool("update", false, "rewrite testdata/sharding/*.pulse from the generator")

// repoDir is Dir relative to this package directory.
var repoDir = filepath.Join("..", "..", Dir)

// TestShardFixtures_MatchGenerator pins every committed shard archive to
// the generator's output. The committed files had drifted: they were
// written before the schema block gained its per-field nullable flag
// byte, the generator was never re-run, and nothing compared the two —
// so the "canonical" fixtures no longer opened under the current codec
// (ENCODING_INVALID reading the canonical schema). Any codec change that
// moves these bytes now fails here until the fixtures are regenerated.
func TestShardFixtures_MatchGenerator(t *testing.T) {
	files, err := Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("Build returned %d archives, want 3", len(files))
	}
	for _, f := range files {
		path := filepath.Join(repoDir, f.Name)
		if *update {
			if err := os.WriteFile(path, f.Bytes, 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s (regenerate with go test ./internal/shardfixtures/ -update): %v", path, err)
		}
		if !bytes.Equal(got, f.Bytes) {
			t.Errorf("%s (%d bytes) differs from the generator (%d bytes); regenerate with `go run ./testdata/sharding/build_fixtures.go` and `git add -f` it",
				f.Name, len(got), len(f.Bytes))
		}
	}
}

// TestShardFixtures_OpenUnderCurrentCodec: byte equality with the
// generator is only half the contract — the committed archives must
// also OPEN, and say what the README says they hold.
func TestShardFixtures_OpenUnderCurrentCodec(t *testing.T) {
	want := map[string]struct {
		shards  int
		records uint64
		dict    []string
	}{
		"two_shards.pulse":   {2, 6, []string{"US", "CA"}},
		"dict_growth.pulse":  {2, 4, []string{"US", "CA", "MX"}},
		"three_shards.pulse": {3, 6, []string{"US", "CA"}},
	}
	for name, w := range want {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(repoDir, name))
			if err != nil {
				t.Fatal(err)
			}
			arch, err := encx.OpenArchive(bytes.NewReader(raw), int64(len(raw)))
			if err != nil {
				t.Fatalf("OpenArchive: %v", err)
			}
			rc, err := arch.Open(encx.ReservedSchemaName)
			if err != nil {
				t.Fatalf("open %s: %v", encx.ReservedSchemaName, err)
			}
			doc, err := encx.ReadSchemaDoc(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatalf("ReadSchemaDoc: %v", err)
			}
			if int(doc.ShardCount) != w.shards || doc.AggregateRecordCount != w.records {
				t.Fatalf("SHRD trailer = %d shards / %d records, want %d / %d",
					doc.ShardCount, doc.AggregateRecordCount, w.shards, w.records)
			}
			if got := doc.Schema.Fields[2].Dictionary.Values(); !equalStrings(got, w.dict) {
				t.Fatalf("canonical region dictionary = %v, want %v", got, w.dict)
			}
			var sum uint64
			for _, e := range arch.Entries() {
				if e.Name == encx.ReservedSchemaName {
					continue
				}
				n, err := arch.PeekShardRecordCount(e.Name)
				if err != nil {
					t.Fatalf("PeekShardRecordCount(%s): %v", e.Name, err)
				}
				sum += uint64(n)
			}
			if sum != w.records {
				t.Fatalf("shard record counts sum to %d, want %d", sum, w.records)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

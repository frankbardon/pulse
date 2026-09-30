package cli

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	cli "github.com/urfave/cli/v3"
)

var updateShardingSnapshots = flag.Bool("update", false, "rewrite testdata/sharding/two_shards.{inspect,process}.json from the CLI")

// shardingSnapshotRequest is the canonical process request the
// testdata/sharding README documents for two_shards.process.json.
const shardingSnapshotRequest = `{
  "cohort": {"filename": "two_shards.pulse"},
  "aggregations": [
    {"label": "score_sum",   "type": "AGG_SUM",   "field": "score"},
    {"label": "score_count", "type": "AGG_COUNT", "field": "score"},
    {"label": "score_min",   "type": "AGG_MIN",   "field": "score"},
    {"label": "score_max",   "type": "AGG_MAX",   "field": "score"}
  ]
}`

// TestShardingSnapshots_MatchCLIOutput pins the two JSON snapshots under
// testdata/sharding/ to what the CLI prints for the committed
// two_shards.pulse. Like the archive bytes they had drifted silently —
// still at format_version "1.0" with no Components — because nothing
// compared them to a live run. Regenerate with
// `go test ./internal/cli/ -run TestShardingSnapshots -update`.
func TestShardingSnapshots_MatchCLIOutput(t *testing.T) {
	fixtureDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sharding"))
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(fixtureDir, "two_shards.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "two_shards.pulse"), archive, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "request.json"), []byte(shardingSnapshotRequest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_DATA_DIR", dir)
	t.Chdir(dir)

	cases := []struct {
		snapshot string
		root     func() *cli.Command
		args     []string
	}{
		{"two_shards.inspect.json", CohortCommand, []string{"cohort", "inspect", "two_shards.pulse", "--json"}},
		{"two_shards.process.json", APICommand, []string{"api", "process", "--request", "request.json", "--json"}},
	}
	for _, c := range cases {
		t.Run(c.snapshot, func(t *testing.T) {
			var buf bytes.Buffer
			root := c.root()
			root.Writer = &buf
			if err := root.Run(context.Background(), c.args); err != nil {
				t.Fatalf("%v: %v", c.args, err)
			}
			path := filepath.Join(fixtureDir, c.snapshot)
			if *updateShardingSnapshots {
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("%s is stale; regenerate with -update\n got  %s\n want %s", c.snapshot, buf.String(), want)
			}
			if bytes.Contains(buf.Bytes(), []byte(`"errors": [
    {`)) {
				t.Fatalf("the CLI answered with an error envelope:\n%s", buf.String())
			}
		})
	}
}

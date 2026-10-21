package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
)

// seedArchiveViaImport imports two CSVs and packs them into dir/arch.pulse
// through the library (the CLI leaf under test reads it by absolute path).
func seedArchiveViaImport(t *testing.T, dir string) string {
	t.Helper()
	a := seedCohortViaImport(t, dir, "a.csv", "id,score\n1,10\n2,20\n3,30\n")
	b := seedCohortViaImport(t, dir, "b.csv", "id,score\n4,40\n5,50\n")
	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rel := func(p string) string {
		r, err := filepath.Rel(dir, p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if _, err := p.CreateShardArchive(context.Background(), "arch.pulse", []string{rel(a), rel(b)}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return filepath.Join(dir, "arch.pulse")
}

// runCohortCLIJSON runs a cohort leaf and decodes its --json envelope.
func runCohortCLIJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	root := CohortCommand()
	var out bytes.Buffer
	root.Writer = &out
	if err := root.Run(context.Background(), append([]string{"cohort"}, args...)); err != nil {
		t.Fatalf("cohort %v: %v", args, err)
	}
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decoding %q: %v", out.String(), err)
	}
	return env
}

// TestCohortFilterCLI_Flatten (U51 E4-S3): `--flatten` writes an
// archive as ONE single-file cohort — with no predicate a pure flatten,
// with --filter only the survivors — and the envelope says so; on a
// single-file input it is a no-op (`flattened: false`).
func TestCohortFilterCLI_Flatten(t *testing.T) {
	dir := withTempDataDir(t)
	arch := seedArchiveViaImport(t, dir)

	flat := filepath.Join(dir, "flat.pulse")
	env := runCohortCLIJSON(t, "filter", "-i", arch, "-o", flat, "--flatten", "--json")
	data := env["data"].(map[string]any)
	if data["flattened"] != true || data["written_records"] != float64(5) {
		t.Fatalf("data = %v, want 5 flattened records", data)
	}
	b, err := os.ReadFile(flat)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("PULSE")) {
		t.Fatalf("flattened output is not a single-file cohort: % x", b[:4])
	}
	if n := inspectRecordCount(t, flat); n != 5 {
		t.Errorf("flat rows = %d, want 5", n)
	}

	some := filepath.Join(dir, "some.pulse")
	if err := runCohortCLI(t, "filter", "-i", arch, "-o", some, "--flatten", "--filter", "score > 25.0"); err != nil {
		t.Fatal(err)
	}
	if n := inspectRecordCount(t, some); n != 3 {
		t.Errorf("filtered flat rows = %d, want 3", n)
	}

	env = runCohortCLIJSON(t, "filter", "-i", flat, "-o", filepath.Join(dir, "copy.pulse"), "--flatten", "--json")
	if data := env["data"].(map[string]any); data["flattened"] != false || data["written_records"] != float64(5) {
		t.Fatalf("single-file data = %v, want 5 records, flattened false", data)
	}

	// Without --flatten the plain envelope carries no flattened key.
	env = runCohortCLIJSON(t, "filter", "-i", arch, "-o", filepath.Join(dir, "plain.pulse"), "--filter", "score > 0.0", "--json")
	if _, ok := env["data"].(map[string]any)["flattened"]; ok {
		t.Fatalf("plain filter envelope gained a flattened key: %v", env["data"])
	}
}

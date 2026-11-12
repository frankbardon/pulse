package gosdk_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/spf13/afero"
)

// TestRegister_CohortScanSkipsAppendJournal: a shard archive's append
// journal (`<archive>.append`, and its `.append.tmp` stage) beside a
// cohort is never enumerated as a `pulse://` cohort resource — the scan
// counts the one cohort only.
func TestRegister_CohortScanSkipsAppendJournal(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeTestCohort(t, fs, "arch.pulse")
	for _, name := range []string{"arch.pulse.append", "arch.pulse.append.tmp"} {
		if err := afero.WriteFile(fs, name, []byte(`{"journal_format_version":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	p, err := pulse.New(pulse.Options{FS: fs, Logger: slog.New(slog.NewJSONHandler(&buf, nil))})
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := gosdk.Register(newServer(), p, gosdk.Config{Version: "9.9.9"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, `"cohorts":1`) {
		t.Fatalf("want cohorts=1 (the journal is not a cohort), got %s", out)
	}
}

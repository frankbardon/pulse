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

// TestRegister_CohortScanLogsCountAndDuration: with Options.Logger set
// on the instance, the startup cohort scan logs one Info record with the
// count found and the duration — never the cohort names. A disabled scan
// logs nothing.
func TestRegister_CohortScanLogsCountAndDuration(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeTestCohort(t, fs, "zzsentinel_a.pulse")
	writeTestCohort(t, fs, "zzsentinel_b.pulse")
	var buf bytes.Buffer
	p, err := pulse.New(pulse.Options{FS: fs, Logger: slog.New(slog.NewJSONHandler(&buf, nil))})
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := gosdk.Register(newServer(), p, gosdk.Config{Version: "9.9.9"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	out := buf.String()
	if strings.Count(out, "mcp cohort scan") != 1 || !strings.Contains(out, `"level":"INFO"`) ||
		!strings.Contains(out, `"op":"mcp_cohort_scan"`) || !strings.Contains(out, `"cohorts":2`) ||
		!strings.Contains(out, `"duration_ms":`) {
		t.Fatalf("want one Info cohort-scan record with cohorts=2, got %s", out)
	}
	if strings.Contains(out, "zzsentinel") {
		t.Fatalf("cohort names leaked into the log: %s", out)
	}

	buf.Reset()
	if err := gosdk.Register(newServer(), p, gosdk.Config{Version: "9.9.9", DisableCohortScan: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if strings.Contains(buf.String(), "mcp cohort scan") {
		t.Fatalf("a disabled scan logged: %s", buf.String())
	}
}

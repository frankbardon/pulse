package imports

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"
)

// TestManager_SweepLogs: an opportunistic sweep that removed nothing is
// quiet, one that removed a handle logs Info, and an explicit Sweep
// always logs — counts only, never handle names.
func TestManager_SweepLogs(t *testing.T) {
	var buf bytes.Buffer
	afs := afero.NewMemMapFs()
	clk := &fixedClock{t: time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)}
	m, err := New(afs, Options{Now: clk.now, Logger: slog.New(slog.NewJSONHandler(&buf, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	writeCSV(t, afs, "zzsentinelhandle.csv")
	writeCSV(t, afs, "other.csv")

	res, err := m.Open(ctx, Spec{SourcePath: "zzsentinelhandle.csv", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "imports swept") {
		t.Fatalf("an opportunistic sweep that removed nothing logged: %s", buf.String())
	}

	clk.advance(2 * time.Hour)
	if _, err := m.Open(ctx, Spec{SourcePath: "other.csv", TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "imports swept") != 1 || !strings.Contains(out, `"removed":1`) || !strings.Contains(out, `"explicit":false`) ||
		!strings.Contains(out, `"level":"INFO"`) || !strings.Contains(out, `"op":"imports_sweep"`) {
		t.Fatalf("opportunistic removing sweep: want one Info removed=1, got %s", out)
	}
	if strings.Contains(out, res.Handle) {
		t.Fatalf("handle name %q leaked into the log: %s", res.Handle, out)
	}

	buf.Reset()
	if _, err := m.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Count(out, "imports swept") != 1 || !strings.Contains(out, `"removed":0`) || !strings.Contains(out, `"explicit":true`) {
		t.Fatalf("explicit sweep: want one Info removed=0 explicit=true, got %s", out)
	}
}

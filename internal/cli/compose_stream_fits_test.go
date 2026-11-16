package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pulse "github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/optfixture"
)

// TestComposeStreamFits_CLI (compose-sweep-2 E2-S4): `pulse api compose
// --stream --stream-fits` ends with one {"fits":{...}} line equal to the
// library's buffered `fits` (after the comparisons line), serial and
// --parallel alike; plain `--stream` writes no fits line and is exactly
// the --stream-fits output minus that last line; --stream-fits alone
// (no --stream) leaves the buffered output untouched.
func TestComposeStreamFits_CLI(t *testing.T) {
	// serial: t.Chdir moves the process working directory; t.Setenv mutates the process environment

	dir, p, at := optCLIFixture(t)
	cohort := `{"filename":"` + optfixture.CohortFile + `"}`
	fit := func(preds string) string {
		return `{"cohort":` + cohort + `,"regressions":[{"type":"REG_OLS","target":"units","predictors":` + preds + `}]}`
	}
	raw := at([]byte(`{"requests":[{"label":"n","cohort":` + cohort + `,"aggregations":[{"type":"AGG_COUNT","field":"units","label":"n"}]}],
		"fits":[{"name":"price","request":` + fit(`["price"]`) + `},{"name":"trend","request":` + fit(`["price","week"]`) + `}],
		"compare":[{"name":"week_adds","restricted":"price","full":"trend"}]}`))
	reqPath := filepath.Join(dir, "stream_fits.json")
	if err := os.WriteFile(reqPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var req pulse.ComposedRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	lib, err := p.Compose(t.Context(), &req)
	if err != nil {
		t.Fatalf("library Compose: %v", err)
	}
	wantLast, err := json.Marshal(map[string]any{"fits": lib.Fits})
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) []byte {
		t.Helper()
		var buf bytes.Buffer
		if err := runAPICLI(t, &buf, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, buf.String())
		}
		return buf.Bytes()
	}
	for _, extra := range [][]string{nil, {"--parallel", "3"}} {
		on := run(append(append([]string{"compose", "--stream", "--stream-fits"}, extra...), "-r", reqPath)...)
		off := run(append(append([]string{"compose", "--stream"}, extra...), "-r", reqPath)...)
		lines := bytes.Split(bytes.TrimSpace(on), []byte("\n"))
		if last := lines[len(lines)-1]; !bytes.Equal(last, wantLast) {
			t.Fatalf("%v: stream ends with %s, want %s", extra, last, wantLast)
		}
		if prev := lines[len(lines)-2]; !bytes.HasPrefix(prev, []byte(`{"comparisons":`)) {
			t.Errorf("%v: the fits line follows %s, want the comparisons line", extra, prev)
		}
		if trimmed := on[:bytes.LastIndexByte(bytes.TrimSuffix(on, []byte("\n")), '\n')+1]; !bytes.Equal(trimmed, off) {
			t.Errorf("%v: --stream-fits minus its last line differs from --stream:\n%s\nvs\n%s", extra, trimmed, off)
		}
		if bytes.Contains(off, []byte(`{"fits":`)) {
			t.Errorf("%v: plain --stream wrote a fits line:\n%s", extra, off)
		}
	}
	if a, b := run("compose", "--json", "--stream-fits", "-r", reqPath), run("compose", "--json", "-r", reqPath); !bytes.Equal(a, b) {
		t.Errorf("--stream-fits without --stream changed the buffered output")
	}
}

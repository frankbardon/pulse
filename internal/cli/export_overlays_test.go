package cli

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
)

// overlayResult imports a small cohort and runs a crosstab request with
// an OVERLAY_SHARE_OF_TOTAL layer through `pulse api process --json`,
// returning the cohort, the request file and the result envelope file —
// the files a user hands `export --overlays`.
func overlayResult(t *testing.T) (cohort, request, result string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "ov.csv")
	if err := os.WriteFile(in, []byte("id,region,segment\n1,n,a\n2,s,b\n3,n,b\n4,s,a\n5,n,a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cohort = filepath.Join(dir, "ov.pulse")
	if err := runImportCLI(t, "csv", "-i", in, "-o", cohort); err != nil {
		t.Fatal(err)
	}
	request = filepath.Join(dir, "req.json")
	body := `{"cohort":{"filename":"` + jsonEsc(cohort) + `"},` +
		`"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"region"}],"columns":[{"type":"GROUP_CATEGORY","field":"segment"}],` +
		`"cell":{"type":"AGG_COUNT","field":"id","label":"n"},"margins":{"rows":true,"columns":true,"grand":true}},` +
		`"overlays":[{"name":"total_share","kind":"OVERLAY_SHARE_OF_TOTAL","scope":"cell","ref":{"margin":{"axis":"grand"}}}]}`
	if err := os.WriteFile(request, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runAPI(t, "process", "--request", request, "--json")
	if err != nil {
		t.Fatalf("api process: %v\n%s", err, out)
	}
	result = filepath.Join(dir, "result.json")
	if err := os.WriteFile(result, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	return cohort, request, result
}

func runExportCLIOut(args ...string) (string, error) {
	var buf bytes.Buffer
	root := ExportCommand()
	root.Writer = &buf
	err := root.Run(context.Background(), append([]string{"export"}, args...))
	return buf.String(), err
}

// TestExport_OverlaysFlag: --overlays embeds a saved result's executed
// layers (io.ExportJob.Overlays) — from the --json envelope, its data
// object or the bare array alike — as NDJSON's trailing `_overlays`
// line; without the flag the export is unchanged; CSV warns and skips;
// a request's overlay specs are refused DATA_FILE and write nothing.
func TestExport_OverlaysFlag(t *testing.T) {
	t.Parallel()

	cohort, request, result := overlayResult(t)
	dir := filepath.Dir(cohort)
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	plain := filepath.Join(dir, "plain.ndjson")
	if err := runExportCLI("ndjson", "-i", cohort, "-o", plain); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(plain), "_overlays") {
		t.Fatalf("an export without --overlays carries layers:\n%s", read(plain))
	}

	withLayers := filepath.Join(dir, "layers.ndjson")
	if err := runExportCLI("ndjson", "-i", cohort, "-o", withLayers, "--overlays", result); err != nil {
		t.Fatal(err)
	}
	got := read(withLayers)
	if !strings.HasPrefix(got, read(plain)) {
		t.Errorf("the host rows changed:\n%s", got)
	}
	trailer := strings.TrimPrefix(got, read(plain))
	if !strings.Contains(trailer, `"_overlays"`) || !strings.Contains(trailer, `"total_share"`) || !strings.Contains(trailer, "OVERLAY_SHARE_OF_TOTAL") {
		t.Fatalf("trailer = %q, want the total_share layer", trailer)
	}

	// the data object and the bare array read the same layers
	var env struct {
		Data struct {
			Overlays json.RawMessage `json:"overlays"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(read(result)), &env); err != nil || len(env.Data.Overlays) == 0 {
		t.Fatalf("result envelope: %v", err)
	}
	data, _ := json.Marshal(map[string]json.RawMessage{"overlays": env.Data.Overlays})
	for name, body := range map[string][]byte{"data.json": data, "array.json": env.Data.Overlays} {
		src := filepath.Join(dir, name)
		if err := os.WriteFile(src, body, 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, name+".ndjson")
		if err := runExportCLI("ndjson", "-i", cohort, "-o", out, "--overlays", src); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if read(out) != got {
			t.Errorf("%s: export differs from the envelope's", name)
		}
	}

	// CSV cannot carry layers: warned, host body unchanged
	envOut, err := runExportCLIOut("csv", "-i", cohort, "-o", filepath.Join(dir, "x.csv"), "--overlays", result, "--json")
	if err != nil || !strings.Contains(envOut, string(perrors.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED)) {
		t.Errorf("csv --overlays: err %v, envelope %s", err, envOut)
	}

	// a request file holds overlay SPECS, not layers: refused, nothing written
	specOut := filepath.Join(dir, "spec.ndjson")
	err = runExportCLI("ndjson", "-i", cohort, "-o", specOut, "--overlays", request)
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.DATA_FILE || ce.Details["path"] != request {
		t.Errorf("spec file: err %v, want DATA_FILE naming the file", err)
	}
	if _, statErr := os.Stat(specOut); statErr == nil {
		t.Error("a refused --overlays file still wrote the export")
	}
	// a compose spec's keys (reference, targets) are no layer's, even
	// beside a payload
	composeSpec := filepath.Join(dir, "compose_spec.json")
	if err := os.WriteFile(composeSpec, []byte(`[{"name":"o","kind":"OVERLAY_INDEX_VS_REF","reference":"a","targets":["b"],"payload":{"shape":"series"}}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runExportCLI("ndjson", "-i", cohort, "-o", specOut, "--overlays", composeSpec); !stderrors.As(err, &ce) || ce.Code != perrors.DATA_FILE || !strings.Contains(err.Error(), "reference") {
		t.Errorf("compose spec: err %v, want DATA_FILE naming the unknown key", err)
	}
	envOut, _ = runExportCLIOut("ndjson", "-i", cohort, "-o", specOut, "--overlays", filepath.Join(dir, "missing.json"), "--json")
	if !strings.Contains(envOut, `"code": "DATA_FILE"`) && !strings.Contains(envOut, `"code":"DATA_FILE"`) {
		t.Errorf("missing file under --json: %s", envOut)
	}
}

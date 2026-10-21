package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
)

// TestAPIPredict_ReadsCohortThroughFacadePath: `pulse api predict`
// predicts a single file, a whole shard archive and an anchored shard
// (`archive.pulse#shard.pulse`) through Pulse.PredictEnvelope — the
// path-based, header-only read. The leaf used to os.ReadFile the cohort
// whole and hand the bytes to PredictBytes, which held a whole archive
// in memory and could not resolve an anchor at all (the file
// "archive.pulse#shard.pulse" does not exist). A missing cohort stays a
// coded PREDICT_ERROR envelope.
func TestAPIPredict_ReadsCohortThroughFacadePath(t *testing.T) {
	dir := withTempDataDir(t)
	c0 := seedCohortViaImport(t, dir, "p0.csv", "n,cat\n1,a\n2,b\n3,c\n")
	c1 := seedCohortViaImport(t, dir, "p1.csv", "n,cat\n4,a\n5,b\n")
	arch := filepath.Join(dir, "arch.pulse")
	p, err := pulse.New(pulse.Options{FS: afero.NewOsFs()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(context.Background(), arch, []string{c0, c1}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	run := func(cohort string) (envelope struct {
		Data *struct {
			Valid bool `json:"valid"`
		} `json:"data"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}, raw string) {
		t.Helper()
		req, err := json.Marshal(map[string]any{
			"cohort":       map[string]any{"filename": cohort},
			"aggregations": []any{map[string]any{"type": "AGG_SUM", "field": "n", "label": "s"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		reqPath := filepath.Join(dir, "r.json")
		if err := os.WriteFile(reqPath, req, 0o644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		root := APICommand()
		root.Writer = &buf
		_ = root.Run(context.Background(), []string{"api", "predict", "-r", reqPath, "--json"})
		raw = buf.String()
		if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
			t.Fatalf("not an envelope: %v\n%s", err, raw)
		}
		return envelope, raw
	}

	for _, cohort := range []string{c0, arch, arch + "#" + filepath.Base(c1)} {
		env, raw := run(cohort)
		if env.Data == nil || !env.Data.Valid || len(env.Errors) != 0 {
			t.Errorf("%s: want a valid verdict, got\n%s", cohort, raw)
		}
	}

	env, raw := run(filepath.Join(dir, "nope.pulse"))
	if len(env.Errors) != 1 || env.Errors[0].Code != "PREDICT_ERROR" {
		t.Errorf("missing cohort: want one PREDICT_ERROR, got\n%s", raw)
	}
}

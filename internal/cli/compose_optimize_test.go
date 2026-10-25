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

// optCLIFixture writes the optfixture cohort and saved demand model
// under a temp PULSE_DATA_DIR (also the working directory) and returns
// it, a library instance over
// it, and a rewrite of a fixture body naming the cohort's path.
func optCLIFixture(t *testing.T) (string, *pulse.Pulse, func([]byte) []byte) {
	t.Helper()
	dir := withTempDataDir(t)
	// The CLI reads through the OS filesystem from the working
	// directory, where a model path (relative by the save-path rule)
	// resolves.
	t.Chdir(dir)
	p, err := newPulse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cohort := filepath.Join(dir, optfixture.CohortFile)
	quoted, err := json.Marshal(cohort)
	if err != nil {
		t.Fatal(err)
	}
	at := func(raw []byte) []byte {
		out := bytes.ReplaceAll(raw, []byte(`"`+optfixture.CohortFile+`"`), quoted)
		if bytes.Equal(out, raw) {
			t.Fatalf("request names no %s cohort", optfixture.CohortFile)
		}
		return out
	}
	b, err := p.NewCohortBuilder(t.Context(), cohort, optfixture.Schema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range optfixture.Rows() {
		if err := b.Append(pulse.CohortRow(r)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	mf, err := optfixture.ModelFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(optfixture.ModelPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, optfixture.ModelPath), mf, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, p, at
}

// TestOptimize_CLIComposeReturnsBlocks (mmm-estimation E3-S2): `pulse
// api compose --json` returns the optimize blocks — the envelope `data`
// is byte for byte the library's ComposedResponse for the fitted price
// optimisation, the saved-model one and the hill() budget allocation,
// serial and --parallel alike — and `--stream` ends with one
// {"optimizations":[...]} line equal to the library's.
func TestOptimize_CLIComposeReturnsBlocks(t *testing.T) {
	dir, p, at := optCLIFixture(t)
	for name, body := range map[string][]byte{"price": optfixture.Price, "price_model": optfixture.PriceModel, "budget": optfixture.Budget} {
		t.Run(name, func(t *testing.T) {
			raw := at(body)
			reqPath := filepath.Join(dir, name+".json")
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
			if len(lib.Optimizations) != 1 || lib.Optimizations[0].Error != nil {
				t.Fatalf("library optimizations %+v", lib.Optimizations)
			}
			want, err := json.Marshal(lib)
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"compose", "--json", "-r", reqPath}, {"compose", "--json", "--parallel", "3", "-r", reqPath}} {
				var buf bytes.Buffer
				if err := runAPICLI(t, &buf, args...); err != nil {
					t.Fatalf("%v: %v\n%s", args, err, buf.String())
				}
				var env struct {
					Errors []any           `json:"errors"`
					Data   json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(buf.Bytes(), &env); err != nil || len(env.Errors) != 0 {
					t.Fatalf("envelope (%v): %s", err, buf.String())
				}
				var got bytes.Buffer
				if err := json.Compact(&got, env.Data); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got.Bytes(), want) {
					t.Fatalf("%v: CLI data differs from the library response:\n cli %s\n lib %s", args, got.Bytes(), want)
				}
			}
			var buf bytes.Buffer
			if err := runAPICLI(t, &buf, "compose", "--stream", "-r", reqPath); err != nil {
				t.Fatalf("stream: %v\n%s", err, buf.String())
			}
			lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
			wantLast, err := json.Marshal(map[string]any{"optimizations": lib.Optimizations})
			if err != nil {
				t.Fatal(err)
			}
			if last := lines[len(lines)-1]; !bytes.Equal(last, wantLast) {
				t.Fatalf("stream ends with %s, want %s", last, wantLast)
			}
		})
	}
}

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/spf13/afero"
)

// sampleRequestFixture writes two copies of the shared id/region cohort
// (writeIndexTestCohort: three records each) and returns their paths.
func sampleRequestFixture(t *testing.T) (dir, real, twin string) {
	t.Helper()
	dir = t.TempDir()
	real = filepath.Join(dir, "real.pulse")
	twin = filepath.Join(dir, "twin.pulse")
	writeIndexTestCohort(t, real)
	writeIndexTestCohort(t, twin)
	return dir, real, twin
}

func writeSampleRequest(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "sample_req.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func unionSampleBody(real, twin, tail string) string {
	return `{"union":[{"cohort":{"filename":` + strconv.Quote(real) + `},"label":"real"},` +
		`{"cohort":{"filename":` + strconv.Quote(twin) + `},"label":"twin"}]` + tail + `}`
}

type sampleRowsEnvelope struct {
	Data   []map[string]any `json:"data"`
	Errors []struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"errors"`
	Request map[string]any `json:"request"`
}

func decodeSampleEnvelope(t *testing.T, raw string) sampleRowsEnvelope {
	t.Helper()
	var env sampleRowsEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, raw)
	}
	return env
}

// TestAPISampleCLI_RequestSamplesUnion: `api sample --request` samples a
// cohort union — rows in member order, each tagged by its member label
// in the default `_source` field — from a file and from stdin ("-"),
// and echoes the decoded request.
func TestAPISampleCLI_RequestSamplesUnion(t *testing.T) {
	t.Parallel()

	dir, real, twin := sampleRequestFixture(t)
	body := unionSampleBody(real, twin, `,"n":100`)
	reqPath := writeSampleRequest(t, dir, body)

	check := func(t *testing.T, raw string) {
		t.Helper()
		env := decodeSampleEnvelope(t, raw)
		if len(env.Errors) != 0 {
			t.Fatalf("errors = %+v", env.Errors)
		}
		if len(env.Data) != 6 {
			t.Fatalf("rows = %d, want 6 (3 per member): %v", len(env.Data), env.Data)
		}
		for i, row := range env.Data {
			want := "real"
			if i >= 3 {
				want = "twin"
			}
			if row["_source"] != want {
				t.Errorf("row %d _source = %v, want %q", i, row["_source"], want)
			}
		}
		if u, _ := env.Request["union"].([]any); len(u) != 2 {
			t.Errorf("echoed request union = %v, want both members", env.Request["union"])
		}
	}

	t.Run("file", func(t *testing.T) {
		t.Parallel()
		out, err := runAPI(t, "sample", "-r", reqPath, "--json", "--echo-request")
		if err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		check(t, out)
	})
	t.Run("stdin", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		root := APICommand()
		root.Writer = &buf
		root.Reader = strings.NewReader(body)
		if err := root.Run(context.Background(), []string{"api", "sample", "--request", "-", "--json", "--echo-request"}); err != nil {
			t.Fatalf("run: %v\n%s", err, buf.String())
		}
		check(t, buf.String())
	})
}

// TestAPISampleCLI_RequestCountLayering: a request file's `n` caps the
// rows; an explicitly set --count overrides it; a file without `n` takes
// --count's default (10, as MCP pulse_sample does) rather than zero.
func TestAPISampleCLI_RequestCountLayering(t *testing.T) {
	t.Parallel()

	dir, real, _ := sampleRequestFixture(t)
	cohort := `{"cohort":{"filename":` + strconv.Quote(real) + `}`
	cases := []struct {
		name  string
		body  string
		extra []string
		want  int
	}{
		{"file n", cohort + `,"n":1}`, nil, 1},
		{"count overrides n", cohort + `,"n":1}`, []string{"--count", "2"}, 2},
		{"no n takes the default", cohort + `}`, nil, 3},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			sub := filepath.Join(dir, strconv.Itoa(i))
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			reqPath := writeSampleRequest(t, sub, c.body)
			args := append([]string{"sample", "--request", reqPath, "--json"}, c.extra...)
			out, err := runAPI(t, args...)
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			if env := decodeSampleEnvelope(t, out); len(env.Data) != c.want {
				t.Errorf("rows = %d, want %d", len(env.Data), c.want)
			}
		})
	}
}

// TestAPISampleCLI_InputAndRequestUsage: --input beside --request, and
// neither, are usage errors (exit 2, no envelope).
func TestAPISampleCLI_InputAndRequestUsage(t *testing.T) {
	t.Parallel()

	dir, real, _ := sampleRequestFixture(t)
	reqPath := writeSampleRequest(t, dir, `{"cohort":{"filename":`+strconv.Quote(real)+`},"n":1}`)
	for name, args := range map[string][]string{
		"both":    {"sample", "--input", real, "--request", reqPath, "--json"},
		"neither": {"sample", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := runAPI(t, args...)
			if status, _ := ExitStatus(err); status != ExitUsage {
				t.Fatalf("err = %v: exit %d, want %d", err, status, ExitUsage)
			}
			if out != "" {
				t.Errorf("a usage error writes no envelope, got %q", out)
			}
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.CLI_INPUT {
				t.Errorf("err = %v, want CLI_INPUT", err)
			}
		})
	}
}

// TestAPISampleCLI_RequestFile_UnknownKeyRefused: the request file goes
// through the strict decoder; a misspelled key is the coded
// PULSE_REQUEST_UNKNOWN_FIELD in the envelope, never dropped.
func TestAPISampleCLI_RequestFile_UnknownKeyRefused(t *testing.T) {
	t.Parallel()

	dir, real, twin := sampleRequestFixture(t)
	reqPath := writeSampleRequest(t, dir, unionSampleBody(real, twin, `,"n":2,"lables":[]`))
	out, err := runAPI(t, "sample", "-r", reqPath, "--json")
	wantEnvelopeFatal(t, err)
	env := decodeSampleEnvelope(t, out)
	if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_REQUEST_UNKNOWN_FIELD) {
		t.Fatalf("errors = %+v, want one PULSE_REQUEST_UNKNOWN_FIELD", env.Errors)
	}
}

// TestAPISampleCLI_HiddenUnionMatchesMCP: on an instance whose feature
// profile hides capability:union, the CLI's request form (its loader,
// then SampleWithRequest, as the leaf runs it) refuses the union slot
// exactly as MCP pulse_sample does — PULSE_REQUEST_UNKNOWN_FIELD naming
// `union` — while the same instance with the capability samples it.
func TestAPISampleCLI_HiddenUnionMatchesMCP(t *testing.T) {
	t.Parallel()

	dir, real, twin := sampleRequestFixture(t)
	body := unionSampleBody(real, twin, `,"n":2`)
	reqPath := writeSampleRequest(t, dir, body)
	features := []string{"capability:sample"}
	instance := func(with bool) *pulse.Pulse {
		fs := append([]string(nil), features...)
		if with {
			fs = append(fs, descx.FeatureUnion)
		}
		p, err := pulse.New(pulse.Options{FS: afero.NewOsFs(), FeatureProfile: &pulse.FeatureProfile{Features: fs}})
		if err != nil {
			t.Fatalf("pulse.New: %v", err)
		}
		return p
	}
	cliRun := func(p *pulse.Pulse) (*pulse.SampleResult, error) {
		req, err := loadSampleRequest(reqPath, nil, nil)
		if err != nil {
			t.Fatalf("loadSampleRequest: %v", err)
		}
		return p.SampleWithRequest(context.Background(), req)
	}
	var sampleTool mcp.ToolDescriptor
	for _, td := range mcp.Tools(mcp.Config{}) {
		if td.Name == toolmeta.ToolSample {
			sampleTool = td
		}
	}
	mcpArgs := json.RawMessage(unionSampleBody(real, twin, `,"count":2`))

	on := instance(true)
	if res, err := cliRun(on); err != nil || len(res.Rows) != 2 {
		t.Fatalf("union enabled: rows %v, err %v; want 2 rows", res, err)
	}

	off := instance(false)
	_, cliErr := cliRun(off)
	_, mcpErr := sampleTool.Invoke(context.Background(), off, mcpArgs)
	for label, err := range map[string]error{"cli": cliErr, "mcp": mcpErr} {
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) {
			t.Fatalf("%s: err = %v, want a coded refusal", label, err)
		}
		if ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
			t.Errorf("%s: code %s (%s), want PULSE_REQUEST_UNKNOWN_FIELD", label, ce.Code, ce.Message)
		}
		unknown, _ := ce.Details["unknown_keys"].([]string)
		if !slices.Contains(unknown, "union") {
			t.Errorf("%s: unknown_keys = %v, want union", label, ce.Details["unknown_keys"])
		}
	}
}

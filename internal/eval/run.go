package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/io/csv"
	"github.com/frankbardon/pulse/internal/mcp"
	pio "github.com/frankbardon/pulse/io"
)

// SynthTool is the submission tool name for a synthetic-cohort spec:
// the simulate route has no MCP tool, so its answer is the spec itself.
const SynthTool = "synth"

// writeTools are the submission tools that write into the data
// directory; a submission calling one runs on its own.
var writeTools = map[string]bool{
	SynthTool: true, "pulse_import": true, "pulse_materialize": true,
	"pulse_rake": true, "pulse_dedup": true, "pulse_drop": true,
}

// Writes reports whether s writes into its data directory, so it
// cannot share one with other submissions.
func (s Submission) Writes() bool { return writeTools[s.Tool] }

// Outcome is what running a submission produced.
type Outcome struct {
	// Result is the tool's output as generic JSON; nil when Err is set.
	Result any
	// Err is the run's failure, if any.
	Err error
	// Cohort is the inspected cohort the submission wrote (import,
	// materialize, synth) as generic JSON; nil otherwise.
	Cohort any
	// args is the submission's arguments as generic JSON (data_dir
	// keys dropped).
	args any
}

// Code returns the coded error's code of a failed run, else "".
func (o *Outcome) Code() string {
	var ce *perr.CodedError
	if errors.As(o.Err, &ce) {
		return string(ce.Code)
	}
	return ""
}

// Env is the data directory a submission runs against.
type Env struct {
	P       *pulse.Pulse
	FS      afero.Fs
	DataDir string
}

// NewEnv builds a fresh data directory at dataDir holding the tasks'
// fixture cohorts (imported from fixtureDir's CSVs and schemas), their
// raw files and their indexes, and a Pulse instance rooted there.
// Tasks whose submissions only read may share one; a write-side task
// gets its own.
func NewEnv(ctx context.Context, fixtureDir, dataDir string, tasks ...*Task) (*Env, error) {
	need := need(tasks)
	fs := afero.NewOsFs()
	if err := fs.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	builder, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		return nil, err
	}
	for _, name := range need.Cohorts {
		raw, err := afero.ReadFile(fs, filepath.Join(fixtureDir, "schemas", name+".json"))
		if err != nil {
			return nil, fmt.Errorf("cohort %s: %w", name, err)
		}
		schema, err := iio.ParseSchemaJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("cohort %s schema: %w", name, err)
		}
		if _, err := builder.Import(ctx, &pio.ImportJob{
			Source: csv.NewReader(fs, filepath.Join(fixtureDir, name+".csv")),
			Target: filepath.Join(dataDir, name+".pulse"),
			Schema: schema,
			FS:     fs,
		}); err != nil {
			return nil, fmt.Errorf("import cohort %s: %w", name, err)
		}
	}
	for _, f := range need.Files {
		raw, err := afero.ReadFile(fs, filepath.Join(fixtureDir, f))
		if err != nil {
			return nil, fmt.Errorf("file %s: %w", f, err)
		}
		if err := afero.WriteFile(fs, filepath.Join(dataDir, f), raw, 0o644); err != nil {
			return nil, err
		}
	}
	for _, ix := range need.Indexes {
		if _, err := builder.BuildIndex(ctx, filepath.Join(dataDir, ix.Cohort+".pulse"), ix.Fields); err != nil {
			return nil, fmt.Errorf("index %s on %v: %w", ix.Cohort, ix.Fields, err)
		}
	}
	// Rooted at the data directory the way `pulse mcp` roots its
	// instance, so a cohort filename resolves with no data_dir.
	p, err := pulse.New(pulse.Options{DataDir: dataDir})
	if err != nil {
		return nil, err
	}
	return &Env{P: p, FS: fs, DataDir: dataDir}, nil
}

// need is the union of the tasks' cohorts, files and indexes.
func need(tasks []*Task) *Task {
	u := &Task{}
	seen := map[string]bool{}
	for _, t := range tasks {
		for _, c := range t.Cohorts {
			if !seen["c:"+c] {
				seen["c:"+c] = true
				u.Cohorts = append(u.Cohorts, c)
			}
		}
		for _, f := range t.Files {
			if !seen["f:"+f] {
				seen["f:"+f] = true
				u.Files = append(u.Files, f)
			}
		}
		for _, ix := range t.Indexes {
			k := "i:" + ix.Cohort + ":" + strings.Join(ix.Fields, ",")
			if !seen[k] {
				seen[k] = true
				u.Indexes = append(u.Indexes, ix)
			}
		}
	}
	return u
}

// Run executes a submission against env the way the MCP server would:
// the named tool's strict decode and handler, under the server's
// default return preset.
func Run(ctx context.Context, env *Env, sub Submission) *Outcome {
	raw, args, err := stripDataDir(sub.Arguments)
	if err != nil {
		return &Outcome{Err: fmt.Errorf("arguments: %w", err)}
	}
	o := &Outcome{args: args}
	var out any
	var written string
	if sub.Tool == SynthTool {
		out, written, err = runSynth(ctx, env, raw)
	} else {
		tool, ok := toolByName(sub.Tool)
		if !ok {
			o.Err = fmt.Errorf("unknown tool %q", sub.Tool)
			return o
		}
		out, err = tool.Invoke(ctx, env.P, raw)
		if err == nil {
			written = writtenCohort(sub.Tool, out)
		}
	}
	if err != nil {
		o.Err = err
		return o
	}
	if o.Result, err = toGeneric(out); err != nil {
		o.Err = err
		return o
	}
	if written != "" {
		if !filepath.IsAbs(written) {
			written = filepath.Join(env.DataDir, written)
		}
		info, err := env.P.Inspect(ctx, written)
		if err != nil {
			o.Err = fmt.Errorf("inspect written cohort %s: %w", written, err)
			return o
		}
		if o.Cohort, err = toGeneric(info); err != nil {
			o.Err = err
		}
	}
	return o
}

func toolByName(name string) (mcp.ToolDescriptor, bool) {
	for _, t := range mcp.Tools(mcp.Config{}) {
		if t.Name == name {
			return t, true
		}
	}
	return mcp.ToolDescriptor{}, false
}

// writtenCohort is the path of the cohort a write-side tool produced.
func writtenCohort(tool string, out any) string {
	switch v := out.(type) {
	case mcp.ImportOut:
		return v.Path
	case *mcp.ImportOut:
		return v.Path
	case mcp.MaterializeOut:
		if v.MaterializeResult != nil {
			return v.Target
		}
	case *mcp.MaterializeOut:
		if v != nil && v.MaterializeResult != nil {
			return v.Target
		}
	}
	return ""
}

// synthArgs is the "synth" submission's arguments.
type synthArgs struct {
	Spec *pulse.SynthSpec `json:"spec"`
	Out  string           `json:"out"`
	Seed int64            `json:"seed,omitempty"`
}

func runSynth(ctx context.Context, env *Env, raw []byte) (any, string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a synthArgs
	if err := dec.Decode(&a); err != nil {
		return nil, "", fmt.Errorf("synth arguments: %w", err)
	}
	if a.Spec == nil || a.Out == "" {
		return nil, "", fmt.Errorf("synth arguments need spec and out")
	}
	out := a.Out
	if !filepath.IsAbs(out) {
		out = filepath.Join(env.DataDir, out)
	}
	res, err := env.P.Synth(ctx, a.Spec, out, pulse.SynthOptions{Seed: a.Seed})
	if err != nil {
		return nil, "", err
	}
	return res, out, nil
}

// stripDataDir drops every data_dir key from the arguments, returning
// the re-encoded bytes and the generic form.
func stripDataDir(raw json.RawMessage) ([]byte, any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, nil, err
	}
	v = dropKey(v, "data_dir")
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	g, err := toGeneric(json.RawMessage(b))
	return b, g, err
}

func dropKey(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		delete(x, key)
		for k, e := range x {
			x[k] = dropKey(e, key)
		}
	case []any:
		for i, e := range x {
			x[i] = dropKey(e, key)
		}
	}
	return v
}

// toGeneric round-trips v through JSON into maps, slices, strings,
// float64s, bools and nils.
func toGeneric(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var g any
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, err
	}
	return g, nil
}

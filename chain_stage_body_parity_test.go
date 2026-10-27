package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Chain predict judges every stage with the Process-predict request body
// (operator names, params, accepted types, overlays, labels) the runtime
// applies when the stage runs, each refusal located details.stage on
// both sides; label bindings on a stage >= 1 are applied, never dropped
// (#254, #201c); the augment-sibling collision set is one list on both
// sides (#201b).

// labelCohort is a small cohort: region (categorical a/b), x (f64),
// GROUP_KMEANS (categorical — its augment sibling is GROUP_KMEANS_label,
// the key column of a GROUP_KMEANS over fields ["label", ...]), label and
// y (f64). The Pulse registers label table "regions" (a → Alpha, b →
// Beta).
func labelCohort(t *testing.T) (*pulse.Pulse, afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs, Extensions: pulse.Extensions{
		LabelTables: map[string]pulse.LabelTable{"regions": {Rows: map[string]string{"a": "Alpha", "b": "Beta"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"region", "x", "GROUP_KMEANS", "label", "y"}
	typesOf := []encoding.FieldType{encoding.FieldTypeCategoricalU8, encoding.FieldTypeF64, encoding.FieldTypeCategoricalU8, encoding.FieldTypeF64, encoding.FieldTypeF64}
	var fields []encoding.Field
	off := 0
	for i, n := range names {
		f := encoding.Field{Name: n, Type: typesOf[i], CsvColumnIdx: i, ByteOffset: off}
		if f.Type.HasDictionary() {
			f.Dictionary = encoding.NewDictionary()
		}
		off += f.Type.ByteSize()
		fields = append(fields, f)
	}
	var b strings.Builder
	b.WriteString(strings.Join(names, ",") + "\n")
	for i := range 24 {
		fmt.Fprintf(&b, "%s,%d,k%d,%d,%d\n", []string{"a", "b"}[i%2], i+1, i%3, i%4, (i*7)%5)
	}
	if err := afero.WriteFile(fs, "lab.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	job := &pio.ImportJob{Source: csv.NewReader(fs, "lab.csv"), Target: "lab.pulse", Schema: &encoding.Schema{Fields: fields}, FS: fs}
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("import: %v", err)
	}
	return p, fs, "lab.pulse"
}

// stage0Grouped is the feeding stage: region keys, x summed as "s".
const stage0Grouped = `{"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}]}`

func decodeStage(t *testing.T, body string) *types.Request {
	t.Helper()
	var r types.Request
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return &r
}

func stageChain(t *testing.T, cohort string, bodies ...string) *types.ChainRequest {
	t.Helper()
	c := &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}}
	for i, b := range bodies {
		c.Stages = append(c.Stages, &types.ChainStage{Name: fmt.Sprintf("s%d", i), Request: decodeStage(t, b)})
	}
	return c
}

// assertChainParity runs mk through ProcessChain and PredictChain and
// requires both to refuse with one entry: the same code and message,
// located details[key] = wantStage on both sides (key "stage", or the
// chain gate's "stage_index").
func assertChainParity(t *testing.T, p *pulse.Pulse, name string, mk func() *types.ChainRequest, wantCode errors.Code, key string, wantStage int) {
	t.Helper()
	ctx := context.Background()
	_, rerr := p.ProcessChain(ctx, mk())
	var ce *errors.CodedError
	if !stderrors.As(rerr, &ce) {
		t.Errorf("%s: runtime %v, want a coded %s", name, rerr, wantCode)
		return
	}
	if ce.Code != wantCode {
		t.Errorf("%s: runtime code %s (%s), want %s", name, ce.Code, ce.Message, wantCode)
	}
	if ce.Details[key] != wantStage {
		t.Errorf("%s: runtime located at %v (%v), want %s %d", name, ce.Details[key], ce.Details, key, wantStage)
	}
	env, err := p.PredictChain(ctx, mk())
	if err != nil {
		t.Fatalf("%s: PredictChain: %v", name, err)
	}
	if len(env.Errors) != 1 {
		t.Errorf("%s: predict %v, want exactly one error (runtime %s %q)", name, envCodes(env), ce.Code, ce.Message)
		return
	}
	got := env.Errors[0]
	if got.Code != string(ce.Code) || got.Message != ce.Message {
		t.Errorf("%s: predict %s %q, runtime %s %q", name, got.Code, got.Message, ce.Code, ce.Message)
	}
	if got.Details[key] != wantStage {
		t.Errorf("%s: predict located at %v, want %s %d", name, got.Details[key], key, wantStage)
	}
}

// TestChainPredict_StageBodyMatchesRuntime (#254, #201c): a stage >= 1
// label binding naming an unknown table, an unknown field or a
// non-categorical (f64) column, a stage whose aggregator refuses its
// field's type, and a stage overlay the stage's shape cannot host are
// each refused by predict-chain with the runtime's code, message and
// stage — the labels used to be dropped silently at runtime and the
// rest used to predict valid. Stage 0 gets the same body check.
func TestChainPredict_StageBodyMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, _, cohort := labelCohort(t)
	for _, c := range []struct {
		name, body string
		code       errors.Code
	}{
		{"label unknown table", `{"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"s","label":"t"}],"labels":[{"field":"region","table":"no_such_table"}]}`, errors.PULSE_LABEL_TABLE_UNKNOWN},
		{"label unknown field", `{"aggregations":[{"type":"AGG_SUM","field":"s","label":"t"}],"labels":[{"field":"nope","table":"regions"}]}`, errors.PULSE_LABEL_FIELD_UNKNOWN},
		{"label on f64 output", `{"aggregations":[{"type":"AGG_SUM","field":"s","label":"t"}],"labels":[{"field":"s","table":"regions"}]}`, errors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"label sibling collision", `{"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"s","label":"region_label"}],"labels":[{"field":"region","table":"regions","mode":"augment"}]}`, errors.PULSE_LABEL_FIELD_COLLISION},
		{"aggregator over categorical key", `{"aggregations":[{"type":"AGG_SUM","field":"region"}]}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"matrix overlay on grouped stage", `{"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"s","label":"t"}],"overlays":[{"name":"o","kind":"OVERLAY_SHARE_OF_ROW","scope":"cell"}]}`, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"formula overlay on grouped stage", `{"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"s","label":"t"}],"overlays":[{"name":"o","kind":"OVERLAY_FORMULA","scope":"group","params":{"formula":"cell"}}]}`, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
	} {
		body := c.body
		assertChainParity(t, p, "stage 1: "+c.name, func() *types.ChainRequest {
			return stageChain(t, cohort, stage0Grouped, body)
		}, c.code, "stage", 1)
	}
	// An unregistered aggregator on a later stage is the chain gate's
	// refusal on both sides, before the stage body (details.stage_index).
	assertChainParity(t, p, "stage 1: unknown aggregator", func() *types.ChainRequest {
		return stageChain(t, cohort, stage0Grouped, `{"aggregations":[{"type":"AGG_NOPE","field":"s"}]}`)
	}, errors.PULSE_CHAIN_NOT_MERGEABLE, "stage_index", 1)
	// Stage 0 runs through Process: the same body rules, located stage 0.
	for _, c := range []struct {
		name, body string
		code       errors.Code
	}{
		{"label unknown table", `{"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"labels":[{"field":"region","table":"no_such_table"}]}`, errors.PULSE_LABEL_TABLE_UNKNOWN},
		{"aggregator over categorical", `{"aggregations":[{"type":"AGG_SUM","field":"region","label":"s"}]}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
	} {
		body := c.body
		assertChainParity(t, p, "stage 0: "+c.name, func() *types.ChainRequest {
			return stageChain(t, cohort, body, `{"aggregations":[{"type":"AGG_SUM","field":"s","label":"t"}]}`)
		}, c.code, "stage", 0)
	}
}

// TestChain_StageLabelsApplied (#201c): a valid label binding on a stage
// >= 1 relabels that stage's output — replace rewrites the key column,
// augment adds the <field>_label sibling — where it used to be dropped
// and the raw key returned.
func TestChain_StageLabelsApplied(t *testing.T) {
	t.Parallel()

	p, _, cohort := labelCohort(t)
	ctx := context.Background()
	for _, c := range []struct {
		mode, col string
	}{
		{"replace", "region"},
		{"augment", "region_label"},
	} {
		stage1 := `{"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"s","label":"t"}],"labels":[{"field":"region","table":"regions","mode":"` + c.mode + `"}]}`
		req := stageChain(t, cohort, stage0Grouped, stage1)
		resp, err := p.ProcessChain(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", c.mode, err)
		}
		got := map[string]bool{}
		for _, row := range resp.Final.Data {
			got[fmt.Sprint(row[c.col])] = true
			if c.mode == "augment" && row["region"] != "a" && row["region"] != "b" {
				t.Errorf("augment: raw key %v rewritten", row["region"])
			}
		}
		if !got["Alpha"] || !got["Beta"] || len(got) != 2 {
			t.Errorf("%s: stage-1 %s values %v, want {Alpha, Beta}", c.mode, c.col, got)
		}
		env, err := p.PredictChain(ctx, stageChain(t, cohort, stage0Grouped, stage1))
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) != 0 {
			t.Errorf("%s: predict-chain %v, want valid", c.mode, envCodes(env))
		}
	}
}

// TestLabelCollision_PredictMatchesRuntime (#201b): an augment sibling
// colliding with an output column of any source — an aggregation, an
// attribute, a window, a feature or a grouper key — is
// PULSE_LABEL_FIELD_COLLISION on both Process and predict (predict used
// to miss the aggregation, window and group-key sources).
func TestLabelCollision_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fs, cohort := labelCohort(t)
	ctx := context.Background()
	aug := `"labels":[{"field":"region","table":"regions","mode":"augment"}]`
	for _, c := range []struct{ name, body string }{
		{"aggregation", `{"groups":[{"type":"GROUP_CATEGORY","field":"region"}],"aggregations":[{"type":"AGG_SUM","field":"x","label":"region_label"}],` + aug + `}`},
		{"attribute", `{"attributes":[{"type":"ATTR_ZSCORE","field":"x","label":"region_label"}],"aggregations":[{"type":"AGG_COUNT","field":"x","label":"n"}],` + aug + `}`},
		{"window", `{"windows":[{"type":"WIN_ROW_NUMBER","label":"region_label","order_by":[{"field":"x"}]}],` + aug + `}`},
		{"feature", `{"features":[{"type":"FEAT_LOG","field":"x","label":"region_label"}],"aggregations":[{"type":"AGG_COUNT","field":"x","label":"n"}],` + aug + `}`},
		{"group key", `{"groups":[{"type":"GROUP_KMEANS","fields":["label","y"],"params":{"k":2}}],"aggregations":[{"type":"AGG_COUNT","field":"x","label":"n"}],"labels":[{"field":"GROUP_KMEANS","table":"regions","mode":"augment"}]}`},
	} {
		_, rerr := p.Process(ctx, slotRequest(t, cohort, c.body))
		var ce *errors.CodedError
		if !stderrors.As(rerr, &ce) || ce.Code != errors.PULSE_LABEL_FIELD_COLLISION {
			t.Errorf("%s: Process %v, want %s", c.name, rerr, errors.PULSE_LABEL_FIELD_COLLISION)
			continue
		}
		env := predictEnvelope(t, p, fs, cohort, slotRequest(t, cohort, c.body))
		if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
			t.Errorf("%s: predict %v, runtime %s %q", c.name, envCodes(env), ce.Code, ce.Message)
		}
	}
}

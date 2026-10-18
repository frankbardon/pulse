package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// auc_test.go pins AGG_AUC (model-workbench E2-S1) at the facade:
// predict / runtime parity for every configuration refusal, the
// run-time-only non-binary outcome refusal, the weighted withholding of
// the DeLong interval and the stream's terminal flush.

const aucCohort = "auc_rows.pulse"

func aucFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, aucCohort, []string{"score", "y", "w", "region", "y3"},
		[][]string{
			{"0.9", "1", "1.5", "north", "1"},
			{"0.2", "0", "1", "south", "0"},
			{"0.6", "1", "2", "north", "2"},
			{"0.7", "0", "0.5", "south", "0"},
			{"0.3", "0", "1", "north", "1"},
			{"0.8", "1", "1", "south", "1"},
			{"0.6", "0", "3", "north", "0"},
		})
	return fsys
}

func aucReq(field, params string) *Request {
	return &Request{
		Cohort:       &types.Cohort{Filename: aucCohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AUC, Field: field, Label: "auc", Params: json.RawMessage(params)}},
	}
}

// TestAUC_PredictMatchesRuntime: every AGG_AUC configuration refusal
// predict reports is exactly the runtime's — code, message, details —
// and every accepted slot is accepted by both.
func TestAUC_PredictMatchesRuntime(t *testing.T) {
	fsys := aucFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	data, err := afero.ReadFile(fsys, aucCohort)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, field, params string
		code                perr.Code // "" = accepted
	}{
		{"accepted", "score", `{"outcome":"y"}`, ""},
		{"accepted positive 0", "score", `{"outcome":"y","positive":0}`, ""},
		{"missing outcome", "score", `{}`, perr.PROCESSING_CONFIG},
		{"positive 2", "score", `{"outcome":"y","positive":2}`, perr.PROCESSING_CONFIG},
		{"malformed params", "score", `{"outcome":3}`, perr.PROCESSING_CONFIG},
		{"categorical outcome", "score", `{"outcome":"region"}`, perr.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"categorical score", "region", `{"outcome":"y"}`, perr.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"unknown outcome column", "score", `{"outcome":"nope"}`, perr.SERVICE_VALIDATION},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := aucReq(tc.field, tc.params)
			env, err := p.PredictBytes(ctx, data, req)
			if err != nil {
				t.Fatal(err)
			}
			_, rerr := p.Process(ctx, req)
			if tc.code == "" {
				if len(env.Errors) != 0 || rerr != nil {
					t.Fatalf("accepted slot refused: predict %+v runtime %v", env.Errors, rerr)
				}
				return
			}
			var ce *perr.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != tc.code {
				t.Fatalf("runtime err = %v, want %s", rerr, tc.code)
			}
			if len(env.Errors) == 0 {
				t.Fatalf("predict accepted what the runtime refused (%v)", rerr)
			}
			pe := env.Errors[0]
			if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
				t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
			}
		})
	}
}

// TestAUC_NonBinaryOutcomeIsRunTimeOnly: an integer outcome holding a 2
// passes predict (no schema type promises 0 / 1, and predict reads no
// record) and fails the run with PROCESSING_AUC_OUTCOME_NOT_BINARY.
func TestAUC_NonBinaryOutcomeIsRunTimeOnly(t *testing.T) {
	fsys := aucFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := afero.ReadFile(fsys, aucCohort)
	req := aucReq("score", `{"outcome":"y3"}`)
	env, err := p.PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("predict refused a type that can hold 0 / 1: %+v", env.Errors)
	}
	_, rerr := p.Process(context.Background(), req)
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) || ce.Code != perr.PROCESSING_AUC_OUTCOME_NOT_BINARY {
		t.Fatalf("runtime err = %v, want PROCESSING_AUC_OUTCOME_NOT_BINARY", rerr)
	}
	if ce.Details["value"] != float64(2) {
		t.Errorf("details.value = %v, want 2", ce.Details["value"])
	}
}

// TestAUC_WeightedWithholdsDeLong: under a weight the AUC is the
// weighted pair figure and the DeLong figures are null on the wire with
// se_note naming PULSE_AUC_DELONG_WEIGHTED; unweighted they are numbers.
func TestAUC_WeightedWithholdsDeLong(t *testing.T) {
	p, err := New(Options{FS: aucFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plain, err := p.Process(ctx, aucReq("score", `{"outcome":"y"}`))
	if err != nil {
		t.Fatal(err)
	}
	req := aucReq("score", `{"outcome":"y"}`)
	req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
	weighted, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	// Positives (0.9, 1.5) (0.6, 2) (0.8, 1); negatives (0.2, 1)
	// (0.7, 0.5) (0.3, 1) (0.6, 3): the weighted pair sum by hand.
	pos := [][2]float64{{0.9, 1.5}, {0.6, 2}, {0.8, 1}}
	neg := [][2]float64{{0.2, 1}, {0.7, 0.5}, {0.3, 1}, {0.6, 3}}
	var num, wp, wn float64
	for _, n := range neg {
		wn += n[1]
	}
	for _, q := range pos {
		wp += q[1]
		for _, n := range neg {
			switch {
			case q[0] > n[0]:
				num += q[1] * n[1]
			case q[0] == n[0]:
				num += 0.5 * q[1] * n[1]
			}
		}
	}
	want := num / (wp * wn)
	if got := weighted.Data[0]["auc"].(float64); math.Abs(got-want) > 1e-12 {
		t.Fatalf("weighted auc = %.17g, want %.17g", got, want)
	}
	var wire struct {
		Components struct {
			Aggregations []struct {
				Operator map[string]any `json:"operator"`
			} `json:"aggregations"`
		} `json:"components"`
	}
	b, err := json.Marshal(weighted)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	op := wire.Components.Aggregations[0].Operator
	for _, k := range []string{"se", "ci_lower", "ci_upper"} {
		if v, ok := op[k]; !ok || v != nil {
			t.Errorf("weighted %s = %v (present %v), want null", k, v, ok)
		}
	}
	if op["se_note"] != string(perr.PULSE_AUC_DELONG_WEIGHTED) {
		t.Errorf("se_note = %v", op["se_note"])
	}
	po := plain.Components.Aggregations[0].Operator
	if se, _ := po["se"].(float64); math.IsNaN(se) || se <= 0 {
		t.Errorf("unweighted se = %v, want a positive number", po["se"])
	}
	if _, ok := po["se_note"]; ok {
		t.Error("unweighted slot carries se_note")
	}
}

// TestAUC_StreamTerminalCarriesOperator: AGG_AUC is mergeability None,
// so a stream carries its operator map on the terminal chunk only, equal
// to the buffered Process call's Components.
func TestAUC_StreamTerminalCarriesOperator(t *testing.T) {
	p, err := New(Options{FS: aucFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	req := aucReq("score", `{"outcome":"y"}`)
	res, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	var chunks []StreamChunk[Row]
	for chunk := range res.Chunks {
		chunks = append(chunks, chunk)
	}
	if term := <-res.Done; term.Status != StreamCompleted {
		t.Fatalf("status = %v (err=%v)", term.Status, term.Error)
	}
	c := chunks[len(chunks)-1].Components
	if c == nil || len(c.Aggregations) != 1 || c.Aggregations[0].Operator["gini"] == nil {
		t.Fatalf("terminal chunk lacks the AUC operator map: %+v", c)
	}
	buffered, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, buffered.Components) {
		t.Fatalf("terminal Components != buffered:\nstream:   %+v\nbuffered: %+v", c, buffered.Components)
	}
}

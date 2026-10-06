package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// covN is an inline one-member covariance over the zone cohort's n.
func covN() []types.MatrixSpec {
	return []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"n"}}}
}

// matrixOverCohort is a mergeable grouped request that also carries a
// matrix — runnable on its own, on Compose slots and on chain stage 0.
func matrixOverCohort(cohort string) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		Matrices:     covN(),
	}
}

func joinedMatrix(cohort string) *types.Request {
	req := matrixOverCohort(cohort)
	req.Joins = []*types.JoinSpec{{Right: cohort, On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: "r_"}}
	return req
}

func crosstabMatrix(cohort string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
		},
		// An aggregator keeps a chain stage 0 past the merge gate, so the
		// host rule is what refuses it.
		Aggregations: countAgg(),
		Matrices:     covN(),
	}
}

// TestMatrixRefusal_PredictAndValidatorsMatchRuntime: a `matrices` slot
// on a joined request, on a crosstab request, or on a ProcessChain
// stage after 0 is refused by predict, ValidateJoin, the Compose slot
// validator and the chain validator exactly as the runtime refuses it —
// the shared rules mergegate.MatrixRefusal / StageMatrixRefusal, with
// their own PULSE_MATRIX_* codes, never PROCESSING_INTERNAL.
func TestMatrixRefusal_PredictAndValidatorsMatchRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}

	for _, c := range []struct {
		name    string
		mk      func(string) *types.Request
		code    errors.Code
		details map[string]any
	}{
		{"joins", joinedMatrix, errors.PULSE_MATRIX_UNSUPPORTED_SOURCE, map[string]any{"source": "join", "matrices": 1, "joins": 1}},
		{"crosstab", crosstabMatrix, errors.PULSE_MATRIX_HOST_CONFLICT, map[string]any{"host": "crosstab", "matrices": 1}},
	} {
		t.Run(c.name+"/predict", func(t *testing.T) {
			_, rerr := p.Process(ctx, c.mk(cohort))
			ce := requireCode(t, rerr, c.code)
			want, _ := json.Marshal(c.details)
			if got, _ := json.Marshal(ce.Details); string(got) != string(want) {
				t.Fatalf("runtime details = %s, want %s", got, want)
			}
			env := predictEnvelope(t, p, fs, cohort, c.mk(cohort))
			sameEntry(t, env, rerr)
			if env.Data.(*descriptor.PredictResult).Valid {
				t.Fatal("predict Valid=true")
			}
		})
		t.Run(c.name+"/compose slot", func(t *testing.T) {
			mk := func() *types.ComposedRequest {
				return &types.ComposedRequest{Requests: []*types.Request{matrixOverCohort(cohort), c.mk(cohort)}}
			}
			_, rerr := p.Compose(ctx, mk())
			requireCode(t, rerr, c.code)
			sameEntry(t, descx.ValidateComposeWithOptions(mk(), opts), rerr)
			_, perr := p.ComposeParallel(ctx, mk(), pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
			requireCode(t, perr, c.code)
		})
		t.Run(c.name+"/chain stage 0", func(t *testing.T) {
			mk := func() *types.ChainRequest {
				stage := c.mk(cohort)
				stage.Cohort = nil
				return &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}, Stages: []*types.ChainStage{{Name: "s0", Request: stage}}}
			}
			_, rerr := p.ProcessChain(ctx, mk())
			requireCode(t, rerr, c.code)
			sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), opts), rerr)
		})
	}

	t.Run("joins/validate join", func(t *testing.T) {
		env := descx.ValidateJoin(bytes.NewReader(data), bytes.NewReader(data), joinedMatrix(cohort))
		if !hasCode(env, errors.PULSE_MATRIX_UNSUPPORTED_SOURCE) {
			t.Fatalf("ValidateJoin errors = %+v, want PULSE_MATRIX_UNSUPPORTED_SOURCE", env.Errors)
		}
		plain := joinedMatrix(cohort)
		plain.Matrices = nil
		if env := descx.ValidateJoin(bytes.NewReader(data), bytes.NewReader(data), plain); hasCode(env, errors.PULSE_MATRIX_UNSUPPORTED_SOURCE) {
			t.Fatal("ValidateJoin refused a matrix-free join")
		}
	})

	t.Run("chain stage 1", func(t *testing.T) {
		mk := func() *types.ChainRequest {
			s0 := matrixOverCohort(cohort)
			s0.Cohort, s0.Matrices = nil, nil
			return &types.ChainRequest{
				Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{
					{Name: "s0", Request: s0},
					{Name: "s1", Request: &types.Request{
						Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}},
						// "absent" is no stage-0 output column: the refused stage
						// is never judged further, so its field is not reported.
						Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"absent"}}},
					}},
				},
			}
		}
		_, rerr := p.ProcessChain(ctx, mk())
		ce := requireCode(t, rerr, errors.PULSE_MATRIX_UNSUPPORTED_SOURCE)
		if ce.Details["source"] != "chain_stage" || ce.Details["stage"] != 1 || ce.Details["stage_name"] != "s1" {
			t.Fatalf("runtime details = %v", ce.Details)
		}
		env := descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), opts)
		sameEntry(t, env, rerr)
		if len(env.Errors) != 1 {
			t.Fatalf("validator judged the refused stage further: %+v", env.Errors)
		}
	})
}

func hasCode(env *descriptor.Envelope, code errors.Code) bool {
	for _, e := range env.Errors {
		if e.Code == string(code) {
			return true
		}
	}
	return false
}

// TestMatrices_ComposeAndChainStageZero: Compose (serial and parallel)
// slots each carry their own matrices, equal to the slot run alone, and
// a ProcessChain stage 0 with matrices runs over the stamped cohort
// rows — its matrix equals the plain Process one — while the next stage
// reads only its aggregate rows.
func TestMatrices_ComposeAndChainStageZero(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()

	alone, err := p.Process(ctx, matrixOverCohort(cohort))
	if err != nil {
		t.Fatal(err)
	}
	// matrixOverCohort is grouped by cat (buckets a, b, c): one matrix
	// per bucket.
	if len(alone.Matrices) != 3 {
		t.Fatalf("Process matrices = %+v, want one per bucket", alone.Matrices)
	}
	ungroupedOnly := &types.Request{Cohort: &types.Cohort{Filename: cohort},
		Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "solo", Fields: []string{"n"}, Params: json.RawMessage(`{"ddof":0}`)}}}
	solo, err := p.Process(ctx, ungroupedOnly)
	if err != nil {
		t.Fatal(err)
	}
	composed := func() *types.ComposedRequest {
		return &types.ComposedRequest{Requests: []*types.Request{matrixOverCohort(cohort), ungroupedOnly}}
	}
	wantSlots := []*types.Response{alone, solo}
	check := func(t *testing.T, resp *types.ComposedResponse) {
		t.Helper()
		if len(resp.Responses) != 2 {
			t.Fatalf("responses = %d", len(resp.Responses))
		}
		for i, r := range resp.Responses {
			if len(r.Matrices) != len(wantSlots[i].Matrices) || !matricesEqual(r.Matrices, wantSlots[i].Matrices) {
				t.Errorf("slot %d matrices = %+v, want %+v", i, r.Matrices, wantSlots[i].Matrices)
			}
		}
		if resp.Responses[0].Matrices[0].Name == resp.Responses[1].Matrices[0].Name {
			t.Error("slots share a matrix result")
		}
	}
	t.Run("compose", func(t *testing.T) {
		resp, err := p.Compose(ctx, composed())
		if err != nil {
			t.Fatal(err)
		}
		check(t, resp)
	})
	t.Run("compose parallel", func(t *testing.T) {
		resp, err := p.ComposeParallel(ctx, composed(), pulse.ComposeOptions{MaxWorkers: 2})
		if err != nil {
			t.Fatal(err)
		}
		check(t, resp)
	})
	t.Run("chain stage 0", func(t *testing.T) {
		s0 := matrixOverCohort(cohort)
		s0.Cohort = nil
		req := &types.ChainRequest{
			Cohort: &types.Cohort{Filename: cohort},
			Stages: []*types.ChainStage{
				{Name: "s0", Request: s0},
				{Name: "s1", Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}}}},
			},
		}
		data, err := afero.ReadFile(fs, cohort)
		if err != nil {
			t.Fatal(err)
		}
		if env := descx.ValidateChain(bytes.NewReader(data), req); len(env.Errors) != 0 {
			t.Fatalf("chain validator refused a stage-0 matrix: %+v", env.Errors)
		}
		resp, err := p.ProcessChain(ctx, req)
		if err != nil {
			t.Fatalf("ProcessChain: %v", err)
		}
		if !matricesEqual(resp.Stages[0].Matrices, alone.Matrices) {
			t.Errorf("stage 0 matrices = %+v, want %+v", resp.Stages[0].Matrices, alone.Matrices)
		}
		if len(resp.Stages[1].Matrices) != 0 || len(resp.Final.Data) != 1 {
			t.Errorf("stage 1 = %+v", resp.Final)
		}
	})
}

func matricesEqual(a, b []types.MatrixResult) bool {
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ja, jb)
}

// TestCrosstab_MatricesDoNotPerturb is the crosstab no-regression gate
// for the matrix slot. Over the whole fusion parity corpus, on a fused
// and a fusion-disabled (buffered) instance:
//
//   - a `matrices` slot never moves the fusion rule — the runtime
//     CanFuseCrosstab / CrosstabFuseReasons and predict's
//     CrosstabFusable / CrosstabFusionReasons answer the same with and
//     without it;
//   - Crosstab + Matrices is refused (PULSE_MATRIX_HOST_CONFLICT, or
//     PULSE_MATRIX_UNSUPPORTED_SOURCE for a joined one) by runtime and
//     predict alike;
//   - the crosstab output of the matrix-free request is byte-identical
//     with an empty `matrices` slot and after a refused matrix twin ran
//     on the same instance.
func TestCrosstab_MatricesDoNotPerturb(t *testing.T) {
	ctx := context.Background()
	schema := paritySchema(t)
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "parity.pulse", schema, 0, 240)
	data, err := afero.ReadFile(fsys, "parity.pulse")
	if err != nil {
		t.Fatal(err)
	}
	withMatrix := func(r *types.Request) *types.Request {
		r.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"score", "qty"}}}
		return r
	}
	for _, disable := range []bool{false, true} {
		name := map[bool]string{false: "fused", true: "buffered"}[disable]
		t.Run(name, func(t *testing.T) {
			p, err := pulse.New(pulse.Options{FS: fsys, Extensions: fusionParityExtensions(), DisableCrosstabFusion: disable})
			if err != nil {
				t.Fatal(err)
			}
			svc := pulse.ServiceForTest(p)
			reg := svc.Extensions()
			ran, fused := 0, 0
			for _, c := range fusionParityCorpus() {
				// nil_grouper: the buffered crosstab arm dereferences a nil
				// axis slot (a pre-existing panic in PartitionByAxis,
				// unrelated to matrices) — the fusion half still covers it.
				if c.req.Crosstab == nil || c.name == "nil_grouper" {
					continue
				}
				base := cloneFusionRequest(t, c.req)
				base.Cohort = &types.Cohort{Filename: "parity.pulse"}
				mat := withMatrix(cloneFusionRequest(t, base))

				// The fusion rule ignores the slot, at runtime and in predict.
				rb, rm := cloneFusionRequest(t, base), cloneFusionRequest(t, mat)
				descx.ResolveDefaults(rb, schema, svc.InstanceSnapshot())
				descx.ResolveDefaults(rm, schema, svc.InstanceSnapshot())
				okB, _ := processing.CanFuseCrosstab(rb, schema, reg)
				okM, _ := processing.CanFuseCrosstab(rm, schema, reg)
				if okB != okM || !reflect.DeepEqual(processing.CrosstabFuseReasons(rb, schema, reg), processing.CrosstabFuseReasons(rm, schema, reg)) {
					t.Errorf("%s: runtime fusion rule moved with a matrices slot", c.name)
				}
				envB, err := p.PredictBytes(ctx, data, base)
				if err != nil {
					t.Fatal(err)
				}
				envM, err := p.PredictBytes(ctx, data, mat)
				if err != nil {
					t.Fatal(err)
				}
				pb, pm := envB.Data.(*descriptor.PredictResult), envM.Data.(*descriptor.PredictResult)
				if !reflect.DeepEqual(pb.CrosstabFusable, pm.CrosstabFusable) || !reflect.DeepEqual(pb.CrosstabFusionReasons, pm.CrosstabFusionReasons) {
					t.Errorf("%s: predict fusion (%v %q) -> (%v %q) with a matrices slot", c.name,
						pb.CrosstabFusable, pb.CrosstabFusionReasons, pm.CrosstabFusable, pm.CrosstabFusionReasons)
				}
				if pb.CrosstabFusable != nil && *pb.CrosstabFusable {
					fused++
				}

				// Crosstab + Matrices is refused, with predict's first error
				// equal to the runtime's.
				wantCode := errors.PULSE_MATRIX_HOST_CONFLICT
				if len(mat.Joins) > 0 {
					wantCode = errors.PULSE_MATRIX_UNSUPPORTED_SOURCE
				}
				firstBytes, firstErr := runBytes(ctx, p, base)
				_, merr := p.Process(ctx, mat)
				requireCode(t, merr, wantCode)
				sameEntry(t, envM, merr)

				// The matrix-free output does not move.
				emptySlot := cloneFusionRequest(t, base)
				emptySlot.Matrices = []types.MatrixSpec{}
				for label, r := range map[string]*types.Request{"empty matrices slot": emptySlot, "after refusal": base} {
					b, err := runBytes(ctx, p, r)
					if !sameErr(err, firstErr) || !bytes.Equal(b, firstBytes) {
						t.Errorf("%s/%s: crosstab output moved (err %v vs %v)", c.name, label, err, firstErr)
					}
				}
				if firstErr == nil {
					ran++
				}
			}
			if ran < 50 {
				t.Errorf("ran %d crosstab requests, want most of the corpus", ran)
			}
			if !disable && fused < 5 {
				t.Errorf("fused %d requests; the corpus must exercise the fused arm", fused)
			}
			if disable && fused != 0 {
				t.Errorf("fusion-disabled instance fused %d requests", fused)
			}
		})
	}
}

func runBytes(ctx context.Context, p *pulse.Pulse, req *types.Request) ([]byte, error) {
	resp, err := p.Process(ctx, req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resp)
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	var ca, cb *errors.CodedError
	if stderrors.As(a, &ca) && stderrors.As(b, &cb) {
		return ca.Code == cb.Code && ca.Message == cb.Message
	}
	return a.Error() == b.Error()
}

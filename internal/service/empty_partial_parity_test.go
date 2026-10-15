package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// A run that folds ZERO records answers the same document on every
// arm. The parallel reducers' merged partial is empty when no worker
// published one (a zero-record segment never publishes), and
// finalizeMergedPartial's empty arm used to emit only the counters —
// no Data row and no Components.Aggregations — while the serial path
// emitted one zero-n entry per aggregator slot (and, grouped, the
// grouped tail's zero-row answer with its groupers block).

// emptyPartialRequests is one ungrouped and two grouped requests over
// componentsParitySchema.
func emptyPartialRequests(path string) map[string]*types.Request {
	grouped := func(g *types.Group) *types.Request {
		req := componentsParityRequest(path)
		req.Groups = []*types.Group{g}
		return req
	}
	withMatrix := func(req *types.Request) *types.Request {
		req.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"id", "score"}}}
		return req
	}
	return map[string]*types.Request{
		"flat":            componentsParityRequest(path),
		"flat_matrix":     withMatrix(componentsParityRequest(path)),
		"group_matrix":    withMatrix(grouped(&types.Group{Type: types.GROUP_RANGE, Field: "score", Interval: 10})),
		"group_range":     grouped(&types.Group{Type: types.GROUP_RANGE, Field: "score", Interval: 10}),
		"group_set_value": grouped(&types.Group{Type: types.GROUP_SET_VALUE, Field: "picks"}),
	}
}

// emptyPartialDoc is the compared document: Data, Matrices and
// Components.
func emptyPartialDoc(t *testing.T, resp *types.Response) string {
	t.Helper()
	b, err := types.MarshalFinite(struct {
		Data       any
		Matrices   any
		Components any
	}{resp.Data, resp.Matrices, resp.Components})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertEmptyServingUseful fails when the serial answer is too thin to
// prove anything: every aggregator slot must carry its zero-n entry.
func assertEmptyServingUseful(t *testing.T, req *types.Request, resp *types.Response) {
	t.Helper()
	if resp.Components == nil || resp.Components.Run == nil {
		t.Fatal("serial run emitted no Components.Run; parity against it proves nothing")
	}
	if got := len(resp.Components.Aggregations); got != len(req.Aggregations) {
		t.Fatalf("serial run emitted %d aggregation components, want %d (one zero-n entry per slot)", got, len(req.Aggregations))
	}
	if len(req.Groups) == 0 && len(resp.Data) != 1 {
		t.Fatalf("serial ungrouped run emitted %d Data rows, want the one zero-n row", len(resp.Data))
	}
	if len(req.Groups) == 0 && len(req.Matrices) > 0 && len(resp.Matrices) == 0 {
		t.Fatal("serial ungrouped matrix run emitted no Matrices; parity against it proves nothing")
	}
	if len(req.Groups) > 0 && len(resp.Components.Groupers) != 1 {
		t.Fatalf("serial grouped run emitted no groupers block: %+v", resp.Components)
	}
}

func TestDecodeWorkers_EmptyCohortMatchesSerial(t *testing.T) {
	schema := componentsParitySchema()
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/empty.pulse"
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, nil, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, req := range emptyPartialRequests(path) {
		t.Run(name, func(t *testing.T) {
			if !processing.CanMergeRequest(req, schema) {
				t.Fatal("request is not mergeable; the parallel decode reducer would never engage")
			}
			serialSvc := New(cfg)
			serialSvc.SetDecodeWorkers(1)
			serial, err := serialSvc.Process(ctx, req)
			if err != nil {
				t.Fatalf("serial Process: %v", err)
			}
			assertEmptyServingUseful(t, req, serial)
			want := emptyPartialDoc(t, serial)

			// buildParallelDecodeContext declines a cohort holding less
			// than one record, so Process never hands the zero-record
			// cohort to the reducer: drive it with a zero-record decode
			// context, every worker's segment empty — no worker
			// publishes, so the merge is the empty partial.
			for _, workers := range []int{1, 2, 4} {
				t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
					svc := New(cfg)
					svc.SetDecodeWorkers(workers)
					pctx := &parallelDecodeContext{
						schema:         schema,
						stride:         schema.RecordByteSize(),
						projectMapHint: len(schema.Fields),
						binding:        recordBindingFor(schema, nil, nil),
					}
					resp, err := svc.reduceParallelBuffered(ctx, req, schema, pctx, workers)
					if err != nil {
						t.Fatalf("reduceParallelBuffered: %v", err)
					}
					if got := emptyPartialDoc(t, resp); got != want {
						t.Errorf("DecodeWorkers=%d answers an empty cohort differently from serial\n got  %s\n want %s", workers, got, want)
					}
				})
			}
		})
	}
}

func TestShardWorkers_ZeroRecordShardsMatchSerial(t *testing.T) {
	schema := componentsParitySchema()
	archives := map[string][]int{
		"all_empty":  {0, 0, 0},
		"some_empty": {0, 7, 0},
	}
	ctx := context.Background()
	for arch, rows := range archives {
		cfg := fs.NewMemMap()
		path := arch + ".pulse"
		if err := afero.WriteFile(cfg.Fs(), path, buildNullableShardArchive(t, schema, rows), 0o644); err != nil {
			t.Fatal(err)
		}
		for name, req := range emptyPartialRequests(path) {
			t.Run(arch+"/"+name, func(t *testing.T) {
				serialSvc := New(cfg)
				serialSvc.SetShardWorkers(1)
				serial, err := serialSvc.Process(ctx, req)
				if err != nil {
					t.Fatalf("serial Process: %v", err)
				}
				assertEmptyServingUseful(t, req, serial)
				want := emptyPartialDoc(t, serial)
				for _, workers := range []int{2, 3} {
					svc := New(cfg)
					svc.SetShardWorkers(workers)
					cohort, err := svc.Open(ctx, path)
					if err != nil {
						t.Fatal(err)
					}
					if _, ok := svc.shouldFanOut(req, cohort); !ok {
						t.Fatalf("shouldFanOut refused %d workers; this would compare serial against serial", workers)
					}
					resp, err := svc.Process(ctx, req)
					if err != nil {
						t.Fatalf("Process(ShardWorkers=%d): %v", workers, err)
					}
					if got := emptyPartialDoc(t, resp); got != want {
						t.Errorf("ShardWorkers=%d answers differently from serial\n got  %s\n want %s", workers, got, want)
					}
				}
			})
		}
	}
}

// The empty arm itself, every plan: a merged partial no worker
// published (mergeShardPartials' all-nil result) finalises to the same
// Data + Components the serial path emits for zero records, grouped
// and ungrouped, components on and off.
func TestFinalizeMergedPartial_EmptyPartialMatchesSerial(t *testing.T) {
	schema := componentsParitySchema()
	cfg := fs.NewMemMap()
	path := "empty.pulse"
	if err := afero.WriteFile(cfg.Fs(), path, writeNullablePulse(t, schema, nil, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, req := range emptyPartialRequests(path) {
		for _, disable := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/disable_%v", name, disable), func(t *testing.T) {
				req.DisableComponents = &disable
				defer func() { req.DisableComponents = nil }()
				svc := New(cfg)
				serial, err := svc.Process(ctx, req)
				if err != nil {
					t.Fatalf("serial Process: %v", err)
				}
				if !disable {
					assertEmptyServingUseful(t, req, serial)
				}
				merged, err := mergeShardPartials(req, schema, []*shardPartial{nil, nil})
				if err != nil {
					t.Fatal(err)
				}
				stamped := processing.StampWeightsWith(req, svc.defaultWeight, svc.extensions)
				resp, err := finalizeMergedPartial(stamped, schema, merged, 0, svc.computePlanFor(ctx, req), svc.extensions, nil)
				if err != nil {
					t.Fatalf("finalizeMergedPartial: %v", err)
				}
				if got, want := emptyPartialDoc(t, resp), emptyPartialDoc(t, serial); got != want {
					t.Errorf("empty merged partial answers differently from serial\n got  %s\n want %s", got, want)
				}
			})
		}
	}
}

package service

import (
	"context"
	"io"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// totals_prepass.go is phase 1 of the parallel reducers' two-phase
// total() (U51 E3-S1, #399; processing/formula_total_merge.go): a
// parallel pre-pass over the same partitions the reduce scans, each
// worker folding its filter-passing rows into one
// processing.TotalsPrepass, the workers' states merged (binned sums:
// order-free, so the merged totals are the serial pre-pass's bits under
// any worker count or shard split) and published for phase 2's bind.
// The pre-pass decode is projected to the pre-pass request
// (processing.TotalsPrepassRequest: the filters and the total()
// argument fields).

// prepassProjectionHook, when set (tests only — never in production),
// receives each phase-1 pre-pass's decode shape as the arm installs
// it: the retained-field filter (nil: full decode) and its count.
var prepassProjectionHook func(keep encx.FieldFilter, projected int)

// newPrepassWorker builds one pre-pass worker's operators: its
// accumulator, its filter chain and scratch filter counters (the
// reduce counts the filters; the pre-pass only asks pass / fail).
func (s *Service) newPrepassWorker(ctx context.Context, req *types.Request, schema *encoding.Schema) (*prepassWorker, error) {
	vecs := processing.ExprVectors(req, schema, catalogOf(model.SetFrom(ctx)))
	acc, err := processing.NewTotalsPrepass(req.Attributes, schema, s.extensions, partitionModels(ctx), vecs)
	if err != nil {
		return nil, err
	}
	filterFns, err := processing.BuildFiltersWithVectors(req.Filterers, schema, s.extensions, model.SetFrom(ctx), vecs)
	if err != nil {
		return nil, err
	}
	return &prepassWorker{req: req, acc: acc, filterFns: filterFns, counters: processing.NewFilterPassCounters(req.Filterers)}, nil
}

// prepassWorker is one pre-pass worker's state.
type prepassWorker struct {
	req       *types.Request
	acc       *processing.TotalsPrepass
	filterFns []processing.FilterFunc
	counters  []processing.FilterPassCounters
}

// fold folds one decoded record: the filter chain, then the pre-pass.
func (w *prepassWorker) fold(rec *processing.Record) error {
	pass, err := processing.ApplyFilterPass(rec, w.req.Filterers, w.filterFns, w.counters)
	if err != nil || !pass {
		return err
	}
	return w.acc.Fold(rec)
}

// publishPrepass merges the workers' accumulators (any order: the
// binned sums merge exactly) and publishes the totals.
func publishPrepass(workers []*prepassWorker) (*processing.PublishedTotals, error) {
	var merged *processing.TotalsPrepass
	for _, w := range workers {
		if w == nil {
			continue
		}
		if merged == nil {
			merged = w.acc
			continue
		}
		if err := merged.Merge(w.acc); err != nil {
			return nil, err
		}
	}
	if merged == nil {
		return nil, nil
	}
	return merged.Publish()
}

// shardTotalsPrepass is the shard-parallel arm's phase 1: workers
// goroutines take the archive's shards in turn and fold every one into
// their own accumulator.
func (s *Service) shardTotalsPrepass(ctx context.Context, req *types.Request, schema *encoding.Schema, arch *archiveFile, shards []ShardEntry, workers int) (*processing.PublishedTotals, error) {
	dec := s.shardDecodeFor(ctx, processing.TotalsPrepassRequest(req, s.extensions), schema)
	if prepassProjectionHook != nil {
		prepassProjectionHook(dec.keep, dec.projected)
	}
	jobs := make(chan int, len(shards))
	for i := range shards {
		jobs <- i
	}
	close(jobs)
	states := make([]*prepassWorker, workers)
	g, gctx := newWorkerGroup(ctx)
	for w := 0; w < workers; w++ {
		g.Go(func() (err error) {
			defer recoverInto(panicOpShardParallel, s.disablePanicRecovery, &err)
			pw, err := s.newPrepassWorker(gctx, req, schema)
			if err != nil {
				return err
			}
			for idx := range jobs {
				if err := s.prepassShard(gctx, schema, dec, arch, shards[idx].Filename, pw); err != nil {
					return err
				}
			}
			states[w] = pw
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return publishPrepass(states)
}

// prepassShard folds one shard's rows into pw, decoded in the pre-pass
// shape; a grouped archive's rows get their parent-group entries, as on
// the reduce (processOneShard), so a filter over a group's members
// reads them.
func (s *Service) prepassShard(ctx context.Context, schema *encoding.Schema, dec *shardDecode, arch *archiveFile, name string, pw *prepassWorker) error {
	rows, err := dec.openRows(arch, name, schema)
	if err != nil {
		return err
	}
	var groupIdx []uint32
	if schema.HasGroups() {
		groupIdx = make([]uint32, len(schema.Groups))
	}
	poll := processing.NewCtxPoller(ctx)
	for {
		if err := poll.Poll(); err != nil {
			return err
		}
		rec, err := rows.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if groupIdx != nil && readGroupIndices(rows.rr, groupIdx) {
			rec.SetGroupIndices(schema, groupIdx)
		}
		if err := pw.fold(rec); err != nil {
			return err
		}
	}
}

// decodeTotalsPrepass is the parallel-decode arm's phase 1: the same
// segments the reduce decodes, each worker folding its segment, under
// the pre-pass projection.
func (s *Service) decodeTotalsPrepass(ctx context.Context, req *types.Request, schema *encoding.Schema, pctx *parallelDecodeContext, workers int) (*processing.PublishedTotals, error) {
	keep, plan, projected := s.decodeProjection(ctx, processing.TotalsPrepassRequest(req, s.extensions), schema)
	if prepassProjectionHook != nil {
		prepassProjectionHook(keep, projected)
	}
	pre := *pctx
	pre.keep, pre.plan = keep, plan
	pre.projectMapHint = len(schema.Fields)
	if keep != nil {
		pre.projectMapHint = projected
	}
	pre.binding = recordBindingFor(schema, plan, keep, attributeLabels(req)...)
	states := make([]*prepassWorker, workers)
	factory := func(workerIdx, _, _ int) DecodeCallback {
		pw, buildErr := s.newPrepassWorker(ctx, req, schema)
		states[workerIdx] = pw
		return func(rec *processing.Record) error {
			if buildErr != nil {
				return buildErr
			}
			return pw.fold(rec)
		}
	}
	if err := parallelDecodeMmap(ctx, &pre, workers, factory); err != nil {
		return nil, err
	}
	return publishPrepass(states)
}

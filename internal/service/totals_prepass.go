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
//
// A request whose total() arguments read an earlier total (U74 E3-S2,
// #494) runs one such parallel pre-pass per layer of the serial
// two-pass plan, in order (prepassLayers): layer L's workers bind every
// earlier layer's published values before their first row, fold the
// layer-L formulas only, and the publication carries the earlier
// layers' values on, so the last one is everything phase 2 binds.

// prepassProjectionHook, when set (tests only — never in production),
// receives each phase-1 pre-pass's decode shape as the arm installs
// it: the retained-field filter (nil: full decode) and its count.
var prepassProjectionHook func(keep encx.FieldFilter, projected int)

// newPrepassWorker builds one pre-pass worker's operators: its
// accumulator, its filter chain and scratch filter counters (the
// reduce counts the filters; the pre-pass only asks pass / fail).
func (s *Service) newPrepassWorker(ctx context.Context, req *types.Request, schema *encoding.Schema, layer int, prior *processing.PublishedTotals) (*prepassWorker, error) {
	vecs := processing.ExprVectors(req, schema, s.extensions.Classifier(catalogOf(model.SetFrom(ctx))))
	acc, err := processing.NewTotalsPrepass(req.Attributes, schema, s.extensions, partitionModels(ctx), vecs, layer, prior)
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

// prepassLayers runs phase 1 layer by layer (processing.TotalsPrepassLayers):
// run folds one layer over every partition given the earlier layers'
// publication. A layer no worker folded (no partition reached a worker)
// publishes nothing, and there is then no row for a later layer or
// phase 2 to score: the loop stops with what it has.
func (s *Service) prepassLayers(req *types.Request, run func(layer int, prior *processing.PublishedTotals) (*processing.PublishedTotals, error)) (*processing.PublishedTotals, error) {
	var pub *processing.PublishedTotals
	for layer, n := 0, processing.TotalsPrepassLayers(req.Attributes, s.extensions); layer < n; layer++ {
		next, err := run(layer, pub)
		if err != nil {
			return nil, err
		}
		if next == nil {
			break
		}
		pub = next
	}
	return pub, nil
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

// shardTotalsPrepass is the shard-parallel arm's phase 1: per pre-pass
// layer, workers goroutines take the archive's shards in turn and fold
// every one into their own accumulator.
func (s *Service) shardTotalsPrepass(ctx context.Context, req *types.Request, schema *encoding.Schema, arch *archiveFile, shards []ShardEntry, workers int) (*processing.PublishedTotals, error) {
	return s.prepassLayers(req, func(layer int, prior *processing.PublishedTotals) (*processing.PublishedTotals, error) {
		return s.shardTotalsPrepassLayer(ctx, req, schema, arch, shards, workers, layer, prior)
	})
}

// shardTotalsPrepassLayer is one layer of shardTotalsPrepass.
func (s *Service) shardTotalsPrepassLayer(ctx context.Context, req *types.Request, schema *encoding.Schema, arch *archiveFile, shards []ShardEntry, workers, layer int, prior *processing.PublishedTotals) (*processing.PublishedTotals, error) {
	dec := s.shardDecodeFor(ctx, processing.TotalsPrepassRequest(req, s.extensions, layer), schema)
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
			pw, err := s.newPrepassWorker(gctx, req, schema, layer, prior)
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

// decodeTotalsPrepass is the parallel-decode arm's phase 1: per
// pre-pass layer, the same segments the reduce decodes, each worker
// folding its segment, under that layer's pre-pass projection.
func (s *Service) decodeTotalsPrepass(ctx context.Context, req *types.Request, schema *encoding.Schema, pctx *parallelDecodeContext, workers int) (*processing.PublishedTotals, error) {
	return s.prepassLayers(req, func(layer int, prior *processing.PublishedTotals) (*processing.PublishedTotals, error) {
		return s.decodeTotalsPrepassLayer(ctx, req, schema, pctx, workers, layer, prior)
	})
}

// decodeTotalsPrepassLayer is one layer of decodeTotalsPrepass.
func (s *Service) decodeTotalsPrepassLayer(ctx context.Context, req *types.Request, schema *encoding.Schema, pctx *parallelDecodeContext, workers, layer int, prior *processing.PublishedTotals) (*processing.PublishedTotals, error) {
	keep, plan, projected := s.decodeProjection(ctx, processing.TotalsPrepassRequest(req, s.extensions, layer), schema)
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
		pw, buildErr := s.newPrepassWorker(ctx, req, schema, layer, prior)
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

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"sort"
	"sync"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// shard_reduce.go implements per-shard parallel execution for the
// subset of requests that processing.CanMergeRequest accepts: every
// aggregator and grouper is mergeable, no windows/features/tests/
// regressions, attributes are at most row-local, and the cohort backs
// onto a shard archive with N >= 2 shards.
//
// The orchestrator opens the archive once, fans out per-shard
// processing across a bounded worker pool, then merges partial states
// in shard insertion order (zip central-directory order). Order
// preservation matters for two reasons: associative+commutative
// aggregators (count, sum, min, max, null_count, frequency,
// distinct_count, mode) produce byte-equal results vs the serial path
// when partials are folded in any order, but the Welford-mean parallel
// merge introduces ULP drift that is sensitive to merge order;
// shard insertion order is the documented, reproducible choice.
//
// Non-mergeable requests fall through to the serial shardIter path
// (process.go's newScanIter dispatch); workers never spawn. This
// preserves byte-for-byte semantics for percentile aggregators,
// window operators, tier-1/tier-2 tests, two-pass attributes combined
// with groupers, and every other gate processing.CanStreamRequest
// already routes through buffered execution.

// shouldFanOut reports whether the current Process call qualifies for
// per-shard parallelization. The decision is gated by:
//
//   - the cohort is archive-backed (cohort.Shards() non-empty)
//   - more than one shard (no point parallelising N=1)
//   - the request is mergeable per processing.CanMergeRequest
//   - the configured worker cap is not 1 (1 forces serial)
//
// Returns the resolved worker count (>= 2) and true when fan-out
// applies, or (0, false) when the caller should fall through to the
// serial path.
func (s *Service) shouldFanOut(req *types.Request, cohort *Cohort) (int, bool) {
	if s.shardWorkers == 1 {
		return 0, false
	}
	shards := cohort.Shards()
	if len(shards) < 2 {
		return 0, false
	}
	if !processing.CanMergeRequest(req, cohort.Schema()) {
		return 0, false
	}
	workers := s.shardWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(shards) {
		workers = len(shards)
	}
	if workers < 2 {
		return 0, false
	}
	return workers, true
}

// processShardArchiveParallel runs the per-shard parallel reducer.
// Caller guarantees shouldFanOut returned true. The archive is read
// once via afero, then each worker carves a SectionReader for its
// assigned shards and folds rows through fresh per-worker operator
// instances. Partials accumulate in a slice indexed by shard insertion
// order; a final pass merges them in that order so Welford drift is
// deterministic across runs.
func (s *Service) processShardArchiveParallel(ctx context.Context, req *types.Request, cohort *Cohort, path string, workers int) (*types.Response, error) {
	shards := cohort.Shards()
	schema := cohort.Schema()

	// Read the archive once. Each worker re-wraps the byte buffer in a
	// bytes.Reader (which is an io.ReaderAt under the hood) so worker
	// reads never contend on a shared file handle.
	data, err := afero.ReadFile(s.fs.Fs(), path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening shard archive for parallel reduce: %s", path))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	partials := make([]*shardPartial, len(shards))
	jobs := make(chan int, len(shards))
	var (
		wg      sync.WaitGroup
		errOnce sync.Once
		firstErr error
	)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if cctx.Err() != nil {
					return
				}
				p, err := s.processOneShard(cctx, req, schema, arch, shards[idx].Filename)
				if err != nil {
					errOnce.Do(func() { firstErr = err; cancel() })
					return
				}
				partials[idx] = p
			}
		}()
	}

	for i := range shards {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	// Merge partials in shard insertion order. The order matters for
	// Welford-mean ULP determinism; for purely associative+commutative
	// aggregators the answer is identical regardless of merge order.
	merged, err := mergeShardPartials(req, schema, partials)
	if err != nil {
		return nil, err
	}
	resp, err := finalizeMergedPartial(req, schema, merged)
	if err != nil {
		return nil, err
	}
	if resp.Metadata != nil {
		resp.Metadata.CohortFile = path
	}
	return resp, nil
}

// shardPartial holds a single shard's per-aggregator (and per-group
// per-aggregator) state after streaming that shard's filter-passing
// records through fresh OnlineAggregator instances. The orchestrator
// merges these in shard insertion order.
//
// Two shapes:
//
//   - ungrouped: aggs is a slice indexed by req.Aggregations position;
//     groups is nil.
//   - grouped: aggs is nil; groups holds one bucket per distinct key
//     observed in this shard, plus keyOrder so the merger can rebuild
//     insertion order when constructing the final response row set.
type shardPartial struct {
	totalRows    int64
	filteredRows int64

	aggs   []processing.OnlineAggregator
	groups map[string][]processing.OnlineAggregator
	// keyOrder preserves the order distinct group keys were first seen
	// in this shard. The merger's stable sort by key ensures the
	// final response row order is deterministic across worker
	// scheduling perturbations.
	keyOrder []string
}

// processOneShard streams one shard's records through fresh per-shard
// OnlineAggregator instances (and per-group buckets when req.Groups is
// non-empty). Returns a partial state ready for merging.
func (s *Service) processOneShard(ctx context.Context, req *types.Request, schema *encoding.Schema, arch *encoding.Archive, shardName string) (*shardPartial, error) {
	sect, err := arch.OpenAt(shardName)
	if err != nil {
		return nil, err
	}
	r := &sect
	if err := encoding.ReadHeader(r); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q header", shardName))
	}
	if _, err := encoding.ReadSchema(r); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard %q schema", shardName))
	}
	rr := encoding.NewRecordReader(r, schema)

	// Pre-build per-shard operators. Each shard worker gets its own
	// instance set so there's no shared mutable state across workers.
	filterFns, err := processing.BuildFilters(req.Filterers, schema, s.extensions)
	if err != nil {
		return nil, err
	}
	rowLocalAttrs, err := buildRowLocalAttrSpecs(req.Attributes, schema, s.extensions)
	if err != nil {
		return nil, err
	}

	specs := make([]aggSpec, len(req.Aggregations))
	for i, agg := range req.Aggregations {
		factory, ok := s.extensions.LookupAggregator(agg.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown aggregation type: %s", agg.Type))
		}
		specs[i] = aggSpec{agg: agg, factory: factory}
	}

	var grouperSpec *types.Group
	var streamGrp processing.StreamingGrouper
	if len(req.Groups) > 0 {
		grouperSpec = req.Groups[0]
		grouperFactory, ok := s.extensions.LookupGrouper(grouperSpec.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown group type: %s", grouperSpec.Type))
		}
		grp, err := grouperFactory(grouperSpec, schema)
		if err != nil {
			return nil, err
		}
		sg, ok := grp.(processing.StreamingGrouper)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("grouper %s does not implement StreamingGrouper", grouperSpec.Type))
		}
		streamGrp = sg
	}

	out := &shardPartial{}
	var aggsUngrouped []processing.OnlineAggregator
	if streamGrp == nil {
		aggsUngrouped = make([]processing.OnlineAggregator, len(specs))
		for i, sp := range specs {
			inst, err := sp.factory(sp.agg, schema)
			if err != nil {
				return nil, err
			}
			online, ok := inst.(processing.OnlineAggregator)
			if !ok {
				return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("aggregator %s does not implement OnlineAggregator", sp.agg.Type))
			}
			aggsUngrouped[i] = online
		}
		out.aggs = aggsUngrouped
	} else {
		out.groups = make(map[string][]processing.OnlineAggregator)
	}

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		values := make(map[string]float64, len(schema.Fields))
		nulls := make(map[string]bool)
		wide := make(map[string]any)
		err := rr.ReadRecordWithWide(values, nulls, wide)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		rec := processing.NewRecordWithWide(schema, values, nulls, wide)
		out.totalRows++

		pass := true
		for _, fn := range filterFns {
			ok, err := fn(rec)
			if err != nil {
				return nil, err
			}
			if !ok {
				pass = false
				break
			}
		}
		if !pass {
			continue
		}
		out.filteredRows++

		for _, ra := range rowLocalAttrs {
			val, err := ra.computer.Row(rec, ra.attr.Field)
			if err != nil {
				return nil, err
			}
			rec.Set(ra.label, val)
		}

		if streamGrp == nil {
			for i, oa := range aggsUngrouped {
				if err := oa.UpdateRow(rec, specs[i].agg.Field); err != nil {
					return nil, err
				}
			}
			continue
		}

		key, ok, err := streamGrp.KeyForRow(rec, grouperSpec.Field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		bucket, exists := out.groups[key]
		if !exists {
			bucket = make([]processing.OnlineAggregator, len(specs))
			for i, sp := range specs {
				inst, err := sp.factory(sp.agg, schema)
				if err != nil {
					return nil, err
				}
				online, ok := inst.(processing.OnlineAggregator)
				if !ok {
					return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("aggregator %s does not implement OnlineAggregator", sp.agg.Type))
				}
				bucket[i] = online
			}
			out.groups[key] = bucket
			out.keyOrder = append(out.keyOrder, key)
		}
		for i, oa := range bucket {
			if err := oa.UpdateRow(rec, specs[i].agg.Field); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// aggSpec carries the resolved factory for one aggregator slot.
// Shared between the per-shard processor and the final-state
// reconstruction step.
type aggSpec struct {
	agg     *types.Aggregation
	factory processing.AggregatorFactory
}

// rowLocalAttrSpec is a parallel structure to processor.rowLocalAttrEntry
// used by the per-shard worker. We can't reuse the processor type
// directly (unexported) so we mirror the shape.
type rowLocalAttrSpec struct {
	attr     *types.Attribute
	computer processing.RowLocalAttribute
	label    string
}

func buildRowLocalAttrSpecs(attrs []*types.Attribute, schema *encoding.Schema, exts *processing.ExtensionRegistry) ([]rowLocalAttrSpec, error) {
	if len(attrs) == 0 {
		return nil, nil
	}
	out := make([]rowLocalAttrSpec, 0, len(attrs))
	for _, attr := range attrs {
		factory, ok := exts.LookupAttribute(attr.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, err := factory(attr, schema)
		if err != nil {
			return nil, err
		}
		if aware, ok := computer.(processing.ExtensionAware); ok {
			aware.SetExtensions(exts)
		}
		rl, ok := computer.(processing.RowLocalAttribute)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("attribute %s is not row-local; not eligible for per-shard parallel reduce", attr.Type))
		}
		label := attr.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", attr.Type, attr.Field)
		}
		out = append(out, rowLocalAttrSpec{attr: attr, computer: rl, label: label})
	}
	return out, nil
}

// mergeShardPartials folds the per-shard partial states into a single
// aggregated state. Walks partials in shard insertion order — the
// caller (processShardArchiveParallel) pre-sorts the slice so order is
// the central-directory enumeration, NOT the worker completion order.
//
// For ungrouped requests we merge each aggregator slot's running state
// across all shards. For grouped requests we union per-key buckets:
// merging by key preserves the per-key associativity, then a stable
// sort produces deterministic row order.
func mergeShardPartials(req *types.Request, schema *encoding.Schema, partials []*shardPartial) (*shardPartial, error) {
	if len(partials) == 0 {
		return &shardPartial{}, nil
	}
	// Find the first non-nil partial to seed; with len(partials) >= 1
	// and shouldFanOut requiring N >= 2, every entry must be non-nil
	// post-worker-success. Defensive guard for the empty/skipped case.
	var head int
	for head < len(partials) && partials[head] == nil {
		head++
	}
	if head >= len(partials) {
		return &shardPartial{}, nil
	}
	merged := partials[head]
	for i := head + 1; i < len(partials); i++ {
		p := partials[i]
		if p == nil {
			continue
		}
		merged.totalRows += p.totalRows
		merged.filteredRows += p.filteredRows

		if merged.aggs != nil {
			if len(p.aggs) != len(merged.aggs) {
				return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
					"shard partial aggregator count mismatch during merge")
			}
			for slot, oa := range merged.aggs {
				m, ok := oa.(processing.MergeableAggregator)
				if !ok {
					return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("aggregator %s does not implement MergeableAggregator", req.Aggregations[slot].Type))
				}
				if err := m.MergeOnline(p.aggs[slot]); err != nil {
					return nil, err
				}
			}
		}

		if merged.groups != nil {
			for _, key := range p.keyOrder {
				bucket := p.groups[key]
				existing, exists := merged.groups[key]
				if !exists {
					merged.groups[key] = bucket
					merged.keyOrder = append(merged.keyOrder, key)
					continue
				}
				for slot, oa := range existing {
					m, ok := oa.(processing.MergeableAggregator)
					if !ok {
						return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
							fmt.Sprintf("aggregator %s does not implement MergeableAggregator", req.Aggregations[slot].Type))
					}
					if err := m.MergeOnline(bucket[slot]); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	_ = schema
	return merged, nil
}

// finalizeMergedPartial calls Finalize on each merged aggregator and
// emits the response row(s). Mirrors the tail of processStreaming /
// processStreamingGrouped in the processing package.
func finalizeMergedPartial(req *types.Request, schema *encoding.Schema, merged *shardPartial) (*types.Response, error) {
	_ = schema
	resp := &types.Response{
		Metadata: &types.ResponseMetadata{
			TotalRows:    merged.totalRows,
			FilteredRows: merged.filteredRows,
		},
	}

	if merged.aggs != nil {
		row := make(map[string]any, len(merged.aggs))
		for i, oa := range merged.aggs {
			val, err := oa.Finalize()
			if err != nil {
				return nil, err
			}
			label := req.Aggregations[i].Label
			if label == "" {
				label = fmt.Sprintf("%s_%s", req.Aggregations[i].Type, req.Aggregations[i].Field)
			}
			row[label] = val
		}
		if len(merged.aggs) > 0 {
			resp.Data = []map[string]any{row}
		}
		return resp, nil
	}

	if merged.groups != nil {
		grp := req.Groups[0]
		// Stable sort by key so output row order is deterministic across
		// runs regardless of map-iteration order or worker scheduling.
		keys := make([]string, len(merged.keyOrder))
		copy(keys, merged.keyOrder)
		sort.SliceStable(keys, func(i, j int) bool { return keys[i] < keys[j] })
		data := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			bucket := merged.groups[key]
			row := make(map[string]any, len(bucket)+1)
			for i, oa := range bucket {
				val, err := oa.Finalize()
				if err != nil {
					return nil, err
				}
				label := req.Aggregations[i].Label
				if label == "" {
					label = fmt.Sprintf("%s_%s", req.Aggregations[i].Type, req.Aggregations[i].Field)
				}
				row[label] = val
			}
			row[grp.Field] = key
			data = append(data, row)
		}
		resp.Data = data
	}
	return resp, nil
}

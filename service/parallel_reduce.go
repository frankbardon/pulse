package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// parallel_reduce.go composes the segment-aware parallel decode
// infrastructure with per-worker partial aggregator state so a buffered
// Process call whose request is mergeable (processing.CanMergeRequest)
// can emit its Response without ever materialising the
// []*processing.Record slice the parallel-decode path produces.
//
// Each worker owns a *shardPartial (the exact same shape shard_reduce.go
// already uses for archive-shard parallelism). When the per-record
// callback fires the worker updates its own partial — no shared mutable
// state across goroutines, no locks on the hot path. After
// parallelDecodeMmap's errgroup boundary completes, the orchestrator
// folds the partials in worker-index order via mergeShardPartials and
// finalises through finalizeMergedPartial — both reused verbatim from
// shard_reduce.go, so byte-equal vs serial holds for every
// associative+commutative aggregator and ULP-bounded equivalence holds
// for the Welford-Pébaÿ merge (the same guarantee shard_reduce already
// ships).

// reduceParallelBuffered is the per-segment partial-state + merge
// orchestrator. The caller guarantees:
//
//   - pctx was built by buildParallelDecodeContext (mmap engaged, stride
//     sane, totalRecords > 0)
//   - workers >= 2 (shouldFanOutDecode already accepted)
//   - processing.CanMergeRequest(req, schema) returns true
//
// On entry the function constructs per-worker partial-state slots via
// the same factory shape the parallel-decode path uses. The factory closure builds
// a fresh aggregator/grouper/filter/attribute instance set per worker
// (no shared mutable state) and returns a DecodeCallback that updates
// the worker's partial on each decoded Record. After the errgroup
// completes, mergeShardPartials folds partials in worker-index order
// (which equals cohort byte order because parallelDecodeMmap assigns
// contiguous record ranges per worker), then finalizeMergedPartial
// emits the Response.
//
// Worker-index iteration order matters for the Welford-Pébaÿ AGG_MEAN /
// AGG_STDDEV / AGG_VARIANCE merge: Chan-Welford is associative but not
// strictly commutative when partition sizes differ, so floating-point
// rounding can drift by 1 ULP if merge order varies between runs. We
// pin the order to worker index (== cohort byte order) so the same
// cohort always produces the same byte sequence — the same policy
// shard_reduce.go enforces by walking shards in central-directory
// order.
//
// On any worker error, parallelDecodeMmap's errgroup propagates the
// first failure and cancels remaining workers; reduceParallelBuffered
// returns the error untouched so the caller can wrap with cohort path
// context.
func (s *Service) reduceParallelBuffered(
	ctx context.Context,
	req *types.Request,
	schema *encoding.Schema,
	pctx *parallelDecodeContext,
	workers int,
) (*types.Response, error) {
	if req == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"reduceParallelBuffered: nil request")
	}
	if schema == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"reduceParallelBuffered: nil schema")
	}
	if pctx == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"reduceParallelBuffered: nil decode context")
	}
	if workers < 1 {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"reduceParallelBuffered: workers must be >= 1")
	}

	// Pre-resolve aggregator factories once; the per-worker factory
	// closure re-uses these so we do not pay an extensions.LookupAggregator
	// hop per worker. Same shape as processOneShard's spec table.
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
	var grouperFactory processing.GrouperFactory
	if len(req.Groups) > 0 {
		grouperSpec = req.Groups[0]
		f, ok := s.extensions.LookupGrouper(grouperSpec.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown group type: %s", grouperSpec.Type))
		}
		grouperFactory = f
	}

	// Per-worker partial slots. The hot loop writes only to its own
	// index; no synchronisation is required during decode. publishMu
	// gates only the final slice-header write per worker so the Go
	// memory model sees a well-defined publication; the errgroup.Wait
	// barrier inside parallelDecodeMmap supplies the happens-before
	// edge before the orchestrator reads the slots.
	partials := make([]*shardPartial, workers)
	var publishMu sync.Mutex
	publish := func(workerIdx int, p *shardPartial) {
		publishMu.Lock()
		partials[workerIdx] = p
		publishMu.Unlock()
	}

	factory := func(workerIdx, recordCount int) DecodeCallback {
		// Build a fresh, per-worker operator set. Returning an error
		// from the callback aborts the worker via the errgroup, but
		// factory itself runs inside the worker goroutine — so a
		// factory-time failure has to be deferred to the first
		// callback invocation. Capture the build error in a closure
		// var and surface on the first record.
		var (
			buildErr      error
			filterFns     []processing.FilterFunc
			rowLocalAttrs []rowLocalAttrSpec
			grouperInst   processing.StreamingGrouper
			ungroupedAggs []processing.OnlineAggregator
		)

		filterFns, buildErr = processing.BuildFilters(req.Filterers, schema, s.extensions)
		if buildErr == nil {
			rowLocalAttrs, buildErr = buildRowLocalAttrSpecs(req.Attributes, schema, s.extensions)
		}
		if buildErr == nil && grouperFactory != nil {
			grp, err := grouperFactory(grouperSpec, schema)
			if err != nil {
				buildErr = err
			} else {
				sg, ok := grp.(processing.StreamingGrouper)
				if !ok {
					buildErr = errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("grouper %s does not implement StreamingGrouper", grouperSpec.Type))
				} else {
					grouperInst = sg
				}
			}
		}

		out := &shardPartial{}
		if buildErr == nil && grouperInst == nil {
			ungroupedAggs = make([]processing.OnlineAggregator, len(specs))
			for i, sp := range specs {
				inst, err := sp.factory(sp.agg, schema)
				if err != nil {
					buildErr = err
					break
				}
				online, ok := inst.(processing.OnlineAggregator)
				if !ok {
					buildErr = errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("aggregator %s does not implement OnlineAggregator", sp.agg.Type))
					break
				}
				ungroupedAggs[i] = online
			}
			if buildErr == nil {
				out.aggs = ungroupedAggs
			}
		} else if buildErr == nil {
			out.groups = make(map[string][]processing.OnlineAggregator)
		}

		// Worker progress counter so we can publish the partial on the
		// final record without an extra barrier. Single-goroutine
		// access — no atomic needed.
		emitted := 0

		return func(rec *processing.Record) error {
			if buildErr != nil {
				return buildErr
			}
			out.totalRows++

			pass := true
			for _, fn := range filterFns {
				ok, err := fn(rec)
				if err != nil {
					return err
				}
				if !ok {
					pass = false
					break
				}
			}
			// Even when the record is filtered out we still need to
			// account for the publish-on-last-record check below — the
			// errgroup ends once all records have been visited, not
			// once filter-passing records are exhausted.
			emitted++

			if pass {
				out.filteredRows++

				for _, ra := range rowLocalAttrs {
					val, err := ra.computer.Row(rec, ra.attr.Field)
					if err != nil {
						return err
					}
					rec.Set(ra.label, val)
				}

				if grouperInst == nil {
					for i, oa := range ungroupedAggs {
						if err := oa.UpdateRow(rec, specs[i].agg.Field); err != nil {
							return err
						}
					}
				} else {
					key, ok, err := grouperInst.KeyForRow(rec, grouperSpec.Field)
					if err != nil {
						return err
					}
					if ok {
						bucket, exists := out.groups[key]
						if !exists {
							bucket = make([]processing.OnlineAggregator, len(specs))
							for i, sp := range specs {
								inst, err := sp.factory(sp.agg, schema)
								if err != nil {
									return err
								}
								online, okOA := inst.(processing.OnlineAggregator)
								if !okOA {
									return errors.NewCodedError(errors.PROCESSING_INTERNAL,
										fmt.Sprintf("aggregator %s does not implement OnlineAggregator", sp.agg.Type))
								}
								bucket[i] = online
							}
							out.groups[key] = bucket
							out.keyOrder = append(out.keyOrder, key)
						}
						for i, oa := range bucket {
							if err := oa.UpdateRow(rec, specs[i].agg.Field); err != nil {
								return err
							}
						}
					}
				}
			}

			// On the final record this worker is going to see, publish
			// the slot back to the orchestrator. The post-Wait merge
			// reads under no lock — Wait() supplies the happens-before
			// edge. Mirrors materializeRecordsParallel's publish shape.
			if emitted == recordCount {
				publish(workerIdx, out)
			}
			return nil
		}
	}

	if err := parallelDecodeMmap(ctx, pctx, workers, factory); err != nil {
		return nil, err
	}

	// Single-worker fast path: when workers == 1 the orchestrator
	// already produced a single partial; skip the merge fold.
	// shouldFanOutDecode rejects workers < 2 today so this branch is
	// defensive — keeps the reducer correct if a future caller wires it
	// up with workers == 1 (e.g. a focused test driver).
	//
	// Single-file parallel buffered runs are not shard-archive runs —
	// ShardCount stays 0 so the omitempty wire shape is byte-identical
	// against the single-cohort baseline.
	if workers == 1 {
		if partials[0] == nil {
			partials[0] = &shardPartial{}
		}
		resp, err := finalizeMergedPartial(req, schema, partials[0], 0, s.effectiveDisableComponents(req))
		if err != nil {
			return nil, err
		}
		return resp, nil
	}

	// Worker-index merge: shard_reduce.go's mergeShardPartials walks
	// the slice in the order it is given. Workers were assigned
	// segments in cohort byte order, so the worker-index iteration
	// here is equivalent to cohort byte order, which mirrors the
	// shard-reduce policy (central-directory order) — the same
	// Welford-merge ULP-stability guarantee carries over.
	merged, err := mergeShardPartials(req, schema, partials)
	if err != nil {
		return nil, err
	}
	resp, err := finalizeMergedPartial(req, schema, merged, 0, s.effectiveDisableComponents(req))
	if err != nil {
		return nil, err
	}
	return resp, nil
}

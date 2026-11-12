package service

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// chain_totals.go is the two-phase total() runtime of a ProcessChain
// stage >= 1 (U74 E3-S3, #494). Stage 0 runs Process, whose parallel
// reducers already run a total() formula two-phase; a later stage reads
// the previous stage's rows through a processing.ChainRowIterator, and
// a total()-bearing one runs the same two phases over them as ONE
// partition: phase 1 folds each pre-pass layer over the rows
// (Service.prepassLayers, the iterator rewound per layer) and publishes
// the totals, phase 2 is the partition reduce (reducePartitions) with
// every formula bound before its first row. The rows stay lazy — one
// record a row as the scan reaches it — where the serial processor
// buffers a grouped total() stage (a two-pass attribute beside groups
// routes buffered), one record a row for the whole stage. The answer is
// the serial processor's bits (the reducers' guarantee: binned totals,
// the same per-row formula, the same partial finalize).

// chainTotalsSource is the previous stage's rows as one partition.
func (s *Service) chainTotalsSource(ctx context.Context, schema *encoding.Schema, rows *processing.ChainRowIterator) partitionSource {
	return partitionSource{
		prepass: func(req *types.Request) (*processing.PublishedTotals, error) {
			return s.prepassLayers(req, func(layer int, prior *processing.PublishedTotals) (*processing.PublishedTotals, error) {
				pw, err := s.newPrepassWorker(ctx, req, schema, layer, prior)
				if err != nil {
					return nil, err
				}
				if err := scanChainRows(ctx, rows, pw.fold); err != nil {
					return nil, err
				}
				return publishPrepass([]*prepassWorker{pw})
			})
		},
		scan: func(factory DecodeCallbackFactory) error {
			return scanChainRows(ctx, rows, factory(0, 0, rows.Len()))
		},
	}
}

// scanChainRows rewinds rows and hands every row's record to fold.
func scanChainRows(ctx context.Context, rows *processing.ChainRowIterator, fold func(*processing.Record) error) error {
	rows.Reset()
	poll := processing.NewCtxPoller(ctx)
	for rows.Next() {
		if err := poll.Poll(); err != nil {
			return err
		}
		if err := fold(rows.Record()); err != nil {
			return err
		}
	}
	return nil
}

// chainStageTwoPhase reports whether chain stage req (>= 1) takes the
// two-phase runtime: it carries a total() formula and has rows to read
// (an empty input runs the serial processor, which has nothing to
// buffer). The chain gate (mergegate.ChainRefusal) has admitted it, so
// every total() formula is one mergegate.ParallelTotals admits.
func chainStageTwoPhase(req *types.Request, rows *processing.ChainRowIterator) bool {
	return req != nil && rows != nil && rows.Len() > 0 && processing.HasFormulaTotals(req.Attributes)
}

// runChainStageTotals is runChainStage's two-phase twin for a stage
// chainStageTwoPhase admits: the partition reduce over rows, then the
// stage's regression advisories and multiplicity fold as runChainStage
// applies them. The stage reports the streaming arm — it folds row by
// row and never materializes its input.
func (s *Service) runChainStageTotals(ctx context.Context, req *types.Request, plan *descx.MultiplicityPlan, schema *encoding.Schema, rows *processing.ChainRowIterator) (*types.Response, error) {
	resp, err := s.reducePartitions(ctx, req, schema, 1, s.chainTotalsSource(ctx, schema, rows))
	if err != nil {
		return nil, err
	}
	execInfoFrom(ctx).setPlan(observe.ArmStreaming, 1, 0, 0)
	attachRegressionAdvisories(req, resp, s.instance)
	if err := foldRequestMultiplicity(plan, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

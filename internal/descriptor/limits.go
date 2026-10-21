package descriptor

import (
	"cmp"
	"encoding/json"
	"math"
	"runtime"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// LimitInputs are the run facts the limit rules read beyond the request
// and schema. Predict fills them from the header (and the facade's
// header-only right-side counter); the process pre-flight from the
// same header counts (Service.CountRecords, an archive's shard
// manifest) — so a certain finding carries the same figure on both
// sides.
type LimitInputs struct {
	// Records is the header record count of the cohort the request
	// scans (a join's left side; a chain stage's input rows); -1 when
	// unknown. It clamps the group and crosstab bounds and drives the
	// memory estimate (unknown: no MaxEstimatedMemory finding).
	Records int64
	// ShardRecords are an archive's per-shard counts (merge blocks
	// restart per shard); nil for a single file, whose blocks derive
	// from Records.
	ShardRecords []int64
	// DirectoryBytes is a shard archive's central-directory bytes
	// (encoding Archive.DirectoryBytes, read the same way by predict and
	// by the runtime's Open), 0 for a single file. Since U51 an archive
	// is never read whole — Open reads the directory and each shard's
	// header, the scan streams one shard at a time — so the memory
	// estimate charges the directory and the largest shard's payload,
	// never the archive's bytes (archiveTerms). An anchor-opened shard
	// (`archive.pulse#shard.pulse`) carries none: its run reads only its
	// own shard, as a single file's does.
	DirectoryBytes int64
	// ShardWorkers is the instance's per-shard worker cap
	// (Options.ShardWorkers: 0 = runtime.NumCPU, 1 = serial) — with the
	// shard count and the merge gate it decides whether an archive run
	// takes the per-shard parallel arm (ShardFanOut).
	ShardWorkers int
	// DecodeWorkers is the instance's parallel-decode worker cap
	// (Options.DecodeWorkers: 0 = runtime.NumCPU, 1 = serial) and
	// Mappable whether the runtime can memory-map the single file the
	// request scans (a real on-disk path: never an archive, an anchored
	// shard, an in-memory or RealPath-less fs) — with the record count
	// and the merge gate they decide whether a single-file run takes the
	// parallel-decode reducer (DecodeFanOut).
	DecodeWorkers int
	Mappable      bool
	// Join reports a request carrying its one JoinSpec; JoinRightRows
	// is that build side's header count (-1 unknown: no memory
	// finding). A join never runs the fused crosstab.
	Join          bool
	JoinRightRows int64
	// JoinSpec is that join (nil without one) — its right key fields,
	// read on the joined schema under the spec's As prefix, bound the
	// joined rows a repeating right key fans the left side out to
	// (JoinRowsBound). The runtime pre-flight judges a Joins-stripped
	// clone, so the spec rides here rather than on the request.
	JoinSpec *types.JoinSpec
	// Extensions and FusionDisabled feed the arm decision (streamable,
	// fused) exactly as predict's computeStreamable / CrosstabFusion
	// and the engine's dispatch read them.
	Extensions     *ExtensionsSnapshot
	FusionDisabled bool
	// DefaultWeight is the instance default weight
	// (Options.DefaultWeight, nil = none): a slot with no weight of its
	// own on a request with none runs weighted under it, and a weighted
	// slot's bucket state is charged as such (EstimateMemory).
	DefaultWeight *types.WeightSpec
}

// RequestLimitFindings is the one predict-time limit rule for a single
// Request against the schema it executes over: every effective limit in
// l (predict passes the snapshot's, the runtime the service's — the
// same resolved values pulse.New installs on both) a figure predict can
// compute from the request + schema + in exceeds, graded
// limits.Certain or limits.Possible, in limit-declaration order.
// Predict reports them (PredictResult.LimitFindings); the process
// pre-flight (LimitRefusal) refuses the first Certain one before any
// record is decoded. Reads only the schema.
//
//   - MaxGroups (Possible): the group-bucket upper bound EstimateGroups
//     derives — the product of every grouper's bound, clamped by
//     in.Records when known (unless a grouper fans out) — times the
//     tier-1 test count when the request carries tests (each runs once
//     per group). Any unknown-cardinality grouper yields no finding.
//   - MaxCrosstabCells (Possible): the crosstab grid upper bound
//     EstimateCrosstabCells derives (rows x cols, each axis clamped by
//     records). An axis with an unknown-cardinality grouper yields no
//     finding.
//   - MaxEstimatedMemory (Certain): EstimateMemory, the per-arm upper
//     bound of the run's resident state. Only when the limit is set —
//     it is Unlimited by default — and the record counts are known.
//   - MaxEstimatedMemory (Possible), a join whose certain estimate
//     fits: the estimate again on the joined-row bound JoinRowsBound
//     derives when the right key may repeat (joinFanOutFinding) —
//     never a refusal here; the runtime refuses the measured fan-out
//     after the build (JoinFanOutRefusal).
//   - MaxMatrixDim (Certain): each matrix's dimension p — its resolved
//     member count, from vectors.ResolveMatrices, the resolver the
//     runtime mints its accumulators from. A refused spec yields no
//     finding (the field-reference pass reports the refusal).
func RequestLimitFindings(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, l limits.Limits, in LimitInputs) []limits.Finding {
	if req == nil {
		return nil
	}
	records := in.Records
	var out []limits.Finding
	if n, ok := EstimateGroups(req.Groups, schema, inst, records); ok {
		// Every tier-1 test runs once per group, so tests x groups
		// counts toward MaxGroups (the runtime's admit check).
		if len(req.Tests) > 0 {
			n = mulSaturating(n, int64(len(req.Tests)))
		}
		if f, ok := limits.Evaluate(l, limits.MaxGroups, n, limits.Possible); ok {
			out = append(out, f)
		}
	}
	if req.Crosstab != nil {
		if n, ok := EstimateCrosstabCells(req.Crosstab, schema, inst, records); ok {
			if f, ok := limits.Evaluate(l, limits.MaxCrosstabCells, n, limits.Possible); ok {
				out = append(out, f)
			}
		}
	}
	if !limits.IsUnlimited(limits.Value(l, limits.MaxEstimatedMemory)) {
		// The per-shard arm runs at the window the budget admits
		// (ShardMergeWindow), so the estimate judged is that window's.
		if n, ok := estimateMemoryWindow(req, schema, inst, in, in.Records, ShardMergeWindow(req, schema, inst, l, in)); ok {
			if f, ok := limits.Evaluate(l, limits.MaxEstimatedMemory, n, limits.Certain); ok {
				out = append(out, f)
			} else if f, ok := joinFanOutFinding(req, schema, inst, l, in); ok {
				out = append(out, f)
			}
		}
	}
	if len(req.Matrices) > 0 {
		plans, err := resolveMatricesFor(req, schema, nil, inst)
		if err == nil {
			for _, m := range plans {
				if f, ok := limits.Evaluate(l, limits.MaxMatrixDim, int64(len(m.Columns())), limits.Certain); ok {
					out = append(out, f)
				}
			}
		}
	}
	return out
}

// LimitRefusal is the process pre-flight: the PULSE_LIMIT_EXCEEDED
// error of the first Certain RequestLimitFindings finding, nil when
// none. The runtime calls it right after the field-reference pass, so a
// certain breach is refused before any record is decoded — with the
// error predict reports for the same request.
func LimitRefusal(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, l limits.Limits, in LimitInputs) *errors.CodedError {
	return limits.FirstCertain(RequestLimitFindings(req, schema, inst, l, in))
}

// MemoryArm is the execution arm req runs on over schema — the arm the
// engine's dispatch picks: the fused crosstab when the shared fusion
// rule accepts (never under a join or with fusion disabled), else the
// buffered crosstab; otherwise streaming iff computeStreamable (the
// predict twin of the processor's canStream, parity-gated).
func MemoryArm(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, in LimitInputs) limits.MemoryArm {
	if req.Crosstab != nil {
		if !in.Join && !in.FusionDisabled {
			if fused, _ := CrosstabFusion(req, schema, in.Extensions, inst); fused {
				return limits.ArmFusedCrosstab
			}
		}
		return limits.ArmBuffered
	}
	if ok, _ := computeStreamable(req, schema, &PredictOptions{Extensions: in.Extensions, Instance: inst}); ok {
		return limits.ArmStreaming
	}
	return limits.ArmBuffered
}

// ShardFanOut reports whether req runs on the per-shard parallel
// reducer over the archive `in` describes — the predict twin of the
// runtime's Service.shouldFanOut (parity: internal/service
// TestShardFanOut_PredictMatchesRuntime): two or more shards, a worker
// cap that resolves to two or more (0 = runtime.NumCPU, clamped by the
// shard count), and a request the shared merge gate
// (mergegate.MergeRefusal over the declared extension facts) admits. A
// crosstab or a join never reaches that dispatch.
func ShardFanOut(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, in LimitInputs) bool {
	return shardWorkers(req, schema, inst, in) >= 2
}

// shardWorkers is the resolved per-shard worker count for req (the
// runtime's: the cap, 0 = runtime.NumCPU, clamped by the shard count),
// 0 when the run stays serial (see ShardFanOut).
func shardWorkers(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, in LimitInputs) int {
	if req == nil || req.Crosstab != nil || in.Join || in.ShardWorkers == 1 || len(in.ShardRecords) < 2 {
		return 0
	}
	workers := in.ShardWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	workers = min(workers, len(in.ShardRecords))
	if workers < 2 || mergegate.MergeRefusal(req, schema, in.Extensions.mergeFacts(inst)) != "" {
		return 0
	}
	return workers
}

// ShardMergeWindow is the merge window req's per-shard parallel run
// keeps — the runtime's scanAndMergeShards reads exactly this, from the
// pre-flight's own inputs: limits.ShardMergeWindow(workers) (2 x
// workers), shrunk by limits.ShardMergeWindowWithin while the whole-run
// estimate at that window exceeds a set MaxEstimatedMemory (U51 E5-S4).
// 0 when req does not take the per-shard arm (ShardFanOut false). The
// window changes only how many shards are in flight, never the merge
// order or an answer.
func ShardMergeWindow(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, l limits.Limits, in LimitInputs) int {
	workers := shardWorkers(req, schema, inst, in)
	if workers < 2 {
		return 0
	}
	return limits.ShardMergeWindowWithin(workers, limits.Value(l, limits.MaxEstimatedMemory), func(w int) (int64, bool) {
		return estimateMemoryWindow(req, schema, inst, in, in.Records, w)
	})
}

// shardWindowPartitions is the per-shard arm's live partitions: at most
// window shards' partials (ShardMergeWindow) are alive beside the
// merged state at once (the incremental merge releases every merged
// shard and dispatches none past the window), charged as the window's
// worst case — the largest bucket bounds (bound: a shard's bucket bound
// from its record count) and, independently, the largest record counts
// (the per-value state). Returns the summed buckets and records.
func shardWindowPartitions(shardRecords []int64, window int, bound func(n int64) int64) (buckets, records int64) {
	window = min(window, len(shardRecords))
	bounds := make([]int64, len(shardRecords))
	for i, n := range shardRecords {
		bounds[i] = bound(n)
	}
	recs := slices.Clone(shardRecords)
	desc := func(a, b int64) int { return cmp.Compare(b, a) }
	slices.SortFunc(bounds, desc)
	slices.SortFunc(recs, desc)
	for i := range window {
		buckets = addSaturating(buckets, bounds[i])
		records = addSaturating(records, recs[i])
	}
	return buckets, records
}

// DecodeFanOut reports whether req runs on the parallel-decode reducer
// over the single file `in` describes — the predict twin of the
// runtime's processSingleFileParallelMaybe gate (canParallelDecode +
// shouldFanOutDecode; parity: internal/service
// TestDecodeFanOut_PredictMatchesRuntime): a mappable single file (no
// shards), at least limits.ParallelDecodeRecordThreshold records, a
// worker cap that resolves to two or more (0 = runtime.NumCPU, clamped
// by the record count), no crosstab or join, and a request the shared
// merge gate admits. A crosstab's parallel segment decode materialises
// the records as the buffered arm does, so it adds no partials and is
// not counted here.
func DecodeFanOut(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, in LimitInputs) bool {
	return decodeWorkers(req, schema, inst, in) >= 2
}

// decodeWorkers is the resolved parallel-decode worker count for req
// (0 when the run stays serial; see DecodeFanOut).
func decodeWorkers(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, in LimitInputs) int64 {
	if req == nil || req.Crosstab != nil || in.Join || !in.Mappable || in.DecodeWorkers == 1 ||
		len(in.ShardRecords) > 0 || in.Records < limits.ParallelDecodeRecordThreshold {
		return 0
	}
	workers := int64(in.DecodeWorkers)
	if workers <= 0 {
		workers = int64(runtime.NumCPU())
	}
	workers = min(workers, in.Records)
	if workers < 2 || mergegate.MergeRefusal(req, schema, in.Extensions.mergeFacts(inst)) != "" {
		return 0
	}
	return workers
}

// EstimateMemory is the upper bound, in bytes, of the resident state
// req builds over schema — limits.EstimateMemory on the arm MemoryArm
// picks, with the record counts from in and the request-shaped figures
// from the shared estimators:
//
//   - buckets: memoryGroupBound — EstimateGroups (clamped by
//     records); a grouper of unknown cardinality by its field's value
//     width (valueKeyBound: 2^bits of an integer or set word), else
//     the record count (x the fan-out multiplicity); 1 ungrouped.
//   - cells: EstimateCrosstabCells, else rows x cols bounded by the
//     record count when an axis is unknown.
//   - matrices: MatrixStateBytes over the merge blocks (per shard) and
//     the matrix bucket bound — EstimatedBytes in PredictResult.Matrices
//     — or records x accumulator bytes when the buckets are unknown
//     (each populated block-bucket pair holds at least one record).
//   - fan-out: FanOutMultiplicity over the groupers and both crosstab
//     axes — a GROUP_SET_PER_ELEMENT record lands in one bucket per
//     selected member, so per-value state, a buffered matrix's row
//     store and a buffered test's values scale with it.
//   - tests: testMemory — an instance per tier-1 test per bucket, the
//     split levels / contingency cells each keys, and every value a
//     buffered (non-streamable) test keeps.
//   - a join adds its build side, and the processor is fed the left
//     count — exact only while no right key repeats; a repeating key's
//     joined rows are judged apart (joinFanOutFinding at predict,
//     JoinFanOutRefusal after the build).
//   - a shard archive charges its byte length in place of records x
//     stride when larger, and one schema decode, the canonical entry's
//     (archiveTerms; an anchor-opened shard its archive's byte length
//     alone); on the per-shard parallel arm (ShardFanOut) the partials
//     alive beside the merged state are at most the merge window's
//     (limits.ShardMergeWindow: the incremental merge releases each
//     merged shard; RequestLimitFindings judges the window a set
//     MaxEstimatedMemory shrinks it to, ShardMergeWindow), charged as the window's largest shard bucket
//     bounds — memoryGroupBound over that shard's records, 1
//     ungrouped — and largest shard
//     record counts (per-value state) (shardWindowPartitions).
//     The parallel-decode reducer (DecodeFanOut) is charged per worker
//     partition, each bounded by its block-aligned segment (at most
//     ceil(blocks / workers) merge blocks). A partition's bucket costs
//     its state only (limits.BucketBytes); a merged bucket adds its
//     finalize output.
//   - aggregations: weighted ones (a weight in force: the slot's own,
//     else the request's, else in.DefaultWeight) cost the weighted
//     accumulator; an AGG_SET_FREQUENCY slot adds its set field's
//     member count (setMembers) per bucket.
//
// False when the record count (or a join's right count) is unknown.
// The model never sees filter selectivity or field projection, so the
// figure is an upper bound — calibrated in internal/service
// TestMemoryEstimate_Calibration.
func EstimateMemory(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, in LimitInputs) (int64, bool) {
	return estimateMemory(req, schema, inst, in, in.Records)
}

// estimateMemory is EstimateMemory with the processor fed rows records
// — in.Records on a plain scan; a fan-out join's joined-row figure
// (JoinRowsBound at predict, left x the measured right-key multiplicity
// after the build). The payload term stays on in.Records (the left
// scan); every per-row processing term — buckets and cells clamped,
// per-value state, buffered records, tests, matrices, point stores —
// reads rows.
func estimateMemory(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, in LimitInputs, rows int64) (int64, bool) {
	return estimateMemoryWindow(req, schema, inst, in, rows, 0)
}

// estimateMemoryWindow is estimateMemory with the per-shard arm's live
// partials charged at window shards (ShardMergeWindow); window 0 is the
// unbudgeted limits.ShardMergeWindow(workers).
func estimateMemoryWindow(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, in LimitInputs, rows int64, window int) (int64, bool) {
	if req == nil || schema == nil || in.Records < 0 || (in.Join && in.JoinRightRows < 0) {
		return 0, false
	}
	records := in.Records
	mi := limits.MemoryInputs{
		Arm:         MemoryArm(req, schema, inst, in),
		Records:     records,
		Stride:      int64(schema.RecordByteSize()),
		RecordBytes: limits.RecordBytes(schema),
		Buckets:     1,
	}
	archiveTerms(&mi, schema, in)
	matrixIn := in
	if rows != records {
		mi.JoinedRows = rows
		matrixIn.Records, matrixIn.ShardRecords = rows, nil
		records = rows
	}
	if workers := shardWorkers(req, schema, inst, in); workers >= 2 {
		// The live partials: at most the merge window's shards beside
		// the merged state (ShardMergeWindow, the runtime's
		// scanAndMergeShards), charged as the window's largest.
		if window <= 0 {
			window = limits.ShardMergeWindow(workers)
		}
		mi.PartitionBuckets, mi.PartitionRecords = shardWindowPartitions(in.ShardRecords, window, func(n int64) int64 {
			if len(req.Groups) == 0 {
				return 1
			}
			return memoryGroupBound(req.Groups, schema, inst, n)
		})
	} else if workers := decodeWorkers(req, schema, inst, in); workers >= 2 {
		// One partial per worker, live until the merge; a worker's
		// segment is whole merge blocks (the runtime's
		// blockAlignedSegments), at most ceil(blocks / workers) of them.
		const block = int64(linalg.MergeBlockSize)
		blocks := (records + block - 1) / block
		seg := min(records, (blocks+workers-1)/workers*block)
		b := int64(1)
		if len(req.Groups) > 0 {
			b = memoryGroupBound(req.Groups, schema, inst, seg)
		}
		mi.PartitionRecords = records
		mi.PartitionBuckets = mulSaturating(b, workers)
	}
	if in.Join {
		mi.JoinRightRows = in.JoinRightRows
	}
	if len(req.Groups) > 0 {
		mi.Buckets = memoryGroupBound(req.Groups, schema, inst, records)
	}
	aggs := req.Aggregations
	if req.Crosstab != nil {
		if n, ok := EstimateCrosstabCells(req.Crosstab, schema, inst, records); ok {
			mi.Cells = n
		} else {
			mi.Cells = records
		}
		aggs = append(append([]*types.Aggregation(nil), aggs...), req.Crosstab.CellAggregations()...)
		aggs = append(append([]*types.Aggregation(nil), aggs...), req.Crosstab.MarginAggregations...)
	}
	mi.Aggregations = int64(len(aggs))
	mi.ExtraGroupers = int64(max(len(req.Groups)-1, 0))
	mi.FanOut = requestFanOut(req, schema)
	var vecInputs map[*types.Aggregation]vectors.AggregationInput
	for _, a := range aggs {
		if a == nil {
			continue
		}
		if valueStateAggregation[a.Type] || inst.Extensions().AggregatorValueState(a.Type) {
			mi.ValueStateAggregations++
		}
		var members int64
		if vectors.IsVectorAggregation(a.Type) {
			if vecInputs == nil {
				vecInputs, _ = vectors.ResolveAggregations(req, schema, aggregationKinds(inst, nil))
			}
			members = vectorAggregationMembers(a, vecInputs, schema)
			// Every fragment holds at least one row, and a bucket (or
			// crosstab cell) holds at most one per merge block.
			fragments := min(mulSaturating(mergeBlocks(in), max(mi.Buckets, mi.Cells)), mulSaturating(records, max(mi.FanOut, 1)))
			buffered := mulSaturating(limits.MergeBufferedRows(in.ShardRecords...), max(mi.FanOut, 1))
			mi.VectorBytes = addSaturating(mi.VectorBytes, limits.VectorAggregationBytes(a.Type, members, fragments, buffered))
		}
		mi.AggStateBytes = addSaturating(mi.AggStateBytes, limits.AggregationStateBytes(a.Type, weightInForce(req, a, in.DefaultWeight), members))
		mi.SetMembers = addSaturating(mi.SetMembers, setMembers(a, schema))
	}
	mi.DerivedColumns = int64(len(req.Attributes) + len(req.Features) + len(req.Windows))
	mi.TestInstances, mi.TestLevels, mi.TestValues = testMemory(req, schema, inst, records, mi.Buckets, mi.FanOut)
	mi.MatrixBytes = matrixMemory(req, schema, inst, matrixIn)
	for _, b := range GroupBufferBytes(req, schema, inst, records) {
		mi.GrouperBytes = addSaturating(mi.GrouperBytes, b)
	}
	return limits.EstimateMemory(mi), true
}

// JoinRowsBound is the joined-row bound a join's memory estimate is
// re-judged on when the right key may repeat (each left row then emits
// one joined row per matching right row, so joined rows = Σ_k L_k·R_k
// can exceed the left count the certain estimate assumes). Header +
// schema hold no uniqueness guarantee — a sidecar index's distinct-key
// count is not a freshness claim, Lookup's assert_unique is a per-call
// check — so a right side of at most one row is the only provably
// unique key: false there (and without a join or a known count). Else:
//
//   - a right key field (OnPairs' right side, on the joined schema
//     under the spec's As prefix) carrying a categorical dictionary of
//     D entries bounds one key's matches by R - D + 1 — sound only when
//     every entry occurs in the right cohort, which a derived, filtered,
//     synthesised or shard-removed cohort does not guarantee, so the
//     finding it drives is Possible. A composite key takes the smallest
//     such bound (a tuple matches no more rows than any of its columns).
//   - no key field carries a dictionary (numeric keys): left x right,
//     every right row matching every left row — the worst case.
func JoinRowsBound(schema *encoding.Schema, in LimitInputs) (limits.JoinBound, bool) {
	spec := in.JoinSpec
	if !in.Join || spec == nil || schema == nil || in.Records < 0 || in.JoinRightRows <= 1 {
		return limits.JoinBound{}, false
	}
	right := in.JoinRightRows
	per, basis := right, limits.JoinBoundLeftXRight
	for _, pair := range spec.On {
		f := schema.Field(spec.As + pair.RightField)
		if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
			continue
		}
		m := max(right-int64(f.Dictionary.Count())+1, 1)
		if basis != limits.JoinBoundDictionary || m < per {
			per, basis = m, limits.JoinBoundDictionary
		}
	}
	return limits.JoinBound{Rows: mulSaturating(in.Records, per), Basis: basis}, true
}

// joinFanOutFinding is the Possible MaxEstimatedMemory finding of a
// join whose certain estimate (the left count fed to the processor)
// fits: the estimate on JoinRowsBound's joined rows, when that exceeds
// the limit, with the bound in Details (join, join_rows_bound,
// join_bound). Never a refusal — the bound is not sound (a dictionary
// entry may not occur) or is the left x right worst case; the runtime
// refuses the MEASURED fan-out after the build (JoinFanOutRefusal).
func joinFanOutFinding(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, l limits.Limits, in LimitInputs) (limits.Finding, bool) {
	bound, ok := JoinRowsBound(schema, in)
	if !ok || bound.Rows <= in.Records {
		return limits.Finding{}, false
	}
	n, ok := estimateMemory(req, schema, inst, in, bound.Rows)
	if !ok {
		return limits.Finding{}, false
	}
	f, ok := limits.Evaluate(l, limits.MaxEstimatedMemory, n, limits.Possible)
	f.Join = bound
	return f, ok
}

// JoinFanOutRefusal is the join build-side backstop (openJoinStream,
// after the right side is hashed and before the left side is probed):
// with maxMatches the most right rows one key holds, the joined rows
// are at most left x maxMatches, and the PULSE_LIMIT_EXCEEDED error of
// the memory estimate on that figure when it exceeds a set
// MaxEstimatedMemory — the details of the pre-flight's memory refusal
// plus the join location (join: 0) and the measured
// right_key_multiplicity / join_rows_bound. Nil when the limit is
// Unlimited, the count is unknown, or no key repeats (maxMatches <= 1:
// the pre-flight's estimate already held).
func JoinFanOutRefusal(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, l limits.Limits, in LimitInputs, maxMatches int64) *errors.CodedError {
	if maxMatches <= 1 || limits.IsUnlimited(limits.Value(l, limits.MaxEstimatedMemory)) {
		return nil
	}
	rows := mulSaturating(max(in.Records, 0), maxMatches)
	n, ok := estimateMemory(req, schema, inst, in, rows)
	if !ok {
		return nil
	}
	f, ok := limits.Evaluate(l, limits.MaxEstimatedMemory, n, limits.Certain)
	if !ok {
		return nil
	}
	err := f.Err()
	err.Details["join"] = 0
	err.Details["join_rows_bound"] = rows
	err.Details["right_key_multiplicity"] = maxMatches
	return err
}

// GroupBufferBytes is every buffered vector grouper slot's point store
// (limits.KMeansBufferBytes over records), keyed by the slot — groups
// and both crosstab axes. Nil when no slot is a GROUP_KMEANS that
// resolves, or records is unknown (< 0). The memory estimate sums it;
// predict reports it per groups[i] slot (GroupPredict.RowBufferBytes).
func GroupBufferBytes(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, records int64) map[*types.Group]int64 {
	if req == nil || records < 0 {
		return nil
	}
	// No extension predicate: an extension grouper's store is its own,
	// so only the built-in GROUP_KMEANS slots resolve here.
	inputs, err := vectors.ResolveGroups(req, schema, func(t types.GroupType) types.GroupType {
		return opRoute(inst, t)
	}, nil)
	if err != nil || len(inputs) == 0 {
		return nil
	}
	out := make(map[*types.Group]int64, len(inputs))
	for g, in := range inputs {
		out[g] = limits.KMeansBufferBytes(records, int64(len(in.Members.Members)), int64(in.KMeans.K))
	}
	return out
}

// FacetLimitRefusal is the facet pre-flight (Service.FacetSchema,
// right after the facet field-reference pass; the simple Service.Facet
// on its streamed path): the PULSE_LIMIT_EXCEEDED error of the first
// certain FacetLimitFindings finding, nil when none. MaxEstimatedMemory
// is the only limit a facet can breach before it scans — each field's
// distinct values are counted against MaxGroups as they mint.
func FacetLimitRefusal(req *types.FacetRequest, schema *encoding.Schema, l limits.Limits, in LimitInputs) *errors.CodedError {
	return limits.FirstCertain(FacetLimitFindings(req, schema, l, in))
}

// FacetLimitFindings is the one limit rule for a rich facet, in limit
// declaration order — PredictFacet reports it
// (FacetValidationResult.LimitFindings), the pre-flight refuses its
// first certain finding (FacetLimitRefusal):
//
//   - MaxGroups (Possible): FacetGroupBound, the largest distinct-value
//     count a faceted field's discrete accumulator can mint.
//   - MaxEstimatedMemory (Certain): EstimateFacetMemory, only when the
//     limit is set and the record count is known.
func FacetLimitFindings(req *types.FacetRequest, schema *encoding.Schema, l limits.Limits, in LimitInputs) []limits.Finding {
	var out []limits.Finding
	if n, ok := FacetGroupBound(req, schema, in.Records); ok {
		if f, ok := limits.Evaluate(l, limits.MaxGroups, n, limits.Possible); ok {
			out = append(out, f)
		}
	}
	if !limits.IsUnlimited(limits.Value(l, limits.MaxEstimatedMemory)) {
		if n, ok := EstimateFacetMemory(req, schema, in); ok {
			if f, ok := limits.Evaluate(l, limits.MaxEstimatedMemory, n, limits.Certain); ok {
				out = append(out, f)
			}
		}
	}
	return out
}

// FacetGroupBound is the upper bound of the distinct values a rich
// facet counts against MaxGroups — the runtime counts each faceted
// field (Fields and AdditiveFields) separately, so the bound is the
// largest per-field one: a categorical field's dictionary size, two
// for packed_bool, clamped by records when known (>= 0). A numeric
// field mints no group (its accumulator is a summary), and an unknown
// field is refused elsewhere; false when no faceted field counts.
func FacetGroupBound(req *types.FacetRequest, schema *encoding.Schema, records int64) (int64, bool) {
	if req == nil || schema == nil {
		return 0, false
	}
	var bound int64
	found := false
	for _, name := range append(append([]string(nil), req.Fields...), req.AdditiveFields...) {
		f := schema.Field(name)
		if f == nil {
			continue
		}
		var n int64
		switch {
		case f.Type.IsCategorical() && f.Dictionary != nil:
			n = int64(f.Dictionary.Count())
		case f.Type == encoding.FieldTypePackedBool:
			n = 2
		default:
			continue
		}
		if records >= 0 && n > records {
			n = records
		}
		if !found || n > bound {
			bound, found = n, true
		}
	}
	return bound, found
}

// EstimateFacetMemory is the upper bound, in bytes, of a rich facet's
// resident state: a streaming scan (the payload, per limits.EstimateMemory)
// with one bucket per faceted accumulator (Fields + AdditiveFields).
// A field without a dictionary keeps per-value state (its distinct
// values; a second slot when numeric_percentiles buffers its values),
// bounded by the record count. False when the record count is unknown.
func EstimateFacetMemory(req *types.FacetRequest, schema *encoding.Schema, in LimitInputs) (int64, bool) {
	if req == nil || schema == nil || in.Records < 0 {
		return 0, false
	}
	mi := limits.MemoryInputs{
		Arm:     limits.ArmStreaming,
		Records: in.Records,
		Stride:  int64(schema.RecordByteSize()),
	}
	archiveTerms(&mi, schema, in)
	for _, name := range append(append([]string(nil), req.Fields...), req.AdditiveFields...) {
		mi.Buckets++
		f := schema.Field(name)
		if f == nil || f.Type.HasDictionary() || f.Type.IsBitPacked() {
			continue
		}
		mi.ValueStateAggregations++
		if len(req.NumericPercentiles) > 0 {
			mi.ValueStateAggregations++
		}
	}
	return limits.EstimateMemory(mi), true
}

// archiveTerms sets the shard-archive inputs of the memory model (U51
// E1-S2): the central directory the scan holds parsed
// (DirectoryBytes), the largest shard's record count — the archive
// streams one shard at a time through a bounded window, so the payload
// term charges that shard's records x stride, never the archive's bytes
// (MaxShardRecords) — and the one schema block the run decodes with its
// dictionaries: the canonical entry, whatever the shard count. Open's
// record-count peek (Archive.PeekShardRecordCount, E8-S4) and the scan
// (shardIter / processOneShard, E8-S5) read every shard's schema
// layout-only and allocate no dictionary, so neither is charged. A
// single file and an anchor-opened shard (no ShardRecords — its run
// reads only its own shard) set nothing.
func archiveTerms(mi *limits.MemoryInputs, schema *encoding.Schema, in LimitInputs) {
	if len(in.ShardRecords) == 0 {
		return
	}
	mi.DirectoryBytes = max(in.DirectoryBytes, 0)
	for _, n := range in.ShardRecords {
		mi.MaxShardRecords = max(mi.MaxShardRecords, n)
	}
	mi.SchemaDecodes = 1
	mi.SchemaBytes = limits.SchemaDecodeBytes(schema)
}

// weightInForce reports whether aggregation slot a runs weighted under
// req and the instance default: its own weight when set (`null` opts
// out), else the request's, else def — processing.StampWeightsWith's
// precedence. The slot's weight awareness is not read, so the figure is
// an upper bound (an unaware slot runs unweighted).
func weightInForce(req *types.Request, a *types.Aggregation, def *types.WeightSpec) bool {
	switch {
	case a.Weight.IsNull():
		return false
	case !a.Weight.IsZero():
		return true
	}
	return req.Weight != nil || def != nil
}

// setMembers is the member count an AGG_SET_FREQUENCY slot keeps a
// counter for per bucket — its set field's dictionary size, capped at
// the set address space (encoding.SetMaskBits), the width
// processing.newSetFrequencyAggregator pre-sizes — 0 for any other slot
// or a field the schema does not carry as a set.
func setMembers(a *types.Aggregation, schema *encoding.Schema) int64 {
	if a.Type != types.AGG_SET_FREQUENCY || schema == nil {
		return 0
	}
	f := schema.Field(a.Field)
	if f == nil || !f.Type.IsSet() {
		return 0
	}
	n := int(f.Type.MaxSetEntries())
	if f.Dictionary != nil {
		n = f.Dictionary.Count()
	}
	return int64(min(n, encoding.SetMaskBits))
}

// vectorAggregationMembers is a built-in vector aggregator slot's member
// count p: its resolved input (inputs, vectors.ResolveAggregations),
// else its inline fields; a slot that did not resolve — refused before
// it runs — is charged every member the schema can supply (one per
// column, a vec_* column's dimension).
func vectorAggregationMembers(a *types.Aggregation, inputs map[*types.Aggregation]vectors.AggregationInput, schema *encoding.Schema) int64 {
	if in, ok := inputs[a]; ok && in.Has() {
		return int64(len(in.Members.Members))
	}
	if len(a.Fields) > 0 {
		return int64(len(a.Fields))
	}
	var n int64
	for i := range schema.Fields {
		n += int64(max(schema.VectorDim(i), 1))
	}
	return n
}

// ValueStateAggregation reports whether the built-in aggregator t keeps
// per-value state (valueStateAggregation): the memory estimate charges
// it limits.ValueEntryBytes a record it can see.
func ValueStateAggregation(t types.AggregationType) bool {
	return valueStateAggregation[t]
}

// valueStateAggregation lists the built-in aggregators whose state
// grows with the distinct values they see (a map entry per value)
// rather than staying a fixed-size accumulator. An extension
// aggregator joins them when its registration declared ValueState
// (ExtensionsSnapshot.AggregatorValueState).
var valueStateAggregation = map[types.AggregationType]bool{
	types.AGG_DISTINCT_COUNT: true,
	types.AGG_DISTINCT_SUM:   true,
	types.AGG_MODE:           true,
	types.AGG_MODE_COUNT:     true,
	types.AGG_FREQUENCY:      true,
	// One map entry per distinct set mask (U51 E2-S4: was uncharged).
	types.AGG_SET_DISTINCT_VALUES: true,
}

// mergeBlocks is the merge-fragment count a run over in's records
// populates: over the per-shard counts for an archive, else over
// Records.
func mergeBlocks(in LimitInputs) int64 {
	if len(in.ShardRecords) > 0 {
		return limits.MergeBlocks(in.ShardRecords...)
	}
	return limits.MergeBlocks(in.Records)
}

// matrixMemory sums every resolved matrix's merge-block state — and a
// buffered spec's row store — for the memory estimate (0 without matrices or when a spec is refused).
func matrixMemory(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, in LimitInputs) int64 {
	if len(req.Matrices) == 0 {
		return 0
	}
	plans, err := resolveMatricesFor(req, schema, nil, inst)
	if err != nil {
		return 0
	}
	blocks := mergeBlocks(in)
	buckets, _, known := vectors.EstimateBuckets(req.Groups, schema)
	fanOut := FanOutMultiplicity(req.Groups, schema)
	var total int64
	for _, m := range plans {
		var b int64
		acc := m.AccumulatorBytes() * m.StateCount(schema)
		if known {
			b = limits.MatrixStateBytes(blocks, buckets, acc)
			// A shard starting inside a block buffers that block's rows.
			b = addSaturating(b, limits.MatrixStateBytes(limits.MergeBufferedRows(in.ShardRecords...), buckets, m.FragmentRowBytes()*m.StateCount(schema)))
		} else {
			b = limits.MatrixStateBytes(in.Records, 1, acc)
		}
		total = addSaturating(total, b)
		// A buffered spec (a rank method) also keeps its admitted rows —
		// one copy per bucket a fan-out grouper routes the row to — and
		// ranks them at finalize.
		total = addSaturating(total, limits.MatrixRowWorkBytes(mulSaturating(in.Records, fanOut), m.RowBytes()))
	}
	return total
}

// addSaturating adds two non-negative estimates, saturating at
// math.MaxInt64 rather than wrapping.
func addSaturating(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// EstimateGroups is the schema-only upper bound on the distinct group
// buckets a grouped request can mint — the figure the MaxGroups rule
// grades. Two or more groupers mint composite keys, so the bound is the
// PRODUCT of every grouper's own bound (the crosstab-axis rule,
// crosstabAxisEstimate): an upper bound, since the runtime emits only
// the combinations the data produces. False when the request is
// ungrouped or ANY grouper's keys depend on the data (GROUP_RANGE,
// GROUP_DATE, GROUP_ROUNDED, GROUP_SET_VALUE, extension groupers, a
// categorical grouper over a numeric field).
//
// Per grouper (groupEstimate):
//   - Built-in dictionary / boolean / include / quantile / k-means
//     cases wrap vectors.EstimateBuckets, the estimator the matrix and
//     return-size predictions share.
//   - GROUP_DATE_RANGES / GROUP_NUMERIC_RANGES: the range count (inline `ranges`, or the named
//     table's range_count from the instance snapshot) plus the
//     unmatched bucket. An unknown table yields no estimate (the
//     runtime reports PULSE_RANGE_TABLE_UNKNOWN).
//
// records >= 0 clamps the product (a run cannot mint more buckets than
// it has records) unless a grouper fans one record into several keys
// (GROUP_SET_PER_ELEMENT): then only the dictionary sizes bound it.
func EstimateGroups(groups []*types.Group, schema *encoding.Schema, inst *InstanceSnapshot, records int64) (int64, bool) {
	if len(groups) == 0 {
		return 0, false
	}
	return crosstabAxisEstimate(groups, schema, inst, records)
}

// memoryGroupBound is the group-bucket bound the memory estimate
// charges over records (the run's, a shard's, a decode segment's):
// EstimateGroups when every grouper's keys are schema-known; else the
// product of each grouper's bound — its groupEstimate, else its
// value-width bound (valueKeyBound), else unbounded — clamped by the
// records (a run cannot mint more keys than it reads rows). A fan-out
// position (GROUP_SET_PER_ELEMENT) lands one record in up to
// FanOutMultiplicity keys, so with one the clamp is records x that
// multiplicity (U51 E5-S6: it was records, below the composite keys a
// fan-out run can mint).
func memoryGroupBound(groups []*types.Group, schema *encoding.Schema, inst *InstanceSnapshot, records int64) int64 {
	if n, ok := EstimateGroups(groups, schema, inst, records); ok {
		return n
	}
	clamp := mulSaturating(records, FanOutMultiplicity(groups, schema))
	n := int64(1)
	for _, g := range groups {
		b, ok := groupEstimate(g, schema, inst)
		if !ok {
			if b, ok = valueKeyBound(g, schema, inst); !ok {
				return clamp
			}
		}
		n = mulSaturating(n, b)
	}
	return min(n, clamp)
}

// valueKeyBound is a grouper's key-count bound from its field's value
// width (U51 E5-S6): a built-in grouper whose key is a function of one
// field's stored value alone — GROUP_CATEGORY, GROUP_ROUNDED and
// GROUP_RANGE over an integer word, GROUP_SET_VALUE over a set mask —
// mints at most one key per distinct stored value, and a w-bit word
// holds at most 2^w (u4 16, u8 and set_u8 256, u16 and set_u16 65,536,
// u32 and set_u32 2^32; a null row mints no key). False for any other
// grouper (a float, date or 64-bit word, an extension grouper), or a
// field the schema does not carry.
func valueKeyBound(g *types.Group, schema *encoding.Schema, inst *InstanceSnapshot) (int64, bool) {
	if g == nil || schema == nil {
		return 0, false
	}
	f := schema.Field(g.Field)
	if f == nil {
		return 0, false
	}
	var bits int
	switch opRoute(inst, g.Type) {
	case types.GROUP_CATEGORY, types.GROUP_ROUNDED, types.GROUP_RANGE:
		switch f.Type {
		case encoding.FieldTypeU4:
			bits = 4
		case encoding.FieldTypeU8, encoding.FieldTypeCategoricalU8:
			bits = 8
		case encoding.FieldTypeU16, encoding.FieldTypeCategoricalU16:
			bits = 16
		case encoding.FieldTypeU32, encoding.FieldTypeCategoricalU32:
			bits = 32
		}
	case types.GROUP_SET_VALUE:
		switch f.Type {
		case encoding.FieldTypeSetU8:
			bits = 8
		case encoding.FieldTypeSetU16:
			bits = 16
		case encoding.FieldTypeSetU32:
			bits = 32
		}
	}
	if bits == 0 {
		return 0, false
	}
	return int64(1) << bits, true
}

// FanOutMultiplicity is the most buckets one record can land in under
// groups: the product, over every position that fans one record into
// several keys (GROUP_SET_PER_ELEMENT — one bucket per selected set
// member), of that position's key bound (the set field's dictionary
// size, or its include list: vectors.EstimateBuckets), else the set
// type's member ceiling; 1 when no position fans out. Popcount is
// data, so the dictionary size is the only schema-derivable bound. An
// extension grouper's fan-out is its own (no schema bound), like its
// state. Saturating.
func FanOutMultiplicity(groups []*types.Group, schema *encoding.Schema) int64 {
	m := int64(1)
	for _, g := range groups {
		if g == nil || !g.Type.FansOut() {
			continue
		}
		if b, _, ok := vectors.EstimateBuckets([]*types.Group{g}, schema); ok {
			m = mulSaturating(m, max(b, 1))
			continue
		}
		if f := schema.Field(g.Field); f != nil && f.Type.IsSet() {
			m = mulSaturating(m, int64(f.Type.MaxSetEntries()))
		}
	}
	return m
}

// requestFanOut is the request's fan-out multiplicity: its groupers'
// times both crosstab axes' (a crosstab record reaches one cell per
// row key x column key).
func requestFanOut(req *types.Request, schema *encoding.Schema) int64 {
	m := FanOutMultiplicity(req.Groups, schema)
	if req.Crosstab != nil {
		m = mulSaturating(m, FanOutMultiplicity(req.Crosstab.Rows, schema))
		m = mulSaturating(m, FanOutMultiplicity(req.Crosstab.Columns, schema))
	}
	return m
}

// testMemory is the tier-1 tests' resident state for the memory
// estimate: instances (every test runs once per bucket — buckets is
// the estimate's group bound, 1 ungrouped), the split levels /
// contingency cells they key (per test the smaller of instances x its
// schema-known levels — a categorical dictionary, 2 for packed_bool —
// and the rows it admits, records x fanOut; a level the schema cannot
// bound is bounded by those rows), and the values a buffered test
// keeps: records x fanOut x its value columns (field, field2, subject,
// every vector member, the weight). A test whose type is not a
// streamable built-in keeps its values — the predict mirror of the
// buffered-only tier (types.TestType.Streamable); an extension test's
// state is its own (instances only).
func testMemory(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, records, buckets, fanOut int64) (instances, levels, values int64) {
	if len(req.Tests) == 0 {
		return 0, 0, 0
	}
	admitted := mulSaturating(records, max(fanOut, 1))
	inputs, _ := vectors.ResolveTests(req, schema, func(t types.TestType) types.TestType {
		return opRoute(inst, t)
	}, nil)
	for i, t := range req.Tests {
		if t == nil {
			continue
		}
		instances = addSaturating(instances, buckets)
		if n, ok := testLevelBound(t, schema); ok && n > 0 {
			levels = addSaturating(levels, min(mulSaturating(buckets, n), admitted))
		} else if !ok {
			levels = addSaturating(levels, admitted)
		}
		typ := opRoute(inst, t.Type)
		if !slices.Contains(types.AllTestTypes(), typ) || typ.Streamable() {
			continue
		}
		cols := int64(1) // the row weight
		for _, f := range []string{t.Field, t.Field2, t.SubjectField} {
			if f != "" {
				cols++
			}
		}
		if i < len(inputs) && inputs[i].Has() {
			cols += int64(inputs[i].P() + len(inputs[i].Members2.Members))
		} else {
			cols += int64(len(t.Fields) + len(t.Fields2))
		}
		values = addSaturating(values, mulSaturating(admitted, cols))
	}
	return instances, levels, values
}

// testLevelBound is the levels one test instance keys — the product of
// its split_by, rows, cols and subject_field level counts — true with 0
// when it keys none; false when a keyed field's levels are not
// schema-known (a numeric, date or missing field).
func testLevelBound(t *types.Test, schema *encoding.Schema) (int64, bool) {
	n, keyed := int64(1), false
	for _, name := range []string{t.SplitBy, t.Rows, t.Cols, t.SubjectField} {
		if name == "" {
			continue
		}
		keyed = true
		f := schema.Field(name)
		switch {
		case f == nil:
			return 0, false
		case f.Type.IsCategorical() && f.Dictionary != nil:
			n = mulSaturating(n, max(int64(f.Dictionary.Count()), 1))
		case f.Type == encoding.FieldTypePackedBool:
			n = mulSaturating(n, 2)
		default:
			return 0, false
		}
	}
	if !keyed {
		return 0, true
	}
	return n, true
}

// groupEstimate is one grouper's unclamped schema-only key-count bound;
// false for a nil grouper or one whose keys depend on the data.
func groupEstimate(g *types.Group, schema *encoding.Schema, inst *InstanceSnapshot) (int64, bool) {
	if g == nil {
		return 0, false
	}
	if g.Type == types.GROUP_DATE_RANGES || g.Type == types.GROUP_NUMERIC_RANGES {
		ranges, ok := dateRangeCount(g, inst)
		if !ok {
			return 0, false
		}
		return ranges + 1, true // + the unmatched bucket
	}
	b, _, known := vectors.EstimateBuckets([]*types.Group{g}, schema)
	if !known {
		return 0, false
	}
	return b, true
}

// EstimateCrosstabCells is the schema-only upper bound on a crosstab's
// grid — rows x cols, the figure both runtime arms check against
// MaxCrosstabCells (buffered after PartitionByAxis, fused at the
// axis-key interners). Each axis is the product of its positions'
// EstimateGroups bounds, clamped by records when records >= 0 unless a
// position fans out (GROUP_SET_PER_ELEMENT). False when any position's
// cardinality is unknown. The product is a Cartesian upper bound — the
// runtime grid holds only the axis keys the data produces.
func EstimateCrosstabCells(spec *types.CrosstabSpec, schema *encoding.Schema, inst *InstanceSnapshot, records int64) (int64, bool) {
	if spec == nil {
		return 0, false
	}
	r, rok := crosstabAxisEstimate(spec.Rows, schema, inst, records)
	c, cok := crosstabAxisEstimate(spec.Columns, schema, inst, records)
	if !rok || !cok {
		return 0, false
	}
	return mulSaturating(r, c), true
}

// crosstabAxisEstimate is one crosstab axis's distinct composite-key
// upper bound: the product of each position's groupEstimate bound,
// clamped by records (>= 0) unless a position fans one record into
// several keys. False when any position is unknown. The return-size
// model reads it with records = -1.
func crosstabAxisEstimate(axis []*types.Group, schema *encoding.Schema, inst *InstanceSnapshot, records int64) (int64, bool) {
	n := int64(1)
	fanout := false
	for _, g := range axis {
		b, ok := groupEstimate(g, schema, inst)
		if !ok {
			return 0, false
		}
		n = mulSaturating(n, b)
		fanout = fanout || g.Type == types.GROUP_SET_PER_ELEMENT
	}
	if records >= 0 && !fanout && n > records {
		n = records
	}
	return n, true
}

// mulSaturating multiplies two non-negative estimates, saturating at
// math.MaxInt64 rather than wrapping.
func mulSaturating(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// dateRangeCount is a GROUP_DATE_RANGES / GROUP_NUMERIC_RANGES grouper's range count: its
// inline `ranges`, else the registered range_count of its named
// `table`. False when the params do not decode or name no source the
// instance knows.
func dateRangeCount(g *types.Group, inst *InstanceSnapshot) (int64, bool) {
	var params struct {
		Ranges []json.RawMessage `json:"ranges"`
		Table  string            `json:"table"`
	}
	if len(g.Params) == 0 || json.Unmarshal(g.Params, &params) != nil {
		return 0, false
	}
	if len(params.Ranges) > 0 {
		return int64(len(params.Ranges)), true
	}
	name := strings.TrimSpace(params.Table)
	ext := inst.Extensions()
	// A table riding a hidden capability:range_tables is one the
	// instance does not offer — sized exactly like a never-registered
	// one (unknown), so predict cannot leak it.
	if name == "" || ext == nil || inst.Hidden(featRangeTables) {
		return 0, false
	}
	for _, t := range ext.RangeTables {
		if t.Name == name {
			return int64(t.RangeCount), true
		}
	}
	return 0, false
}

// limitFindingsDescriptor projects findings onto the public
// PredictResult shape (nil when there are none, so the field is
// omitted).
func limitFindingsDescriptor(fs []limits.Finding) []descriptor.LimitFinding {
	if len(fs) == 0 {
		return nil
	}
	out := make([]descriptor.LimitFinding, len(fs))
	for i, f := range fs {
		out[i] = descriptor.LimitFinding{
			Limit:      string(f.Limit),
			Configured: f.Configured,
			Estimated:  f.Estimated,
			Grade:      descriptor.LimitGrade(f.Grade),
			Details:    f.DetailsMap(),
		}
	}
	return out
}

// joinBuildLimitFinding is the predict-time MaxJoinBuildRows rule: a
// Certain finding when req carries exactly one JoinSpec whose right
// (build) side holds more records than l allows. rightRows is the
// count opts.RecordCounter reads from the right cohort's header — the
// runtime decodes the right side unfiltered, so it is the exact build
// size. The runtime raises the same breach through
// limits.CheckJoinBuildRows on the same header-only count before the
// build decodes a record.
func joinBuildLimitFinding(req *types.Request, l limits.Limits, rightRows func(path string) (int64, error)) (limits.Finding, bool) {
	if req == nil || len(req.Joins) != 1 || req.Joins[0] == nil || rightRows == nil {
		return limits.Finding{}, false
	}
	if limits.IsUnlimited(limits.Value(l, limits.MaxJoinBuildRows)) {
		return limits.Finding{}, false
	}
	n, err := rightRows(req.Joins[0].Right)
	if err != nil {
		// The right cohort's open fault is reported by the joined-schema
		// pass (predictJoinedSchema) under its own code.
		return limits.Finding{}, false
	}
	return limits.Evaluate(l, limits.MaxJoinBuildRows, n, limits.Certain)
}

// CohortCounts are the header facts of one cohort the limit rules read
// (LimitInputs.Records / ShardRecords / DirectoryBytes): the record
// count (-1 unknown), an archive's per-shard counts and its
// central-directory bytes. PredictOptions.
// CohortCounter returns them for a Compose slot's (or fit's) cohort —
// the facade passes the runtime pre-flight's own reader, so a slot's
// certain finding carries the figure the slot's Process refuses with.
type CohortCounts struct {
	Records        int64
	ShardRecords   []int64
	DirectoryBytes int64
}

// unknownCounts is CohortCounts with no record count — a chain stage
// >= 1, whose input rows exist only at runtime.
var unknownCounts = CohortCounts{Records: -1}

// cohortCounts reads the counts of the cohort at path: opts.
// CohortCounter when wired, else opts.RecordCounter's header count
// (no archive terms), else unknown. A read error is unknown — the
// cohort's open fault is reported by the pass that loads its schema.
func (o *PredictOptions) cohortCounts(c *types.Cohort) CohortCounts {
	if o == nil || c == nil {
		return unknownCounts
	}
	path := cohortPathFor(c)
	if o.CohortCounter != nil {
		if cc, err := o.CohortCounter(path); err == nil {
			return cc
		}
		return unknownCounts
	}
	if o.RecordCounter != nil {
		if n, err := o.RecordCounter(path); err == nil {
			return CohortCounts{Records: n}
		}
	}
	return unknownCounts
}

// requestFindings is the one finding set predict reports for a Request
// over the schema it executes over: RequestLimitFindings on the run
// facts opts carries plus counts, then MaxJoinBuildRows. Process
// predict reports it on PredictResult; a Compose slot or fit and a
// chain stage report it located (locatedFindings). A join's right-side
// count is read through opts.RecordCounter only when MaxEstimatedMemory
// is set.
func requestFindings(req *types.Request, schema *encoding.Schema, opts *PredictOptions, counts CohortCounts) []limits.Finding {
	inst := opts.instance()
	l := inst.Limits()
	in := LimitInputs{
		Records:        counts.Records,
		ShardRecords:   counts.ShardRecords,
		DirectoryBytes: counts.DirectoryBytes,
		ShardWorkers:   opts.ShardWorkers,
		DecodeWorkers:  opts.DecodeWorkers,
		JoinRightRows:  -1,
		Extensions:     opts.Extensions,
		FusionDisabled: opts.DisableCrosstabFusion,
		DefaultWeight:  opts.DefaultWeight,
	}
	// The mmap probe runs only when it can change the answer: a memory
	// limit set, a single file at or past the threshold, decode not
	// forced serial — so an unbounded predict never touches the fs.
	if opts.DecodeMappable != nil && req != nil && req.Cohort != nil && len(counts.ShardRecords) == 0 &&
		counts.Records >= limits.ParallelDecodeRecordThreshold && opts.DecodeWorkers != 1 &&
		!limits.IsUnlimited(limits.Value(l, limits.MaxEstimatedMemory)) {
		in.Mappable = opts.DecodeMappable(cohortPathFor(req.Cohort))
	}
	if req != nil && len(req.Joins) == 1 && req.Joins[0] != nil {
		in.Join, in.JoinSpec = true, req.Joins[0]
		if opts.RecordCounter != nil && !limits.IsUnlimited(limits.Value(l, limits.MaxEstimatedMemory)) {
			if n, err := opts.RecordCounter(req.Joins[0].Right); err == nil {
				in.JoinRightRows = n
			}
		}
	}
	fs := RequestLimitFindings(req, schema, inst, l, in)
	if f, ok := joinBuildLimitFinding(req, l, opts.RecordCounter); ok {
		fs = append(fs, f)
	}
	return fs
}

// locatedFindingsAt reports fs on a multi-request root: every certain
// finding's PULSE_LIMIT_EXCEEDED error added to env located under
// key = idx (RefusalAt's convention, the runtime locate's), and the
// findings returned with the same Details. ok is false when a certain
// finding refuses the request — the runtime stops there.
func locatedFindingsAt(env *descriptor.Envelope, fs []limits.Finding, key string, idx int) (out []descriptor.LimitFinding, ok bool) {
	ok = true
	for _, f := range fs {
		if f.Grade == limits.Certain {
			addCodedError(env, RefusalAt(f.Err(), key, idx))
			ok = false
		}
	}
	out = limitFindingsDescriptor(fs)
	for i := range out {
		if out[i].Details == nil {
			out[i].Details = map[string]any{}
		}
		out[i].Details[key] = idx
	}
	return out, ok
}

// limitRefusalFinding is the certain finding a PULSE_LIMIT_EXCEEDED
// refusal raised by an exact count (MaxComposeSlots, MaxChainStages,
// MaxDerivedColumns) states — read off the error's own details, so the
// finding and the error never disagree — with the error's location
// key (request / stage) carried over. False for any other error.
func limitRefusalFinding(err error) (descriptor.LimitFinding, bool) {
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		return descriptor.LimitFinding{}, false
	}
	name, _ := ce.Details["limit"].(string)
	configured, ok1 := ce.Details["configured"].(int64)
	observed, ok2 := ce.Details["observed"].(int64)
	if name == "" || !ok1 || !ok2 {
		return descriptor.LimitFinding{}, false
	}
	out := descriptor.LimitFinding{Limit: name, Configured: configured, Estimated: observed, Grade: descriptor.LimitGradeCertain}
	for _, key := range []string{"request", "stage", "fit"} {
		if v, ok := ce.Details[key]; ok {
			out.Details = map[string]any{key: v}
		}
	}
	return out, true
}

// predictLimits fills PredictResult.LimitFindings and adds the
// PULSE_LIMIT_EXCEEDED error of every Certain finding — the error the
// process pre-flight raises — so a certain finding leaves Valid false.
// A Possible finding is reported only. Findings follow limit
// declaration order: the request-level rules, then MaxJoinBuildRows.
//
// records is the cohort's header record count (-1 when unknown),
// shardRecords an archive's per-shard counts (nil for a single file)
// and directoryBytes its central-directory bytes (0 for a single file);
// they clamp the group-bucket estimate and drive the memory estimate.
func predictLimits(env *descriptor.Envelope, result *descriptor.PredictResult, req *types.Request, schema *encoding.Schema, opts *PredictOptions, records int64, shardRecords []int64, directoryBytes int64) {
	fs := requestFindings(req, schema, opts, CohortCounts{Records: records, ShardRecords: shardRecords, DirectoryBytes: directoryBytes})
	result.LimitFindings = limitFindingsDescriptor(fs)
	for _, f := range fs {
		if f.Grade == limits.Certain {
			addCodedError(env, f.Err())
		}
	}
}

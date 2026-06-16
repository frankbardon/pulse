package service

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Service is the orchestration layer connecting filesystem, encoding, and processing.
type Service struct {
	fs              *fs.Config
	disableDefaults bool
	projectBuffered bool
	extensions      *processing.ExtensionRegistry
	extensionsSnap  *descriptor.ExtensionsSnapshot

	// shardWorkers caps the per-shard parallel worker pool the Process
	// path spawns when a request is mergeable per
	// processing.CanMergeRequest. Zero is interpreted as
	// runtime.NumCPU() at dispatch time; 1 forces strictly serial
	// execution (the pre-S6 path). The reducer caps spawn count at
	// the shard count regardless of this knob.
	shardWorkers int

	// decodeWorkers caps the per-cohort parallel decode worker pool
	// the buffered Process path spawns when the cohort exceeds
	// parallelDecodeRecordThreshold and the request is mergeable.
	// Zero is interpreted as runtime.NumCPU() at dispatch time; 1
	// forces strictly serial execution (the pre-E3 path). Cohorts
	// below the threshold stay serial regardless of this knob.
	// Matches pulse.Options.DecodeWorkers.
	//
	// E3-S1 plumbing only: the buffered Process path reads this field
	// but does not yet act on it; the fan-out logic lands in E3-S2.
	decodeWorkers int

	// strict promotes runtime request-validation warnings into hard
	// errors. Currently governs the categorical-aggregation check in
	// Process; matches pulse.Options.Strict.
	strict bool

	// autoLabels are default LabelBindings injected into every read
	// request before validation. Each is applied only when its field is
	// present + categorical in the target cohort's schema and the caller
	// has not already bound that field — so a default that does not fit a
	// given cohort is silently skipped rather than failing the request.
	// Matches pulse.Options.AutoLabels.
	autoLabels []*types.LabelBinding

	// echoRequest causes ProcessChain to capture per-stage normalized
	// requests into ChainResponse.NormalizedRequest so the CLI / MCP
	// boundary can publish them on the envelope. Other execution paths
	// (Process, Compose, Facet, Sample) keep the in-place defaults
	// mutation behavior — the boundary clones for echo purposes itself.
	// Matches pulse.Options.EchoRequest.
	echoRequest bool

	// disableCrosstabFusion forces processCrosstab to skip the fused
	// streaming dispatch and always take the buffered RunCrosstab
	// path. Default false (fusion engages when the gate accepts).
	// Used by the equivalence test below to compare fused vs buffered
	// output byte-for-byte; embedders can also set it via
	// SetDisableCrosstabFusion(true) to disable the optimisation
	// without rebuilding pulse.New().
	disableCrosstabFusion bool
}

// SetDisableCrosstabFusion toggles the fused-crosstab dispatch in
// processCrosstab. When true, every crosstab request takes the
// buffered RunCrosstab path even when CanFuseCrosstab would accept
// it. Exposed so the equivalence test in service/crosstab_fused_test.go
// can drive the same request through both paths and assert
// byte-equal output without relying on internals.
func (s *Service) SetDisableCrosstabFusion(disabled bool) {
	s.disableCrosstabFusion = disabled
}

// New creates a new Service with the given filesystem configuration.
// Smart-defaults resolution runs by default; call DisableDefaults to opt
// out per instance (or pass pulse.Options{DisableDefaults: true} via the
// facade).
func New(fsConfig *fs.Config) *Service {
	return &Service{fs: fsConfig}
}

// SetDisableDefaults toggles the smart-defaults pass. Predict still
// computes and reports DefaultsApplied independently; this flag governs
// only what the runtime mutates before Process / Compose execution.
func (s *Service) SetDisableDefaults(disabled bool) {
	s.disableDefaults = disabled
}

// SetProjectBufferedFields enables buffered-decode field projection.
// When enabled the streaming iterator computes the set of fields a
// request actually reads (NeededFields) and skips map writes for
// fields outside that set. Per-record memory drops proportional to
// the projection ratio; decode CPU is unchanged. Default is false;
// pulse.Options{ProjectBufferedFields: true} opts in.
//
// Extension operators without a registered FieldInputs hook widen
// the projection to "every field" automatically — projection is
// always a strict superset of the fields actually consumed, so it
// can never produce a wrong answer.
func (s *Service) SetProjectBufferedFields(enabled bool) {
	s.projectBuffered = enabled
}

// ProjectBufferedFields reports the current setting. Read by callers
// that need to know whether to compute NeededFields ahead of time.
func (s *Service) ProjectBufferedFields() bool {
	return s.projectBuffered
}

// SetExtensions installs an ExtensionRegistry containing embedder-
// registered operator overlays. The registry is read-only after this
// call; pass nil to clear. The processor consults this registry
// before falling through to built-in factories.
func (s *Service) SetExtensions(r *processing.ExtensionRegistry) {
	s.extensions = r
}

// Extensions returns the installed ExtensionRegistry, or nil when no
// extensions are registered. Exposed for descriptor/manifest emission
// and MCP schema-binding paths.
func (s *Service) Extensions() *processing.ExtensionRegistry {
	return s.extensions
}

// SetShardWorkers configures the per-shard parallel worker pool cap.
// Zero falls back to runtime.NumCPU() at dispatch time; 1 disables
// parallelism (strictly serial path). Negative values are not
// rejected here — pulse.New() performs that validation at the public
// API boundary.
func (s *Service) SetShardWorkers(n int) {
	s.shardWorkers = n
}

// ShardWorkers returns the configured cap. Exposed for tests and the
// shard-reduce orchestrator.
func (s *Service) ShardWorkers() int {
	return s.shardWorkers
}

// parallelDecodeRecordThreshold is the minimum cohort record count
// for the buffered Process path to consider per-segment parallel
// decode. Below this floor, worker spawn + state-merge overhead
// dominates the savings from segmenting decode, so the path stays
// serial regardless of decodeWorkers. Chosen to match the in-tree
// BenchmarkBufferedProcessWideCohort row count (100K) where the
// parallel decode win first becomes measurable on the canonical
// reference cohort.
//
// E3-S1 sets the constant and the dispatch site reads it; the
// fan-out logic that consults the threshold lands in E3-S2.
const parallelDecodeRecordThreshold = 100_000

// SetDecodeWorkers configures the per-cohort parallel decode worker
// pool cap used by the buffered Process path. Zero falls back to
// runtime.NumCPU() at dispatch time when the cohort exceeds
// parallelDecodeRecordThreshold; 1 disables parallelism (strictly
// serial path). Negative values are not rejected here — pulse.New()
// performs that validation at the public API boundary.
//
// E3-S1 plumbing only: the setter installs the value and the
// buffered Process path reads it via DecodeWorkers(), but the fan-
// out logic lands in E3-S2.
func (s *Service) SetDecodeWorkers(n int) {
	s.decodeWorkers = n
}

// DecodeWorkers returns the configured cap. Exposed for tests and
// the buffered-decode orchestrator that lands in E3-S2.
func (s *Service) DecodeWorkers() int {
	return s.decodeWorkers
}

// SetStrict toggles the strict request-validation flag. When true,
// Process promotes the categorical-aggregation warning into a
// PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL CodedError instead of
// running the request.
func (s *Service) SetStrict(strict bool) {
	s.strict = strict
}

// Strict reports the current strict-validation setting.
func (s *Service) Strict() bool {
	return s.strict
}

// SetAutoLabels installs the default LabelBindings injected into read
// requests before validation. Pass nil to clear. See the autoLabels
// field and applyAutoLabels for the schema-aware filtering applied per
// request. Matches pulse.Options.AutoLabels.
func (s *Service) SetAutoLabels(bindings []*types.LabelBinding) {
	s.autoLabels = bindings
}

// AutoLabels returns the configured default bindings. Exposed for tests.
func (s *Service) AutoLabels() []*types.LabelBinding {
	return s.autoLabels
}

// SetEchoRequest toggles per-stage normalized-request capture in
// ProcessChain. When true, ChainResponse carries a NormalizedRequest
// snapshot built during execution; when false, that field is nil.
// Matches pulse.Options.EchoRequest.
func (s *Service) SetEchoRequest(enabled bool) {
	s.echoRequest = enabled
}

// EchoRequest reports the current echo setting.
func (s *Service) EchoRequest() bool {
	return s.echoRequest
}

// SetExtensionsSnapshot installs the descriptor-side projection of
// the registered extensions for manifest + predict consumption. Pass
// nil to clear; pulse.New populates this alongside SetExtensions.
func (s *Service) SetExtensionsSnapshot(snap *descriptor.ExtensionsSnapshot) {
	s.extensionsSnap = snap
}

// ExtensionsSnapshot returns the descriptor-side projection of the
// registered extensions, or nil when no extensions are installed.
func (s *Service) ExtensionsSnapshot() *descriptor.ExtensionsSnapshot {
	return s.extensionsSnap
}

// applyDefaults runs descriptor.ResolveDefaults against the cohort schema
// unless defaults are disabled on this Service. Mutates req in place.
func (s *Service) applyDefaults(req *types.Request, schema *encoding.Schema) {
	if s.disableDefaults || req == nil || schema == nil {
		return
	}
	descriptor.ResolveDefaults(req, schema)
}

// Open reads a .pulse file and returns a Cohort with the parsed schema.
// Dispatches on the file's leading magic bytes:
//
//   - "PULSE\x00\x00\x00" (single-file cohort) — reads header + schema
//     directly from the file. Shards is empty.
//   - "PK\x03\x04" (zip archive) — parses the zip central directory,
//     reads the canonical schema from the reserved `_schema.pulse`
//     entry, and populates Shards with every other entry in
//     central-directory order. S1 leaves RecordCount at zero; later
//     phases populate from `_schema.pulse` metadata or shard headers.
//
// Returns PULSE_ARCHIVE_MAGIC_INVALID when the file matches neither
// magic, PULSE_ARCHIVE_CORRUPT when the zip EOCD or central directory
// is unreadable, and PULSE_SHARD_MISSING when an archive lacks the
// reserved `_schema.pulse` entry.
func (s *Service) Open(ctx context.Context, path string) (*Cohort, error) {
	// Anchor syntax: "archive.pulse#shard.pulse" opens one shard inside
	// the archive as a single-shard cohort. Recognised before the magic
	// dispatch so anchored facades (Process, Sample, Facet, ...) reach
	// the right code path.
	if archivePath, anchor, ok := splitAnchorPath(path); ok {
		return s.OpenAnchor(ctx, archivePath, anchor)
	}

	// Streaming open: read only the 4-byte magic prefix to discriminate
	// between single-file (.pulse) and shard archive (Zip64) layouts.
	// The single-file branch then advances through the 9-byte header +
	// schema block without ever materializing the record payload — on
	// large cohorts (hundreds of MB) this turns an O(file size) alloc
	// into an O(header + schema) read. The shard archive branch keeps
	// the full slurp because encoding.OpenArchive needs the central
	// directory at the file's tail and the EOCD requires random access
	// across the whole zip; switching that to a streaming open is a
	// separate scope (see story Notes).
	f, err := s.fs.Fs().Open(path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file: %s", path))
	}
	defer f.Close()

	var magic [4]byte
	n, err := io.ReadFull(f, magic[:])
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("reading magic prefix from cohort file: %s", path))
	}
	if n >= 4 && magic[0] == 'P' && magic[1] == 'K' && magic[2] == 0x03 && magic[3] == 0x04 {
		// Shard archive: slurp via afero so encoding.OpenArchive can
		// scan the EOCD at the file tail. afero.ReadFile opens its own
		// handle; the deferred Close above releases the streaming one.
		// This is intentionally not optimised here — see comment above.
		data, rerr := afero.ReadFile(s.fs.Fs(), path)
		if rerr != nil {
			return nil, errors.WrapCodedError(rerr, errors.SERVICE_RESOURCE,
				fmt.Sprintf("opening cohort file: %s", path))
		}
		return s.openArchive(path, data)
	}

	// Single-file branch: feed the already-consumed magic bytes back to
	// ReadHeader via a MultiReader so it sees the full 9-byte prefix.
	// ReadSchema then streams field descriptors + inline dictionary
	// blocks directly off the file handle.
	r := io.MultiReader(bytes.NewReader(magic[:n]), f)

	if err := encoding.ReadHeader(r); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("invalid pulse file: %s", path))
	}

	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading schema from: %s", path))
	}

	return &Cohort{
		path:   path,
		schema: schema,
		fs:     s.fs.Fs(),
	}, nil
}

// OpenAnchor opens a single shard inside a Pulse shard archive as if
// it were a standalone single-file `.pulse` cohort. The schema is
// read from the shard's own header (NOT the archive's canonical
// schema) so anchor-addressed shards stand alone for the purposes of
// facade methods. Shards is empty on the returned Cohort.
//
// Errors:
//   - PULSE_ARCHIVE_MAGIC_INVALID — archivePath is not a Pulse shard archive.
//   - PULSE_SHARD_MISSING — the named entry is not present in the archive.
//   - PULSE_SHARD_RESERVED_NAME — entry is the reserved `_schema.pulse`.
//   - PULSE_SHARD_HEADER_INVALID / ENCODING_INVALID — the shard payload is malformed.
func (s *Service) OpenAnchor(_ context.Context, archivePath, entry string) (*Cohort, error) {
	if entry == encoding.ReservedSchemaName {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot anchor-open the reserved canonical schema entry",
			map[string]any{"entry": entry})
	}
	data, err := afero.ReadFile(s.fs.Fs(), archivePath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file for anchor: %s", archivePath))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	// Read the entry payload into memory so the returned Cohort owns
	// its byte buffer — the section reader's lifetime is tied to the
	// archive (and therefore to `data`), but a Cohort just stores the
	// path. We synthesise a backing afero.Fs that serves the path
	// "<archive>#<entry>" returning the shard bytes, so downstream
	// readers (streamingIterator) round-trip cleanly.
	rc, err := arch.Open(entry)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	payload, err := io.ReadAll(rc)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("reading anchored shard %q from %s", entry, archivePath))
	}

	r := bytes.NewReader(payload)
	if err := encoding.ReadHeader(r); err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("invalid shard header for anchor: %s#%s", archivePath, entry))
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading shard schema for anchor: %s#%s", archivePath, entry))
	}

	// Surface the shard via an in-memory overlay so streamingIterator
	// can re-open by path. Path is the literal anchor form so error
	// messages stay informative.
	anchorPath := archivePath + "#" + entry
	overlay := newAnchorOverlay(s.fs.Fs(), anchorPath, payload)
	return &Cohort{
		path:   anchorPath,
		schema: schema,
		fs:     overlay,
	}, nil
}

// openArchive parses a Pulse shard archive from data and constructs a
// Cohort whose Schema reads from the archive's reserved
// `_schema.pulse` entry and whose Shards slice enumerates every
// non-reserved entry in central-directory order.
//
// Record counts on every ShardEntry are populated by peeking each
// shard's own header — that is the authoritative source per the
// design contract (§3 of the sharding placeholder). The
// `_schema.pulse` extension's AggregateRecordCount is a cached
// sanity check; the per-shard headers win.
func (s *Service) openArchive(path string, data []byte) (*Cohort, error) {
	reader := bytes.NewReader(data)
	arch, err := encoding.OpenArchive(reader, int64(len(data)))
	if err != nil {
		return nil, err
	}

	// Read canonical schema + sharding metadata from the reserved
	// _schema.pulse entry via ReadSchemaDoc.
	rc, err := arch.Open(encoding.ReservedSchemaName)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	doc, err := encoding.ReadSchemaDoc(rc)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading schema doc from %s in archive: %s",
				encoding.ReservedSchemaName, path))
	}

	// Enumerate shard entries — every entry except the reserved
	// schema. Per-shard RecordCount comes from peeking each shard's
	// header.
	entries := arch.Entries()
	shards := make([]ShardEntry, 0, len(entries))
	for _, e := range entries {
		if e.Name == encoding.ReservedSchemaName {
			continue
		}
		count, perr := arch.PeekShardRecordCount(e.Name)
		if perr != nil {
			return nil, perr
		}
		shards = append(shards, ShardEntry{
			Filename:    e.Name,
			RecordCount: count,
		})
	}

	return &Cohort{
		path:   path,
		schema: doc.Schema,
		fs:     s.fs.Fs(),
		shards: shards,
	}, nil
}

// Process executes a single request against the specified cohort.
// Records are streamed from disk — the full file is never held in memory as raw bytes
// alongside the decoded records.
func (s *Service) Process(ctx context.Context, req *types.Request) (*types.Response, error) {
	if req.Cohort == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "request cohort is required")
	}

	if req.Crosstab != nil {
		return s.processCrosstab(ctx, req)
	}

	if len(req.Joins) > 0 {
		return s.processWithJoin(ctx, req)
	}

	path := resolveCohortPath(req.Cohort)

	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	// Smart-defaults resolution: fill in operator types that the caller
	// omitted, based on each named field's schema type. Caller can opt
	// out via SetDisableDefaults / pulse.Options{DisableDefaults: true}.
	s.applyDefaults(req, cohort.Schema())

	// Inject configured default label bindings (schema-filtered) before
	// validation so registered tables render display strings without the
	// caller specifying bindings per request.
	s.applyAutoLabels(&req.Labels, cohort.Schema(), collectOutputLabels(req), nil)

	// Label-binding validation runs before any record bytes are read so
	// schema / table / collision failures surface as typed errors.
	if err := s.validateProcessLabels(req, cohort.Schema()); err != nil {
		return nil, err
	}

	// Strict-mode runtime validation: when a request asks for a numeric
	// aggregation (SUM/AVG/MIN/MAX/STDDEV/VARIANCE/RANGE/ZSCORE/MEDIAN/
	// PERCENTILE/SKEWNESS/KURTOSIS) against a categorical_* field, we
	// already emit a warning via the predict envelope. In strict mode
	// we promote that warning to a hard error here so callers fail
	// fast instead of producing meaningless aggregates. Non-strict
	// callers still see the warning through the predict path / the
	// CLI envelope wiring.
	if s.strict {
		if issues := descriptor.CategoricalAggregationIssues(req, cohort.Schema()); len(issues) > 0 {
			first := issues[0]
			return nil, errors.NewCodedErrorWithDetails(
				errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL,
				first.Message,
				first.Details,
			)
		}
	}

	// Per-shard parallel fast path: when the cohort is archive-backed,
	// the request is mergeable, and ShardWorkers != 1, fan out across
	// a bounded worker pool and reduce partials in shard insertion
	// order. Non-mergeable requests (percentiles, windows, tier-2
	// tests, etc.) fall through to the serial shardIter path below
	// with byte-for-byte identical semantics.
	if workers, ok := s.shouldFanOut(req, cohort); ok {
		return s.processShardArchiveParallel(ctx, req, cohort, path, workers)
	}

	// Per-cohort parallel decode fast path (single-file branch). When
	// the request is mergeable (processing.CanMergeRequest), the cohort
	// is a single .pulse file (not a shard archive — those route through
	// shouldFanOut above), the cohort is large enough to amortise
	// per-worker spawn overhead (recordCount >= the documented threshold),
	// mmap is available on this fs (resolveRealPath succeeds), and the
	// caller has not forced strictly serial (DecodeWorkers != 1),
	// segment the mmap'd record region across N workers and fold
	// per-worker partial aggregator state in worker-index order.
	//
	// The eligibility gate is the canParallelDecode predicate; it stays
	// pure (no mmap, no payload decode) so the bail path is free. Record
	// count is taken from the header-fast pulse.CountRecords path, NOT
	// a whole-file slurp — a cohort that bails on the threshold check
	// must not pay the parallel path's mmap setup cost.
	//
	// On eligibility we re-derive the same DecodePlan + projection the
	// scanIter would have installed (processing.NeededFields →
	// Schema.BuildDecodePlan), build the parallelDecodeContext, and
	// dispatch into reduceParallelBuffered. Ineligible (and any post-
	// gate failure that doesn't surface a hard error) drops through to
	// the serial scanIter path below — byte-equal vs today's output for
	// every bail case is a load-bearing acceptance criterion.
	if resp, ok, err := s.processSingleFileParallelMaybe(ctx, req, cohort, path); err != nil {
		return nil, err
	} else if ok {
		return resp, nil
	}

	iter := s.newScanIter(cohort, path)
	defer iter.Close()

	s.applyProjection(iter, req, cohort.Schema())

	proc := processing.NewProcessorWithExtensions(cohort.Schema(), s.extensions)
	resp, err := proc.Process(ctx, req, iter)
	if err != nil {
		return nil, err
	}
	if iter.Err() != nil {
		return nil, iter.Err()
	}

	if resp.Metadata != nil {
		resp.Metadata.CohortFile = path
	}

	if err := s.buildAndApplyLabels(req, resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// processSingleFileParallelMaybe is the per-cohort parallel-decode
// dispatcher invoked from Process. Returns (resp, true, nil) on a
// successful parallel-reduce run; (nil, false, nil) when the gate
// rejects (caller falls through to the serial scanIter path); or
// (nil, false, err) on a hard failure that should not be silently
// swallowed (e.g. corrupted cohort header surfaced during the parallel
// context setup).
//
// The gate composes canParallelDecode with the parallel context setup
// (which does the actual mmap). Even after canParallelDecode accepts,
// the post-pctx checks shouldFanOutDecode and buildParallelDecodeContext
// can still bail (mmap failure on a real path, empty cohort, etc.); in
// every bail branch we release any cleanup the setup already acquired
// and return (nil, false, nil) so Process drops into the serial arm
// with byte-equal output.
//
// The DecodePlan + projection passed into buildParallelDecodeContext
// mirrors what scanIter + applyProjection would have installed on the
// serial path: when ProjectBufferedFields is enabled and the request
// has a narrower needed-set than the schema, we build the same plan
// the streamingIterator caches and feed it to the parallel decoder so
// per-record decode cost is the same as the serial projected path.
// When projection is disabled or the needed set is wide, plan == nil
// and the parallel decoder falls back to ReadRecordWithWide — same as
// today's serial unprojected path.
func (s *Service) processSingleFileParallelMaybe(
	ctx context.Context,
	req *types.Request,
	cohort *Cohort,
	path string,
) (*types.Response, bool, error) {
	schema := cohort.Schema()

	// Resolve worker count once. canParallelDecode treats workers != 1
	// as "may parallelise"; the resolution (zero → NumCPU, cap at record
	// count) lives inside shouldFanOutDecode so we keep the worker-cap
	// math in one place.
	workers := s.decodeWorkers

	// Cheap pre-gate: every condition canParallelDecode checks EXCEPT
	// the record-count threshold. We pay this round of probes BEFORE
	// calling CountRecords so a MemMapFs/shard-archive/serial-forced
	// cohort never opens the file an extra time. Counting records on
	// the single-file branch costs one Open + header+schema read;
	// negligible for an mmap'd OsFs cohort but observable on hermetic
	// MemMapFs tests that count file opens to assert mmap is NOT
	// engaging (see service/fs_counting_test.go). The downstream
	// canParallelDecode call duplicates these checks so the gate stays
	// a pure single-call predicate for callers that already know the
	// record count.
	if workers == 1 {
		return nil, false, nil
	}
	if len(cohort.Shards()) > 0 {
		return nil, false, nil
	}
	if cohort.fs == nil {
		return nil, false, nil
	}
	if _, ok := resolveRealPath(cohort.fs, path); !ok {
		return nil, false, nil
	}
	if !processing.CanMergeRequest(req, schema) {
		return nil, false, nil
	}

	// Header-fast record count via the CountRecords path — single-file
	// branch reads only header + schema bytes (no payload decode), so
	// the gate cost is bounded regardless of cohort size. A failure here
	// is unusual (we already opened the cohort upstream) but if it does
	// occur we fall back to serial; the serial path will surface the
	// same error if it is a real cohort-integrity issue.
	count, err := s.CountRecords(ctx, path)
	if err != nil {
		return nil, false, nil
	}
	if eligible, _ := s.canParallelDecode(req, schema, cohort, workers, int(count)); !eligible {
		return nil, false, nil
	}

	// Compute the projected decode plan + keep filter so the parallel
	// workers walk the same plan the serial streamingIterator would
	// have installed. Mirrors installProjection in service.go and the
	// streamingIterator.installPlan path in stream.go.
	var (
		keep           encoding.FieldFilter
		plan           *encoding.DecodePlan
		projectMapHint = len(schema.Fields)
	)
	if s.projectBuffered {
		needed := processing.NeededFields(req, schema, s.extensions)
		if !needed.IsWide() {
			size := needed.Len()
			if size > 0 && size < len(schema.Fields) {
				keep = func(name string) bool { return needed.Has(name) }
				projectMapHint = size
				retained := retainedFromFilter(schema, keep)
				if p, perr := schema.BuildDecodePlan(retained); perr == nil {
					plan = p
				}
			}
		}
	}

	pctx, cleanup, available, err := buildParallelDecodeContext(
		s, path, schema, plan, keep, projectMapHint,
	)
	if err != nil {
		// A real header/schema replay error inside buildParallelDecodeContext
		// is surfaced as a typed error; the serial path would re-read the
		// same bytes and hit the same failure, so bubble it up.
		return nil, false, err
	}
	if !available {
		// pctx unavailable: empty cohort, mmap rejected, RealPather
		// missed despite canParallelDecode's probe. Fall back to serial.
		return nil, false, nil
	}
	resolvedWorkers, ok := shouldFanOutDecode(workers, pctx.totalRecords)
	if !ok {
		// Post-pctx resolved worker count fell below 2 (e.g. cohort
		// totalRecords from buildParallelDecodeContext disagreed with
		// the header-fast count, or the cap collapsed). Release the
		// mmap and bail.
		if cleanup != nil {
			_ = cleanup()
		}
		return nil, false, nil
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	resp, err := s.reduceParallelBuffered(ctx, req, schema, pctx, resolvedWorkers)
	if err != nil {
		return nil, false, err
	}
	if resp.Metadata != nil {
		resp.Metadata.CohortFile = path
	}
	if err := s.buildAndApplyLabels(req, resp); err != nil {
		return nil, false, err
	}
	return resp, true, nil
}

// scanIterator is the internal interface satisfied by both the
// single-file streamingIterator and the multi-shard shardIter. It
// extends processing.RecordIterator with the auxiliary hooks Service
// needs (projection, reuse, error reporting, lifecycle). Callers
// constructed via newScanIter receive whichever implementation matches
// the cohort shape — shard archives route through shardIter, single
// files through streamingIterator. The processing layer sees only
// processing.RecordIterator and consumes both uniformly.
type scanIterator interface {
	processing.RecordIterator
	SetProjection(keep encoding.FieldFilter, size int)
	SetReuse(reuse bool)
	Err() error
	Close() error
}

// newScanIter dispatches on whether the cohort backs onto a shard
// archive (non-empty Shards) or a single .pulse file. The returned
// iterator carries the same semantics in both branches; the multi-
// shard variant walks shards in central-directory order and folds
// records through the same processing pipeline as a concatenated
// single-file cohort.
//
// The iterator is constructed against the cohort's own fs handle
// rather than the service's fs. For regular Open these are the
// same; for OpenAnchor the cohort carries an in-memory overlay so
// the streaming iterator can resolve the virtual `archive#shard`
// path back to the anchored shard bytes.
func (s *Service) newScanIter(cohort *Cohort, path string) scanIterator {
	fsys := cohort.fs
	if fsys == nil {
		fsys = s.fs.Fs()
	}
	if shards := cohort.Shards(); len(shards) > 0 {
		return newShardIter(fsys, path, cohort.Schema(), shards)
	}
	return newStreamingIterator(fsys, path, cohort.Schema())
}

// applyProjection installs a NeededFields-based projection filter on
// the scanning iterator when ProjectBufferedFields is enabled and the
// request's needed-field set is provably narrower than the schema. A
// wide set (extraction couldn't introspect) leaves the iterator on
// the full-decode path. Works uniformly for single-file and multi-
// shard iterators via the scanIterator interface.
func (s *Service) applyProjection(iter scanIterator, req *types.Request, schema *encoding.Schema) {
	if !s.projectBuffered || iter == nil || req == nil || schema == nil {
		return
	}
	s.installProjection(iter, req, schema)
}

// applyCrosstabProjection is the crosstab-specific variant of
// applyProjection that ignores the opts.ProjectBufferedFields gate.
// Crosstab always materializes the filter-passing record set; on wide
// cohorts the projection win is the difference between a working query
// and an out-of-memory event, so the optimisation is mandatory for the
// crosstab path even when the cohort-wide flag is off. Falls back to
// full decode silently when the needed-field set widens (extension
// operator without FieldInputs, malformed expression, etc.).
func (s *Service) applyCrosstabProjection(iter scanIterator, req *types.Request, schema *encoding.Schema) {
	if iter == nil || req == nil || schema == nil {
		return
	}
	s.installProjection(iter, req, schema)
}

// installProjection is the shared mechanics behind applyProjection
// and applyCrosstabProjection. The two callers diverge only on the
// gate that decides whether projection is attempted at all.
func (s *Service) installProjection(iter scanIterator, req *types.Request, schema *encoding.Schema) {
	needed := processing.NeededFields(req, schema, s.extensions)
	if needed.IsWide() {
		return
	}
	size := needed.Len()
	if size == 0 || size >= len(schema.Fields) {
		return
	}
	iter.SetProjection(func(name string) bool {
		return needed.Has(name)
	}, size)
}

// Compose executes multiple requests, returning a response for each.
//
// Before dispatching any slot, the validate phase synthesizes a default
// Label of `request_<index+1>` (1-based) on every slot whose caller-
// supplied Label is empty, against an in-memory clone of each *Request
// so the caller's pointer is not mutated. Two slots resolving to the
// same final Label are rejected with PULSE_COMPOSE_LABEL_COLLISION.
// Future Compose-only overlay kinds (E7) resolve sibling references by
// final Label so the names must be unique across the batch.
func (s *Service) Compose(ctx context.Context, composed *types.ComposedRequest) (*types.ComposedResponse, error) {
	if composed == nil || len(composed.Requests) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "composed request must contain at least one request")
	}

	requests, err := applyComposeLabelDefaults(composed)
	if err != nil {
		return nil, err
	}

	responses := make([]*types.Response, len(requests))
	for i, req := range requests {
		resp, err := s.Process(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("request %d: %w", i, err)
		}
		responses[i] = resp
	}

	// Compose-only overlay barrier (E7-S4). Runs AFTER every slot has
	// produced a finalised *Response and BEFORE the response is
	// returned to the caller. The serial path has no FailFast knob
	// (a slot error returned early above), so when we reach this
	// barrier every slot succeeded — the hook is unconditional.
	// Empty / nil req.Overlays short-circuits with no allocation
	// (byte-identical JSON vs pre-E7-S4 output).
	layers, warnings, err := s.applyComposeOverlays(ctx, composed, requests, responses)
	if err != nil {
		return nil, err
	}

	// Build the ComposedResponse wrapper. Overlay-free composes leave
	// `Overlays == nil` (no make-with-zero-len allocation, no
	// `overlays: []` empty-array marshalling) so the byte-identity
	// contract locked by TestComposedResponse_OverlayFreeByteIdentical
	// (types/types_test.go:1106) is preserved when the caller declared
	// no Compose-only overlays. The `Responses` slot is the SAME slice
	// that the pre-lift facade returned bare — no per-slot shape
	// change.
	out := &types.ComposedResponse{Responses: responses}
	if len(layers) > 0 {
		out.Overlays = layers
		// Distribute the flat warning slice into the matching layer's
		// `Warnings` slot. The dispatcher
		// (processing.ApplyComposeOverlays) stamps
		// `Details["overlay_index"]` on every warning it appends so
		// routing is a single lookup per warning; warnings missing the
		// key (defensive — should never happen with the current
		// dispatcher) fall back to layer 0 so they are still surfaced
		// rather than silently dropped. Layers with no warnings keep
		// `Warnings == nil` (no empty slice allocation) so the
		// `omitempty` JSON tag preserves byte identity for the
		// overlay-free path. Mirrors the E2-S1 chain-host convention
		// in service.applyChainOverlays.
		for i := range warnings {
			layerIdx := 0
			if warnings[i].Details != nil {
				if v, ok := warnings[i].Details["overlay_index"]; ok {
					if idx, ok := v.(int); ok && idx >= 0 && idx < len(out.Overlays) {
						layerIdx = idx
					}
				}
			}
			out.Overlays[layerIdx].Warnings = append(out.Overlays[layerIdx].Warnings, warnings[i])
		}
	}

	return out, nil
}

// resolveCohortPath builds the file path from a Cohort specification.
func resolveCohortPath(c *types.Cohort) string {
	if c.DataDir != "" {
		return c.DataDir + "/" + c.Filename
	}
	return c.Filename
}

// splitAnchorPath splits an archive-and-anchor path of the form
// "archive.pulse#entry.pulse" into (archivePath, entry, true). Returns
// ("", "", false) when no `#` is present. Resolves at the FIRST `#` —
// literal `#` in a filename is not supported in v1.
func splitAnchorPath(path string) (string, string, bool) {
	return SplitAnchorPath(path)
}

// SplitAnchorPath is the exported form of splitAnchorPath, used by the
// pulse facade (Predict, Inspect) so anchor paths resolve consistently
// across every entry point. See splitAnchorPath for semantics.
func SplitAnchorPath(path string) (string, string, bool) {
	for i := 0; i < len(path); i++ {
		if path[i] == '#' {
			return path[:i], path[i+1:], true
		}
	}
	return "", "", false
}

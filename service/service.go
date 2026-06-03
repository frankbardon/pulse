package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"

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

	data, err := afero.ReadFile(s.fs.Fs(), path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file: %s", path))
	}

	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04 {
		return s.openArchive(path, data)
	}

	r := bytes.NewReader(data)

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

// AskInput captures the per-call options the facade hands the service.
// Mirrors the public pulse.AskRequest shape but reads the cohort path
// from the embedded request's Cohort so the service does not duplicate
// the path-resolution dance.
type AskInput struct {
	// Request is the structured processing request. Required.
	Request *types.Request

	// OnInvalid controls behavior when predict reports the request as
	// invalid. "" / "abort" returns a SERVICE_VALIDATION error; "suggest"
	// returns a successful AskOutput with structured Suggestions populated.
	OnInvalid string

	// PredictOnly skips Process even when predict succeeds. Predict
	// always runs; the caller can read schema info, streamability,
	// suggestions, and defaults_applied from PredictResult.
	PredictOnly bool
}

// AskOutput is the service-level envelope returned by Service.Ask.
// The facade re-marshals into the public pulse.AskResponse — see pulse.go.
type AskOutput struct {
	// Predict is always populated when Ask returns without error.
	Predict *descriptor.PredictResult

	// Process is the execution result. Nil when PredictOnly=true,
	// nil when predict reported the request invalid (OnInvalid="suggest").
	Process *types.Response

	// Suggestions enumerates structured Fixup entries derived from every
	// predict error code's metadata template, de-duplicated by
	// (Code, Action). Empty when there were no errors or when
	// OnInvalid != "suggest".
	Suggestions []errors.Fixup

	// Errors / Warnings echo the predict envelope's entries so callers
	// can present them without re-running predict. Never nil — empty slices
	// when there are none.
	Errors   []*descriptor.EnvelopeEntry
	Warnings []*descriptor.EnvelopeEntry
}

// Ask is the unified pipeline that collapses inspect → predict → process
// into one call. It opens the cohort file, validates the request against
// the schema via Predict, and on success runs Process. The exposed
// pulse.Ask facade is a thin shim over this method.
//
// OnInvalid governs predict-invalid behavior:
//   - "" or "abort" — return a SERVICE_VALIDATION error wrapping the predict envelope.
//   - "suggest"     — return AskOutput with Suggestions populated; Process stays nil.
//
// PredictOnly skips execution even on a valid request (the caller's
// "what would happen if I ran this?" probe).
func (s *Service) Ask(ctx context.Context, in AskInput) (*AskOutput, error) {
	if in.Request == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "ask requires a request")
	}
	if in.Request.Cohort == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "ask requires a cohort")
	}

	mode := in.OnInvalid
	if mode == "" {
		mode = "abort"
	}
	if mode != "abort" && mode != "suggest" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			"ask: on_invalid must be \"abort\" or \"suggest\"",
			map[string]any{"on_invalid": in.OnInvalid},
		)
	}

	path := resolveCohortPath(in.Request.Cohort)

	data, err := afero.ReadFile(s.fs.Fs(), path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("ask: reading cohort file: %s", path))
	}

	env := descriptor.PredictFromBytes(data, in.Request, &descriptor.PredictOptions{Extensions: s.extensionsSnap})
	result, _ := env.Data.(*descriptor.PredictResult)
	if result == nil {
		return nil, errors.NewCodedError(errors.SERVICE_INTERNAL,
			"ask: predict returned no result")
	}

	out := &AskOutput{
		Predict:  result,
		Errors:   normalizedEntries(env.Errors),
		Warnings: normalizedEntries(env.Warnings),
	}

	if !result.Valid {
		if mode == "abort" {
			return nil, errors.NewCodedErrorWithDetails(
				errors.SERVICE_VALIDATION,
				"ask: request failed predict validation",
				map[string]any{
					"errors":   out.Errors,
					"warnings": out.Warnings,
				},
			)
		}
		out.Suggestions = materializeFixups(out.Errors, in.Request)
		return out, nil
	}

	if in.PredictOnly {
		return out, nil
	}

	resp, err := s.Process(ctx, in.Request)
	if err != nil {
		return nil, err
	}
	out.Process = resp
	return out, nil
}

// normalizedEntries returns a non-nil slice so JSON serializers emit
// `[]` rather than `null` for callers that consume the envelope shape.
func normalizedEntries(in []*descriptor.EnvelopeEntry) []*descriptor.EnvelopeEntry {
	if in == nil {
		return []*descriptor.EnvelopeEntry{}
	}
	return in
}

// materializeFixups walks the predict envelope's errors, looks up each
// code's structured fixup templates via errors.MetadataFor, and returns
// a de-duplicated slice sorted by (Code, Action, Hint) for stable output.
// Entries whose code is FixupNotApplicable contribute nothing.
func materializeFixups(entries []*descriptor.EnvelopeEntry, req *types.Request) []errors.Fixup {
	type key struct {
		code   string
		action errors.FixupAction
		hint   string
	}
	seen := make(map[key]struct{})
	var out []errors.Fixup
	for _, e := range entries {
		if e == nil {
			continue
		}
		fixups := errors.Code(e.Code).Fixup(req)
		for _, f := range fixups {
			k := key{code: e.Code, action: f.Action, hint: f.Hint}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Action != out[j].Action {
			return out[i].Action < out[j].Action
		}
		return out[i].Hint < out[j].Hint
	})
	return out
}

// Compose executes multiple requests, returning a response for each.
func (s *Service) Compose(ctx context.Context, composed *types.ComposedRequest) ([]*types.Response, error) {
	if composed == nil || len(composed.Requests) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "composed request must contain at least one request")
	}

	responses := make([]*types.Response, len(composed.Requests))
	for i, req := range composed.Requests {
		resp, err := s.Process(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("request %d: %w", i, err)
		}
		responses[i] = resp
	}

	return responses, nil
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

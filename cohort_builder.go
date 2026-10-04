package pulse

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	pio "github.com/frankbardon/pulse/io"
)

// CohortBuilderOptions configures Pulse.NewCohortBuilder.
type CohortBuilderOptions struct {
	// Strict turns every PULSE_FIELD_DESCRIPTION_LOW_QUALITY finding
	// into a fatal error of the same code, raised by NewCohortBuilder
	// before anything is written (as `--strict` does for import). It
	// makes the group viability gate strict too: PULSE_GROUP_TOO_NARROW
	// fails NewCohortBuilder, PULSE_DEDUP_LOW_RATIO fails Close, and
	// neither leaves anything on disk.
	Strict bool
	// Overwrite lets the build replace an existing cohort at the
	// target. Without it an existing target is refused with
	// SERVICE_VALIDATION, at NewCohortBuilder and again at Close.
	Overwrite bool
	// Groups declares parent groups (key + the members it determines),
	// exactly as `pulse import --group` / pio.ImportJob.Groups do: the
	// same PULSE_GROUP_* declaration checks (at NewCohortBuilder), the
	// same viability gate — a group no wider than its index is dropped
	// with PULSE_GROUP_TOO_NARROW and its members stay row fields; a
	// group below RatioFloor rows per tuple is written with
	// PULSE_DEDUP_LOW_RATIO — and the same encoder. A key violation
	// (PULSE_GROUP_MEMBER_NOT_CONSTANT) surfaces at Close, whose
	// details carry source_row: the 1-based Append call. The format
	// version follows: 0x02 iff a group is written.
	Groups []pio.GroupDecl
	// ElideConstants stores every field holding one value on every row
	// once, as `pulse import --elide-constants` does, decided by a full
	// pass over the appended rows at Close.
	ElideConstants bool
	// RatioFloor is the rows-per-distinct-tuple floor below which a
	// declared group draws PULSE_DEDUP_LOW_RATIO (import's
	// --dedup-ratio-floor); 0 selects the default (2).
	RatioFloor float64
	// Shards, when set, makes Close publish a shard archive at the
	// target instead of a single-file cohort. See ShardSplit.
	Shards *ShardSplit
}

// ShardSplit makes a CohortBuilder write a NEW shard archive, split
// automatically: the appended rows are cut, in order, into consecutive
// shards of MaxRecords rows — part-00001.pulse, part-00002.pulse, … —
// the last one partial (an empty build is one empty shard), and Close
// publishes them with ONE Pulse.CreateShardArchive call, so the archive
// appears whole or not at all. Groups and ElideConstants are decided
// ONCE over all rows and applied to every shard: each shard carries the
// same schema, dictionaries and parent-group layout, and a group the
// gate drops is dropped in every shard. MaxRecords must be positive
// (SERVICE_VALIDATION otherwise); a build holds at most 65,535 shards
// (the archive's shard-count limit), and the row that would open one
// more is refused with SERVICE_VALIDATION.
type ShardSplit struct {
	// MaxRecords is the number of rows per shard.
	MaxRecords int
}

// CohortBuildResult is what a successful CohortBuilder.Close wrote.
type CohortBuildResult struct {
	// Target is the cohort path written, as given to NewCohortBuilder.
	Target string `json:"target"`
	// Records is the number of rows written: the accepted Append calls.
	Records int64 `json:"records"`
	// FormatVersion is the cohort header's format version byte
	// (encoding.FormatVersionV1 for an ungrouped cohort). It is derived
	// from the schema's content, never chosen.
	FormatVersion byte `json:"cohort_format_version"`
	// Schema is the schema written: the caller's fields with the layout
	// recomputed and the final dictionaries (pre-seeded entries first,
	// then labels in first-seen order).
	Schema *encoding.Schema `json:"-"`
	// Warnings are the non-fatal coded findings, in order: one
	// PULSE_FIELD_DESCRIPTION_LOW_QUALITY per field with a weak
	// description, the group gate's PULSE_GROUP_TOO_NARROW then
	// PULSE_DEDUP_LOW_RATIO findings (each fatal under Strict instead),
	// then, for a sharded build, CreateShardArchive's warnings unchanged
	// (PULSE_SHARD_*, with their details), then a failure to enumerate
	// the sidecars an overwrite invalidated.
	Warnings []*errors.CodedError `json:"warnings,omitempty"`
	// Groups describes every declared group in declaration order — its
	// verdict and the figures behind it — as an import report does.
	// Empty when no group was declared.
	Groups []pio.GroupReport `json:"groups,omitempty"`
	// ElidedConstants names the fields ElideConstants stored once, in
	// schema order. Empty when nothing was elided.
	ElidedConstants []string `json:"elided_constants,omitempty"`
	// Shards names a sharded build's (ShardSplit) archive entries, in
	// archive order. Empty for a single-file build.
	Shards []string `json:"shards,omitempty"`
	// InvalidatedSidecars names each sidecar beside the target that an
	// Overwrite of an existing cohort invalidated — the point-lookup
	// index and its manifest, the SPSS metadata sidecar — with the
	// command that rebuilds it, exactly as Pulse.Dedup reports them.
	// Nothing is rebuilt: each sidecar self-invalidates through its own
	// fingerprint. Empty when no cohort was replaced.
	InvalidatedSidecars []StaleSidecar `json:"invalidated_sidecars,omitempty"`
}

// CohortBuilder writes a new cohort from rows appended one at a time.
// Obtain one from Pulse.NewCohortBuilder and finish it with Close (or
// discard it with Abort). It is not safe for concurrent use.
type CohortBuilder struct {
	p     *Pulse
	ctx   context.Context
	inner *iio.CohortBuild
}

// NewCohortBuilder starts a single-file cohort at target — ungrouped
// (format 0x01) unless opts declares a group that survives the gate or
// elides constants (0x02) — or, with opts.Shards, a new shard archive
// (see ShardSplit), resolved against the instance filesystem
// (Options.FS / Options.DataDir), with the given schema.
//
// The schema is the contract, as an explicit-schema import's is: field
// order, names, types, nullability, descriptions, decimal precision /
// scale and optional pre-seeded dictionaries (Field.Dictionary). The
// layout fields — ByteOffset, BitPosition, CsvColumnIdx — are
// RECOMPUTED from field order and types, so caller values are ignored,
// and the caller's schema and dictionaries are never mutated.
// Dictionaries grow from the pre-seeded entries in first-seen order;
// the declared rung is a ceiling, never auto-promoted.
//
// The cohort is byte-identical to an import of the same rows with the
// same schema (pio.ImportJob with Schema set, and the same Groups /
// ElideConstants): the builder runs the import path's own dictionary
// assignment, row encoding, group gate and encoder. Shared checks are
// the description length and the row conversion; the schema-shape
// refusals below are the builder's own (import trusts its schema).
//
// Refusals: SERVICE_VALIDATION for an empty, anchored
// (`archive.pulse#shard.pulse`) or `.zst` target, an existing target
// without Overwrite, and a malformed schema (no fields, empty or
// duplicate names, unknown type, bad decimal precision / scale, a
// pre-seeded dictionary longer than its rung, parent groups in the
// schema); PULSE_IMPORT_DESCRIPTION_TOO_LONG for a description past
// 1000 bytes; PULSE_FIELD_DESCRIPTION_LOW_QUALITY under Strict; a bad
// group declaration (PULSE_GROUP_*) and, under Strict, a too-narrow
// group (PULSE_GROUP_TOO_NARROW); SERVICE_VALIDATION for a ShardSplit
// whose MaxRecords is not positive.
func (p *Pulse) NewCohortBuilder(ctx context.Context, target string, schema encoding.Schema, opts CohortBuilderOptions) (*CohortBuilder, error) {
	bo := iio.CohortBuildOptions{
		Strict:         opts.Strict,
		Overwrite:      opts.Overwrite,
		Groups:         opts.Groups,
		ElideConstants: opts.ElideConstants,
		RatioFloor:     opts.RatioFloor,
	}
	if opts.Shards != nil {
		if opts.Shards.MaxRecords <= 0 {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"ShardSplit.MaxRecords must be positive",
				map[string]any{"target": target, "reason": "invalid_shard_split", "max_records": opts.Shards.MaxRecords})
		}
		bo.ShardMaxRecords = opts.Shards.MaxRecords
		bo.Archive = p.archiveShards
	}
	inner, err := iio.NewCohortBuild(ctx, p.fsys, target, &schema, bo)
	if err != nil {
		return nil, err
	}
	return &CohortBuilder{p: p, ctx: ctx, inner: inner}, nil
}

// Append adds one row: one value per schema field, in field order,
// under the CohortRow type mapping (a row read by CohortReader.RecordAt
// is accepted unchanged). No coercion is applied.
//
// A row that does not fit is rejected with PULSE_IMPORT_ROW_ERROR
// whose details carry row (the 1-based Append call), field and reason
// — "arity" (wrong value count), "type" (wrong Go type; details also
// carry expected and got), "null" (nil in a non-nullable field) or
// "overflow" (an integer past its type, a decimal past its precision,
// a new label past the dictionary rung). A rejected row leaves no
// trace — nothing is written and no dictionary entry it introduced is
// kept — and the builder stays usable. A filesystem failure while
// spooling is returned by every later call.
func (b *CohortBuilder) Append(row CohortRow) error {
	return b.inner.Append(row)
}

// Close writes the cohort atomically — with Groups or ElideConstants
// it first makes the full pass (constant detection, group encoding
// into a second spool, the ratio gate) — then the schema block and
// every spooled row, into a temp file beside the target that is fsynced and
// renamed over it — and returns what was written. With Shards it
// instead cuts the rows into shard files staged in a directory beside
// the target and publishes them with one CreateShardArchive call (its
// errors are Close's, unchanged); the staging directory is removed
// whatever the outcome. On any failure
// nothing is left at the target (an overwritten cohort stays as it
// was) and the spool is removed. The builder is finished either way: a
// second Close returns SERVICE_RESOURCE.
func (b *CohortBuilder) Close() (*CohortBuildResult, error) {
	rep, err := b.inner.Close()
	if err != nil {
		return nil, err
	}
	res := &CohortBuildResult{
		Target:          rep.Target,
		Records:         rep.Records,
		FormatVersion:   rep.FormatVersion,
		Schema:          rep.Schema,
		Warnings:        append([]*errors.CodedError(nil), rep.Warnings...),
		Groups:          rep.Groups,
		ElidedConstants: rep.ElidedConstants,
		Shards:          rep.Shards,
	}
	if rep.Replaced {
		sidecars, scanErr := b.p.InvalidatedSidecars(b.ctx, rep.Target)
		res.InvalidatedSidecars = sidecars
		if scanErr != nil {
			res.Warnings = append(res.Warnings, sidecarScanWarning(rep.Target, scanErr))
		}
	}
	return res, nil
}

// Abort discards the build: the spool is removed and the target is
// left untouched. It is idempotent and a no-op after Close.
func (b *CohortBuilder) Abort() error {
	return b.inner.Abort()
}

// archiveShards publishes a sharded build's staged shard files through
// CreateShardArchive and lifts its warnings, unchanged, into coded
// findings.
func (p *Pulse) archiveShards(ctx context.Context, target string, shardPaths []string) ([]*errors.CodedError, error) {
	res, err := p.CreateShardArchive(ctx, target, shardPaths)
	if err != nil {
		return nil, err
	}
	return cohesionFindings(res.Warnings), nil
}

// cohesionFindings converts shard-archive warnings to coded findings,
// code, message and details intact.
func cohesionFindings(ws []CohesionWarning) []*errors.CodedError {
	if len(ws) == 0 {
		return nil
	}
	out := make([]*errors.CodedError, len(ws))
	for i, w := range ws {
		out[i] = errors.NewCodedErrorWithDetails(errors.Code(w.Code), w.Message, w.Details)
	}
	return out
}

package pulse

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
)

// CohortBuilderOptions configures Pulse.NewCohortBuilder.
type CohortBuilderOptions struct {
	// Strict turns every PULSE_FIELD_DESCRIPTION_LOW_QUALITY finding
	// into a fatal error of the same code, raised by NewCohortBuilder
	// before anything is written (as `--strict` does for import).
	Strict bool
	// Overwrite lets the build replace an existing cohort at the
	// target. Without it an existing target is refused with
	// SERVICE_VALIDATION, at NewCohortBuilder and again at Close.
	Overwrite bool
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
	// description (fatal under Strict instead), then a failure to
	// enumerate the sidecars an overwrite invalidated.
	Warnings []*errors.CodedError `json:"warnings,omitempty"`
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

// NewCohortBuilder starts a single-file, ungrouped (format 0x01)
// cohort at target, resolved against the instance filesystem
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
// same schema (pio.ImportJob with Schema set): the builder runs the
// import path's own checks, dictionary assignment and row encoding.
//
// Refusals: SERVICE_VALIDATION for an empty, anchored
// (`archive.pulse#shard.pulse`) or `.zst` target, an existing target
// without Overwrite, and a malformed schema (no fields, empty or
// duplicate names, unknown type, bad decimal precision / scale, a
// pre-seeded dictionary longer than its rung, parent groups in the
// schema); PULSE_IMPORT_DESCRIPTION_TOO_LONG for a description past
// 1000 bytes; PULSE_FIELD_DESCRIPTION_LOW_QUALITY under Strict.
func (p *Pulse) NewCohortBuilder(ctx context.Context, target string, schema encoding.Schema, opts CohortBuilderOptions) (*CohortBuilder, error) {
	inner, err := iio.NewCohortBuild(ctx, p.fsys, target, &schema, iio.CohortBuildOptions{
		Strict:    opts.Strict,
		Overwrite: opts.Overwrite,
	})
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

// Close writes the cohort atomically — the schema block, then every
// spooled row, into a temp file beside the target that is fsynced and
// renamed over it — and returns what was written. On any failure
// nothing is left at the target (an overwritten cohort stays as it
// was) and the spool is removed. The builder is finished either way: a
// second Close returns SERVICE_RESOURCE.
func (b *CohortBuilder) Close() (*CohortBuildResult, error) {
	rep, err := b.inner.Close()
	if err != nil {
		return nil, err
	}
	res := &CohortBuildResult{
		Target:        rep.Target,
		Records:       rep.Records,
		FormatVersion: rep.FormatVersion,
		Schema:        rep.Schema,
		Warnings:      append([]*errors.CodedError(nil), rep.Warnings...),
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

package pulse

import (
	"context"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/service"
	"github.com/frankbardon/pulse/observe"
)

// MaterializeOptions configures Pulse.Materialize.
type MaterializeOptions struct {
	// Overwrite lets the run replace an existing cohort at the target
	// (as CohortBuilderOptions.Overwrite). Without it an existing
	// target is refused with SERVICE_VALIDATION before any record is
	// read.
	Overwrite bool
	// Strict turns every PULSE_FIELD_DESCRIPTION_LOW_QUALITY finding on
	// the output schema into a fatal error of the same code, raised
	// before any record is read (as CohortBuilderOptions.Strict). The
	// generated derived-column descriptions always pass; a source field
	// with an empty or placeholder description does not.
	Strict bool
}

// MaterializeResult is what a successful Pulse.Materialize wrote.
type MaterializeResult struct {
	// Target is the cohort path written.
	Target string `json:"target"`
	// Records is the number of rows written: one per filter-passing
	// source record.
	Records int64 `json:"records"`
	// DerivedColumns names the appended columns in output order —
	// feature outputs, then attributes, then windows.
	DerivedColumns []string `json:"derived_columns"`
	// Warnings are the non-fatal coded findings: the cohort write's
	// (PULSE_FIELD_DESCRIPTION_LOW_QUALITY per weak source
	// description), then one PULSE_MATERIALIZE_SIDECARS_INVALIDATED
	// when an overwrite invalidated sidecars beside the target, then a
	// failure to enumerate them.
	Warnings []*errors.CodedError `json:"warnings,omitempty"`
	// InvalidatedSidecars names each sidecar beside the target that an
	// Overwrite invalidated, with the command that rebuilds it (as
	// CohortBuildResult.InvalidatedSidecars). Empty when nothing was
	// replaced.
	InvalidatedSidecars []StaleSidecar `json:"invalidated_sidecars,omitempty"`
}

// Materialize writes a NEW single-file cohort at out holding every
// filter-passing record of req's cohort — the source columns verbatim,
// then one nullable f64 column per derived output of req (features,
// attributes, windows) — so the derived columns become ordinary fields
// any later request can read, filter, group or join on.
//
// req may carry only features, filterers, attributes, windows, sort,
// weight and time_zone (plus cohort, label and disable_components). It runs
// Process's own pre-flight — the instance slot gate, smart defaults,
// zones, the field-reference rule, the limits pre-flight — so a request
// Process refuses is refused here with the same error; then:
// PULSE_MATERIALIZE_SLOT_UNSUPPORTED (details.slot) for any other slot,
// PULSE_MATERIALIZE_NO_DERIVED when no feature, attribute or window is
// given, and PULSE_MATERIALIZE_OUTPUT_UNDECLARED for a feature whose
// output columns cannot be named before the run (an embedder-registered
// feature). Every one fails before anything is created at out.
//
// The pipeline is Process's record-level half over a full decode:
// features over every record, filters, attributes over the survivors,
// windows over the survivors in source order. Output rows follow source
// order (an archive's records in shard order) unless req carries sort:
// then they follow the order Request.Sort gives Process's row set —
// the engine's own comparator, ties kept in source order, nulls last in
// both directions — on keys naming source fields or derived columns.
// Sorting moves rows, never values: each window has already run under
// its own partition_by / order_by. Each source cell is the exact stored
// value (Cohort.Reader). The output schema is the
// source's logical schema unchanged — order, types, nullability,
// dictionaries, descriptions — followed by the derived columns in
// pipeline order (declaration order within features, attributes,
// windows; a multi-output feature contributes every output), each
// nullable f64 with a generated description naming its operator,
// source field, params (a window's partition, order and — when the
// operator reads it — frame) and source cohort. An integer window output
// (row number, rank) is stored as f64; a value that is not a number
// fails the run with PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC.
//
// The output is always an ungrouped (format 0x01) single file, written
// atomically through NewCohortBuilder: any failure — a refusal, a limit
// breach (PULSE_LIMIT_EXCEEDED), a write error, cancellation — leaves
// nothing at out (an overwritten cohort stays as it was). Sidecars of
// the source are not carried; a managed-import source slides its
// expiry, as a Process read does. An anchored target
// (`archive.pulse#name.pulse`) is refused with SERVICE_VALIDATION.
func (p *Pulse) Materialize(ctx context.Context, req *Request, out string, opts MaterializeOptions) (*MaterializeResult, error) {
	return observed(p, ctx, requestOp(observe.OpMaterialize, req), func(ctx context.Context) (*MaterializeResult, error) {
		return p.materialize(ctx, req, out, opts)
	})
}

func (p *Pulse) materialize(ctx context.Context, req *Request, out string, opts MaterializeOptions) (*MaterializeResult, error) {
	if strings.Contains(out, "#") {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"materialize writes a single-file cohort; an anchored archive#shard target is not accepted",
			map[string]any{"target": out, "reason": "anchored_target"})
	}
	sink := &materializeSink{p: p, ctx: ctx, out: out, opts: opts}
	err := p.svc.Materialize(ctx, req, sink)
	if sink.reader != nil {
		if cerr := sink.reader.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}
	if err != nil {
		if sink.builder != nil {
			_ = sink.builder.Abort()
		}
		return nil, err
	}
	built, err := sink.builder.Close()
	if err != nil {
		return nil, err
	}
	// A read of a managed import slides its expiry, as Process does.
	p.touchManaged(ctx, resolveCohortPath(req.Cohort))
	res := &MaterializeResult{
		Target:              built.Target,
		Records:             built.Records,
		DerivedColumns:      sink.names,
		Warnings:            built.Warnings,
		InvalidatedSidecars: built.InvalidatedSidecars,
	}
	if len(built.InvalidatedSidecars) > 0 {
		paths := make([]string, len(built.InvalidatedSidecars))
		for i, s := range built.InvalidatedSidecars {
			paths[i] = s.Path
		}
		res.Warnings = append(res.Warnings, errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_SIDECARS_INVALIDATED,
			"the overwritten cohort's sidecars are now stale; rebuild them (invalidated_sidecars names each with its rebuild command)",
			map[string]any{"target": built.Target, "sidecars": paths}))
	}
	return res, nil
}

// materializeSink is the facade half of a materialize run: Begin opens
// the source's exact-value reader and the cohort builder over the
// output schema, Row appends source cells + derived values.
type materializeSink struct {
	p       *Pulse
	ctx     context.Context
	out     string
	opts    MaterializeOptions
	reader  *service.CohortRecordReader
	builder *CohortBuilder
	names   []string
}

func (m *materializeSink) Begin(cohort *service.Cohort, cols []descx.MaterializeColumn) error {
	r, err := cohort.OpenRecordReader()
	if err != nil {
		return err
	}
	m.reader = r
	// Schema.Fields is the LOGICAL field list whatever the source's
	// layout; only Fields is carried, so a grouped source's parent
	// groups (and a shard archive's layout) never reach the output.
	src := r.Schema()
	fields := make([]encoding.Field, 0, len(src.Fields)+len(cols))
	fields = append(fields, src.Fields...)
	m.names = make([]string, len(cols))
	for i, c := range cols {
		m.names[i] = c.Name
		fields = append(fields, encoding.Field{
			Name:        c.Name,
			Type:        encoding.FieldTypeF64,
			Nullable:    true,
			Description: c.Description,
		})
	}
	b, err := m.p.NewCohortBuilder(m.ctx, m.out, encoding.Schema{Fields: fields},
		CohortBuilderOptions{Strict: m.opts.Strict, Overwrite: m.opts.Overwrite})
	if err != nil {
		return err
	}
	m.builder = b
	return nil
}

func (m *materializeSink) Row(source int64, derived []any) error {
	row, err := m.reader.RecordAt(source)
	if err != nil {
		return err
	}
	return m.builder.Append(append(CohortRow(row), derived...))
}

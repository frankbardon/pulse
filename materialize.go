package pulse

import (
	"context"
	"math"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/service"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
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
	// Warnings are the non-fatal coded findings: the derived columns'
	// (a fitted vector attribute's PULSE_MATRIX_SINGULAR /
	// PULSE_MATRIX_INSUFFICIENT_N fit, whose column is then all null),
	// the cohort write's (PULSE_FIELD_DESCRIPTION_LOW_QUALITY per weak
	// source description), then a PULSE_SPSS_SIDECAR_STALE / _INVALID when
	// the source's SPSS sidecar could not be carried, then one
	// PULSE_MATERIALIZE_SIDECARS_INVALIDATED
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
// attributes, windows; an ATTR_PREVIOUS_STATE output instead takes its
// state field's categorical_* / packed_bool type and dictionary) — so the derived columns become ordinary fields
// any later request can read, filter, group or join on.
//
// req may carry only features, filterers, attributes, windows, sort,
// weight, time_zone and output (plus cohort, label and
// disable_components). It runs
// Process's own pre-flight — the instance slot gate, smart defaults,
// zones, the field-reference rule, the limits pre-flight — so a request
// Process refuses is refused here with the same error; then:
// PULSE_MATERIALIZE_SLOT_UNSUPPORTED (details.slot) for any other slot,
// PULSE_MATERIALIZE_NO_DERIVED when no feature, attribute or window is
// given and no output block either, the output block's own refusals
// (PULSE_MATERIALIZE_OUTPUT_INVALID / _UNKNOWN_COLUMN / _DUPLICATE),
// and PULSE_MATERIALIZE_OUTPUT_UNDECLARED for a feature whose
// output columns cannot be named before the run (an embedder-registered
// feature). Every one fails before anything is created at out.
//
// The pipeline is Process's record-level half over a full decode:
// features over every record, ATTR_PREVIOUS_STATE over every record,
// filters, the other attributes over the survivors,
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
// nullable f64 (a previous-state column: nullable, of its state
// field's type, carrying that field's dictionary, its cells the
// previous states' labels / flags) with a generated description naming its operator,
// source field, params (a window's partition, order and — when the
// operator reads it — frame) and source cohort. An integer window output
// (row number, rank) is stored as f64; a value that is not a number
// fails the run with PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC.
//
// req.Output (types.MaterializeOutput) replaces that layout, LAST —
// after every slot above has read the full schema: `columns` writes
// exactly the listed columns in list order (each a source field or a
// derived column, renamed by `as`, its description / nullability
// overridable, every omitted key inherited), `exclude` drops the named
// ones. A null in a column the layout declares non-nullable fails the
// run with PULSE_MATERIALIZE_OUTPUT_NULL (details column + row). With
// an output block a request needs no derived column (a pure reshape),
// and the source's SPSS sidecar is not carried (one
// PULSE_SPSS_SIDECAR_INVALID warning). Absent, the output is the layout
// above, byte for byte.
//
// The output is always an ungrouped (format 0x01) single file, written
// atomically through NewCohortBuilder: any failure — a refusal, a limit
// breach (PULSE_LIMIT_EXCEEDED), a write error, cancellation — leaves
// nothing at out (an overwritten cohort stays as it was). The source's
// SPSS metadata sidecar is carried — rewritten with a plain numeric
// variable per derived column (a previous-state column copies its state
// variable's codes and value labels) and re-fingerprinted, so value labels
// survive to a .sav export; a stale or unreadable one is not, with one
// warning carrying its code. No other sidecar is carried; a
// managed-import source slides its expiry, as a Process read does. An anchored target
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
	var source string
	if req != nil && req.Cohort != nil {
		source = resolveCohortPath(req.Cohort)
	}
	carry, carryWarn := p.prepareSidecarCarry(source)
	if carry != nil && req.Output != nil {
		// The carry rewrites the sidecar against the source's columns;
		// an output block may drop, rename, duplicate, reorder or
		// redescribe them, which it cannot follow — so the reshaped
		// cohort is written without one, never with a stale one.
		carry, carryWarn = nil, errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_SIDECAR_INVALID,
			"the source's SPSS metadata sidecar is not carried onto a cohort whose columns an output block lays out; "+
				"it is written without one (a .sav export of it synthesises its dictionary)",
			map[string]any{errors.DetailSPSSCohort: source, errors.DetailSPSSSidecar: spss.SidecarPath(source)})
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
	carried := p.carrySidecar(carry, carryWarn, built, nil, sink.mirrors())
	// A read of a managed import slides its expiry, as Process does.
	p.touchManaged(ctx, source)
	res := &MaterializeResult{
		Target:              built.Target,
		Records:             built.Records,
		DerivedColumns:      sink.names,
		Warnings:            append(append(append([]*errors.CodedError(nil), sink.warns...), built.Warnings...), carried...),
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
	// names are the written columns holding derived values, under their
	// output names, in output order (MaterializeResult.DerivedColumns).
	names []string
	// layout is the written cohort's columns in output order, each
	// reading a source field or a derived column; rows counts the rows
	// appended (a nullable: false refusal names the row).
	layout []descx.MaterializeOutputField
	rows   int64
	// warns are the run's attribute warnings (service.MaterializeWarner).
	warns []*errors.CodedError
	// inherited holds, per derived column, the field shape it inherits
	// (nil for a nullable f64) and sources the source field whose SPSS
	// description it carries; observed marks, per inheriting column and
	// dictionary code, whether a written row carried that code — the
	// carried variable's Observed flags (spss.Mirror).
	inherited []*encoding.Field
	sources   []string
	observed  [][]bool
}

// mirrors maps each inheriting column to its SPSS mirror: the source
// field it holds the codes of and the codes the written rows carried.
// Nil when no column inherits.
func (m *materializeSink) mirrors() map[string]spss.Mirror {
	var out map[string]spss.Mirror
	for i, f := range m.inherited {
		if f == nil {
			continue
		}
		if out == nil {
			out = map[string]spss.Mirror{}
		}
		out[f.Name] = spss.Mirror{Source: m.sources[i], Observed: m.observed[i]}
	}
	return out
}

// Warn records the run's attribute warnings (a fitted vector
// attribute's singular or insufficient fit) as coded warnings.
func (m *materializeSink) Warn(ws []*types.ResponseWarning) {
	for _, w := range ws {
		m.warns = append(m.warns, errors.NewCodedErrorWithDetails(errors.Code(w.Code), w.Message, w.Details))
	}
}

func (m *materializeSink) Begin(cohort *service.Cohort, cols []descx.MaterializeColumn, layout []descx.MaterializeOutputField) error {
	r, err := cohort.OpenRecordReader()
	if err != nil {
		return err
	}
	m.reader = r
	m.inherited = make([]*encoding.Field, len(cols))
	m.sources = make([]string, len(cols))
	m.observed = make([][]bool, len(cols))
	for i, c := range cols {
		if c.Inherits != "" {
			// A previous-state column: its state field's type and
			// dictionary, its values that field's codes.
			f := encoding.Field{Name: c.Name, Type: c.Type, Nullable: true, Description: c.Description, Dictionary: c.Dictionary}
			m.inherited[i], m.sources[i] = &f, c.Inherits
		}
	}
	// The layout's fields are the source's LOGICAL fields (a grouped
	// source's parent groups and a shard archive's layout never reach
	// the output) and the derived columns, as the output block arranges
	// them — without one, every source field verbatim then every
	// derived column.
	m.layout = layout
	fields := make([]encoding.Field, len(layout))
	for i, col := range layout {
		fields[i] = col.Field
		if col.Derived >= 0 {
			m.names = append(m.names, col.Field.Name)
		}
	}
	if m.names == nil {
		m.names = []string{}
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
	for i, f := range m.inherited {
		if f == nil {
			continue
		}
		code := derived[i]
		v, err := inheritedCell(f, code)
		if err != nil {
			return err
		}
		if v != nil {
			// A non-nil cell is a valid code (inheritedCell): mark it.
			id := int(code.(float64))
			if id >= len(m.observed[i]) {
				m.observed[i] = append(m.observed[i], make([]bool, id+1-len(m.observed[i]))...)
			}
			m.observed[i][id] = true
		}
		derived[i] = v
	}
	out := make(CohortRow, len(m.layout))
	for i, col := range m.layout {
		var v any
		if col.Source >= 0 {
			v = row[col.Source]
		} else {
			v = derived[col.Derived]
		}
		if v == nil && !col.Field.Nullable {
			return descx.MaterializeOutputNull(col.Field.Name, m.rows)
		}
		out[i] = v
	}
	m.rows++
	return m.builder.Append(out)
}

// inheritedCell turns one inherited column's derived value — its source
// field's code as a float64, or nil — into the CohortRow value of f's
// type: the dictionary label (categorical_*) or the flag (packed_bool).
// A NaN is null; a code outside the dictionary is an invariant
// violation (PROCESSING_INTERNAL), never a silently wrong label.
func inheritedCell(f *encoding.Field, v any) (any, error) {
	code, ok := v.(float64)
	if !ok || math.IsNaN(code) {
		return nil, nil
	}
	if f.Type == encoding.FieldTypePackedBool {
		return code != 0, nil
	}
	if f.Dictionary != nil && code >= 0 && code == math.Trunc(code) && code < float64(f.Dictionary.Count()) {
		return f.Dictionary.Resolve(uint32(code)), nil
	}
	return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		"derived column "+f.Name+" holds a code outside its inherited dictionary",
		map[string]any{"column": f.Name, "code": code})
}

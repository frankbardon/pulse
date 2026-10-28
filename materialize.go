package pulse

import (
	"context"
	"math"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fieldcast"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/service"
	"github.com/frankbardon/pulse/internal/temporal"
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
	// one PULSE_MATERIALIZE_CAST_NULLED per output column whose
	// on_cast_error: "null" cast nulled a value,
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
// run with PULSE_MATERIALIZE_OUTPUT_NULL (details column + row). A
// column's `type` retypes it (MaterializeOutputColumn.Type: the
// numeric / packed_bool / date-family / set-widen / categorical matrix,
// exact or failing; date ↔ datetime in the request time_zone, else
// Options.DefaultTimeZone, else UTC; a categorical rung change keeps
// its dictionary, a categorical → numeric cast parses each label, a
// numeric → categorical cast stringifies each value into a dictionary
// built in first-seen output order): an illegal pair, or a categorical
// narrowed below its dictionary's size, is
// PULSE_MATERIALIZE_CAST_UNSUPPORTED before anything is read, a stored
// value that does not fit fails the run with
// PULSE_MATERIALIZE_CAST_FAILED (details column + row + value + reason)
// unless the column says on_cast_error: "null", which writes a null and
// warns once with the count; source integers (u64 past 2^53 included)
// never route through float64. A categorical column's `recode`
// (types.MaterializeRecode) rewrites its dictionary first — relabel,
// merge, map to null (a nullable column only), `order` — and the cast
// reads the recoded labels; a map key the source dictionary lacks is
// PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL before anything is read, and a
// recoded column's SPSS variable is rebuilt like a cast one's. A
// numeric or date-family column's `bucket` (types.MaterializeBucket:
// inline ranges XOR a registered range_table, compiled by the shared
// range pass — numeric half-open, dates inclusive, a datetime by its
// local day in the cast zone) writes it as the categorical_* of its
// range labels in declared order before any cast; a value outside every
// range follows on_cast_error (reason no_range), and its SPSS variable
// is rebuilt. With
// an output block a request needs no derived column (a pure reshape),
// and the source's SPSS sidecar is carried through the layout (a
// dropped column loses its variable, a renamed or duplicated one keeps
// its codes and value labels under its new name; one it cannot describe
// leaves the cohort without a sidecar, with one warning). Absent, the
// output is the layout above, byte for byte.
//
// The output is always an ungrouped single file — format 0x01, or 0x02
// when it keeps a vec_* field, whose VECTORS entry (dim, kind, scale,
// labels) follows it to its output position — written
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

// PredictMaterialize validates a Materialize request without executing
// it: the source cohort's header and schema only, never a record (a
// shard archive is judged on its canonical schema, an anchored shard on
// its own entry, as Predict reads them). Nothing is written anywhere.
//
// The envelope's Data is a *descriptor.MaterializePredictResult: the
// output schema the run writes (each column's name, type, nullability,
// description, dictionary and the column it reads), the derived column
// names, and the data-dependent risks — the checks only the records
// can settle (a cast a stored value can fail, coded by its
// on_cast_error; a `nullable: false` column over a column that may hold
// a null). Every static refusal the run raises is a coded env.Errors
// entry with the run's code and details, Valid false: the slot gates
// (the instance's, then PULSE_MATERIALIZE_SLOT_UNSUPPORTED),
// PULSE_MATERIALIZE_NO_DERIVED, Process's pre-flight and operator
// checks, PULSE_MATERIALIZE_OUTPUT_UNDECLARED, the output block's
// refusals (an illegal cast pair, PULSE_MATERIALIZE_CAST_UNSUPPORTED,
// included) and an over-long written description — the run's pre-flight
// shares the one plan call (descx.MaterializePlan), so the two cannot
// drift. A weak description of the written schema is a
// PULSE_FIELD_DESCRIPTION_LOW_QUALITY warning, as the run warns.
//
// A returned error is a cancelled ctx, a nil request or cohort
// (SERVICE_VALIDATION), or a cohort that cannot be read (DATA_FILE;
// ENCODING_INVALID for a malformed header or schema). Options.Strict
// promotes every warning into an error; Options.EchoRequest sets
// env.Request to the item-expanded request. Reports as a `predict`
// operation and slides a managed import's expiry, as Predict does.
func (p *Pulse) PredictMaterialize(ctx context.Context, req *Request) (*descriptor.Envelope, error) {
	return observed(p, ctx, requestOp(observe.OpPredict, req), func(ctx context.Context) (*descriptor.Envelope, error) {
		if req == nil {
			return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "predict: materialize request is required")
		}
		return p.predictCohortRoot(ctx, req.Cohort, "predict", true, func(src *guideCohort, opts *descx.PredictOptions) *descriptor.Envelope {
			p.predictRootOptions(opts)
			return descx.ValidateMaterializeWithOptions(src.rs, req, opts)
		})
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
	var carried []*errors.CodedError
	if req.Output != nil {
		// An output block lays the columns out itself: the carry follows
		// it column for column (drop, rename, duplicate, rebuild).
		carried = p.carryProjectedSidecar(carry, carryWarn, built, sink.projection())
	} else {
		carried = p.carrySidecar(carry, carryWarn, built, nil, sink.mirrors())
	}
	// A read of a managed import slides its expiry, as Process does.
	p.touchManaged(ctx, source)
	res := &MaterializeResult{
		Target:              built.Target,
		Records:             built.Records,
		DerivedColumns:      sink.names,
		Warnings:            append(append(append(append([]*errors.CodedError(nil), sink.warns...), sink.castWarnings()...), built.Warnings...), carried...),
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
	// srcNames are the source's logical field names, by layout Source
	// index (the SPSS carry's projection names the column each reads).
	srcNames        []string
	srcDescriptions []string
	// casts holds, per layout column, its cast (nil when it keeps its
	// type); nulled / firstNulled count, per column, the values an
	// on_cast_error: "null" cast nulled and the first output row.
	casts       []*fieldcast.Plan
	nulled      []int64
	firstNulled []int64
	// buckets holds, per layout column, its bucketing (nil when it has
	// none), its datetime zone loaded; a value it nulls counts in nulled.
	buckets []*fieldcast.Bucket
}

// castWarnings is one PULSE_MATERIALIZE_CAST_NULLED warning per column
// whose on_cast_error: "null" cast nulled a value, in output order.
func (m *materializeSink) castWarnings() []*errors.CodedError {
	var out []*errors.CodedError
	for i, n := range m.nulled {
		if n == 0 {
			continue
		}
		// A bucketed column names the field it read and the field it
		// writes; a cast-only column its cast's pair.
		from, to := m.layout[i].Field, m.layout[i].Field
		if b := m.layout[i].Bucket; b != nil {
			from = b.From
		} else if c := m.casts[i]; c != nil {
			from, to = c.From, c.To
		}
		out = append(out, descx.MaterializeCastNulled(m.layout[i].Field.Name, n, m.firstNulled[i], from, to))
	}
	return out
}

// projection describes each written column for the SPSS carry
// (spss.ProjectedColumn): the source field it copies, its description
// override, whether it was rebuilt (MaterializeOutputField.Rebuild), and
// — a derived column inheriting a source field's codes — its mirror,
// unless the column is rebuilt (a cast previous-state column holds new
// values, so its variable is regenerated, never the state's labels).
func (m *materializeSink) projection() []spss.ProjectedColumn {
	cols := make([]spss.ProjectedColumn, len(m.layout))
	for i, col := range m.layout {
		pc := spss.ProjectedColumn{Rebuild: col.Rebuild}
		switch {
		case col.Source >= 0:
			pc.From = m.srcNames[col.Source]
			if d := col.Field.Description; d != m.srcDescriptions[col.Source] {
				pc.Label = &d
			}
		case col.Derived >= 0 && m.inherited[col.Derived] != nil && !col.Rebuild:
			pc.Mirror = &spss.Mirror{Source: m.sources[col.Derived], Observed: m.observed[col.Derived]}
		}
		cols[i] = pc
	}
	return cols
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
	if s := cohort.Schema(); s != nil {
		m.srcNames = make([]string, len(s.Fields))
		m.srcDescriptions = make([]string, len(s.Fields))
		for i, f := range s.Fields {
			m.srcNames[i], m.srcDescriptions[i] = f.Name, f.Description
		}
	}
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
	// A kept vector field carries its VECTORS entry (dim, kind, scale,
	// labels) to its output position, under its output name.
	var vecs []encoding.VectorField
	src := cohort.Schema()
	for i, col := range layout {
		fields[i] = col.Field
		if col.Derived >= 0 {
			m.names = append(m.names, col.Field.Name)
		}
		if col.Source >= 0 && col.Field.Type.IsVector() && src != nil {
			if v, ok := src.Vector(col.Source); ok {
				v.FieldIndex = i
				vecs = append(vecs, v)
			}
		}
	}
	if m.names == nil {
		m.names = []string{}
	}
	if err := m.planCasts(); err != nil {
		return err
	}
	b, err := m.p.NewCohortBuilder(m.ctx, m.out, encoding.Schema{Fields: fields, Vectors: vecs},
		CohortBuilderOptions{Strict: m.opts.Strict, Overwrite: m.opts.Overwrite})
	if err != nil {
		return err
	}
	m.builder = b
	return nil
}

// planCasts builds each cast column's fieldcast.Plan, loading the zone
// a date ↔ datetime cast reads local days in (validated by the zone
// pre-flight before Begin).
func (m *materializeSink) planCasts() error {
	m.casts = make([]*fieldcast.Plan, len(m.layout))
	m.buckets = make([]*fieldcast.Bucket, len(m.layout))
	m.nulled = make([]int64, len(m.layout))
	m.firstNulled = make([]int64, len(m.layout))
	for i, col := range m.layout {
		if b := col.Bucket; b != nil {
			bucket := b.Bucket
			if b.Zone != "" {
				z, err := temporal.LoadZone(b.Zone)
				if err != nil {
					return err
				}
				bucket.Zone = z
			}
			m.buckets[i] = &bucket
		}
		c := col.Cast
		if c == nil {
			continue
		}
		plan := &fieldcast.Plan{From: c.From, To: col.Field}
		if c.Zone != "" {
			z, err := temporal.LoadZone(c.Zone)
			if err != nil {
				return err
			}
			plan.Zone = z
		}
		m.casts[i] = plan
	}
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
		if label, isLabel := v.(string); isLabel && col.Recode != nil {
			// Recode, then cast: a label with an entry is written as its
			// value (nil: null), any other label as itself.
			if to, ok := col.Recode[label]; ok {
				if v = nil; to != nil {
					v = *to
				}
			}
		}
		if b := m.buckets[i]; b != nil && v != nil {
			// Bucket, then cast: the cast reads the range label.
			bv, fail := b.Apply(v)
			if fail != nil {
				if !col.Bucket.NullOnError {
					return descx.MaterializeCastFailed(col.Field.Name, m.rows, b.From, col.Bucket.To, fail.Value, fail.Reason)
				}
				if m.nulled[i] == 0 {
					m.firstNulled[i] = m.rows
				}
				m.nulled[i]++
			}
			v = bv
		}
		if c := m.casts[i]; c != nil && v != nil {
			cv, fail := c.Apply(v)
			if fail != nil {
				if !col.Cast.NullOnError {
					return descx.MaterializeCastFailed(col.Field.Name, m.rows, c.From, c.To, fail.Value, fail.Reason)
				}
				if m.nulled[i] == 0 {
					m.firstNulled[i] = m.rows
				}
				m.nulled[i]++
			}
			v = cv
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

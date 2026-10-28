package descriptor

import (
	"io"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// materialize_predict.go is the no-execute verdict on a materialize
// request (Pulse.PredictMaterialize) and the schema-reading half of the
// run's pre-flight it shares with Service.Materialize, so the two cannot
// drift: MaterializePlan resolves the derived columns, the output
// layout and the written schema's description-length rule in one call
// both sides make.

// MaterializePlan is the schema-reading materialize pre-flight a run and
// its predict share: the derived columns (MaterializeColumns, refusing
// PULSE_MATERIALIZE_NO_DERIVED / _OUTPUT_UNDECLARED), the output layout
// (MaterializeOutputLayout, refusing PULSE_MATERIALIZE_OUTPUT_INVALID /
// _UNKNOWN_COLUMN / _DUPLICATE), then the written schema's
// description-length rule (encx.DescriptionLengthRefusal:
// PULSE_IMPORT_DESCRIPTION_TOO_LONG, details field + length + max — an
// output block's `description` past encoding.MaxDescriptionBytes, which
// the cohort writer would refuse with the same code and details). req is
// the item-expanded request, schema the source's logical schema and
// source the cohort path the run reads (only its base name is used).
// Reads no record.
func MaterializePlan(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, source string) ([]MaterializeColumn, []MaterializeOutputField, *errors.CodedError) {
	cols, ce := MaterializeColumns(req, schema, inst, source)
	if ce != nil {
		return nil, nil, ce
	}
	layout, ce := MaterializeOutputLayout(req, schema, cols)
	if ce != nil {
		return nil, nil, ce
	}
	if ce := encx.DescriptionLengthRefusal(layoutFields(layout)); ce != nil {
		return nil, nil, ce
	}
	return cols, layout, nil
}

// layoutFields is the written schema's fields, in output order.
func layoutFields(layout []MaterializeOutputField) []encoding.Field {
	out := make([]encoding.Field, len(layout))
	for i, col := range layout {
		out[i] = col.Field
	}
	return out
}

// MaterializeRisks lists the data-dependent checks a materialize run of
// req makes on the records, over its resolved layout: today one per
// output.columns entry declaring `nullable: false` over a column that
// may hold a null (a nullable source field, or any derived column) — the
// run fails with PULSE_MATERIALIZE_OUTPUT_NULL at the first null row
// (MaterializeOutputNull). Empty, never nil. Schema-only.
func MaterializeRisks(req *types.Request, schema *encoding.Schema, layout []MaterializeOutputField) []descriptor.MaterializeRisk {
	risks := []descriptor.MaterializeRisk{}
	if req == nil || req.Output == nil || req.Output.Columns == nil || len(layout) != len(req.Output.Columns) {
		return risks
	}
	for i, c := range req.Output.Columns {
		if c.Nullable == nil || *c.Nullable {
			continue
		}
		col := layout[i]
		// A derived column is always written nullable; a source field
		// carries its own flag.
		mayBeNull := col.Derived >= 0
		if col.Source >= 0 && schema != nil && col.Source < len(schema.Fields) {
			mayBeNull = schema.Fields[col.Source].Nullable
		}
		if !mayBeNull {
			continue
		}
		risks = append(risks, descriptor.MaterializeRisk{
			Column: col.Field.Name,
			Path:   outputPath("columns", i, "nullable"),
			Code:   string(errors.PULSE_MATERIALIZE_OUTPUT_NULL),
			Check:  "column " + strconv.Quote(c.From) + " may hold a null, which nullable: false refuses: the run fails at the first null row and writes nothing",
		})
	}
	return risks
}

// ValidateMaterializeWithOptions is the no-execute verdict on a
// materialize request over the source cohort in fileData (a single
// file or a shard archive; header and schema only, never a record). It
// follows the run's own order (Service.Materialize):
//
//  1. a nil request or cohort is SERVICE_VALIDATION;
//  2. the instance slot gate with the materialize-only `output` slot
//     accepted (MaterializeSlotGate), then item expansion
//     (ExpandRequestItems), then the materialize slot gate
//     (MaterializeSlotRefusal) and PULSE_MATERIALIZE_NO_DERIVED — each
//     stops the verdict, as it stops the run;
//  3. MaterializePlan over the source's schema — the derived columns,
//     the output layout and the written descriptions' length, the
//     run's own pre-flight call; its refusal leads the errors;
//  4. the request less its `output` slot through Predict — every check
//     Process's pre-flight and operator construction make (defaults,
//     zones, field references, weights, limits, operator params), the
//     source schema's description findings excepted;
//  5. one PULSE_FIELD_DESCRIPTION_LOW_QUALITY warning per weak
//     description of the WRITTEN schema, as the cohort writer warns.
//
// Data is a *descriptor.MaterializePredictResult: the output columns,
// the derived column names and the data-dependent risks. opts.Strict
// promotes every warning into an error; opts.EchoRequest echoes the
// item-expanded request on env.Request.
func ValidateMaterializeWithOptions(fileData io.ReadSeeker, req *types.Request, opts *PredictOptions) *descriptor.Envelope {
	if opts == nil {
		opts = &PredictOptions{}
	}
	result := &descriptor.MaterializePredictResult{
		Valid:          true,
		Columns:        []descriptor.MaterializePredictColumn{},
		DerivedColumns: []string{},
		Risks:          []descriptor.MaterializeRisk{},
	}
	env := descriptor.NewEnvelope(result)
	echo := validateMaterialize(env, result, fileData, req, opts)
	finishPredictRoot(env, opts, func() { result.Valid = false }, func() any {
		if echo == nil {
			return nil
		}
		return echo
	})
	return env
}

// validateMaterialize is ValidateMaterializeWithOptions before the
// Strict and echo pass; it returns the request to echo (nil before
// item expansion succeeds).
func validateMaterialize(env *descriptor.Envelope, result *descriptor.MaterializePredictResult, fileData io.ReadSeeker, req *types.Request, opts *PredictOptions) *types.Request {
	fail := func(err error) {
		addCodedError(env, err)
		result.Valid = false
	}
	if req == nil || req.Cohort == nil {
		fail(errors.NewCodedError(errors.SERVICE_VALIDATION, "request cohort is required"))
		return nil
	}
	inst := opts.instance()
	if err := MaterializeSlotGate(req, inst); err != nil {
		fail(err)
		return nil
	}
	expanded, _, _, xerr := ExpandRequestItems(req, inst.Limits())
	if xerr != nil {
		fail(xerr)
		return nil
	}
	if ce := MaterializeSlotRefusal(expanded); ce != nil {
		fail(ce)
		return expanded
	}
	if expanded.Output == nil && len(expanded.Features) == 0 && len(expanded.Attributes) == 0 && len(expanded.Windows) == 0 {
		_, ce := MaterializeColumns(expanded, nil, inst, "")
		fail(ce)
		return expanded
	}

	rewind := func() bool {
		if _, err := fileData.Seek(0, io.SeekStart); err != nil {
			fail(errors.WrapCodedError(err, errors.DATA_FILE, "predict materialize: rewinding the cohort"))
			return false
		}
		return true
	}
	if !rewind() {
		return expanded
	}
	schema, _, fault := readCohortHead(fileData)
	if fault != nil {
		env.AddError(string(fault.code), fault.message, fault.details)
		result.Valid = false
		return expanded
	}

	// The materialize plan — the run's own pre-flight call. Its refusal
	// leads the errors: a request Process's operator construction would
	// also refuse (FEAT_POLY degree 1) is refused by the run's pre-flight
	// first, before any operator is built.
	cols, layout, planErr := MaterializePlan(expanded, schema, inst, cohortPathFor(expanded.Cohort))
	if planErr != nil {
		addCodedError(env, planErr)
	}

	// Process's pre-flight and operator checks, on the request less the
	// materialize-only slot — every finding reported, as Predict does.
	// The source schema's description findings are dropped: the run
	// writes the OUTPUT schema, judged below.
	body := *expanded
	body.Output = nil
	bodyOpts := *opts
	bodyOpts.Strict, bodyOpts.EchoRequest = false, false
	if !rewind() {
		return expanded
	}
	penv := Predict(fileData, &body, &bodyOpts)
	env.Errors = append(env.Errors, penv.Errors...)
	for _, w := range penv.Warnings {
		if w.Code != string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY) {
			env.Warnings = append(env.Warnings, w)
		}
	}
	if pr, ok := penv.Data.(*descriptor.PredictResult); ok {
		result.SchemaInfo = pr.SchemaInfo
	}

	if planErr == nil {
		for _, col := range layout {
			result.Columns = append(result.Columns, predictColumn(col, schema, cols))
			if col.Derived >= 0 {
				result.DerivedColumns = append(result.DerivedColumns, col.Field.Name)
			}
		}
		result.Risks = MaterializeRisks(expanded, schema, layout)
		validateDescriptionQuality(env, &encoding.Schema{Fields: layoutFields(layout)}, &PredictOptions{})
	}
	if len(env.Errors) > 0 {
		result.Valid = false
	}
	return expanded
}

// predictColumn renders one layout column as the predict result's
// output column.
func predictColumn(col MaterializeOutputField, schema *encoding.Schema, cols []MaterializeColumn) descriptor.MaterializePredictColumn {
	f := col.Field
	out := descriptor.MaterializePredictColumn{
		Name:        f.Name,
		Type:        f.Type.String(),
		Nullable:    f.Nullable,
		Description: f.Description,
		Derived:     col.Derived >= 0,
	}
	switch {
	case col.Source >= 0 && schema != nil && col.Source < len(schema.Fields):
		out.From = schema.Fields[col.Source].Name
	case col.Derived >= 0 && col.Derived < len(cols):
		out.From = cols[col.Derived].Name
	}
	if f.Type.IsDecimal() {
		p, s := f.Precision, f.Scale
		out.Precision, out.Scale = &p, &s
	}
	if f.Type.HasDictionary() && f.Dictionary != nil {
		out.Dictionary = truncatedEntries(f.Dictionary.Values(), DefaultDictionaryLimit)
	}
	return out
}

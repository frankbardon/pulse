package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// materialize_output.go is the no-execute half of the materialize-only
// `output` slot (types.MaterializeOutput): the static validation and the
// resolved output layout. It reads the source schema and the derived
// columns (MaterializeColumns) only — never a record — so the run's
// pre-flight and a predict of the same request share it.

// MaterializeOutputField is one column of the cohort a materialize run
// writes, in output order: the field written (name, type, nullability,
// dictionary, description) and the column it reads — the source field
// at Source (an index into the source schema's logical Fields), or the
// derived column at Derived (an index into the MaterializeColumns
// list). Exactly one of Source / Derived is >= 0.
//
// Rebuild marks a column whose values the run re-codes (a cast, recode
// or bucket) rather than copies: the source's SPSS sidecar variable for
// it is regenerated from the written type and dictionary, never carried
// (spss.RebuildVariable) — old value labels never land on new codes.
type MaterializeOutputField struct {
	Field   encoding.Field
	Source  int
	Derived int
	Rebuild bool
}

// outputPath renders a path inside the `output` slot for details.path.
func outputPath(list string, i int, key string) string {
	p := "output." + list + "[" + strconv.Itoa(i) + "]"
	if key != "" {
		p += "." + key
	}
	return p
}

// derivedField is the field a derived column is written as: a nullable
// f64 with its generated description, or — an inheriting column
// (ATTR_PREVIOUS_STATE) — its state field's type and dictionary.
func derivedField(c MaterializeColumn) encoding.Field {
	f := encoding.Field{Name: c.Name, Type: encoding.FieldTypeF64, Nullable: true, Description: c.Description}
	if c.Inherits != "" {
		f.Type, f.Dictionary = c.Type, c.Dictionary
	}
	return f
}

// MaterializeOutputLayout resolves the cohort a materialize run of req
// writes over schema (the source's logical schema) and cols (its derived
// columns, MaterializeColumns' order).
//
// Without req.Output the layout is every source field verbatim, then
// every derived column — the shape Materialize has always written.
// With it:
//
//   - columns XOR exclude: both set is PULSE_MATERIALIZE_OUTPUT_INVALID
//     (reason columns_and_exclude); a present-but-empty columns list, or
//     an exclude list dropping every column, is reason no_columns; a
//     column entry without `from` is reason from_missing;
//   - a `from` (or an exclude name) that is neither a source field nor a
//     derived label is PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN, details
//     column + path (+ suggestion when a name is close);
//   - an output name written twice after renaming is
//     PULSE_MATERIALIZE_OUTPUT_DUPLICATE, details column + path (the
//     second entry) + first (the earlier entry's path). The same `from`
//     under two `as` names is a duplicated column, never a refusal.
//
// A column inherits every key it leaves out from the column it reads
// (type, nullability, dictionary, description; decimal shape);
// `description` replaces the description, `nullable` the nullability.
// Projection names output columns only: filters, sort, weight and the
// derived slots have already read the full source schema. Schema-only;
// reads no record.
func MaterializeOutputLayout(req *types.Request, schema *encoding.Schema, cols []MaterializeColumn) ([]MaterializeOutputField, *errors.CodedError) {
	var src []encoding.Field
	if schema != nil {
		src = schema.Fields
	}
	all := make([]MaterializeOutputField, 0, len(src)+len(cols))
	for i := range src {
		all = append(all, MaterializeOutputField{Field: src[i], Source: i, Derived: -1})
	}
	for i, c := range cols {
		all = append(all, MaterializeOutputField{Field: derivedField(c), Source: -1, Derived: i})
	}
	if req == nil || req.Output == nil {
		return all, nil
	}
	out := req.Output
	if ce := materializeOutputShape(out); ce != nil {
		return nil, ce
	}
	byName := make(map[string]int, len(all))
	names := make([]string, len(all))
	for i, f := range all {
		byName[f.Field.Name] = i
		names[i] = f.Field.Name
	}
	unknown := func(name, path string) *errors.CodedError {
		details := map[string]any{"column": name, "path": path}
		if near := NearestKey(name, names); near != "" {
			details["suggestion"] = near
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN,
			"output names "+strconv.Quote(name)+" ("+path+"), which is neither a source field nor a derived column of this request",
			details)
	}

	if out.Columns == nil {
		drop := make(map[string]bool, len(out.Exclude))
		for i, name := range out.Exclude {
			if _, ok := byName[name]; !ok {
				return nil, unknown(name, "output.exclude["+strconv.Itoa(i)+"]")
			}
			drop[name] = true
		}
		kept := all[:0:0]
		for _, f := range all {
			if !drop[f.Field.Name] {
				kept = append(kept, f)
			}
		}
		if len(kept) == 0 {
			return nil, outputInvalid("no_columns", "output.exclude",
				"output.exclude drops every column; the written cohort needs at least one")
		}
		return kept, nil
	}

	layout := make([]MaterializeOutputField, 0, len(out.Columns))
	seen := make(map[string]string, len(out.Columns))
	for i, c := range out.Columns {
		at, ok := byName[c.From]
		if !ok {
			return nil, unknown(c.From, outputPath("columns", i, "from"))
		}
		f := all[at]
		if c.As != "" {
			f.Field.Name = c.As
		}
		if c.Description != nil {
			f.Field.Description = *c.Description
		}
		if c.Nullable != nil {
			f.Field.Nullable = *c.Nullable
		}
		path := outputPath("columns", i, "")
		if first, dup := seen[f.Field.Name]; dup {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_OUTPUT_DUPLICATE,
				"output writes the column "+strconv.Quote(f.Field.Name)+" twice ("+first+" and "+path+"); give one of them a different `as`",
				map[string]any{"column": f.Field.Name, "path": path, "first": first})
		}
		seen[f.Field.Name] = path
		layout = append(layout, f)
	}
	return layout, nil
}

// materializeOutputShape is the schema-free half of the `output`
// validation: columns XOR exclude, a non-empty columns list, a `from`
// on every entry. Nil when out is well-formed.
func materializeOutputShape(out *types.MaterializeOutput) *errors.CodedError {
	if out.Columns != nil && out.Exclude != nil {
		return outputInvalid("columns_and_exclude", "output",
			"output sets both columns and exclude; list the columns to keep OR the columns to drop")
	}
	if out.Columns != nil && len(out.Columns) == 0 {
		return outputInvalid("no_columns", "output.columns",
			"output.columns is empty; the written cohort needs at least one column")
	}
	for i, c := range out.Columns {
		if c.From == "" {
			return outputInvalid("from_missing", outputPath("columns", i, "from"),
				"output.columns["+strconv.Itoa(i)+"] has no `from`: name the source field or derived column it reads")
		}
	}
	return nil
}

func outputInvalid(reason, path, msg string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_OUTPUT_INVALID, msg,
		map[string]any{"reason": reason, "path": path})
}

// MaterializeOutputNull is the run-time refusal of a null cell in an
// output column the layout declares non-nullable: details column (the
// output name) and row (the 0-based output row). Raised before the row
// is appended, so nothing is written at the target.
func MaterializeOutputNull(column string, row int64) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_OUTPUT_NULL,
		"output column "+strconv.Quote(column)+" is declared nullable: false but row "+strconv.FormatInt(row, 10)+" holds a null; nothing was written",
		map[string]any{"column": column, "row": row})
}

// MaterializeOutputOnly is the refusal of the materialize-only `output`
// slot on every other operation: PULSE_MATERIALIZE_OUTPUT_ONLY,
// details.slot "output".
func MaterializeOutputOnly() *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_OUTPUT_ONLY,
		"the output slot lays out the cohort Materialize writes and is accepted only by Materialize; remove it, or run the request through Materialize",
		map[string]any{"slot": "output"})
}

// outputSlotRefusal refuses a set `output` on any request root other
// than a materialize run's: the Request itself, every ComposedRequest
// slot (details.request) and fit request (details.fit), every
// ChainRequest stage (details.stage). FacetRequest and SampleRequest
// declare no `output` key.
func outputSlotRefusal(v any) error {
	switch r := v.(type) {
	case *types.Request:
		if r != nil && r.Output != nil {
			return MaterializeOutputOnly()
		}
	case *types.ComposedRequest:
		if r == nil {
			return nil
		}
		for i, req := range r.Requests {
			if req != nil && req.Output != nil {
				return RefusalAt(MaterializeOutputOnly(), "request", i)
			}
		}
		for i, fit := range r.Fits {
			if fit.Request != nil && fit.Request.Output != nil {
				return RefusalAt(MaterializeOutputOnly(), "fit", i)
			}
		}
	case *types.ChainRequest:
		if r == nil {
			return nil
		}
		for i, st := range r.Stages {
			if st != nil && st.Request != nil && st.Request.Output != nil {
				return RefusalAt(MaterializeOutputOnly(), "stage", i)
			}
		}
	}
	return nil
}

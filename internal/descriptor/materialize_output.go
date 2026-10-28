package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldcast"
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
//
// Cast is the column's `type` cast (nil when it keeps its type): every
// cell read is converted by fieldcast before the append. A cast column
// is always Rebuild.
type MaterializeOutputField struct {
	Field   encoding.Field
	Source  int
	Derived int
	Rebuild bool
	Cast    *MaterializeCast
}

// MaterializeCast is one output column's cast: From is the field read
// (the source field or derived column before any override — its type
// and decimal shape), the written field is MaterializeOutputField.Field.
// NullOnError is on_cast_error: "null". Zone is the zone a date ↔
// datetime cast reads local days in (MaterializePlan sets it:
// Request.TimeZone, else the instance default, else "UTC"); "" for
// every other pair.
type MaterializeCast struct {
	From        encoding.Field
	NullOnError bool
	Zone        string
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
		cast, ce := outputCast(c, i, all[at].Field, &f.Field)
		if ce != nil {
			return nil, ce
		}
		if cast != nil {
			f.Cast, f.Rebuild = cast, true
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

// outputCast resolves column i's `type` / `precision` / `scale` /
// `on_cast_error` against from, the field it reads, retyping *to (the
// written field, its name / description / nullability already
// resolved). Nil when the column keeps its type. Refuses
// PULSE_MATERIALIZE_OUTPUT_INVALID (unknown_type, decimal_shape,
// on_cast_error_invalid, on_cast_error_without_type,
// on_cast_error_not_nullable) and PULSE_MATERIALIZE_CAST_UNSUPPORTED
// (fieldcast.Refused: an illegal pair, or a categorical narrowing whose
// dictionary does not fit) — the one matrix the run's conversion uses.
func outputCast(c types.MaterializeOutputColumn, i int, from encoding.Field, to *encoding.Field) (*MaterializeCast, *errors.CodedError) {
	nullOnError := false
	switch c.OnCastError {
	case "", "fail":
	case "null":
		nullOnError = true
	default:
		return nil, outputInvalid("on_cast_error_invalid", outputPath("columns", i, "on_cast_error"),
			"output.columns["+strconv.Itoa(i)+"].on_cast_error is "+strconv.Quote(c.OnCastError)+"; use \"fail\" (the default) or \"null\"")
	}
	if c.Type == "" {
		switch {
		case c.OnCastError != "":
			return nil, outputInvalid("on_cast_error_without_type", outputPath("columns", i, "on_cast_error"),
				"output.columns["+strconv.Itoa(i)+"] sets on_cast_error without a type; it applies only to a cast column")
		case c.Precision != nil || c.Scale != nil:
			return nil, outputInvalid("decimal_shape", outputPath("columns", i, "precision"),
				"output.columns["+strconv.Itoa(i)+"] sets precision / scale without type: \"decimal128\"")
		}
		return nil, nil
	}
	typ, ok := encoding.ParseFieldType(c.Type)
	if !ok {
		return nil, outputInvalid("unknown_type", outputPath("columns", i, "type"),
			"output.columns["+strconv.Itoa(i)+"].type "+strconv.Quote(c.Type)+" is not a field type")
	}
	if why := fieldcast.Refused(from, typ); why != "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_CAST_UNSUPPORTED,
			"output column "+strconv.Quote(to.Name)+" cannot cast "+strconv.Quote(from.Name)+" from "+from.Type.String()+" to "+typ.String()+": "+why,
			map[string]any{"column": to.Name, "path": outputPath("columns", i, "type"), "from": from.Name,
				"from_type": from.Type.String(), "to_type": typ.String(), "reason": why})
	}
	var precision, scale uint8
	if typ.IsDecimal() {
		p, s := 38, 0
		if from.Type.IsDecimal() {
			p, s = int(from.Precision), int(from.Scale)
		}
		if c.Precision != nil {
			p = *c.Precision
		}
		if c.Scale != nil {
			s = *c.Scale
		}
		if p < 1 || p > encoding.MaxDecimalPrecision || s < 0 || s > p {
			return nil, outputInvalid("decimal_shape", outputPath("columns", i, "precision"),
				"output.columns["+strconv.Itoa(i)+"] declares decimal128("+strconv.Itoa(p)+","+strconv.Itoa(s)+"); precision must be 1..38 and scale 0..precision")
		}
		precision, scale = uint8(p), uint8(s)
	} else if c.Precision != nil || c.Scale != nil {
		return nil, outputInvalid("decimal_shape", outputPath("columns", i, "precision"),
			"output.columns["+strconv.Itoa(i)+"] sets precision / scale on type "+strconv.Quote(c.Type)+"; they shape only a decimal128")
	}
	if nullOnError && !to.Nullable {
		return nil, outputInvalid("on_cast_error_not_nullable", outputPath("columns", i, "on_cast_error"),
			"output.columns["+strconv.Itoa(i)+"] sets on_cast_error: \"null\" on a column that is not nullable; set nullable: true or drop on_cast_error")
	}
	if typ == from.Type && (!typ.IsDecimal() || (precision == from.Precision && scale == from.Scale)) {
		return nil, nil
	}
	to.Type, to.Precision, to.Scale = typ, precision, scale
	if !typ.HasDictionary() || !from.Type.HasDictionary() {
		// A set widen and a categorical rung change keep the whole
		// dictionary (labels, order, codes); a numeric → categorical
		// cast builds its own from the data in first-seen order; every
		// other target carries none.
		to.Dictionary = nil
	}
	return &MaterializeCast{From: from, NullOnError: nullOnError}, nil
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

// MaterializeCastFailed is the run-time refusal of a stored value an
// output column's cast cannot represent (fieldcast.Failure): details
// column (the output name), row (the 0-based output row), from_type,
// to_type, value (rendered exactly) and reason. Raised before the row is
// appended, so nothing is written at the target.
func MaterializeCastFailed(column string, row int64, from, to encoding.Field, value, reason string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_CAST_FAILED,
		"output column "+strconv.Quote(column)+" cannot cast row "+strconv.FormatInt(row, 10)+"'s value "+value+" from "+from.Type.String()+" to "+to.Type.String()+" ("+reason+"); nothing was written",
		map[string]any{"column": column, "row": row, "from_type": from.Type.String(), "to_type": to.Type.String(), "value": value, "reason": reason})
}

// MaterializeCastNulled is the warning an on_cast_error: "null" column
// raises once when its cast nulled count values, the first at output
// row first.
func MaterializeCastNulled(column string, count, first int64, from, to encoding.Field) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_CAST_NULLED,
		"output column "+strconv.Quote(column)+" nulled "+strconv.FormatInt(count, 10)+" value(s) its "+from.Type.String()+" → "+to.Type.String()+" cast could not represent (on_cast_error: null)",
		map[string]any{"column": column, "count": count, "first_row": first, "from_type": from.Type.String(), "to_type": to.Type.String()})
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

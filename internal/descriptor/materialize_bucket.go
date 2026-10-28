package descriptor

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/daterange"
	"github.com/frankbardon/pulse/internal/fieldcast"
	"github.com/frankbardon/pulse/internal/numrange"
	"github.com/frankbardon/pulse/types"
)

// materialize_bucket.go resolves a materialize output column's `bucket`
// (types.MaterializeBucket) — schema-only, shared by the run's
// pre-flight and predict through MaterializeOutputLayout. The ranges go
// through the ONE shared range-compilation pass: numrange.Compile for a
// numeric column (half-open [start, end), the GROUP_NUMERIC_RANGES rule),
// daterange.Compile for a date-family column (inclusive, the
// GROUP_DATE_RANGES / FILTER_DATE_RANGES rule).

// bucketKind is the range kind a field type is bucketed by: "numeric",
// "date", or "" when the type cannot be bucketed.
func bucketKind(t encoding.FieldType) string {
	switch t {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64, encoding.FieldTypeDecimal128:
		return daterange.KindNumeric
	case encoding.FieldTypeDate, encoding.FieldTypeDateTime:
		return daterange.KindDate
	}
	return ""
}

// bucketCategorical is the narrowest categorical_* rung holding n labels.
func bucketCategorical(n int) encoding.FieldType {
	switch {
	case uint64(n) <= uint64(encoding.FieldTypeCategoricalU8.MaxCategoricalEntries()):
		return encoding.FieldTypeCategoricalU8
	case uint64(n) <= uint64(encoding.FieldTypeCategoricalU16.MaxCategoricalEntries()):
		return encoding.FieldTypeCategoricalU16
	}
	return encoding.FieldTypeCategoricalU32
}

// outputBucket resolves column i's `bucket` against from, the field it
// reads, retyping *to (the written field, its name and nullability
// already resolved) to the narrowest categorical_* holding the range
// labels, its dictionary the labels in declared order. Nil when the
// column has no bucket. Refuses, all statically (details column + path
// on every one):
//
//   - from neither numeric nor date-family: PULSE_MATERIALIZE_OUTPUT_INVALID,
//     reason bucket_unsupported_type;
//   - both or neither of ranges / range_table: PULSE_RANGE_SOURCE_AMBIGUOUS;
//   - a range_table the instance does not register (or hides with
//     capability:range_tables): PULSE_RANGE_TABLE_UNKNOWN; one of the
//     other kind: PULSE_RANGE_TABLE_KIND_MISMATCH (details table, want,
//     got);
//   - an inline bound of the wrong JSON kind (a string on a numeric
//     column, a number on a date one): PULSE_RANGE_INVALID (details
//     label);
//   - every shared-pass refusal (PULSE_RANGE_EMPTY / _INVALID /
//     _DUPLICATE_LABEL / _OVERLAP) with the pass's own message.
func outputBucket(c types.MaterializeOutputColumn, i int, from encoding.Field, to *encoding.Field, inst *InstanceSnapshot) (*MaterializeBucket, *errors.CodedError) {
	bk := c.Bucket
	if bk == nil {
		return nil, nil
	}
	path := outputPath("columns", i, "bucket")
	at := func(ce *errors.CodedError, p string) *errors.CodedError {
		if ce.Details == nil {
			ce.Details = map[string]any{}
		}
		ce.Details["column"], ce.Details["path"] = to.Name, p
		return ce
	}
	kind := bucketKind(from.Type)
	if kind == "" {
		return nil, outputInvalid("bucket_unsupported_type", path,
			"output.columns["+strconv.Itoa(i)+"] buckets "+strconv.Quote(from.Name)+", a "+from.Type.String()+" column; bucket reads a numeric (u4..u64, f32, f64, decimal128) or date / datetime column")
	}
	table := strings.TrimSpace(bk.RangeTable)
	if (len(bk.Ranges) > 0) == (table != "") {
		msg := "output.columns[" + strconv.Itoa(i) + "].bucket sets both ranges and range_table; give exactly one"
		if table == "" {
			msg = "output.columns[" + strconv.Itoa(i) + "].bucket has neither ranges nor range_table; give exactly one"
		}
		return nil, at(errors.NewCodedError(errors.PULSE_RANGE_SOURCE_AMBIGUOUS, msg), path)
	}

	var (
		labels []string
		bucket = fieldcast.Bucket{From: from}
		err    error
	)
	if table != "" {
		tpath := path + ".range_table"
		meta, ok := rangeTableMeta(table, inst)
		if !ok {
			return nil, at(errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_TABLE_UNKNOWN,
				"output.columns["+strconv.Itoa(i)+"].bucket names the unknown range table "+strconv.Quote(table),
				map[string]any{"table": table}), tpath)
		}
		if meta.Kind != kind {
			return nil, at(errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_TABLE_KIND_MISMATCH,
				"output.columns["+strconv.Itoa(i)+"].bucket names range table "+strconv.Quote(table)+", a "+meta.Kind+" table; "+strconv.Quote(from.Name)+" is a "+from.Type.String()+" column, bucketed by a "+kind+" table",
				map[string]any{"table": table, "want": kind, "got": meta.Kind}), tpath)
		}
		specs := inst.Extensions().RangeTableSpecs[table]
		if kind == daterange.KindNumeric {
			labels = numericLabels(specs.Numeric)
			bucket.Numeric, err = numrange.Compile(specs.Numeric)
		} else {
			labels = dateLabels(specs.Dates)
			bucket.Dates, err = daterange.Compile(specs.Dates)
		}
		if err != nil {
			return nil, at(codedOf(err), tpath)
		}
	} else {
		rpath := path + ".ranges"
		if kind == daterange.KindNumeric {
			specs := make([]numrange.Spec, len(bk.Ranges))
			for j, r := range bk.Ranges {
				specs[j].Label = r.Label
				for _, b := range []struct {
					raw json.RawMessage
					dst **float64
					key string
				}{{r.Start, &specs[j].Start, "start"}, {r.End, &specs[j].End, "end"}} {
					if openBound(b.raw) {
						continue
					}
					var f float64
					if json.Unmarshal(b.raw, &f) != nil {
						return nil, at(badBound(i, j, r.Label, b.key, "a JSON number: "+strconv.Quote(from.Name)+" is numeric"), rpath+"["+strconv.Itoa(j)+"]."+b.key)
					}
					*b.dst = &f
				}
			}
			labels = numericLabels(specs)
			bucket.Numeric, err = numrange.Compile(specs)
		} else {
			specs := make([]daterange.Spec, len(bk.Ranges))
			for j, r := range bk.Ranges {
				specs[j].Label = r.Label
				for _, b := range []struct {
					raw json.RawMessage
					dst **string
					key string
				}{{r.Start, &specs[j].Start, "start"}, {r.End, &specs[j].End, "end"}} {
					if openBound(b.raw) {
						continue
					}
					var s string
					if json.Unmarshal(b.raw, &s) != nil {
						return nil, at(badBound(i, j, r.Label, b.key, "an ISO date string: "+strconv.Quote(from.Name)+" is "+from.Type.String()), rpath+"["+strconv.Itoa(j)+"]."+b.key)
					}
					*b.dst = &s
				}
			}
			labels = dateLabels(specs)
			bucket.Dates, err = daterange.Compile(specs)
		}
		if err != nil {
			return nil, at(codedOf(err), rpath)
		}
	}

	dict := encoding.NewDictionary()
	for _, l := range labels {
		// Add never fails: the rung is sized from the label count, and
		// the shared pass refused a duplicate label.
		_, _ = dict.Add(l)
	}
	to.Type, to.Dictionary, to.Precision, to.Scale = bucketCategorical(len(labels)), dict, 0, 0
	return &MaterializeBucket{Bucket: bucket, To: *to, NullOnError: c.OnCastError == "null"}, nil
}

// openBound reports whether an inline bound is omitted or JSON null.
func openBound(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}

func badBound(i, j int, label, key, want string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_INVALID,
		"output.columns["+strconv.Itoa(i)+"].bucket.ranges["+strconv.Itoa(j)+"] ("+strconv.Quote(label)+") "+key+" bound must be "+want,
		map[string]any{"label": label})
}

// codedOf is the shared pass's refusal as a fresh *CodedError (its
// details copied, so adding column / path never aliases).
func codedOf(err error) *errors.CodedError {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return errors.WrapCodedError(err, errors.PULSE_RANGE_INVALID, err.Error())
	}
	details := make(map[string]any, len(ce.Details)+2)
	for k, v := range ce.Details {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(ce.Code, ce.Message, details)
}

func numericLabels(specs []numrange.Spec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Label
	}
	return out
}

func dateLabels(specs []daterange.Spec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Label
	}
	return out
}

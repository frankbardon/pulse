// Package postfit is the ONE rule set for a Compose batch's `post_fit`
// entries (types.PostFitSpec): the request rules every entry is judged
// by before any fit or slot runs (Check), the registration's output
// schema rules (CheckSchema, at pulse.New), the output rule every
// operator run is held to (CheckOutput) and the read-only input an
// operator runs over (Run).
//
// A post-fit operator is extension-only (pulse.PostFitRegistration,
// extend.PostFitFunc): it reads the fits its entry names, the batch's
// slot results (the result() reader), the optimize solutions and its
// params — never a record. Check is no-execute (it reads only the
// request and the declarations), so predict can judge an entry exactly
// as the runtime does.
//
// It imports only types, errors, descriptor, extend and the no-execute
// leaves internal/model and internal/resultcall — never the engine.
package postfit

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// Slot is the request slot's key (the ComposedRequest JSON name) and
// the PULSE_SLOT_FAILED slot_kind of a failed entry.
const Slot = "post_fit"

// The PULSE_POSTFIT_INVALID reasons (details.reason).
const (
	ReasonLabelMissing   = "label_missing"
	ReasonLabelDuplicate = "label_duplicate"
	ReasonTypeMissing    = "type_missing"
	ReasonTypeUnknown    = "type_unknown"
	ReasonFitMissing     = "fit_missing"
	ReasonFitDuplicate   = "fit_duplicate"
	ReasonFitUnknown     = "fit_unknown"
	ReasonFitKind        = "fit_kind"
)

// Decl is one registered operator's declared contract: what Check and
// CheckOutput judge by. It holds no code, so predict can build one from
// the instance snapshot.
type Decl struct {
	Name    types.PostFitType
	Params  []descriptor.OperatorParamMeta
	Outputs extend.PostFitSchema
}

// Operator is a registered operator: its declaration and its function.
type Operator struct {
	Decl
	Run extend.PostFitFunc
}

// ParamsCheck refuses an entry's params object raw (at path, e.g.
// post_fit[0]) against op's declared params — the shared extension
// params check (internal/descriptor.DeclaredParamsRefusal). Nil when
// the params conform.
type ParamsCheck func(path, op string, params []descriptor.OperatorParamMeta, raw json.RawMessage) *errors.CodedError

// Check judges the batch's post-fit entries against the registered
// operators (decls, by name) and the batch's fits, in order, each fully
// before the next; the first fault wins:
//
//   - an empty label                    — label_missing
//   - a label an earlier entry uses     — label_duplicate
//   - an empty type                     — type_missing
//   - a type no operator registers      — type_unknown (details available)
//   - an empty fits name                — fit_missing
//   - a fits name listed twice          — fit_duplicate
//   - a name no `fits` entry carries    — fit_unknown (details available)
//   - a name of a non-regression fit    — fit_kind
//   - the params, by params             — PULSE_REQUEST_UNKNOWN_FIELD / PULSE_REQUEST_PARAM_INVALID
//
// Every structural fault is PULSE_POSTFIT_INVALID with details
// {post_fit: i, label, reason} plus the reason's own keys.
func Check(specs []types.PostFitSpec, fits []types.FitSpec, decls map[types.PostFitType]Decl, params ParamsCheck) *errors.CodedError {
	if len(specs) == 0 {
		return nil
	}
	byName := make(map[string]*types.FitSpec, len(fits))
	var available []string
	for k := range fits {
		f := &fits[k]
		if _, dup := byName[f.Name]; dup || f.Name == "" {
			continue
		}
		byName[f.Name] = f
		if isRegressionFit(f) {
			available = append(available, f.Name)
		}
	}
	slices.Sort(available)
	seen := map[string]bool{}
	for i := range specs {
		s := &specs[i]
		if s.Label == "" {
			return invalid(i, s.Label, ReasonLabelMissing, "a post-fit entry needs a label", nil)
		}
		if seen[s.Label] {
			return invalid(i, s.Label, ReasonLabelDuplicate, "the label is used by an earlier post-fit entry", nil)
		}
		seen[s.Label] = true
		if s.Type == "" {
			return invalid(i, s.Label, ReasonTypeMissing, "a post-fit entry needs a type (a registered POSTFIT_<NAMESPACE>_<NAME> operator)", nil)
		}
		d, ok := decls[s.Type]
		if !ok {
			return invalid(i, s.Label, ReasonTypeUnknown, "type "+strconv.Quote(string(s.Type))+" names no registered post-fit operator",
				map[string]any{"type": string(s.Type), "available": typeNames(decls)})
		}
		named := map[string]bool{}
		for j, name := range s.Fits {
			field := "fits[" + strconv.Itoa(j) + "]"
			switch f, ok := byName[name]; {
			case name == "":
				return invalid(i, s.Label, ReasonFitMissing, "`"+field+"` must name a `fits` entry", map[string]any{"field": field})
			case named[name]:
				return invalid(i, s.Label, ReasonFitDuplicate, "`"+field+"` "+strconv.Quote(name)+" is listed more than once",
					map[string]any{"field": field, "value": name})
			case !ok:
				return invalid(i, s.Label, ReasonFitUnknown, "`"+field+"` "+strconv.Quote(name)+" names no `fits` entry",
					map[string]any{"field": field, "value": name, "available": nonNil(available)})
			case !isRegressionFit(f):
				return invalid(i, s.Label, ReasonFitKind, "`"+field+"` "+strconv.Quote(name)+" is not a regression fit",
					map[string]any{"field": field, "value": name, "available": nonNil(available)})
			}
			named[name] = true
		}
		if params != nil && len(d.Params) > 0 {
			if cerr := params(Slot+"["+strconv.Itoa(i)+"]", string(s.Type), d.Params, s.Params); cerr != nil {
				return cerr
			}
		}
	}
	return nil
}

// isRegressionFit reports a regression fit: a request holding one
// regression, or a fit bound from the sweep (always a regression).
func isRegressionFit(f *types.FitSpec) bool {
	return f.FromSweep != nil || model.FitKindOf(f.Request) == types.FitKindRegression
}

func typeNames(decls map[types.PostFitType]Decl) []string {
	out := make([]string, 0, len(decls))
	for n := range decls {
		out = append(out, string(n))
	}
	slices.Sort(out)
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func invalid(i int, label, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{Slot: i, "label": label, "reason": reason}
	maps.Copy(details, extra)
	return errors.NewCodedErrorWithDetails(errors.PULSE_POSTFIT_INVALID,
		fmt.Sprintf("post_fit[%d] %q: %s", i, label, msg), details)
}

// CheckSchema is the registration's output-schema rule (pulse.New):
// at least one scalar or table; every scalar and table name non-empty
// and unique across both lists; every table with at least one column,
// its column names non-empty and unique and its kinds float or string.
// It returns the failing reason and a message, "" when the schema is
// well formed.
func CheckSchema(s extend.PostFitSchema) (reason, msg string) {
	if len(s.Scalars) == 0 && len(s.Tables) == 0 {
		return "empty_output_schema", "Outputs declares no scalar and no table"
	}
	seen := map[string]bool{}
	for _, n := range s.Scalars {
		if n == "" {
			return "empty_output_name", "Outputs declares a scalar with an empty name"
		}
		if seen[n] {
			return "duplicate_output_name", "Outputs declares " + strconv.Quote(n) + " more than once"
		}
		seen[n] = true
	}
	for _, t := range s.Tables {
		if t.Name == "" {
			return "empty_output_name", "Outputs declares a table with an empty name"
		}
		if seen[t.Name] {
			return "duplicate_output_name", "Outputs declares " + strconv.Quote(t.Name) + " more than once"
		}
		seen[t.Name] = true
		if len(t.Columns) == 0 {
			return "empty_table", "Outputs table " + strconv.Quote(t.Name) + " declares no column"
		}
		cols := map[string]bool{}
		for _, c := range t.Columns {
			if c.Name == "" {
				return "empty_column_name", "Outputs table " + strconv.Quote(t.Name) + " declares a column with an empty name"
			}
			if cols[c.Name] {
				return "duplicate_column_name", "Outputs table " + strconv.Quote(t.Name) + " declares column " + strconv.Quote(c.Name) + " more than once"
			}
			cols[c.Name] = true
			if c.Kind != extend.PostFitFloat && c.Kind != extend.PostFitString {
				return "invalid_column_kind", "Outputs table " + strconv.Quote(t.Name) + " column " + strconv.Quote(c.Name) + " has kind " + strconv.Quote(string(c.Kind)) + "; want float or string"
			}
		}
	}
	return "", ""
}

// CheckOutput holds one run's output to the declared schema: exactly
// the declared scalars (any float64, NaN included) and tables, each
// table's Columns equal to its declared names in order, and every row
// one cell per column of its column's kind (float64 / string) or nil.
// A breach is PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH with details
// {post_fit: i, label, type, reason, …}.
func CheckOutput(i int, label string, d Decl, out extend.PostFitOutput) *errors.CodedError {
	bad := func(reason, msg string, extra map[string]any) *errors.CodedError {
		details := map[string]any{Slot: i, "label": label, "type": string(d.Name), "reason": reason}
		maps.Copy(details, extra)
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH,
			fmt.Sprintf("post_fit[%d] %q: operator %s returned output its declared Outputs do not admit: %s", i, label, d.Name, msg), details)
	}
	declared := map[string]bool{}
	for _, n := range d.Outputs.Scalars {
		declared[n] = true
		if _, ok := out.Scalars[n]; !ok {
			return bad("missing_scalar", "declared scalar "+strconv.Quote(n)+" is absent", map[string]any{"name": n})
		}
	}
	for _, n := range slices.Sorted(maps.Keys(out.Scalars)) {
		if !declared[n] {
			return bad("undeclared_scalar", "scalar "+strconv.Quote(n)+" is not declared", map[string]any{"name": n})
		}
	}
	tables := map[string]extend.PostFitTableSchema{}
	for _, ts := range d.Outputs.Tables {
		tables[ts.Name] = ts
		if out.Tables[ts.Name] == nil {
			return bad("missing_table", "declared table "+strconv.Quote(ts.Name)+" is absent", map[string]any{"name": ts.Name})
		}
	}
	for _, n := range slices.Sorted(maps.Keys(out.Tables)) {
		ts, ok := tables[n]
		if !ok {
			return bad("undeclared_table", "table "+strconv.Quote(n)+" is not declared", map[string]any{"name": n})
		}
		t := out.Tables[n]
		if t == nil {
			return bad("missing_table", "declared table "+strconv.Quote(n)+" is absent", map[string]any{"name": n})
		}
		want := make([]string, len(ts.Columns))
		for k, c := range ts.Columns {
			want[k] = c.Name
		}
		if !slices.Equal(t.Columns, want) {
			return bad("columns_mismatch", "table "+strconv.Quote(n)+" columns are not its declared columns in order",
				map[string]any{"name": n, "expected": want, "got": nonNil(t.Columns)})
		}
		for r, row := range t.Rows {
			if len(row) != len(want) {
				return bad("row_width", "table "+strconv.Quote(n)+" row "+strconv.Itoa(r)+" holds "+strconv.Itoa(len(row))+" cells, not "+strconv.Itoa(len(want)),
					map[string]any{"name": n, "row": r})
			}
			for c, cell := range row {
				if cell == nil {
					continue
				}
				ok := false
				switch ts.Columns[c].Kind {
				case extend.PostFitFloat:
					_, ok = cell.(float64)
				case extend.PostFitString:
					_, ok = cell.(string)
				}
				if !ok {
					return bad("cell_kind", fmt.Sprintf("table %q row %d column %q holds a %T, not a %s or null", n, r, want[c], cell, ts.Columns[c].Kind),
						map[string]any{"name": n, "row": r, "column": want[c], "expected": string(ts.Columns[c].Kind), "got": fmt.Sprintf("%T", cell)})
				}
			}
		}
	}
	return nil
}

// Wire is a checked output as the entry's result: deep copies, so the
// operator keeps no alias into the response.
func Wire(spec *types.PostFitSpec, out extend.PostFitOutput) types.PostFitResult {
	res := types.PostFitResult{Label: spec.Label, Type: spec.Type}
	if len(out.Scalars) > 0 {
		res.Scalars = maps.Clone(out.Scalars)
	}
	if len(out.Tables) > 0 {
		res.Tables = make(map[string]*types.PostFitTable, len(out.Tables))
		for n, t := range out.Tables {
			rows := make([][]any, len(t.Rows))
			for r, row := range t.Rows {
				rows[r] = slices.Clone(row)
			}
			res.Tables[n] = &types.PostFitTable{Columns: slices.Clone(t.Columns), Rows: rows}
		}
	}
	return res
}

// Failure is the entry's error as a result: identity and error only.
func Failure(spec *types.PostFitSpec, se *types.SlotError) types.PostFitResult {
	return types.PostFitResult{Label: spec.Label, Type: spec.Type, Error: se}
}

// SlotErrorOf is an operator error as the entry's SlotError: a coded
// error keeps its code, message and details; any other is
// PULSE_POSTFIT_FAILED.
func SlotErrorOf(spec *types.PostFitSpec, err error) *types.SlotError {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		se := &types.SlotError{Code: string(ce.Code), Message: ce.Message}
		if len(ce.Details) > 0 {
			se.Details = maps.Clone(ce.Details)
		}
		return se
	}
	return &types.SlotError{Code: string(errors.PULSE_POSTFIT_FAILED),
		Message: "post-fit operator " + string(spec.Type) + " failed: " + err.Error(),
		Details: map[string]any{"label": spec.Label, "type": string(spec.Type)}}
}

// nan is the absent kernel parameter.
func orNaN(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}

package io

import (
	"context"
	stderrors "errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/iocore"
)

// Import-time vector folding (format 0x02, VECTORS section).
//
// A wide-format source stores one logical vector as N sibling scalar
// columns (rate_a .. rate_e). Declaring a vector at import folds those
// columns into ONE native vec_f32 / vec_f64 field: the members vanish
// from the cohort and the vector field takes the position of its first
// member in source order. Folding is a row-assembly step between the
// stringly []string reader and rowConverter (foldReader): the folded
// header carries the vector's name, the folded row carries a presence
// marker in its cell, and the converter reads the element cells through
// the fold (rowConverter.vecCells) — so no vector ever squeezes through
// the 32-byte wideFieldBytes scratch, and every downstream step
// (inference, by-name explicit-schema matching, null promotion, the
// measured predict) sees one column per field exactly as before.
//
// A declaration with no Columns and no Pattern names a NATIVE list
// column (Arrow FixedSizeList / LIST, Parquet LIST, a JSON array) of the
// same name. Its elements are read TYPED through iocore.ListAwareReader
// — the fork happens before the source's pipe-joined row text is ever
// consulted — and the vector keeps the column's position. Its dim is
// the length of the first non-null list (a plan-time probe pass, then a
// Reset); every list must have that length, hold only numbers and nulls
// (a null element is NaN, an all-null list a null vector, a null cell a
// null vector), and anything else refuses the whole import with
// PULSE_IMPORT_VECTOR_INVALID (reasons list_length_varies,
// element_not_numeric, not_a_list, list_dim_unknown, dim_out_of_range,
// malformed_list_cell for a ListCell outside its contract).
// No labels are written: no list format carries element names. A source
// with no typed lists (CSV / TSV / Excel) refuses the declaration with
// reason "no_columns". Without a column-less declaration the list
// channel is never consulted, so a list column still rides the set_*
// ladder byte-identically.

// VectorDecl declares one native vector field at import.
//
// Name is the cohort field the vector becomes. Element is "f64" (the
// default when empty) or "f32", selecting vec_f64 / vec_f32. Exactly
// one of Columns (the member columns, in element order) and Pattern (a
// Go regular expression matched against every source column name; the
// members are the matches in SOURCE column order) names the members.
// Labels come from the pattern's first capture group when it has one,
// else from the full member column names, and are always written.
// Kind is the optional vector kind (measure, scale, composition,
// binary); ScaleMin / ScaleMax the optional declared value range, both
// or neither, ScaleMin < ScaleMax. Kind is never inferred.
type VectorDecl struct {
	Name     string   `json:"name"`
	Element  string   `json:"element,omitempty"`
	Columns  []string `json:"columns,omitempty"`
	Pattern  string   `json:"pattern,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	ScaleMin *float64 `json:"scale_min,omitempty"`
	ScaleMax *float64 `json:"scale_max,omitempty"`
}

// vectorInvalid builds the PULSE_IMPORT_VECTOR_INVALID refusal. details
// always carry "reason"; "vector" names the declaration when known.
func vectorInvalid(reason, msg string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details["reason"] = reason
	return errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_VECTOR_INVALID, msg, details)
}

// fieldType resolves Element to the vec field type.
func (d VectorDecl) fieldType() (encoding.FieldType, error) {
	switch d.Element {
	case "", "f64":
		return encoding.FieldTypeVecF64, nil
	case "f32":
		return encoding.FieldTypeVecF32, nil
	}
	return 0, vectorInvalid("element_invalid",
		fmt.Sprintf("vector %q: element %q is not f32 or f64", d.Name, d.Element),
		map[string]any{"vector": d.Name, "element": d.Element, "valid": []string{"f32", "f64"}})
}

// kindMeta validates Kind / ScaleMin / ScaleMax and returns them as
// VECTORS metadata.
func (d VectorDecl) kindMeta() (encoding.VectorKind, float64, float64, bool, error) {
	kind := encoding.VectorKindNone
	if d.Kind != "" {
		k, ok := encoding.ParseVectorKind(d.Kind)
		if !ok {
			return 0, 0, 0, false, vectorInvalid("kind_invalid",
				fmt.Sprintf("vector %q: kind %q is not one of measure, scale, composition, binary", d.Name, d.Kind),
				map[string]any{"vector": d.Name, "kind": d.Kind, "valid": vectorKindNames()})
		}
		kind = k
	}
	if (d.ScaleMin == nil) != (d.ScaleMax == nil) {
		return 0, 0, 0, false, vectorInvalid("scale_invalid",
			fmt.Sprintf("vector %q: a scale range needs both scale_min and scale_max", d.Name),
			map[string]any{"vector": d.Name})
	}
	if d.ScaleMin == nil {
		return kind, 0, 0, false, nil
	}
	lo, hi := *d.ScaleMin, *d.ScaleMax
	if math.IsNaN(lo) || math.IsInf(lo, 0) || math.IsNaN(hi) || math.IsInf(hi, 0) || lo >= hi {
		return 0, 0, 0, false, vectorInvalid("scale_invalid",
			fmt.Sprintf("vector %q: scale range must be finite with scale_min < scale_max", d.Name),
			map[string]any{"vector": d.Name, "scale_min": strconv.FormatFloat(lo, 'g', -1, 64), "scale_max": strconv.FormatFloat(hi, 'g', -1, 64)})
	}
	return kind, lo, hi, true, nil
}

func vectorKindNames() []string {
	var out []string
	for k := encoding.VectorKindMeasure; k.IsKnown(); k++ {
		out = append(out, k.String())
	}
	return out
}

// ParseVectorDecl parses the CLI form of one vector declaration:
//
//	NAME:COL1,COL2,...          listed member columns, element order as listed
//	NAME:/REGEX/                members = columns the regex matches, source order
//	NAME:f32:COL1,... / NAME:f64:/REGEX/   with an explicit element type
//	NAME / NAME:f32             a native list column of that name
//
// The element defaults to f64. Names are trimmed; the regex is taken
// verbatim between its slashes (it may contain ':' or ','). A column
// literally named "f32" or "f64" cannot be the sole listed member in
// this form (use VectorDecl directly). An empty name, an empty column
// name or an empty regex is PULSE_IMPORT_VECTOR_INVALID (reason
// "malformed").
func ParseVectorDecl(s string) (VectorDecl, error) {
	bad := func(msg string) (VectorDecl, error) {
		return VectorDecl{}, vectorInvalid("malformed",
			"vector declaration "+strconv.Quote(s)+": "+msg,
			map[string]any{"declaration": s})
	}
	name, rest, hasRest := strings.Cut(s, ":")
	name = strings.TrimSpace(name)
	if name == "" {
		return bad("expected NAME[:f32|f64]:COL1,COL2,... or NAME:/REGEX/ with a non-empty NAME")
	}
	d := VectorDecl{Name: name}
	if !hasRest {
		return d, nil
	}
	for _, el := range []string{"f32", "f64"} {
		if rest == el {
			d.Element = el
			return d, nil
		}
		if strings.HasPrefix(rest, el+":") {
			d.Element = el
			rest = rest[len(el)+1:]
			break
		}
	}
	trimmed := strings.TrimSpace(rest)
	if len(trimmed) >= 2 && trimmed[0] == '/' && trimmed[len(trimmed)-1] == '/' {
		d.Pattern = trimmed[1 : len(trimmed)-1]
		if d.Pattern == "" {
			return bad("empty regular expression between the slashes")
		}
		return d, nil
	}
	for _, c := range strings.Split(rest, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			return bad("expected COL1,COL2,... with no empty column names")
		}
		d.Columns = append(d.Columns, c)
	}
	return d, nil
}

// ApplyVectorKinds parses each CLI `--vector-kind NAME:KIND[:MIN:MAX]`
// value and sets Kind / ScaleMin / ScaleMax on the declaration it names.
// A value naming no declared vector (reason "kind_unknown_vector"), a
// vector given two kinds ("kind_duplicate"), a malformed value
// ("malformed"), an unknown kind ("kind_invalid") or a bad range
// ("scale_invalid") is PULSE_IMPORT_VECTOR_INVALID.
func ApplyVectorKinds(decls []VectorDecl, kinds []string) error {
	seen := map[string]bool{}
	for _, s := range kinds {
		parts := strings.Split(s, ":")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		if (len(parts) != 2 && len(parts) != 4) || parts[0] == "" || parts[1] == "" {
			return vectorInvalid("malformed",
				"vector kind "+strconv.Quote(s)+": expected NAME:KIND or NAME:KIND:MIN:MAX",
				map[string]any{"declaration": s})
		}
		name := parts[0]
		at := -1
		for i := range decls {
			if decls[i].Name == name {
				at = i
				break
			}
		}
		if at < 0 {
			defined := make([]string, len(decls))
			for i := range decls {
				defined[i] = decls[i].Name
			}
			return vectorInvalid("kind_unknown_vector",
				fmt.Sprintf("vector kind %q names vector %q, which no vector declaration defines", s, name),
				map[string]any{"vector": name, "declaration": s, "defined": defined})
		}
		if seen[name] {
			return vectorInvalid("kind_duplicate",
				fmt.Sprintf("vector %q is given a kind more than once", name),
				map[string]any{"vector": name, "declaration": s})
		}
		seen[name] = true
		d := decls[at]
		d.Kind = parts[1]
		d.ScaleMin, d.ScaleMax = nil, nil
		if len(parts) == 4 {
			lo, err1 := strconv.ParseFloat(parts[2], 64)
			hi, err2 := strconv.ParseFloat(parts[3], 64)
			if err1 != nil || err2 != nil {
				return vectorInvalid("scale_invalid",
					fmt.Sprintf("vector kind %q: scale bounds must be numbers", s),
					map[string]any{"vector": name, "declaration": s})
			}
			d.ScaleMin, d.ScaleMax = &lo, &hi
		}
		if _, _, _, _, err := d.kindMeta(); err != nil {
			return err
		}
		decls[at] = d
	}
	return nil
}

// resolvedVector is one declaration resolved against a source header.
type resolvedVector struct {
	name    string
	typ     encoding.FieldType
	members []int    // source column indices, element order
	cols    []string // the member column names, element order
	meta    encoding.VectorField
	// list marks a native list column (members is that one column);
	// its elements come through iocore.ListAwareReader.
	list bool
}

// vectorFold is the resolved fold plan of a header: which source
// columns become which folded column, and the per-row scratch.
type vectorFold struct {
	vectors []resolvedVector
	header  []string // folded header
	// srcOf[c] is folded column c's source column, or -1 for a vector.
	srcOf []int
	// vecOf[c] is folded column c's vector index, or -1 for a scalar.
	vecOf []int
	// byName maps a vector's name to its index.
	byName map[string]int

	// Per-row scratch, reused across rows. lists[k] is list vector k's
	// typed cell (unused for a column fold).
	row   []string
	nulls []bool
	cells [][]string
	lists []iocore.ListCell
	// rowNum counts the source rows folded, for refusal details.
	rowNum int
}

// vectorPresent is the folded cell of a vector with at least one
// present element: any non-null token works, since the converter reads
// the elements through the fold, never this text.
const vectorPresent = "\x1evector"

// planVectorFold resolves decls against the source header and the
// declared groups. lists is the source's typed list channel (nil when
// it has none). A list vector leaves planning with dim 0: probeListDims
// takes it from the data. Every refusal is PULSE_IMPORT_VECTOR_INVALID.
func planVectorFold(header []string, decls []VectorDecl, groups []GroupDecl, lists iocore.ListAwareReader) (*vectorFold, error) {
	colIdx := make(map[string]int, len(header))
	for i, c := range header {
		if _, dup := colIdx[c]; !dup {
			colIdx[c] = i
		}
	}
	claimed := map[int]string{} // source column -> vector name
	fold := &vectorFold{byName: map[string]int{}}
	for _, d := range decls {
		if strings.TrimSpace(d.Name) == "" {
			return nil, vectorInvalid("malformed", "vector declaration has an empty name", nil)
		}
		if _, dup := fold.byName[d.Name]; dup {
			return nil, vectorInvalid("duplicate_name",
				fmt.Sprintf("vector %q is declared more than once", d.Name),
				map[string]any{"vector": d.Name})
		}
		typ, err := d.fieldType()
		if err != nil {
			return nil, err
		}
		kind, lo, hi, hasScale, err := d.kindMeta()
		if err != nil {
			return nil, err
		}
		rv := resolvedVector{name: d.Name, typ: typ}
		var labels []string
		switch {
		case len(d.Columns) > 0 && d.Pattern != "":
			return nil, vectorInvalid("malformed",
				fmt.Sprintf("vector %q: declare columns or a pattern, not both", d.Name),
				map[string]any{"vector": d.Name})
		case len(d.Columns) > 0:
			for _, c := range d.Columns {
				i, ok := colIdx[c]
				if !ok {
					return nil, vectorInvalid("column_unknown",
						fmt.Sprintf("vector %q names column %q, which the source header does not carry", d.Name, c),
						map[string]any{"vector": d.Name, "column": c, "columns": append([]string(nil), header...)})
				}
				rv.members = append(rv.members, i)
				rv.cols = append(rv.cols, c)
				labels = append(labels, c)
			}
		case d.Pattern != "":
			re, err := regexp.Compile(d.Pattern)
			if err != nil {
				return nil, vectorInvalid("pattern_invalid",
					fmt.Sprintf("vector %q: pattern %q does not compile: %v", d.Name, d.Pattern, err),
					map[string]any{"vector": d.Name, "pattern": d.Pattern, "error": err.Error()})
			}
			for i, c := range header {
				m := re.FindStringSubmatch(c)
				if m == nil {
					continue
				}
				rv.members = append(rv.members, i)
				rv.cols = append(rv.cols, c)
				if re.NumSubexp() >= 1 {
					labels = append(labels, m[1])
				} else {
					labels = append(labels, c)
				}
			}
			if len(rv.members) == 0 {
				return nil, vectorInvalid("pattern_no_match",
					fmt.Sprintf("vector %q: pattern %q matches no source column", d.Name, d.Pattern),
					map[string]any{"vector": d.Name, "pattern": d.Pattern, "columns": append([]string(nil), header...)})
			}
		case lists == nil:
			return nil, vectorInvalid("no_columns",
				fmt.Sprintf("vector %q declares no columns or pattern; reading a native list column needs a source with typed lists (Arrow, Parquet, NDJSON, JSON array), which this one is not — declare the member columns with --vector %s:COL1,COL2,...", d.Name, d.Name),
				map[string]any{"vector": d.Name})
		default:
			i, ok := colIdx[d.Name]
			if !ok {
				return nil, vectorInvalid("column_unknown",
					fmt.Sprintf("vector %q names list column %q, which the source header does not carry", d.Name, d.Name),
					map[string]any{"vector": d.Name, "column": d.Name, "columns": append([]string(nil), header...)})
			}
			rv.members = []int{i}
			rv.cols = []string{d.Name}
			rv.list = true
		}
		for _, m := range rv.members {
			if prev, taken := claimed[m]; taken {
				return nil, vectorInvalid("column_claimed",
					fmt.Sprintf("column %q is claimed by vector %q and vector %q", header[m], prev, d.Name),
					map[string]any{"column": header[m], "vectors": []string{prev, d.Name}})
			}
			claimed[m] = d.Name
		}
		dim := len(rv.members)
		if rv.list {
			dim = 0 // probeListDims
		} else if dim < encoding.MinVectorDim || dim > encoding.MaxVectorDim {
			return nil, vectorInvalid("dim_out_of_range",
				fmt.Sprintf("vector %q folds %d columns; a vector's dim must be %d..%d", d.Name, dim, encoding.MinVectorDim, encoding.MaxVectorDim),
				map[string]any{"vector": d.Name, "dim": dim, "min_dim": encoding.MinVectorDim, "max_dim": encoding.MaxVectorDim})
		}
		rv.meta = encoding.VectorField{Dim: dim, Kind: kind, ScaleMin: lo, ScaleMax: hi, HasScale: hasScale, Labels: labels}
		fold.byName[d.Name] = len(fold.vectors)
		fold.vectors = append(fold.vectors, rv)
	}

	// A column a vector folds away cannot also be a group's key or
	// member: after the fold it is no field at all.
	for gi, g := range groups {
		for _, name := range append(append([]string(nil), g.Key...), g.Members...) {
			if i, ok := colIdx[name]; ok {
				if v, taken := claimed[i]; taken {
					return nil, vectorInvalid("column_claimed",
						fmt.Sprintf("column %q is claimed by vector %q and by parent group %d", name, v, gi+1),
						map[string]any{"column": name, "vector": v, "group": gi + 1})
				}
			}
		}
	}

	// The folded header: every unclaimed column in place, each vector
	// at its first member's source position.
	first := make(map[int]int, len(fold.vectors)) // source col -> vector index
	for k := range fold.vectors {
		lo := fold.vectors[k].members[0]
		for _, m := range fold.vectors[k].members {
			if m < lo {
				lo = m
			}
		}
		first[lo] = k
	}
	names := map[string]bool{}
	for i, c := range header {
		if k, ok := first[i]; ok {
			fold.header = append(fold.header, fold.vectors[k].name)
			fold.srcOf = append(fold.srcOf, -1)
			fold.vecOf = append(fold.vecOf, k)
			names[fold.vectors[k].name] = true
			continue
		}
		if _, folded := claimed[i]; folded {
			continue
		}
		fold.header = append(fold.header, c)
		fold.srcOf = append(fold.srcOf, i)
		fold.vecOf = append(fold.vecOf, -1)
	}
	for i, c := range fold.header {
		if fold.vecOf[i] < 0 && names[c] {
			return nil, vectorInvalid("name_collision",
				fmt.Sprintf("vector %q has the name of source column %q, which it does not fold", c, c),
				map[string]any{"vector": c, "column": c})
		}
	}
	fold.cells = make([][]string, len(fold.vectors))
	fold.lists = make([]iocore.ListCell, len(fold.vectors))
	for k := range fold.vectors {
		fold.cells[k] = make([]string, len(fold.vectors[k].members))
	}
	return fold, nil
}

// hasLists reports whether any vector reads a native list column.
func (f *vectorFold) hasLists() bool {
	for k := range f.vectors {
		if f.vectors[k].list {
			return true
		}
	}
	return false
}

// probeListDims fixes every list vector's dim to the length of its
// first non-null list, reading rows off src until each has one (the
// cells read on the way are checked exactly as the row pass checks
// them), then rewinds src and re-reads its header. A list column with
// no non-null list at all is list_dim_unknown; a dim outside
// MinVectorDim..MaxVectorDim is dim_out_of_range.
func (f *vectorFold) probeListDims(ctx context.Context, src iocore.ListAwareReader) error {
	pending := 0
	for k := range f.vectors {
		if f.vectors[k].list && f.vectors[k].meta.Dim == 0 {
			pending++
		}
	}
	if pending == 0 {
		return nil
	}
	rr, ok := src.(ResetReader)
	if !ok {
		return vectorInvalid("no_columns",
			"reading a native list column needs a source that can be rewound after its dim is probed, which this one cannot",
			nil)
	}
	ns, _ := src.(NullAwareReader)
	row := 0
	err := src.ReadRows(ctx, func([]string) error {
		row++
		var srcNulls []bool
		if ns != nil {
			srcNulls = ns.RowNulls()
		}
		for k := range f.vectors {
			v := &f.vectors[k]
			if !v.list || v.meta.Dim != 0 {
				continue
			}
			col := v.members[0]
			if srcNulls != nil && col < len(srcNulls) && srcNulls[col] {
				continue
			}
			cell := &f.lists[k]
			if err := src.RowList(col, cell); err != nil {
				return listReadFailure(v, row, err)
			}
			if err := checkListCell(v, cell, row, false); err != nil {
				return err
			}
			if cell.State != iocore.ListValues {
				continue
			}
			n := len(cell.Values)
			if n < encoding.MinVectorDim || n > encoding.MaxVectorDim {
				return vectorInvalid("dim_out_of_range",
					fmt.Sprintf("vector %q: list column %q holds %d-element lists (row %d); a vector's dim must be %d..%d", v.name, v.cols[0], n, row, encoding.MinVectorDim, encoding.MaxVectorDim),
					map[string]any{"vector": v.name, "column": v.cols[0], "dim": n, "row": row, "min_dim": encoding.MinVectorDim, "max_dim": encoding.MaxVectorDim})
			}
			v.meta.Dim = n
			pending--
		}
		if pending == 0 {
			return errStopIteration
		}
		return nil
	})
	if err != nil && err != errStopIteration {
		return err
	}
	for k := range f.vectors {
		v := &f.vectors[k]
		if v.list && v.meta.Dim == 0 {
			return vectorInvalid("list_dim_unknown",
				fmt.Sprintf("vector %q: list column %q holds no non-null list to take a dim from", v.name, v.cols[0]),
				map[string]any{"vector": v.name, "column": v.cols[0]})
		}
	}
	if err := rr.Reset(); err != nil {
		return fmt.Errorf("resetting reader after the vector list probe: %w", err)
	}
	_, err = src.ReadHeader()
	return err
}

// listReadFailure carries a ListAwareReader.RowList error — the source
// failed to READ the cell — out of the import exactly as a ReadRows
// failure travels: never restamped. A *errors.CodedError anywhere in the
// chain keeps its code (and its own details, gaining the vector, column
// and row keys it did not set) and stays reachable through errors.As /
// errors.Is; an uncoded error stays uncoded, wrapped with the vector,
// column and row in its message. row is the 1-based source row.
func listReadFailure(v *resolvedVector, row int, err error) error {
	where := fmt.Sprintf("vector %q: reading list column %q at row %d", v.name, v.cols[0], row)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return fmt.Errorf("%s: %w", where, err)
	}
	details := make(map[string]any, len(ce.Details)+3)
	maps.Copy(details, ce.Details)
	for key, val := range map[string]any{"vector": v.name, "column": v.cols[0], "row": row} {
		if _, set := details[key]; !set {
			details[key] = val
		}
	}
	return &errors.CodedError{
		Code:    ce.Code,
		Message: where + ": " + ce.Message,
		Details: details,
		Cause:   err,
	}
}

// checkListCell refuses a list cell a vector cannot hold: a non-list
// value (not_a_list), a non-numeric element (element_not_numeric) and,
// when sized, a list whose length is not the vector's dim
// (list_length_varies). row is the 1-based source row.
func checkListCell(v *resolvedVector, cell *iocore.ListCell, row int, sized bool) error {
	switch cell.State {
	case iocore.ListNotList:
		return vectorInvalid("not_a_list",
			fmt.Sprintf("vector %q: column %q holds %q at row %d, which is not a list", v.name, v.cols[0], cell.BadValue, row),
			map[string]any{"vector": v.name, "column": v.cols[0], "row": row, "value": cell.BadValue})
	case iocore.ListNonNumeric:
		return vectorInvalid("element_not_numeric",
			fmt.Sprintf("vector %q: column %q row %d element %d is %s, which is not a number", v.name, v.cols[0], row, cell.BadIndex, cell.BadValue),
			map[string]any{"vector": v.name, "column": v.cols[0], "row": row, "element": cell.BadIndex, "value": cell.BadValue})
	case iocore.ListNull:
	case iocore.ListValues:
		if err := normalizeListCell(v, cell, row); err != nil {
			return err
		}
		if sized && len(cell.Values) != v.meta.Dim {
			return vectorInvalid("list_length_varies",
				fmt.Sprintf("vector %q: column %q row %d holds a %d-element list, but the vector's dim is %d (every list must have the same length)", v.name, v.cols[0], row, len(cell.Values), v.meta.Dim),
				map[string]any{"vector": v.name, "column": v.cols[0], "row": row, "length": len(cell.Values), "dim": v.meta.Dim})
		}
	default:
		return vectorInvalid("malformed_list_cell",
			fmt.Sprintf("vector %q: the source reported list state %d for column %q row %d, which is not a ListState", v.name, cell.State, v.cols[0], row),
			map[string]any{"vector": v.name, "column": v.cols[0], "row": row, "state": int(cell.State)})
	}
	return nil
}

// normalizeListCell holds a ListValues cell to the ListCell contract a
// reader outside this module may not have kept: Nulls nil means no null
// element (padded to len(Values) so the all-null check sees every
// element), any other length mismatch is malformed_list_cell, and a
// null element is NaN whatever Values held. The built-in sources fill
// cells through Append, which already keeps all three.
func normalizeListCell(v *resolvedVector, cell *iocore.ListCell, row int) error {
	switch {
	case len(cell.Nulls) == len(cell.Values):
	case len(cell.Nulls) == 0:
		for range cell.Values {
			cell.Nulls = append(cell.Nulls, false)
		}
	default:
		return vectorInvalid("malformed_list_cell",
			fmt.Sprintf("vector %q: the source reported %d element values but %d null marks for column %q row %d; Nulls must be nil or as long as Values", v.name, len(cell.Values), len(cell.Nulls), v.cols[0], row),
			map[string]any{"vector": v.name, "column": v.cols[0], "row": row, "length": len(cell.Values), "nulls_length": len(cell.Nulls)})
	}
	for e, null := range cell.Nulls {
		if null {
			cell.Values[e] = math.NaN()
		}
	}
	return nil
}

// foldRow assembles the folded row (and folded declared-null channel)
// of one source row into the fold's scratch. A vector's cell is "" when
// every element cell is null (a null vector) and vectorPresent
// otherwise; its element cells — a declared-null element blanked — are
// left in fold.cells for the converter. A list vector's typed cell is
// read off lists into fold.lists (a null cell, or a list of nulls only,
// is a null vector); a cell it cannot hold refuses the import.
func (f *vectorFold) foldRow(src []string, srcNulls []bool, lists iocore.ListAwareReader) ([]string, []bool, error) {
	f.rowNum++
	f.row = f.row[:0]
	f.nulls = f.nulls[:0]
	for c := range f.header {
		if s := f.srcOf[c]; s >= 0 {
			var cell string
			if s < len(src) {
				cell = src[s]
			}
			f.row = append(f.row, cell)
			if srcNulls != nil {
				f.nulls = append(f.nulls, s < len(srcNulls) && srcNulls[s])
			}
			continue
		}
		k := f.vecOf[c]
		allNull := true
		if v := &f.vectors[k]; v.list {
			col := v.members[0]
			cell := &f.lists[k]
			if (srcNulls != nil && col < len(srcNulls) && srcNulls[col]) || lists == nil {
				cell.Reset(iocore.ListNull)
			} else if err := lists.RowList(col, cell); err != nil {
				return nil, nil, listReadFailure(v, f.rowNum, err)
			}
			if err := checkListCell(v, cell, f.rowNum, true); err != nil {
				return nil, nil, err
			}
			if cell.State == iocore.ListValues {
				for _, null := range cell.Nulls {
					if !null {
						allNull = false
						break
					}
				}
			}
		}
		for e, m := range f.vectors[k].members {
			if f.vectors[k].list {
				break
			}
			var cell string
			if m < len(src) {
				cell = strings.TrimSpace(src[m])
			}
			if srcNulls != nil && m < len(srcNulls) && srcNulls[m] {
				cell = ""
			}
			f.cells[k][e] = cell
			if !isNullToken(cell) {
				allNull = false
			}
		}
		if allNull {
			f.row = append(f.row, "")
		} else {
			f.row = append(f.row, vectorPresent)
		}
		if srcNulls != nil {
			f.nulls = append(f.nulls, allNull)
		}
	}
	if srcNulls == nil {
		return f.row, nil, nil
	}
	return f.row, f.nulls, nil
}

// elements returns the current row's element cells of the vector at
// folded column col and the member column names (for errors) — or, for
// a list vector, its typed element values (NaN at a null element) —
// all nil when col is not a vector.
func (f *vectorFold) elements(col int) ([]string, []string, []float64) {
	if col < 0 || col >= len(f.vecOf) || f.vecOf[col] < 0 {
		return nil, nil, nil
	}
	k := f.vecOf[col]
	if f.vectors[k].list {
		return nil, f.vectors[k].cols, f.lists[k].Values
	}
	return f.cells[k], f.vectors[k].cols, nil
}

// field returns the vec field and its VECTORS metadata the vector named
// name becomes.
func (f *vectorFold) vector(name string) (resolvedVector, bool) {
	k, ok := f.byName[name]
	if !ok {
		return resolvedVector{}, false
	}
	return f.vectors[k], true
}

// foldReader is the row-assembly step: a Reader over the folded view of
// its inner source. The optional interfaces the import consults on its
// source (ResetReader, NullAwareReader, LateKeyRefuser) are forwarded;
// the job-level ones (SchemaAwareReader, SourceWarningEmitter,
// SidecarEmitter) are read off the inner source directly (rawSource).
type foldReader struct {
	inner Reader
	decls []VectorDecl
	group []GroupDecl
	fold  *vectorFold
	nulls []bool // folded declared nulls of the current row
}

// plan reads the inner header (always: after a Reset the inner source
// may need its header consumed again) and plans the fold once.
func (r *foldReader) plan() (*vectorFold, error) {
	h, err := r.inner.ReadHeader()
	if err != nil {
		return nil, err
	}
	if r.fold != nil {
		return r.fold, nil
	}
	lists := r.lists()
	fold, err := planVectorFold(h, r.decls, r.group, lists)
	if err != nil {
		return nil, err
	}
	if fold.hasLists() {
		if err := fold.probeListDims(context.Background(), lists); err != nil {
			return nil, err
		}
	}
	r.fold = fold
	return fold, nil
}

// lists is the inner source's typed list channel, or nil.
func (r *foldReader) lists() iocore.ListAwareReader {
	l, _ := r.inner.(iocore.ListAwareReader)
	return l
}

// ReadHeader returns the folded header.
func (r *foldReader) ReadHeader() ([]string, error) {
	fold, err := r.plan()
	if err != nil {
		return nil, err
	}
	return fold.header, nil
}

// ReadRows hands fn each folded row.
func (r *foldReader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	fold := r.fold
	if fold == nil {
		var err error
		if fold, err = r.plan(); err != nil {
			return err
		}
	}
	ns, _ := r.inner.(NullAwareReader)
	lists := r.lists()
	fold.rowNum = 0
	return r.inner.ReadRows(ctx, func(row []string) error {
		var srcNulls []bool
		if ns != nil {
			srcNulls = ns.RowNulls()
		}
		folded, nulls, err := fold.foldRow(row, srcNulls, lists)
		if err != nil {
			return err
		}
		r.nulls = nulls
		return fn(folded)
	})
}

// RowNulls is the folded declared-null channel (nil when the inner
// source has none).
func (r *foldReader) RowNulls() []bool { return r.nulls }

// Reset rewinds the inner source.
func (r *foldReader) Reset() error {
	rr, ok := r.inner.(ResetReader)
	if !ok {
		return fmt.Errorf("schema inference requires a ResetReader source")
	}
	return rr.Reset()
}

// RefuseLateKeys forwards to a keyed inner source.
func (r *foldReader) RefuseLateKeys(on bool) {
	if lk, ok := r.inner.(LateKeyRefuser); ok {
		lk.RefuseLateKeys(on)
	}
}

// Close closes the inner source.
func (r *foldReader) Close() error { return r.inner.Close() }

// rawSource is the job's source with any fold peeled off: the
// job-level optional interfaces are the inner source's.
func (j *ImportJob) rawSource() Reader {
	if fr, ok := j.Source.(*foldReader); ok {
		return fr.inner
	}
	return j.Source
}

// beginVectorFold installs the fold reader over j.Source for the length
// of one Run / Predict and returns the restore func and the fold (nil
// without Vectors). The fold is planned here, so a bad declaration is
// refused before inference reads a row. A source carrying an
// authoritative schema (SPSS) is refused: its dictionary describes the
// unfolded columns.
func (j *ImportJob) beginVectorFold() (func(), *foldReader, error) {
	if len(j.Vectors) == 0 {
		return func() {}, nil, nil
	}
	if _, ok := j.Source.(SchemaAwareReader); ok {
		if s, err := readerSchema(j.Source); err == nil && s != nil {
			return func() {}, nil, vectorInvalid("authoritative_source",
				"vector declarations fold text columns; this source carries an authoritative schema (SPSS) and cannot be folded",
				nil)
		}
	}
	fr := &foldReader{inner: j.Source, decls: j.Vectors, group: j.Groups}
	if _, err := fr.plan(); err != nil {
		return func() {}, nil, err
	}
	orig := j.Source
	j.Source = fr
	return func() { j.Source = orig }, fr, nil
}

// vectorCells is the converter's element source (nil without a fold).
func (fr *foldReader) vectorCells() func(col int) ([]string, []string, []float64) {
	if fr == nil || fr.fold == nil {
		return nil
	}
	return fr.fold.elements
}

// inferVectorColumns is the inference hook for folded columns: name ->
// the vec type and metadata the column takes (nil without a fold).
func (fr *foldReader) inferVectorColumns() map[string]resolvedVector {
	if fr == nil {
		return nil
	}
	out := make(map[string]resolvedVector, len(fr.fold.vectors))
	for _, v := range fr.fold.vectors {
		out[v.name] = v
	}
	return out
}

// reconcileExplicitVectors checks a user-authored schema (already
// matched by name to the folded header) against the fold: every vec
// field must be a declared vector of the same dim (and element type
// when the declaration names one), and every declared vector the
// schema names must be a vec field. A schema entry with no labels takes
// the fold's; a declared kind / scale wins over the schema's. Returns
// the schema with its own VECTORS copy.
func reconcileExplicitVectors(schema *encoding.Schema, fr *foldReader) (*encoding.Schema, error) {
	vecs := encx.CloneVectors(schema.Vectors)
	entry := func(i int) *encoding.VectorField {
		for k := range vecs {
			if vecs[k].FieldIndex == i {
				return &vecs[k]
			}
		}
		return nil
	}
	for i := range schema.Fields {
		f := &schema.Fields[i]
		var rv resolvedVector
		var folded bool
		if fr != nil {
			rv, folded = fr.fold.vector(f.Name)
		}
		if !f.Type.IsVector() {
			if folded {
				return nil, vectorInvalid("type_mismatch",
					fmt.Sprintf("vector %q is declared, but the explicit schema types field %q as %s", f.Name, f.Name, f.Type),
					map[string]any{"vector": f.Name, "field": f.Name, "type": f.Type.String()})
			}
			continue
		}
		if !folded {
			return nil, vectorInvalid("not_declared",
				fmt.Sprintf("explicit schema field %q is %s, but no vector declaration folds columns into it; declare it with --vector %s:COL1,COL2,...", f.Name, f.Type, f.Name),
				map[string]any{"field": f.Name, "type": f.Type.String()})
		}
		e := entry(i)
		if e == nil {
			return nil, vectorInvalid("not_declared",
				fmt.Sprintf("explicit schema field %q is %s with no dim", f.Name, f.Type),
				map[string]any{"field": f.Name, "type": f.Type.String()})
		}
		if e.Dim != rv.meta.Dim {
			return nil, vectorInvalid("dim_mismatch",
				fmt.Sprintf("explicit schema field %q declares dim %d, but its vector declaration folds %d columns", f.Name, e.Dim, rv.meta.Dim),
				map[string]any{"field": f.Name, "schema_dim": e.Dim, "folded_dim": rv.meta.Dim})
		}
		decl := fr.decls[fr.fold.byName[f.Name]]
		if decl.Element != "" && rv.typ != f.Type {
			return nil, vectorInvalid("type_mismatch",
				fmt.Sprintf("vector %q declares %s, but the explicit schema types it %s", f.Name, rv.typ, f.Type),
				map[string]any{"vector": f.Name, "declared": rv.typ.String(), "type": f.Type.String()})
		}
		if len(e.Labels) == 0 {
			e.Labels = append([]string(nil), rv.meta.Labels...)
		}
		if decl.Kind != "" {
			e.Kind = rv.meta.Kind
		}
		if rv.meta.HasScale {
			e.HasScale, e.ScaleMin, e.ScaleMax = true, rv.meta.ScaleMin, rv.meta.ScaleMax
		}
	}
	out := *schema
	out.Vectors = vecs
	return &out, nil
}

// vectorCellError is a vector element that does not parse as a number.
type vectorCellError struct {
	element int
	column  string
	value   string
}

func (e *vectorCellError) Error() string {
	return fmt.Sprintf("vector element %d (column %q): %q is not a number", e.element, e.column, e.value)
}

// parseVectorCells parses one row's element cells: a null token is
// NaN, anything else must parse as a float. cols name the member
// columns in errors.
func parseVectorCells(cells []string, dst []float64, cols []string) ([]float64, error) {
	dst = dst[:0]
	for e, c := range cells {
		if isNullToken(c) {
			dst = append(dst, math.NaN())
			continue
		}
		v, err := strconv.ParseFloat(c, 64)
		if err != nil {
			col := ""
			if e < len(cols) {
				col = cols[e]
			}
			return nil, &vectorCellError{element: e, column: col, value: c}
		}
		dst = append(dst, v)
	}
	return dst, nil
}

// refuseVectorOverrides refuses a column type override to a vec type:
// an override types ONE column, and a vector's dim is a property of the
// columns it folds (declare it with Vectors instead).
func refuseVectorOverrides(overrides map[string]encoding.FieldType) error {
	var cols []string
	for c, ft := range overrides {
		if ft.IsVector() {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	sort.Strings(cols)
	return errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_OVERRIDE_INVALID,
		fmt.Sprintf("column_type_overrides forces column %q to %s; a vector has no single-column form — declare it with a vector declaration (--vector NAME:COLS) instead", cols[0], overrides[cols[0]]),
		map[string]any{"column": cols[0], "type": overrides[cols[0]].String(), "reason": "vector_type"})
}

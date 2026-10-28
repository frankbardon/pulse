package spss

// CARRYING the metadata sidecar onto a RESHAPED derived cohort.
//
// Materialize's `output` block lays the written cohort out itself: it may
// drop a column, rename one, write one twice under two names, reorder and
// redescribe them. The plain carry (carry.go) keys every recorded variable
// to a schema field of the same name, so it cannot follow that — a renamed
// column would lose its value labels and a dropped one would be refused.
// WriteProjected is the carry that can: it is handed the written columns,
// in output order, each naming the column it reads, and rewrites the
// document column for column.
//
//   - A DROPPED column removes its variable (or derived-registry entry),
//     its very-long-string declaration, its variable-set membership, every
//     multiple-response set naming it (a set cannot be written back
//     without its members) and the weight when it was the weight.
//   - A RENAMED column renames its variable: codes, value labels, missing
//     specification and formats kept, the short name kept (records 7/5,
//     7/7, 7/14 and 7/19 key by it), the new name its long name; every
//     registry source, multiple-response set field and the weight follow.
//   - A DUPLICATED column keeps its variable on the column that keeps the
//     source's own name (else the first), and every other copy gets a deep
//     copy under its own name with a freshly minted short name.
//   - A REBUILT column (ProjectedColumn.Rebuild — its values were re-coded
//     by a cast, recode or bucket) gets a variable regenerated from its new
//     type and dictionary: no value labels, codes or missing specification
//     of the old variable survive onto the new codes.
//   - A new column (a derived output) is described as the plain carry
//     describes one: a mirror of a recorded variable, else plain numeric.
//
// Anything the rewrite cannot describe truthfully refuses the carry — one
// warning, the cohort written without a sidecar — exactly as the plain
// carry does: a derived-registry column kept while a column it reads is
// dropped, a duplicated registry column or very long string, a rebuilt
// column the registry, a response set or a very-long-string layout names,
// two output names SPSS would fold together.

import (
	stdjson "encoding/json"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// ProjectedColumn describes one column of a reshaped derived cohort for
// WriteProjected, index-aligned with the written schema's fields (the
// column's own name is its schema field's).
type ProjectedColumn struct {
	// From names the source cohort field the column copies verbatim, ""
	// for a new column (a derived output).
	From string
	// Label, non-nil, replaces the carried variable's label (an output
	// column's description override). A rebuilt or new column is always
	// labelled with its field's description.
	Label *string
	// Rebuild marks a column whose values were re-coded (a cast, recode
	// or bucket): its variable is regenerated from the written field's
	// type and dictionary, never carried — see RebuildVariable.
	Rebuild bool
	// Mirror, for a new column, describes it as a recorded variable's
	// codes (Materialize's previous-state column); nil for a plain
	// numeric.
	Mirror *Mirror
}

// WriteProjected rewrites the carried document against a reshaped
// dstCohort — cols describes each of schema's fields, in order — and
// writes it beside dstCohort, fingerprinted over its bytes (which must
// already be written). A nil receiver writes nothing. The returned
// warning, when non-nil, says the sidecar was not carried and why; the
// cohort itself is unaffected.
func (c *SidecarCarry) WriteProjected(fs afero.Fs, dstCohort string, schema *encoding.Schema, cols []ProjectedColumn) *errors.CodedError {
	if c == nil {
		return nil
	}
	doc, err := c.project(schema, cols)
	if err != nil {
		return notCarried(err, c.source)
	}
	return c.write(fs, dstCohort, doc)
}

// project returns a copy of the carried document describing the reshaped
// schema, column for column (see the file comment).
func (c *SidecarCarry) project(schema *encoding.Schema, cols []ProjectedColumn) (*Document, error) {
	if len(cols) != len(schema.Fields) {
		return nil, carryRefused("", "the projection describes "+strconv.Itoa(len(cols))+
			" columns but the derived cohort has "+strconv.Itoa(len(schema.Fields)))
	}
	doc, err := c.copyDocument()
	if err != nil {
		return nil, err
	}
	// orig is never mutated: mirrors and references are resolved
	// against the source's own description.
	orig, err := c.copyDocument()
	if err != nil {
		return nil, err
	}
	op := &orig.Payload
	p := &doc.Payload

	// SPSS names are case-insensitive: two written columns folding to
	// one name cannot both bind.
	folded := make(map[string]string, len(schema.Fields))
	for _, f := range schema.Fields {
		key := strings.ToLower(f.Name)
		if first, dup := folded[key]; dup {
			return nil, carryRefused(f.Name, "SPSS variable names are case-insensitive, and it folds to the same name as "+strconv.Quote(first))
		}
		folded[key] = f.Name
	}

	varAt := make(map[string]int, len(op.Variables))
	minter := newNameMinter()
	display := false
	for i, v := range op.Variables {
		varAt[strings.ToLower(v.Name)] = i
		minter.used[strings.ToUpper(v.ShortName)] = true
		if vls := v.VeryLongString; vls != nil {
			for _, s := range vls.Segments {
				minter.used[strings.ToUpper(s.Name)] = true
			}
		}
		if v.HasDisplayParams {
			display = true
		}
	}
	regAt := make(map[string]int, len(op.Derived))
	for i, d := range op.Derived {
		regAt[strings.ToLower(d.Name)] = i
	}

	// owner is, per source column read, the written column that keeps its
	// identity: the one keeping the source's own name, else the first.
	owner := map[string]int{}
	for i, col := range cols {
		if col.From == "" {
			continue
		}
		key := strings.ToLower(col.From)
		at, seen := owner[key]
		if !seen || (schema.Fields[i].Name == col.From && schema.Fields[at].Name != col.From) {
			owner[key] = i
		}
	}
	rename := make(map[string]string, len(owner))
	for key, i := range owner {
		rename[key] = schema.Fields[i].Name
	}

	vars := make([]Variable, 0, len(cols))
	derived := []Derived{}
	for i := range cols {
		col, fld := cols[i], &schema.Fields[i]
		key := strings.ToLower(col.From)
		if col.From != "" {
			if at, ok := regAt[key]; ok {
				if owner[key] != i {
					return nil, carryRefused(fld.Name, "it copies "+strconv.Quote(col.From)+
						", a column the source's import derived, which the derived-column registry can record only once")
				}
				if col.Rebuild {
					return nil, carryRefused(fld.Name, "its values were re-coded, and the derived-column registry describes the column it was")
				}
				d := op.Derived[at]
				d.Name, d.Position = fld.Name, i
				derived = append(derived, d)
				continue
			}
			if at, ok := varAt[key]; ok {
				v, err := deepCopyVariable(op.Variables[at])
				if err != nil {
					return nil, err
				}
				if col.Rebuild {
					if why := referenced(op, key); why != "" {
						return nil, carryRefused(fld.Name, "its values were re-coded, and the sidecar's "+why+" refers to "+strconv.Quote(col.From))
					}
					if err := RebuildVariable(&v, fld, display); err != nil {
						return nil, err
					}
				}
				if owner[key] != i {
					if v.VeryLongString != nil {
						return nil, carryRefused(fld.Name, "it copies the very long string "+strconv.Quote(col.From)+", whose segment names cannot be copied")
					}
					v.ShortName = minter.mint(fld.Name)
				}
				if v.Name != fld.Name || owner[key] != i {
					v.Name, v.LongName = fld.Name, ""
					if v.ShortName != fld.Name {
						v.LongName = fld.Name
					}
				}
				if col.Label != nil {
					v.Label, v.HasLabel = *col.Label, *col.Label != ""
				}
				v.Nullable, v.Position = fld.Nullable, i
				vars = append(vars, v)
				continue
			}
		}
		// A new column: a mirror of a recorded variable, else a plain
		// numeric — the plain carry's rule.
		v := Variable{Segments: 1}
		if col.Mirror != nil {
			m, err := mirroredVariable(op, *col.Mirror, fld)
			if err != nil {
				return nil, err
			}
			v = m
		} else if err := plainNumeric(&v, fld, display); err != nil {
			return nil, err
		}
		v.Name, v.LongName, v.Position = fld.Name, "", i
		v.ShortName = minter.mint(fld.Name)
		if v.ShortName != fld.Name {
			v.LongName = fld.Name
		}
		vars = append(vars, v)
	}

	// The registry's sources follow their columns; a kept registry
	// column reading a dropped one cannot be folded back.
	for i := range derived {
		d := &derived[i]
		srcs := make([]string, len(d.Sources))
		for j, s := range d.Sources {
			to, ok := rename[strings.ToLower(s)]
			if !ok {
				return nil, carryRefused(d.Name, "the source's import derived it from "+strconv.Quote(s)+", which the output drops")
			}
			srcs[j] = to
		}
		if d.Sources != nil {
			d.Sources = srcs
		}
	}

	// Indices are recounted over the written variables, in output order.
	next := int32(1)
	for i := range vars {
		vars[i].Index = next
		next += int32(max(vars[i].Segments, 1))
	}

	// droppedShort names the dropped variables by the short name the
	// set, variable-set and very-long-string records key by.
	droppedShort := map[string]bool{}
	for _, v := range op.Variables {
		if _, kept := owner[strings.ToLower(v.Name)]; !kept {
			droppedShort[strings.ToUpper(v.ShortName)] = true
		}
	}

	if w := p.Weight; w != nil && w.Variable != "" {
		to, ok := rename[strings.ToLower(w.Variable)]
		if !ok {
			p.Weight = nil
		} else {
			w.Variable = to
			for _, v := range vars {
				if v.Name == to {
					w.Index = v.Index
				}
			}
		}
	}

	sets := p.MultipleResponseSets[:0]
	for _, set := range p.MultipleResponseSets {
		gone := false
		for _, short := range set.Variables {
			if droppedShort[strings.ToUpper(short)] {
				gone = true
			}
		}
		for _, f := range set.Fields {
			if _, ok := rename[strings.ToLower(f)]; f != "" && !ok {
				gone = true
			}
		}
		if gone {
			continue
		}
		for j, f := range set.Fields {
			if f != "" {
				set.Fields[j] = rename[strings.ToLower(f)]
			}
		}
		sets = append(sets, set)
	}
	p.MultipleResponseSets = nilIfEmpty(sets)

	vsets := p.VariableSets[:0]
	for _, vs := range p.VariableSets {
		members := vs.Variables[:0]
		for _, short := range vs.Variables {
			if !droppedShort[strings.ToUpper(short)] {
				members = append(members, short)
			}
		}
		if len(members) > 0 {
			vs.Variables = members
			vsets = append(vsets, vs)
		}
	}
	p.VariableSets = nilIfEmpty(vsets)

	vlss := p.VeryLongStrings[:0]
	for _, d := range p.VeryLongStrings {
		if !droppedShort[strings.ToUpper(d.Name)] {
			vlss = append(vlss, d)
		}
	}
	p.VeryLongStrings = nilIfEmpty(vlss)

	p.Variables, p.Derived = vars, derived
	return doc, nil
}

// nilIfEmpty keeps an emptied `omitempty` slot absent rather than `[]`.
func nilIfEmpty[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	return s
}

// deepCopyVariable copies v through JSON so the copy shares no slice.
func deepCopyVariable(v Variable) (Variable, error) {
	raw, err := stdjson.Marshal(v)
	if err != nil {
		return Variable{}, err
	}
	var out Variable
	if err := stdjson.Unmarshal(raw, &out); err != nil {
		return Variable{}, err
	}
	return out, nil
}

// RebuildVariable regenerates v as the description of the written field
// fld, whose values were re-coded (a cast, recode or bucket): v keeps its
// names, index and position, and everything describing its values comes
// from fld alone — no value label, SPSS code, missing specification or
// very-long-string layout of the old variable survives onto the new
// codes. A numeric is a plain numeric; a date / datetime a DATE /
// DATETIME numeric; a categorical a string variable whose categories are
// fld's dictionary entries, unlabelled (Labelled false — the entries are
// the values; Observed false — no case was read for them). A set or
// vector column, or a categorical entry wider than one SPSS string
// variable (255 bytes), cannot be described and refuses the carry.
func RebuildVariable(v *Variable, fld *encoding.Field, display bool) error {
	switch {
	case fld.Type.IsSet() || fld.Type.IsVector():
		return carryRefused(fld.Name, "it was rebuilt as a "+fld.Type.String()+" column, which a carried sidecar cannot describe")
	case fld.Type.HasDictionary():
		width, entries := dictionaryWidth(fld.Dictionary)
		width = max(width, 1)
		if width > 255 {
			return carryRefused(fld.Name, "it was rebuilt as a categorical whose widest entry is "+strconv.Itoa(width)+
				" bytes, wider than one SPSS string variable")
		}
		f := Format{Code: fmtA, Width: stringFormatWidth(width)}
		v.TypeCode, v.DeclaredWidth, v.Segments = int32(width), width, (width+elementSize-1)/elementSize
		v.PrintFormat, v.WriteFormat = f, f
		v.Measure, v.Alignment, v.Kind = "nominal", "left", "categorical"
		v.Categories = make([]Category, len(entries))
		for id, text := range entries {
			t := text
			v.Categories[id] = Category{ID: uint32(id), Value: text, Text: &t}
		}
	case fld.Type == encoding.FieldTypeDate || fld.Type == encoding.FieldTypeDateTime:
		f, kind := Format{Code: fmtDATE, Width: 11}, "date"
		if fld.Type == encoding.FieldTypeDateTime {
			f, kind = Format{Code: fmtDATETIME, Width: 20}, "datetime"
		}
		v.TypeCode, v.DeclaredWidth, v.Segments = 0, 0, 1
		v.PrintFormat, v.WriteFormat = f, f
		v.Measure, v.Alignment, v.Kind = "scale", "right", kind
		v.Categories = nil
	default:
		return plainNumeric(v, fld, display)
	}
	v.Label, v.HasLabel = fld.Description, fld.Description != ""
	v.DisplayWidth, v.HasDisplayParams = nil, display
	v.PulseType, v.Nullable = fld.Type.String(), fld.Nullable
	v.DefaultAggregation, v.DefaultGrouper = "", ""
	v.Missing, v.VeryLongString = nil, nil
	return nil
}

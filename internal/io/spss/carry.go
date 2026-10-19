package spss

// CARRYING the metadata sidecar onto a derived cohort.
//
// Pulse.Materialize and Pulse.Rake write a NEW cohort holding every source
// column verbatim plus f64 columns of their own. The source's SPSS metadata
// sidecar is the only record of its value codes and labels, and a derived
// cohort that dropped it would export with neither — a weighting step would
// silently strip the labels off every variable. So the sidecar is carried:
// read and validated BEFORE the new cohort is written (the target may be
// the source itself, whose sidecar the write is about to stale), then
// rewritten against the new cohort's schema and re-fingerprinted over its
// bytes AFTER it is written.
//
// The rewrite is mechanical and narrow. Every recorded variable keeps its
// entry, because its column survives verbatim. A column the document does
// not know — a derived column, a new weight — gets a plain numeric
// variable, because a column the sidecar path cannot bind is refused at
// export (PULSE_SPSS_COLUMN_UNMAPPED). A recorded variable whose VALUES were
// replaced (a rake weight_field naming an existing column) is rewritten as
// a plain numeric, unless the document's response sets, derived registry or
// very-long-string layout refer to it — those facts would then describe a
// column that no longer holds them, so the sidecar is not carried at all.
//
// Nothing here fails the derived cohort's write. A sidecar that cannot be
// carried is reported as a warning carrying the reason's own code, and the
// cohort is written without one — exactly as if the source had none, which
// the export then reports as PULSE_SPSS_SIDECAR_ABSENT.

import (
	stdjson "encoding/json"
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// SidecarCarry is a source cohort's validated SPSS metadata sidecar, held
// between the read before a derived cohort is written and the rewrite after.
type SidecarCarry struct {
	doc    *Document
	source string
}

// PrepareSidecarCarry reads the metadata sidecar beside srcCohort for a
// derived-cohort write. No sidecar returns (nil, nil). A stale or unreadable
// one returns (nil, warning): the warning carries LoadSidecar's own code
// (PULSE_SPSS_SIDECAR_STALE / _INVALID) and says the derived cohort is
// written without it.
func PrepareSidecarCarry(fs afero.Fs, srcCohort string) (*SidecarCarry, *errors.CodedError) {
	res, err := LoadSidecar(fs, srcCohort, WriterOptions{})
	if err != nil {
		return nil, notCarried(err, srcCohort)
	}
	if res == nil || res.Document == nil {
		return nil, nil
	}
	return &SidecarCarry{doc: res.Document, source: srcCohort}, nil
}

// WriteFor rewrites the carried document against dstCohort's schema and
// writes it beside dstCohort, fingerprinted over dstCohort's bytes (which
// must already be written). replaced names the schema fields whose values
// the write replaced. mirrors maps a new column to the recorded source
// field whose values (codes) it holds — Materialize's previous-state
// column, which inherits its state field's type and dictionary — so the
// column is described as that variable is (same codes, value labels and
// missing specification); every other new column is a plain numeric.
// A nil receiver writes nothing. The returned warning,
// when non-nil, says the sidecar was not carried and why; the cohort itself
// is unaffected.
func (c *SidecarCarry) WriteFor(fs afero.Fs, dstCohort string, schema *encoding.Schema, replaced []string, mirrors map[string]string) *errors.CodedError {
	if c == nil {
		return nil
	}
	doc, err := c.rewrite(schema, replaced, mirrors)
	if err != nil {
		return notCarried(err, c.source)
	}
	fp, err := fingerprintCohort(fs, dstCohort)
	if err != nil {
		return notCarried(err, c.source)
	}
	doc.Fingerprint = fp
	if err := writeDocument(fs, dstCohort, doc); err != nil {
		return notCarried(err, c.source)
	}
	return nil
}

// rewrite returns a copy of the carried document describing schema.
func (c *SidecarCarry) rewrite(schema *encoding.Schema, replaced []string, mirrors map[string]string) (*Document, error) {
	// A deep copy through JSON: the document is plain data by contract
	// (`payload` is flat and self-contained), and the carry must never
	// mutate the source's resolution.
	raw, err := stdjson.Marshal(c.doc)
	if err != nil {
		return nil, err
	}
	doc := &Document{}
	if err := stdjson.Unmarshal(raw, doc); err != nil {
		return nil, err
	}
	p := &doc.Payload

	byName := schemaIndex(schema)
	known := make(map[string]bool, len(p.Variables)+len(p.Derived))
	minter := newNameMinter()
	display := false
	nextIndex := int32(1)
	for i := range p.Variables {
		v := &p.Variables[i]
		key := strings.ToLower(v.Name)
		if _, ok := byName[key]; !ok {
			return nil, carryRefused(v.Name, "the derived cohort has no field for it")
		}
		known[key] = true
		minter.used[strings.ToUpper(v.ShortName)] = true
		if vls := v.VeryLongString; vls != nil {
			for _, s := range vls.Segments {
				minter.used[strings.ToUpper(s.Name)] = true
			}
		}
		if v.HasDisplayParams {
			display = true
		}
		if end := v.Index + int32(max(v.Segments, 1)); end > nextIndex {
			nextIndex = end
		}
	}
	for _, d := range p.Derived {
		known[strings.ToLower(d.Name)] = true
	}

	for _, name := range replaced {
		key := strings.ToLower(name)
		if !known[key] {
			continue
		}
		if why := referenced(p, key); why != "" {
			return nil, carryRefused(name, "its values were replaced, and the sidecar's "+why+" refers to it")
		}
		for i := range p.Variables {
			if strings.ToLower(p.Variables[i].Name) == key {
				fld := &schema.Fields[byName[key]]
				if err := plainNumeric(&p.Variables[i], fld, display); err != nil {
					return nil, err
				}
			}
		}
	}

	for at := range schema.Fields {
		fld := &schema.Fields[at]
		if known[strings.ToLower(fld.Name)] {
			continue
		}
		v := Variable{Segments: 1}
		if src, ok := mirrors[fld.Name]; ok {
			m, err := mirroredVariable(p, src, fld)
			if err != nil {
				return nil, err
			}
			v = m
		} else if err := plainNumeric(&v, fld, display); err != nil {
			return nil, err
		}
		v.Name, v.LongName, v.Index, v.Position = fld.Name, "", nextIndex, len(p.Variables)
		v.ShortName = minter.mint(fld.Name)
		if v.ShortName != fld.Name {
			v.LongName = fld.Name
		}
		p.Variables = append(p.Variables, v)
		nextIndex += int32(max(v.Segments, 1))
	}
	return doc, nil
}

// mirroredVariable is the description of the new column fld, which holds
// the codes of the recorded field src under src's own type and
// dictionary: a copy of src's variable — codes, value labels, missing
// specification, formats — relabelled with fld's description and
// nullable as fld is. The caller assigns the name, short name, index and
// position. Refused (the sidecar is then not carried, with a warning)
// when src is not a recorded variable, does not share fld's type and
// dictionary size (a replaced src is a plain numeric by now), or is a
// very long string (its segment names cannot be copied).
func mirroredVariable(p *Payload, src string, fld *encoding.Field) (Variable, error) {
	for i := range p.Variables {
		v := p.Variables[i]
		if !strings.EqualFold(v.Name, src) {
			continue
		}
		dictLen := 0
		if fld.Dictionary != nil {
			dictLen = fld.Dictionary.Count()
		}
		if v.PulseType != fld.Type.String() || v.VeryLongString != nil || (fld.Dictionary != nil && len(v.Categories) > 0 && maxCategoryID(v.Categories) >= dictLen) {
			return Variable{}, carryRefused(fld.Name, "it holds the codes of "+strconv.Quote(src)+", whose recorded description does not fit it")
		}
		// A deep copy: the column must never share the source
		// variable's slices.
		raw, err := stdjson.Marshal(v)
		if err != nil {
			return Variable{}, err
		}
		var out Variable
		if err := stdjson.Unmarshal(raw, &out); err != nil {
			return Variable{}, err
		}
		out.Label, out.HasLabel = fld.Description, fld.Description != ""
		out.Nullable = fld.Nullable
		return out, nil
	}
	return Variable{}, carryRefused(fld.Name, "it holds the codes of "+strconv.Quote(src)+", which the sidecar does not record")
}

// maxCategoryID is the largest dictionary ID cats names.
func maxCategoryID(cats []Category) int {
	m := 0
	for _, c := range cats {
		m = max(m, int(c.ID))
	}
	return m
}

// referenced reports which part of the document names the variable or
// column key, or "" when none does.
func referenced(p *Payload, key string) string {
	short := ""
	for _, v := range p.Variables {
		if strings.ToLower(v.Name) == key {
			short = strings.ToLower(v.ShortName)
		}
	}
	for _, d := range p.Derived {
		if strings.ToLower(d.Name) == key {
			return "derived-column registry"
		}
		for _, s := range d.Sources {
			if strings.ToLower(s) == key {
				return "derived-column registry"
			}
		}
	}
	for _, m := range p.MultipleResponseSets {
		for _, f := range m.Fields {
			if strings.ToLower(f) == key {
				return "multiple-response sets"
			}
		}
		for _, s := range m.Variables {
			if short != "" && strings.ToLower(s) == short {
				return "multiple-response sets"
			}
		}
	}
	for _, vls := range p.VeryLongStrings {
		if short != "" && strings.ToLower(vls.Name) == short {
			return "very-long-string layout"
		}
	}
	return ""
}

// plainNumeric makes v the recorded description of a plain numeric column
// with no value codes, labels or missing specification. Only a numeric
// field qualifies: Materialize and Rake add nullable f64 columns.
func plainNumeric(v *Variable, fld *encoding.Field, display bool) error {
	if !fld.Type.IsNumeric() || fld.Type.HasDictionary() || fld.Type.IsVector() ||
		fld.Type == encoding.FieldTypeDate || fld.Type == encoding.FieldTypeDateTime {
		return carryRefused(fld.Name, "it is a "+fld.Type.String()+" column, and only a plain numeric column can be added to a carried sidecar")
	}
	f := numericFormat(fld)
	v.Label, v.HasLabel = fld.Description, fld.Description != ""
	v.TypeCode, v.DeclaredWidth, v.Segments = 0, 0, 1
	v.PrintFormat, v.WriteFormat = f, f
	v.Measure, v.Alignment = "scale", "right"
	v.DisplayWidth, v.HasDisplayParams = nil, display
	v.PulseType, v.Kind, v.Nullable = fld.Type.String(), "numeric", fld.Nullable
	v.DefaultAggregation, v.DefaultGrouper = "", ""
	v.Missing, v.Categories, v.VeryLongString = nil, nil, nil
	return nil
}

func carryRefused(field, why string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_SIDECAR_INVALID,
		"the SPSS metadata sidecar cannot describe the column "+strconv.Quote(field)+": "+why,
		map[string]any{errors.DetailSPSSField: field})
}

// notCarried wraps why the sidecar was not carried as a warning carrying
// the cause's own code.
func notCarried(err error, source string) *errors.CodedError {
	code := errors.PULSE_SPSS_SIDECAR_INVALID
	details := map[string]any{}
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		code = ce.Code
		for k, v := range ce.Details {
			details[k] = v
		}
	}
	details[errors.DetailSPSSCohort] = source
	details[errors.DetailSPSSSidecar] = SidecarPath(source)
	return errors.NewCodedErrorWithDetails(code,
		"the source's SPSS metadata sidecar was not carried onto the derived cohort, which is written without one "+
			"(a .sav export of it synthesises its dictionary): "+err.Error(),
		details)
}

// writeDocument encodes doc and writes it to SidecarPath(cohortPath).
func writeDocument(fs afero.Fs, cohortPath string, doc *Document) error {
	// encoding/json, never fmt.Sprintf; indented like every sidecar.
	raw, err := stdjson.MarshalIndent(doc, "", "  ")
	if err != nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss: encoding the metadata sidecar for "+cohortPath+": "+err.Error(),
			map[string]any{"path": SidecarPath(cohortPath)})
	}
	raw = append(raw, '\n')
	if err := afero.WriteFile(fs, SidecarPath(cohortPath), raw, 0644); err != nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss: writing "+SidecarPath(cohortPath)+": "+err.Error(),
			map[string]any{"path": SidecarPath(cohortPath)})
	}
	return nil
}

package spss

// Building the metadata sidecar from a parsed dictionary and its
// resolved mapping.
//
// The two inputs are index-aligned by construction — buildMapping
// allocates one columnMapping per dictionary variable and never
// reorders — and they answer different halves of the question. The
// dictionary says what the FILE declared; the mapping says what Pulse
// RESOLVED it to. A round trip needs both: the declaration to write
// back, and the resolution to know which cohort column carries it.

import "encoding/binary"

// buildDocument assembles the whole sidecar.
func buildDocument(d *dictionary, m *mapping, fp Fingerprint) *Document {
	return &Document{
		FormatVersion: SidecarFormatVersion,
		Kind:          SidecarKind,
		Fingerprint:   fp,
		Payload:       buildPayload(d, m),
	}
}

// buildPayload assembles the liftable half. Nothing it reaches may
// depend on the document being a separate file.
func buildPayload(d *dictionary, m *mapping) Payload {
	p := Payload{
		Source:               buildSource(d, m),
		Charset:              buildCharset(d, m),
		Weight:               buildWeight(d),
		Documents:            copyStrings(d.documents),
		ProductInfo:          buildRawText(d, extProductInfo),
		FileAttributes:       buildRawText(d, extFileAttributes),
		VariableAttributes:   buildRawText(d, extVarAttributes),
		MultipleResponseSets: buildMRSets(d),
		VariableSets:         buildVarSets(d),
		VeryLongStrings:      buildVLSDeclarations(d),
		Variables:            buildVariables(d, m),
		Derived:              buildDerived(m),
	}
	return p
}

// buildSource records the file header and the case geometry.
func buildSource(d *dictionary, m *mapping) Source {
	// Identity against the stdlib constant the parser itself assigned,
	// not a string comparison on String(), which is not a documented
	// contract of binary.ByteOrder.
	order := "little"
	if d.byteOrder == binary.ByteOrder(binary.BigEndian) {
		order = "big"
	}

	declared := int64(d.header.caseCount)
	if d.hasCaseCount64 {
		declared = d.caseCount64
	}

	var cases int64
	if m != nil {
		cases = int64(m.cases)
	}

	s := Source{
		Magic:             d.header.magic,
		ProductName:       d.header.productName,
		FileLabel:         d.header.fileLabel,
		CreationDate:      d.header.creationDate,
		CreationTime:      d.header.creationTime,
		ByteOrder:         order,
		LayoutCode:        d.header.layoutCode,
		Compression:       compressionName(d.header.compression),
		CompressionBias:   Float(d.header.bias),
		NominalCaseSize:   d.header.nominalCaseSize,
		ElementCount:      d.elementCount,
		CaseCount:         cases,
		DeclaredCaseCount: declared,
		Sysmis:            Float(d.sysmis),
	}
	if d.machineFloat.present {
		s.MachineFloat = &MachineFloat{
			Sysmis:  Float(d.machineFloat.sysmis),
			Highest: Float(d.machineFloat.highest),
			Lowest:  Float(d.machineFloat.lowest),
		}
	}
	return s
}

// compressionName renders the header compression flag. An unrecognised
// flag would already have failed the parse, so the default arm exists
// only so this function is total.
func compressionName(c int32) string {
	switch c {
	case compressionNone:
		return "none"
	case compressionBytecode:
		return "bytecode"
	case compressionZSAV:
		return "zsav"
	default:
		return "unknown"
	}
}

// buildCharset records the file's own declaration alongside what was
// decoded with. The mapping carries the same charsetInfo the dictionary
// does; it is preferred only because the mapping is what a write path
// holds, and reading the two from one place is what keeps them from
// drifting.
func buildCharset(d *dictionary, m *mapping) Charset {
	cs := d.charset
	if m != nil && (m.charset.name != "" || m.charset.declared()) {
		cs = m.charset
	}
	return Charset{
		DeclaredName: cs.declaredName,
		DeclaredCode: cs.declaredCode,
		Declared:     cs.declared(),
		Overridden:   cs.overridden,
		ResolvedName: cs.name,
	}
}

// buildWeight resolves the header's 1-based element index to a name.
//
// The index counts ELEMENTS, not variables, so a weight variable
// following a wide string does not have its ordinal position as its
// index; variableByIndex is what accounts for that. An index landing on
// a string continuation resolves to no name rather than to the wrong
// one — the index is still recorded, so nothing is lost.
func buildWeight(d *dictionary) *Weight {
	if d.header.weightIndex == 0 {
		return nil
	}
	w := &Weight{Index: d.header.weightIndex}
	if v, first, ok := d.variableByIndex(d.header.weightIndex); ok && first {
		w.Variable = v.fieldName()
	}
	return w
}

// buildRawText captures one free-form extension payload verbatim.
//
// The bytes are authoritative. Text is offered only when the file's own
// decoder consumed the whole payload cleanly; where it did not, the
// field is omitted rather than filled with a lossy approximation,
// because encoding/json would otherwise substitute U+FFFD for every
// undecodable byte without saying so.
func buildRawText(d *dictionary, subtype int32) *RawText {
	x, ok := d.rawExtension(subtype)
	if !ok {
		return nil
	}
	out := &RawText{Subtype: subtype, Raw: append([]byte(nil), x.payload...)}
	if dec := d.charset.dec; dec != nil {
		if text, at := dec.decodeString(string(x.payload)); at < 0 {
			out.Text = text
		}
	}
	return out
}

// buildMRSets projects the two multiple-response set types onto one
// JSON shape with an explicit discriminant.
//
// The type switch is preserved as the discriminant rather than erased:
// a category set gets no CountedValue field at all, so a consumer that
// forgets to check Kind reads nil instead of a meaningless "".
func buildMRSets(d *dictionary) []MRSet {
	if len(d.mrSets) == 0 {
		return nil
	}
	out := make([]MRSet, 0, len(d.mrSets))
	for _, set := range d.mrSets {
		e := MRSet{
			Name:      set.setName(),
			Label:     set.setLabel(),
			Subtype:   set.setSubtype(),
			Variables: copyStrings(set.setVars()),
		}
		switch s := set.(type) {
		case *mrDichotomySet:
			e.Kind = "dichotomy"
			counted := s.countedValue
			e.CountedValue = &counted
			e.LabelFromVariableLabel = s.labelFromVarLabel
			e.Extended = s.extended
		case *mrCategorySet:
			e.Kind = "category"
		}
		out = append(out, e)
	}
	return out
}

// buildVarSets records the record 7/5 display groupings. They are
// sidecar-only: a display grouping has no Pulse home whatsoever.
func buildVarSets(d *dictionary) []VarSet {
	if len(d.variableSets) == 0 {
		return nil
	}
	out := make([]VarSet, 0, len(d.variableSets))
	for _, vs := range d.variableSets {
		out = append(out, VarSet{Name: vs.name, Variables: copyStrings(vs.vars)})
	}
	return out
}

// buildVLSDeclarations records the record 7/14 entries. They survive
// the fold rather than being consumed by it, because they plus each
// folded variable's layout are what a write path re-segments against.
func buildVLSDeclarations(d *dictionary) []VLSDeclaration {
	if len(d.veryLongStrings) == 0 {
		return nil
	}
	out := make([]VLSDeclaration, 0, len(d.veryLongStrings))
	for _, decl := range d.veryLongStrings {
		out = append(out, VLSDeclaration{Name: decl.name, Width: decl.width})
	}
	return out
}

// buildVariables records one entry per source variable, in cohort order.
//
// d.vars and m.cols are index-aligned. The mapping is still indexed
// defensively rather than assumed: a document that silently dropped the
// Pulse half of half its columns would be worse than one that records
// the declaration alone.
func buildVariables(d *dictionary, m *mapping) []Variable {
	positions := cohortPositions(d, m)
	out := make([]Variable, 0, len(d.vars))
	for i, v := range d.vars {
		var col *columnMapping
		if m != nil && i < len(m.cols) {
			col = &m.cols[i]
		}
		out = append(out, buildVariable(positions[i], v, col))
	}
	return out
}

// cohortPositions returns each source variable's 0-based COLUMN position
// in the cohort.
//
// It is not the variable's ordinal position once derived columns exist: a
// `<var>_missing` sibling is interleaved immediately after the variable
// it belongs to, so every variable after the first one carrying a missing
// specification sits one column further right than its index. Recording
// the index here would give an export a coordinate that addresses the
// wrong column, which is the quiet kind of wrong this document exists to
// prevent.
func cohortPositions(d *dictionary, m *mapping) []int {
	pos := make([]int, len(d.vars))
	if m == nil || len(m.out) == 0 {
		for i := range pos {
			pos[i] = i
		}
		return pos
	}
	for at, slot := range m.out {
		if !slot.sibling && slot.col < len(pos) {
			pos[slot.col] = at
		}
	}
	return pos
}

// buildDerived records every column the import SYNTHESISED rather than
// read, in cohort order.
//
// The reason dictionary rides along because it is what makes the column
// foldable: an export re-emitting the source variable reads the sibling's
// ID and finds the SPSS state it stands for, rather than re-deriving the
// mapping from the missing specification and the value labels and hoping
// it lands on the same answer this import did.
func buildDerived(m *mapping) []Derived {
	if m == nil {
		return nil
	}
	var out []Derived
	for at, slot := range m.out {
		if !slot.sibling || slot.col >= len(m.cols) {
			continue
		}
		sib := m.cols[slot.col].sibling
		if sib == nil {
			continue
		}
		e := Derived{
			Name:     sib.name,
			Kind:     DerivedKindNumericMissing,
			Sources:  []string{sib.source},
			Position: at,
		}
		for _, r := range sib.reasons {
			entry := DerivedReason{
				ID: r.id, Reason: r.text, Sysmis: r.sysmis,
				Label: r.label, Declared: r.declared, Observed: r.observed,
			}
			if !r.sysmis {
				code := Float(r.code)
				entry.Code = &code
			}
			e.Reasons = append(e.Reasons, entry)
		}
		out = append(out, e)
	}
	return out
}

func buildVariable(pos int, v variable, col *columnMapping) Variable {
	e := Variable{
		Name:             v.fieldName(),
		ShortName:        v.name,
		LongName:         v.longName,
		Index:            v.index,
		Position:         pos,
		Label:            v.label,
		HasLabel:         v.hasLabel,
		TypeCode:         v.typeCode,
		DeclaredWidth:    v.width,
		Segments:         v.segments,
		PrintFormat:      buildFormat(v.print),
		WriteFormat:      buildFormat(v.write),
		Measure:          v.display.measure.String(),
		Alignment:        v.display.align.String(),
		HasDisplayParams: v.display.present,
		Missing:          buildMissing(v),
		VeryLongString:   sidecarVLSLayout(v.vls),
	}
	if v.display.present && v.display.hasWidth {
		w := v.display.width
		e.DisplayWidth = &w
	}
	if col != nil {
		e.Name = col.name
		e.DeclaredWidth = col.declaredWidth
		e.PulseType = col.fieldType.String()
		e.Kind = col.kind.String()
		e.Nullable = col.nullable
		e.DefaultAggregation = string(col.defaultAgg)
		e.DefaultGrouper = string(col.defaultGroup)
		e.Categories = buildCategories(col)
		if col.vls != nil {
			e.VeryLongString = sidecarVLSLayout(col.vls)
		}
	}
	return e
}

func buildFormat(f format) Format {
	return Format{Code: f.code, Width: f.width, Decimals: f.decimals}
}

func sidecarVLSLayout(l *vlsLayout) *VLSLayout {
	if l == nil {
		return nil
	}
	out := &VLSLayout{Width: l.width, Segments: make([]VLSSegment, 0, len(l.segments))}
	for _, s := range l.segments {
		out.Segments = append(out.Segments, VLSSegment{
			Name: s.name, Width: s.width, Content: s.content, Elements: s.elements,
		})
	}
	return out
}

// buildMissing converts a variable's missing-value specification into
// all three of the shapes the format defines.
//
// The raw eight-byte slots are copied verbatim and are the
// authoritative record; the decoded fields are a projection. That
// ordering is what makes the range/discrete split safe to interpret at
// all: a shape read narrowly here has still lost nothing.
//
// A record 7/22 long-string specification arrives on the same slot and
// converts the same way — it carries text rather than numbers, which
// the numeric/text split already covers.
func buildMissing(v variable) *Missing {
	spec := v.missing
	if spec.count() == 0 {
		return nil
	}
	out := &Missing{Code: spec.code, Raw: make([][]byte, 0, len(spec.raw))}
	for _, slot := range spec.raw {
		out.Raw = append(out.Raw, append([]byte(nil), slot[:]...))
	}

	discreteFrom := 0
	if spec.isRange() {
		// Codes -2 and -3 open with a lo..hi pair; -3 follows it with
		// exactly one discrete value. Both bounds are numeric — the
		// format has no range form for strings.
		if len(spec.numeric) >= 2 {
			out.Range = &MissingRange{
				Low:  Float(spec.numeric[0]),
				High: Float(spec.numeric[1]),
			}
		}
		discreteFrom = 2
		if spec.discreteCount() > 0 {
			out.Kind = "range_plus_discrete"
		} else {
			out.Kind = "range"
		}
	} else {
		out.Kind = "discrete"
	}

	for i := discreteFrom; i < len(spec.numeric); i++ {
		out.Discrete = append(out.Discrete, Float(spec.numeric[i]))
	}
	for i := discreteFrom; i < len(spec.text); i++ {
		out.DiscreteText = append(out.DiscreteText, spec.text[i])
	}
	return out
}

// buildCategories records the code <-> label <-> Pulse ID triple.
//
// Nothing here is deduplicated, reordered or flattened. Entry order IS
// the cohort's dictionary order, two entries may legitimately share an
// ID, an entry may be labelled but unobserved, and an entry may be
// observed but unlabelled. Every one of those is a real state of a real
// file, and collapsing any of them is the failure this slot exists to
// prevent.
func buildCategories(col *columnMapping) []Category {
	if len(col.categories) == 0 {
		return nil
	}
	out := make([]Category, 0, len(col.categories))
	for _, c := range col.categories {
		e := Category{
			ID:       c.id,
			Value:    c.value,
			Numeric:  c.numeric,
			Label:    c.label,
			Labelled: c.labelled,
			Observed: c.observed,
		}
		if c.numeric {
			code := Float(c.code)
			e.Code = &code
		} else {
			text := c.text
			e.Text = &text
		}
		out = append(out, e)
	}
	return out
}

func copyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

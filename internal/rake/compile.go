package rake

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// fieldRef is one schema field a plan reads.
type fieldRef struct {
	name  string
	index int
	typ   encoding.FieldType
	dict  *encoding.Dictionary
}

// planMargin is one compiled margin: its fields, its categories in
// report order with their normalised target shares, and the lookup from
// a record's joined key to a category index.
type planMargin struct {
	fields []fieldRef
	keys   [][]string
	shares []float64
	index  map[string]int
}

// Plan is a request compiled against a schema: every static refusal has
// already been raised. Load reads the records' categories, Solve runs
// the fit.
type Plan struct {
	margins     []planMargin
	by          *fieldRef
	base        *fieldRef
	weightField string
	// replace is the source index of the column WeightField overwrites,
	// or -1 when the weight is appended.
	replace     int
	maxIter     int
	tol         float64
	minW, maxW  float64
	total       float64
	missingBase bool
	cohort      string
}

// WeightField is the output weight column's name.
func (p *Plan) WeightField() string { return p.weightField }

// ReplaceIndex is the source field index the weight column replaces, or
// -1 when it is appended after the source fields.
func (p *Plan) ReplaceIndex() int { return p.replace }

// invalid builds a PULSE_RAKE_INVALID refusal.
func invalid(reason, field, msg string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_RAKE_INVALID, msg,
		map[string]any{"reason": reason, "field": field})
}

// keySep joins a joint margin's category tuple into one lookup key; it
// cannot occur in a dictionary label written by import.
const keySep = "\x00"

// Compile validates req against schema (the source's LOGICAL schema)
// and returns the plan. Every refusal is PULSE_RAKE_INVALID or
// PULSE_RAKE_UNKNOWN_CATEGORY and is raised before any record is read,
// so a predict over the same schema raises the same code.
func Compile(req *Request, schema *encoding.Schema) (*Plan, error) {
	if req == nil || strings.TrimSpace(req.Cohort) == "" {
		return nil, invalid("cohort_missing", "cohort", "rake needs a source cohort")
	}
	if strings.TrimSpace(req.Out) == "" {
		return nil, invalid("out_missing", "out", "rake writes a new cohort; out is required")
	}
	if req.Out == req.Cohort {
		return nil, invalid("out_is_source", "out", "rake writes a NEW cohort; out may not be the source cohort")
	}
	if len(req.Margins) == 0 {
		return nil, invalid("margins_empty", "margins", "rake needs at least one margin")
	}
	p := &Plan{replace: -1, cohort: req.Cohort}
	switch req.Missing {
	case "", MissingRefuse:
	case MissingBase:
		p.missingBase = true
	default:
		return nil, invalid("missing_policy", "missing", `missing must be "refuse" or "base"`)
	}
	if req.MaxIter < 0 {
		return nil, invalid("max_iter", "max_iter", "max_iter must be non-negative")
	}
	p.maxIter = req.MaxIter
	if p.maxIter == 0 {
		p.maxIter = DefaultMaxIter
	}
	if !finiteNonNeg(req.Tol) {
		return nil, invalid("tol", "tol", "tol must be finite and non-negative")
	}
	p.tol = req.Tol
	if p.tol == 0 {
		p.tol = DefaultTol
	}
	if !finiteNonNeg(req.Total) {
		return nil, invalid("total", "total", "total must be finite and non-negative")
	}
	p.total = req.Total
	if !finiteNonNeg(req.MinWeight) || !finiteNonNeg(req.MaxWeight) || req.MinWeight > 1 || (req.MaxWeight != 0 && req.MaxWeight < 1) {
		return nil, invalid("caps", "min_weight", "caps are relative to the mean weight: 0 <= min_weight <= 1 <= max_weight (0 = no cap)")
	}
	p.minW, p.maxW = req.MinWeight, req.MaxWeight

	used := map[string]bool{}
	for i := range req.Margins {
		pm, err := compileMargin(i, &req.Margins[i], schema, used)
		if err != nil {
			return nil, err
		}
		p.margins = append(p.margins, pm)
	}
	if req.By != "" {
		f, err := categoryField(schema, req.By, "by")
		if err != nil {
			return nil, err
		}
		if used[req.By] {
			return nil, invalid("field_repeated", "by", "the by field may not also be a margin field")
		}
		used[req.By] = true
		p.by = f
	}
	if req.BaseWeight != "" {
		idx := fieldIndex(schema, req.BaseWeight)
		if idx < 0 {
			return nil, invalid("field_unknown", "base_weight", "base_weight names no field of the cohort")
		}
		t := schema.Fields[idx].Type
		if !t.IsNumeric() || t.IsDecimal() {
			return nil, invalid("field_type", "base_weight", "base_weight must be an unsigned-integer or float field")
		}
		p.base = &fieldRef{name: req.BaseWeight, index: idx, typ: t}
	}
	p.weightField = req.WeightField
	if p.weightField == "" {
		p.weightField = DefaultWeightField
	}
	if idx := fieldIndex(schema, p.weightField); idx >= 0 {
		if !req.Overwrite {
			return nil, invalid("weight_field_exists", "weight_field", "the cohort already has a field named weight_field; choose another name or set overwrite")
		}
		if used[p.weightField] {
			return nil, invalid("weight_field_in_use", "weight_field", "weight_field may not replace a margin or by field")
		}
		p.replace = idx
	}
	return p, nil
}

func finiteNonNeg(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

func fieldIndex(schema *encoding.Schema, name string) int {
	for i := range schema.Fields {
		if schema.Fields[i].Name == name {
			return i
		}
	}
	return -1
}

// categoryField resolves a margin / by field: it must exist and be a
// categorical, packed_bool or unsigned integer.
func categoryField(schema *encoding.Schema, name, path string) (*fieldRef, error) {
	idx := fieldIndex(schema, name)
	if idx < 0 {
		return nil, invalid("field_unknown", path, "the field "+strconv.Quote(name)+" is not in the cohort")
	}
	f := schema.Fields[idx]
	switch {
	case f.Type.IsCategorical(), f.Type == encoding.FieldTypePackedBool, isUnsigned(f.Type):
	default:
		return nil, invalid("field_type", path, "a margin or by field must be categorical, packed_bool or an unsigned integer; "+strconv.Quote(name)+" is "+f.Type.String())
	}
	return &fieldRef{name: name, index: idx, typ: f.Type, dict: f.Dictionary}, nil
}

func isUnsigned(t encoding.FieldType) bool {
	switch t {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64:
		return true
	}
	return false
}

func unsignedMax(t encoding.FieldType) uint64 {
	switch t {
	case encoding.FieldTypeU4:
		return 15
	case encoding.FieldTypeU8:
		return math.MaxUint8
	case encoding.FieldTypeU16:
		return math.MaxUint16
	case encoding.FieldTypeU32:
		return math.MaxUint32
	}
	return math.MaxUint64
}

// rank orders a category of f for reporting: dictionary position,
// false before true, numeric value. ok is false when f cannot hold the
// category.
func (f *fieldRef) rank(cat string) (uint64, bool) {
	switch {
	case f.typ.IsCategorical():
		if f.dict == nil {
			return 0, false
		}
		id, ok := f.dict.IDFor(cat)
		return uint64(id), ok
	case f.typ == encoding.FieldTypePackedBool:
		switch cat {
		case "false":
			return 0, true
		case "true":
			return 1, true
		}
		return 0, false
	}
	v, err := strconv.ParseUint(cat, 10, 64)
	if err != nil || strconv.FormatUint(v, 10) != cat || v > unsignedMax(f.typ) {
		return 0, false
	}
	return v, true
}

func compileMargin(i int, m *Margin, schema *encoding.Schema, used map[string]bool) (planMargin, error) {
	path := "margins[" + strconv.Itoa(i) + "]"
	single := m.Field != ""
	if single == (len(m.Fields) > 0) || (single && (len(m.Targets) == 0 || len(m.Cells) > 0)) || (!single && (len(m.Fields) < 2 || len(m.Cells) == 0 || len(m.Targets) > 0)) {
		return planMargin{}, invalid("margin_shape", path, "a margin is either field + targets, or two or more fields + cells")
	}
	names := m.Fields
	if single {
		names = []string{m.Field}
	}
	var pm planMargin
	for j, name := range names {
		fp := path + ".field"
		if !single {
			fp = path + ".fields[" + strconv.Itoa(j) + "]"
		}
		f, err := categoryField(schema, name, fp)
		if err != nil {
			return planMargin{}, err
		}
		if used[name] {
			return planMargin{}, invalid("field_repeated", fp, "a field may appear in one margin only")
		}
		used[name] = true
		pm.fields = append(pm.fields, *f)
	}
	var raw []float64
	if single {
		f := &pm.fields[0]
		type cat struct {
			key  string
			rank uint64
			v    float64
		}
		cats := make([]cat, 0, len(m.Targets))
		for k, v := range m.Targets {
			r, ok := f.rank(k)
			if !ok {
				return planMargin{}, errors.NewCodedErrorWithDetails(errors.PULSE_RAKE_UNKNOWN_CATEGORY,
					"the target category "+strconv.Quote(k)+" is not a value field "+strconv.Quote(f.name)+" can hold",
					map[string]any{"margin": i, "field": f.name, "category": k})
			}
			cats = append(cats, cat{k, r, v})
		}
		sort.Slice(cats, func(a, b int) bool { return cats[a].rank < cats[b].rank })
		for _, c := range cats {
			pm.keys = append(pm.keys, []string{c.key})
			raw = append(raw, c.v)
		}
	} else {
		seen := map[string]bool{}
		for j, c := range m.Cells {
			cp := path + ".cells[" + strconv.Itoa(j) + "]"
			if len(c.Values) != len(pm.fields) {
				return planMargin{}, invalid("cell_shape", cp, "a cell holds one value per margin field")
			}
			for k, v := range c.Values {
				if _, ok := pm.fields[k].rank(v); !ok {
					return planMargin{}, errors.NewCodedErrorWithDetails(errors.PULSE_RAKE_UNKNOWN_CATEGORY,
						"the target category "+strconv.Quote(v)+" is not a value field "+strconv.Quote(pm.fields[k].name)+" can hold",
						map[string]any{"margin": i, "field": pm.fields[k].name, "category": v})
				}
			}
			key := strings.Join(c.Values, keySep)
			if seen[key] {
				return planMargin{}, invalid("cell_duplicate", cp, "a joint margin lists each category tuple once")
			}
			seen[key] = true
			pm.keys = append(pm.keys, append([]string(nil), c.Values...))
			raw = append(raw, c.Target)
		}
	}
	sum := 0.0
	for _, v := range raw {
		if !finiteNonNeg(v) {
			return planMargin{}, invalid("targets_invalid", path, "every target must be finite and non-negative")
		}
		sum += v
	}
	if sum <= 0 {
		return planMargin{}, invalid("targets_zero", path, "a margin needs at least one positive target")
	}
	switch m.TargetsAs {
	case "", TargetsShares:
		if math.Abs(sum-1) > 1e-6 && math.Abs(sum-100) > 1e-4 {
			return planMargin{}, invalid("targets_sum", path, "shares must sum to 1 (or percentages to 100); use targets_as \"counts\" for population counts")
		}
	case TargetsCounts:
	default:
		return planMargin{}, invalid("targets_as", path+".targets_as", `targets_as must be "shares" or "counts"`)
	}
	pm.index = make(map[string]int, len(pm.keys))
	for k, key := range pm.keys {
		pm.index[strings.Join(key, keySep)] = k
		pm.shares = append(pm.shares, raw[k]/sum)
	}
	return pm, nil
}

// fieldNames lists a margin's field names.
func (pm *planMargin) fieldNames() []string {
	out := make([]string, len(pm.fields))
	for i, f := range pm.fields {
		out[i] = f.name
	}
	return out
}

// descriptionLimit is the .pulse field-description ceiling
// (PULSE_IMPORT_DESCRIPTION_TOO_LONG).
const descriptionLimit = 1000

// Description is the generated description of the output weight column:
// what it is, what it was raked to, and how it was scaled.
func (p *Plan) Description() string {
	var b strings.Builder
	b.WriteString("Raking weight (probability kind) computed by Pulse.Rake from ")
	b.WriteString(p.cohort)
	b.WriteString(", reproducing the margins ")
	for i := range p.margins {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(strings.Join(p.margins[i].fieldNames(), " x "))
	}
	if p.by != nil {
		b.WriteString(" within each group of ")
		b.WriteString(p.by.name)
	}
	if p.base != nil {
		b.WriteString(", starting from base weight ")
		b.WriteString(p.base.name)
	}
	if p.minW > 0 || p.maxW > 0 {
		b.WriteString(", capped relative to the mean weight")
	}
	if p.total > 0 {
		b.WriteString(", scaled to sum to " + strconv.FormatFloat(p.total, 'g', -1, 64))
	} else {
		b.WriteString(", scaled to sum to the record count")
	}
	if p.by != nil {
		b.WriteString(" per group")
	}
	b.WriteString(".")
	s := b.String()
	if len(s) > descriptionLimit {
		cut := descriptionLimit - len("...")
		for cut > 0 && !utf8RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

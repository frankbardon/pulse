package encoding

import "bytes"

// Global-constant field elision (format 0x02).
//
// A field with exactly ONE distinct value across the whole cohort — the
// same on-wire bytes AND the same null state on every row — carries no
// information per row. Elision stores it once, as a member of a
// GroupKindConstant group (one entry, no per-row index), and drops it
// from the physical row. Decode rehydrates it through the ordinary group
// expansion, so the field reads back indistinguishably.
//
// The hazard is WHERE constancy is decided. Import infers its schema
// from a bounded sample (500 rows by default); a column constant across
// the sample may vary later, and eliding it would make EncodeRow refuse
// the cohort half-way — or, in a writer that trusted the sample, corrupt
// it. ConstantDetector therefore observes EVERY logical row, and
// PlanConstantElision decides only from a detector that has seen the
// full pass. It is the one detection path shared by import (E3-S5),
// import-time group declaration and retro-dedup, so they cannot drift.

// ConstantDetector tracks, over a stream of logical rows of an
// ungrouped schema, which fields still hold the value of the first row.
// Two cells are the same value iff their on-wire bytes are equal AND
// their null bits are equal: a field that is null on every row is a
// (null) constant, a field that is one value except for some nulls is
// NOT constant. The comparison is conservative on null cells — two nulls
// whose placeholder bytes differ count as different, which can only
// forgo an elision, never make one wrong.
type ConstantDetector struct {
	schema *Schema
	offs   []int
	widths []int
	bmOff  int // logical bitmap offset, -1 when the row has no bitmap
	stride int
	first  []byte
	live   []bool
	nLive  int
	rows   int64
}

// NewConstantDetector prepares a detector for logical rows of flat, an
// ungrouped schema (pass a grouped schema's Logical() view).
func NewConstantDetector(flat *Schema) (*ConstantDetector, error) {
	if flat.HasGroups() {
		return nil, groupErr("constant detection runs over an ungrouped schema (use Logical())", nil)
	}
	d := &ConstantDetector{
		schema: flat,
		offs:   make([]int, len(flat.Fields)),
		widths: make([]int, len(flat.Fields)),
		bmOff:  -1,
		live:   make([]bool, len(flat.Fields)),
		nLive:  len(flat.Fields),
	}
	off := 0
	for i := range flat.Fields {
		d.offs[i] = off
		d.widths[i] = onWireWidth(flat.Fields[i].Type)
		off += d.widths[i]
		d.live[i] = true
	}
	if flat.HasBitmap() {
		d.bmOff = off
	}
	d.stride = off + flat.BitmapByteSize()
	return d, nil
}

// Stride is the logical row width Observe expects.
func (d *ConstantDetector) Stride() int { return d.stride }

// Rows is the number of rows observed.
func (d *ConstantDetector) Rows() int64 { return d.rows }

// FirstRow returns a copy-owned view of the first observed row (nil
// before any row). It is the value every still-constant field holds.
func (d *ConstantDetector) FirstRow() []byte { return d.first }

// Observe folds one logical row into the detector. A row of the wrong
// width is ENCODING_INVALID.
func (d *ConstantDetector) Observe(lrow []byte) error {
	if len(lrow) != d.stride {
		return groupErr("logical row has the wrong width", map[string]any{"bytes": len(lrow), "stride": d.stride, "row": d.rows})
	}
	d.rows++
	if d.first == nil {
		d.first = append([]byte(nil), lrow...)
		return nil
	}
	if d.nLive == 0 {
		return nil
	}
	for i := range d.live {
		if !d.live[i] {
			continue
		}
		o, w := d.offs[i], d.widths[i]
		same := bytes.Equal(lrow[o:o+w], d.first[o:o+w])
		if same && d.bmOff >= 0 && d.schema.Fields[i].Nullable {
			same = BitmapIsNull(lrow[d.bmOff:], i) == BitmapIsNull(d.first[d.bmOff:], i)
		}
		if !same {
			d.live[i] = false
			d.nLive--
		}
	}
	return nil
}

// ConstantFields returns the logical indices of the fields that held one
// value across every observed row, ascending. Nil before any row: over
// zero rows no field has a value to be constant at.
func (d *ConstantDetector) ConstantFields() []int {
	if d.rows == 0 {
		return nil
	}
	out := make([]int, 0, d.nLive)
	for i, ok := range d.live {
		if ok {
			out = append(out, i)
		}
	}
	return out
}

// IsNullConstant reports whether constant field fi is constant at NULL
// (as opposed to constant at a value). Meaningful only for a field
// ConstantFields returned.
func (d *ConstantDetector) IsNullConstant(fi int) bool {
	if d.first == nil || d.bmOff < 0 || !d.schema.Fields[fi].Nullable {
		return false
	}
	return BitmapIsNull(d.first[d.bmOff:], fi)
}

// MinElisionRows is the smallest cohort constant elision considers. Over
// a single row every field is trivially "constant", which says nothing
// about the data; over zero rows a constant group has no value to hold
// and cannot be written.
const MinElisionRows = 2

// ConstantPlan is PlanConstantElision's decision.
type ConstantPlan struct {
	// Spec is the constant group to encode, or nil when nothing is
	// elided (see Skipped).
	Spec *GroupSpec
	// Fields names the elided fields in logical order (Spec.Members).
	Fields []string
	// Retained names a constant field deliberately kept in the row
	// because eliding every field would leave a zero-byte stride, from
	// which no record count can be derived. Empty otherwise.
	Retained string
	// BytesSaved is the net file-size reduction: the per-row bytes
	// removed times the row count, minus the schema-block growth. Zero
	// when nothing is elided.
	BytesSaved int64
	// Skipped says why nothing was elided ("" when Spec is set):
	// "too_few_rows", "no_constant_fields" or "not_smaller".
	Skipped string
}

// PlanConstantElision decides, from a detector that has observed EVERY
// row of the cohort, which fields to elide as one constant group.
// reserved names fields already claimed by another group (a field
// belongs to at most one); they are never elided. The rules:
//
//   - fewer than MinElisionRows rows: nothing is elided;
//   - when the candidates would leave no field in the row (every field
//     constant), the LOWEST-index candidate stays in the row, so the
//     physical stride stays positive;
//   - the elision must make the file strictly smaller: the per-row bytes
//     removed times the row count must exceed the schema block's growth
//     (the constant group's descriptor and entry, and the extension
//     block framing). A small cohort whose constants cost more to
//     declare than they save is left alone.
//
// It never changes a value: the plan's group is encoded by GroupEncoder,
// which refuses a constant member that changes on any row.
func PlanConstantElision(d *ConstantDetector, reserved []string) (*ConstantPlan, error) {
	return planConstantElision(d.schema, d.ConstantFields(), d.rows, d.first, reserved)
}

// PlanConstantElisionFor is PlanConstantElision for a caller that
// decided constancy itself over EVERY row of a cohort whose final
// schema is flat: constant lists the constant fields' logical indices
// (ascending) and rows the row count. The rules and the arithmetic are
// PlanConstantElision's — only the row the saving is sized on is a
// zeroed stand-in, which cannot change a byte count because entries are
// fixed-width. Import predict uses it: it sees every row but not in
// the final schema's layout (a later null can still promote a field).
func PlanConstantElisionFor(flat *Schema, constant []int, rows int64, reserved []string) (*ConstantPlan, error) {
	if flat.HasGroups() {
		return nil, groupErr("constant elision plans over an ungrouped schema (use Logical())", nil)
	}
	stride := 0
	for i := range flat.Fields {
		stride += onWireWidth(flat.Fields[i].Type)
	}
	stride += flat.BitmapByteSize()
	return planConstantElision(flat, constant, rows, make([]byte, stride), reserved)
}

func planConstantElision(flat *Schema, constant []int, rows int64, first []byte, reserved []string) (*ConstantPlan, error) {
	if rows < MinElisionRows {
		return &ConstantPlan{Skipped: "too_few_rows"}, nil
	}
	skip := make(map[string]bool, len(reserved))
	for _, n := range reserved {
		skip[n] = true
	}
	var cand []int
	for _, fi := range constant {
		if !skip[flat.Fields[fi].Name] {
			cand = append(cand, fi)
		}
	}
	if len(cand) == 0 {
		return &ConstantPlan{Skipped: "no_constant_fields"}, nil
	}
	plan := &ConstantPlan{}
	// Candidates exclude reserved fields, so they cover every field only
	// when nothing is reserved and every field is constant. Keep the
	// lowest-index one in the row: a zero-byte stride is unwritable.
	// (Reserved fields stay row fields in this plan; a caller combining
	// the plan with its own groups re-validates the whole shape in
	// NewGroupEncoder, which refuses a zero stride.)
	if len(cand) == len(flat.Fields) {
		plan.Retained = flat.Fields[cand[0]].Name
		cand = cand[1:]
		if len(cand) == 0 {
			return &ConstantPlan{Skipped: "no_constant_fields", Retained: plan.Retained}, nil
		}
	}
	spec := GroupSpec{Kind: GroupKindConstant}
	for _, fi := range cand {
		spec.Members = append(spec.Members, flat.Fields[fi].Name)
	}
	enc, err := NewGroupEncoder(flat, []GroupSpec{spec})
	if err != nil {
		return nil, err
	}
	if _, err := enc.EncodeRow(nil, first); err != nil {
		return nil, err
	}
	grown, err := preambleGrowth(flat, enc.Schema())
	if err != nil {
		return nil, err
	}
	saved := rows*int64(enc.LogicalStride()-enc.PhysicalStride()) - grown
	if saved <= 0 {
		return &ConstantPlan{Skipped: "not_smaller", Retained: plan.Retained}, nil
	}
	plan.Spec = &spec
	plan.Fields = spec.Members
	plan.BytesSaved = saved
	return plan, nil
}

// preambleGrowth is how many bytes the grouped preamble adds over the
// flat one.
func preambleGrowth(flat, grouped *Schema) (int64, error) {
	var a, b countWriter
	if err := WritePreamble(&a, flat); err != nil {
		return 0, err
	}
	if err := WritePreamble(&b, grouped); err != nil {
		return 0, err
	}
	return b.n - a.n, nil
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

package io

import (
	"bytes"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// This file holds the per-cell encoding rules an explicit-schema write
// shares, whoever produces the values: ImportJob converts source TEXT
// into them, the cohort builder (BuildJob) converts typed Go values.
// Both reach the same dictionary assignment, the same rung ceilings,
// the same decimal precision check and the same row byte layout, so a
// cohort built from rows is byte-identical to the same rows imported
// with the same schema by construction rather than by parallel code.

// labelInterner assigns a dictionary ID to a label under a rung
// ceiling. *encoding.Dictionary implements it (the import path interns
// straight into the field's dictionary); the builder interns through a
// staged view that commits only once the whole row converted, so a
// rejected row leaves no entry behind.
type labelInterner interface {
	AddWithLimit(label string, maxEntries uint32) (uint32, error)
}

// internCategorical returns the dictionary ID of a categorical label,
// appending it first-seen. The declared rung is a ceiling: a label past
// ft.MaxCategoricalEntries() is PULSE_IMPORT_CATEGORICAL_OVERFLOW.
func internCategorical(d labelInterner, label string, ft encoding.FieldType) (uint64, error) {
	id, err := d.AddWithLimit(label, ft.MaxCategoricalEntries())
	if err != nil {
		return 0, err
	}
	return uint64(id), nil
}

// internSetTokens interns each token of a present set cell and returns
// the membership mask: bit i is dictionary entry i. It is the single
// token -> bit assignment for ALL six set rungs, and the rung is a
// ceiling (PULSE_IMPORT_SET_OVERFLOW). An empty token list is the empty
// mask — "no selection", distinct from null.
func internSetTokens(d labelInterner, tokens []string, ft encoding.FieldType) (encoding.SetMask, error) {
	var mask encoding.SetMask
	maxEntries := ft.MaxSetEntries()
	for _, tok := range tokens {
		id, err := d.AddWithLimit(tok, maxEntries)
		if err != nil {
			return mask, errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_SET_OVERFLOW,
				fmt.Sprintf("set dictionary overflowed %s (max %d entries)", ft, maxEntries),
				map[string]any{"type": ft.String(), "max_entries": maxEntries, "token": tok})
		}
		mask = mask.WithBit(int(id))
	}
	return mask, nil
}

// narrowSetWord narrows a mask to the uint64 a narrow set rung (set_u8
// .. set_u64) stores. The narrowing is safe by construction — the
// rung ceiling caps the dictionary at ft.MaxSetEntries() <= 64 — so the
// failure arm guards that invariant rather than an expected input.
func narrowSetWord(mask encoding.SetMask, ft encoding.FieldType) (uint64, error) {
	low, ok := mask.Uint64()
	if !ok {
		return 0, errors.NewCodedErrorWithDetails(
			errors.PULSE_IMPORT_SET_OVERFLOW,
			fmt.Sprintf("set mask has bit %d beyond the 64 bits %s stores", mask.HighestBit(), ft),
			map[string]any{"type": ft.String(), "max_entries": ft.MaxSetEntries(), "highest_bit": mask.HighestBit()})
	}
	return low, nil
}

// wideSetCell lays a mask down as the on-wire bytes of a wide set rung
// (set_u128 / set_u256). Only the leading ft.ByteSize() bytes are
// meaningful.
func wideSetCell(mask encoding.SetMask, ft encoding.FieldType) (wideFieldBytes, error) {
	var out wideFieldBytes
	if err := encoding.PutSetMask(out[:ft.ByteSize()], ft, mask); err != nil {
		return out, err
	}
	return out, nil
}

// decimalCell rescales d (an unscaled mantissa at srcScale) to the
// field's declared scale, refuses a value past the field's precision
// (PULSE_DECIMAL_OVERFLOW) and returns its 16 on-wire bytes.
func decimalCell(d encoding.Decimal128, srcScale uint8, f encoding.Field, shown string) (wideFieldBytes, error) {
	var out wideFieldBytes
	d, err := d.Rescale(srcScale, f.Scale)
	if err != nil {
		return out, err
	}
	if !d.FitsPrecision(f.Precision) {
		return out, errors.NewCodedErrorWithDetails(
			errors.PULSE_DECIMAL_OVERFLOW,
			"decimal value exceeds field precision",
			map[string]any{"value": shown, "precision": f.Precision, "scale": f.Scale})
	}
	enc := encoding.EncodeDecimal128(d)
	copy(out[:], enc[:])
	return out, nil
}

// rowCells is one converted row in the form both writers encode: narrow
// values in vals, wide (decimal128 / set_u128 / set_u256) raw bytes in
// wide, and the null flags. writeFields and fillBitmap are the ONE row
// byte layout of an explicit-schema write.
type rowCells struct {
	vals     []uint64
	wide     []wideFieldBytes
	wideUsed []bool
	null     []bool
}

func newRowCells(n int) rowCells {
	return rowCells{
		vals:     make([]uint64, n),
		wide:     make([]wideFieldBytes, n),
		wideUsed: make([]bool, n),
		null:     make([]bool, n),
	}
}

// reset clears the per-row flags before a new row is converted.
func (c *rowCells) reset() {
	for i := range c.wideUsed {
		c.wideUsed[i] = false
		c.null[i] = false
	}
}

// setNull marks field i (of type ft) null: the payload is a zeroed
// placeholder of the field's FULL width and the bit rides the bitmap.
// A decimal's placeholder is the encoded zero; a set's zero is also the
// empty mask, which is only reachable as a VALUE on a non-null cell.
func (c *rowCells) setNull(i int, ft encoding.FieldType) {
	c.null[i] = true
	if isWideFieldType(ft) {
		c.wide[i] = wideFieldBytes{}
		if ft == encoding.FieldTypeDecimal128 {
			enc := encoding.EncodeDecimal128(encoding.ZeroDecimal128())
			copy(c.wide[i][:], enc[:])
		}
		c.wideUsed[i] = true
		return
	}
	c.vals[i] = 0
}

// setWide stores a wide field's raw bytes.
func (c *rowCells) setWide(i int, b wideFieldBytes) {
	c.wide[i] = b
	c.wideUsed[i] = true
}

// writeFields appends the converted row's field bytes — every field in
// schema order, bit-packed types as one whole byte — to buf.
func (c *rowCells) writeFields(buf *bytes.Buffer, schema *encoding.Schema) error {
	for i, f := range schema.Fields {
		if c.wideUsed[i] {
			// Slice to the field's own width: 16 for decimal128 and
			// set_u128, 32 for set_u256. Writing the whole scratch
			// array would pad every decimal by 16 zero bytes and
			// desynchronize the stride for the rest of the file.
			if _, err := buf.Write(c.wide[i][:f.Type.ByteSize()]); err != nil {
				return err
			}
			continue
		}
		if f.Type.IsBitPacked() {
			// Bit-packed types: write as single byte for simplicity.
			buf.WriteByte(byte(c.vals[i]))
			continue
		}
		if err := encoding.WriteFieldValue(buf, f.Type, c.vals[i]); err != nil {
			return err
		}
	}
	return nil
}

// fillBitmap sets bit i of bm (a zeroed full-width ceil(field_count/8)
// bitmap) for every null cell of the converted row. A cell is only ever
// null-marked for a nullable field (a non-nullable null fails the row),
// so this is safe.
func (c *rowCells) fillBitmap(bm []byte) {
	for i := range c.null {
		if c.null[i] {
			encoding.BitmapSetNull(bm, i)
		}
	}
}

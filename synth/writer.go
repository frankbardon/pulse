package synth

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	mrand "math/rand/v2"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// fieldTypeFromName maps the spec's string type name to a FieldType.
// The mapping mirrors encoding.FieldType.String() — keep them in sync.
func fieldTypeFromName(name string) (encoding.FieldType, bool) {
	switch name {
	case "u4":
		return encoding.FieldTypeU4, true
	case "u8":
		return encoding.FieldTypeU8, true
	case "u16":
		return encoding.FieldTypeU16, true
	case "u32":
		return encoding.FieldTypeU32, true
	case "u64":
		return encoding.FieldTypeU64, true
	case "f32":
		return encoding.FieldTypeF32, true
	case "f64":
		return encoding.FieldTypeF64, true
	case "date":
		return encoding.FieldTypeDate, true
	case "packed_bool":
		return encoding.FieldTypePackedBool, true
	case "categorical_u8":
		return encoding.FieldTypeCategoricalU8, true
	case "categorical_u16":
		return encoding.FieldTypeCategoricalU16, true
	case "categorical_u32":
		return encoding.FieldTypeCategoricalU32, true
	case "decimal128":
		return encoding.FieldTypeDecimal128, true
	case "set_u8":
		return encoding.FieldTypeSetU8, true
	case "set_u16":
		return encoding.FieldTypeSetU16, true
	case "set_u32":
		return encoding.FieldTypeSetU32, true
	case "set_u64":
		return encoding.FieldTypeSetU64, true
	}
	return 0, false
}

// fieldOf builds an encoding.Field for a FieldSpec. Computes byte/bit
// offsets via cursor state in buildSchema.
type writerField struct {
	spec    FieldSpec
	field   *encoding.Field
	sampler sampler
}

// nextFieldLayout advances the running (byteOffset, bitCursor) cursor by
// one field of type ft and returns that field's own (byteOffset,
// bitPosition). Bit-packed types (u4, packed_bool) each consume one whole
// fresh byte — the same simplified convention buildSchema has always used
// (matches io/import.go's WriteByte path) — rather than sharing a byte
// across neighbouring bit-packed fields. Shared by buildSchema and
// augment.go's buildMergedSchema so both lay out fields identically.
func nextFieldLayout(ft encoding.FieldType, byteOffset, bitCursor *int) (offset, bitPosition int) {
	offset = *byteOffset
	if ft.IsBitPacked() {
		bitPosition = *bitCursor % 8
		*byteOffset++
		*bitCursor = 8
	} else {
		*byteOffset += ft.ByteSize()
	}
	return offset, bitPosition
}

// buildSchema returns the encoding.Schema and per-field samplers laid
// out in record order. Byte offsets / bit positions are populated.
func buildSchema(s *Spec) (*encoding.Schema, []*writerField, error) {
	fields := make([]encoding.Field, len(s.Fields))
	wfs := make([]*writerField, len(s.Fields))

	byteOffset := 0
	bitCursor := 8 // bit offsets advance from the high bit of a fresh byte
	for i, fs := range s.Fields {
		ft, ok := fieldTypeFromName(fs.Type)
		if !ok {
			return nil, nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("unknown field type %q for field %q", fs.Type, fs.Name),
				map[string]any{"type": fs.Type, "field": fs.Name})
		}
		field := encoding.Field{
			Name:         fs.Name,
			Type:         ft,
			Nullable:     fs.Nullable,
			Description:  fs.Description,
			CsvColumnIdx: i,
		}
		// Bit-packed fields each consume one byte in the importer's writer
		// pattern (see io/import.go: WriteByte path); replicate here so
		// bytes laid down match what RecordReader expects.
		if ft.IsCategorical() {
			field.Dictionary = encoding.NewDictionary()
		}
		if ft.IsSet() {
			// set_* dictionaries are pre-populated HERE, once, in the
			// field spec's own declared params.options order — never
			// lazily via first-touch during row generation. A
			// set_bernoulli sampler's row value is a map[string]bool
			// (synth/distributions.go), and Go map iteration order is
			// randomized per process; if bit IDs were assigned by
			// first-encounter order while encoding that map,
			// "same spec + same seed -> byte-identical output" (the
			// Determinism contract, skills/synthetic-data.md) would
			// break. Pre-registering in a fixed, spec-declared order
			// means writeFieldValueForField only ever needs an ID
			// LOOKUP against an already-complete dictionary, so the
			// order in which map entries happen to be visited while
			// building a row's mask can never affect the result.
			options, ok, perr := paramStringSlice(fs.Name, fs.Params, "options")
			if perr != nil {
				return nil, nil, perr
			}
			if !ok || len(options) == 0 {
				return nil, nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					fmt.Sprintf("field %q: set field requires non-empty params.options", fs.Name), nil)
			}
			dict := encoding.NewDictionary()
			maxEntries := ft.MaxSetEntries()
			for _, opt := range options {
				if _, derr := dict.AddWithLimit(opt, maxEntries); derr != nil {
					return nil, nil, errors.WrapCodedError(derr, errors.PULSE_IMPORT_SET_OVERFLOW,
						fmt.Sprintf("field %q: registering set options", fs.Name))
				}
			}
			field.Dictionary = dict
		}
		if ft.IsDecimal() {
			prec := fs.Precision
			scale := fs.Scale
			if prec == 0 {
				prec = encoding.MaxDecimalPrecision
			}
			if scale > prec {
				return nil, nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					fmt.Sprintf("field %q: scale > precision", fs.Name),
					map[string]any{"precision": prec, "scale": scale})
			}
			field.Precision = prec
			field.Scale = scale
		}
		field.ByteOffset, field.BitPosition = nextFieldLayout(ft, &byteOffset, &bitCursor)

		smp, err := buildSampler(fs)
		if err != nil {
			return nil, nil, err
		}

		fields[i] = field
		wfs[i] = &writerField{spec: fs, field: &fields[i], sampler: smp}
	}

	schema := &encoding.Schema{Fields: fields}
	for i := range wfs {
		wfs[i].field = &schema.Fields[i]
	}
	return schema, wfs, nil
}

// generate writes synthetic records to a bytes.Buffer, respecting any
// declared constraints via rejection sampling.
func generate(s *Spec, schema *encoding.Schema, wfs []*writerField, recordsBuf *bytes.Buffer, rng *mrand.Rand) (rowsGenerated, rowsRejected int, warnings []string, err error) {
	maxRej := s.MaxRejectionRate
	if maxRej == 0 {
		maxRej = 0.5
	}

	row := make(map[string]any, len(wfs))
	rowNullMask := make(map[string]bool, len(wfs))
	bitmapSize := schema.BitmapByteSize()

	cons, err := compileConstraints(s.Constraints, wfs)
	if err != nil {
		return 0, 0, nil, err
	}

	// resolveConflicts runs once per Spec, not per row — conflicts among
	// the conditional-pairing relationships below are static for a given
	// spec, so per-row detection would be pure waste. It walks the exact
	// priority order the stage sequence below already implies and prunes
	// any later claimant whose target an earlier stage already claimed,
	// producing one warning per exclusion instead of drawRow's previous
	// silent last-write-wins. See synth/conflict.go.
	conflicts := resolveConflicts(s)
	warnings = conflicts.warnings

	// corrWarnings names what the correlation matrix had to invent to be
	// usable — pairs never supplied and completed as independent, and a
	// ridge that had to be added because the supplied ones were not
	// jointly realizable. Both were silent before E3-S1; see
	// buildCorrelator for why the policy is assume-and-record rather
	// than refuse.
	corr, corrWarnings, err := buildCorrelator(conflicts.correlations, wfs)
	if err != nil {
		return 0, 0, nil, err
	}
	warnings = append(warnings, corrWarnings...)
	models, modelWarnings, err := buildModelDrawers(conflicts.models, wfs)
	if err != nil {
		return 0, 0, nil, err
	}
	// Compilation warnings follow the arbitration warnings: a model that
	// won its claim and then turned out to be uncompilable is a second,
	// later fact about the same field, and reporting it before the
	// conflicts it already resolved would invert the causal order — the
	// same ordering rule SpecFromProfile applies to its own model-drop
	// warnings.
	warnings = append(warnings, modelWarnings...)

	// The residual correlator is built LAST because it is built over the
	// COMPILED drawers, not over Spec.Models: a model that lost its
	// claim or failed to compile has no residual for a correlation to
	// attach to, and deciding participation from the spec list would
	// stamp a component index onto a field nothing draws. It also stamps
	// each participant's component index onto its drawer, so it must run
	// after buildModelDrawers has fixed their schema order.
	residual, residualWarnings, err := buildResidualCorrelator(s.ResidualCorrelations, models)
	if err != nil {
		return 0, 0, nil, err
	}
	warnings = append(warnings, residualWarnings...)

	stages := &rowStages{
		catPairs:    buildCategoricalPairSamplers(conflicts.catPairs),
		catNumPairs: buildCategoricalNumericPairSamplers(conflicts.catNumPairs),
		setSetPairs: buildSetSetPairSamplers(conflicts.setSetPairs),
		setCatPairs: buildSetCategoricalPairSamplers(conflicts.setCatPairs),
		setNumPairs: buildSetNumericPairSamplers(conflicts.setNumPairs),
		corr:        corr,
		models:      models,
		residual:    residual,
	}

	for rowsGenerated < s.RowCount {
		if err := drawRow(rng, wfs, row, rowNullMask, stages); err != nil {
			return rowsGenerated, rowsRejected, warnings, err
		}
		ok, evalErr := cons.evaluate(row)
		if evalErr != nil {
			return rowsGenerated, rowsRejected, warnings, evalErr
		}
		if !ok {
			rowsRejected++
			total := rowsGenerated + rowsRejected
			// Enforce only after a sane sample so a few unlucky early
			// rejections don't trip the threshold.
			if total >= 64 && float64(rowsRejected)/float64(total) > maxRej {
				return rowsGenerated, rowsRejected, warnings,
					errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_CONSTRAINT_INFEASIBLE,
						"rejection rate exceeded threshold; relax constraint or change distribution",
						map[string]any{
							"rows_accepted": rowsGenerated,
							"rows_rejected": rowsRejected,
							"threshold":     maxRej,
						})
			}
			continue
		}
		if encErr := encodeRow(recordsBuf, wfs, row, rowNullMask, bitmapSize); encErr != nil {
			return rowsGenerated, rowsRejected, warnings, encErr
		}
		rowsGenerated++
	}
	return rowsGenerated, rowsRejected, warnings, nil
}

// rowStages is the fixed, per-Spec set of post-processing stages
// drawRow applies to every row, compiled once at generate() setup time
// and held in the exact order they run. Bundled into a struct rather
// than passed as seven positional arguments so adding a stage is a
// named field and a documented position in drawRow, not another
// unlabelled parameter at a call site.
//
// Every field is independently a no-op (nil / empty slice) unless the
// Spec that produced it actually declared that structure — see the
// Spec.CategoricalPairs / CategoricalNumericPairs / SetCategoricalPairs
// / SetNumericPairs / SetSetPairs / Correlations / Models doc comments.
type rowStages struct {
	catPairs    []*categoricalPairSampler
	catNumPairs []*categoricalNumericPairSampler
	setSetPairs []*setSetPairSampler
	setCatPairs []*setCategoricalPairSampler
	setNumPairs []*setNumericPairSampler
	corr        *correlator
	models      []*modelDrawer
	// residual carries the correlation structure among the MODELS'
	// residuals. It is not a stage of its own — nothing it produces
	// reaches the row directly — it is the shared source of randomness
	// the model stage draws through. nil whenever fewer than two
	// modelled fields participate, in which case every drawer takes its
	// own independent z exactly as it did before the slot existed.
	residual *residualCorrelator
}

// drawRow draws one row: every field's own independent sampler first,
// then a fixed chain of conditional post-processing steps that each
// overwrite an already-drawn field's value in place — categorical joint
// structure (categorical-categorical, then categorical-numeric) applied
// before set-field joint structure (set-set, then set-categorical, then
// set-numeric), before the numeric-numeric correlator, so a numeric or
// set field's final value reflects whichever categorical/set value it
// ends up conditioned on before any requested Pearson correlation is
// layered on top. set-set runs before set-categorical/set-numeric so a
// set option's OWN state — potentially itself resampled from another
// set field's option — is settled before it is used as the conditioning
// input for a numeric resample.
//
// The linear-model stage runs LAST, after every other stage has
// settled. Its inputs are the row's categorical levels and set option
// bits, which the pair stages above can still be rewriting, so reading
// them any earlier would evaluate a model against values the emitted
// row does not carry. Nothing runs after it, and nothing needs to:
// resolveConflicts claims a modelled field before any pair or
// correlation stage can bid for it, so a modelled numeric is written
// exactly once per row. See synth/model_draw.go for the construction.
//
// The model stage is preceded by one draw that is NOT a stage: the row's
// shared correlated normal vector (synth/residual_draw.go). It writes
// nothing into the row — it supplies the z each participating drawer
// composes its value through — which is how a modelled field ends up
// both conditioned on its predictors and correlated with a sibling
// numeric without either structure overwriting the other.
//
// ORDER IS FIXED AND SPEC-DERIVED, NEVER MAP-DERIVED. Fields are walked
// via the ordered []*writerField and never via the row map, and the
// model stage is sorted into schema field order at compile time, so the
// per-row sequence of RNG draws is a function of the Spec alone — the
// property "same spec + same seed produces a byte-identical .pulse
// file" rests on it, and buildSchema's set-dictionary pre-registration
// comment and writeFieldValueForField's dictionary-order iteration are
// the same rule applied on the encode side.
func drawRow(rng *mrand.Rand, wfs []*writerField, row map[string]any, nullMask map[string]bool, stages *rowStages) error {
	for k := range row {
		delete(row, k)
	}
	for k := range nullMask {
		delete(nullMask, k)
	}
	for _, wf := range wfs {
		v, isNull := wf.sampler.next(rng)
		row[wf.spec.Name] = v
		if isNull {
			nullMask[wf.spec.Name] = true
		}
	}
	for _, cp := range stages.catPairs {
		cp.transform(rng, row)
	}
	for _, cnp := range stages.catNumPairs {
		cnp.transform(rng, row)
	}
	for _, ssp := range stages.setSetPairs {
		ssp.transform(rng, row)
	}
	for _, scp := range stages.setCatPairs {
		scp.transform(rng, row)
	}
	for _, snp := range stages.setNumPairs {
		snp.transform(rng, row)
	}
	if stages.corr != nil {
		stages.corr.transform(rng, row)
	}
	// One correlated normal vector for the whole row, drawn before the
	// first drawer runs so every participant reads the same row's
	// residual structure. It consumes exactly one rng.NormFloat64() per
	// participant in component order (= drawer order = schema order),
	// and is skipped entirely — no draws at all — when nothing
	// participates, which is what keeps a spec without residual
	// correlations byte-identical to one produced before the slot
	// existed.
	stages.residual.draw(rng)
	for _, md := range stages.models {
		md.transform(rng, row, nullMask, stages.residual)
	}
	return nil
}

func encodeRow(buf *bytes.Buffer, wfs []*writerField, row map[string]any, nullMask map[string]bool, bitmapSize int) error {
	for _, wf := range wfs {
		val := row[wf.spec.Name]
		isNull := nullMask[wf.spec.Name]
		if err := writeFieldValue(buf, wf, val, isNull); err != nil {
			return err
		}
	}
	if bitmapSize > 0 {
		bitmap := make([]byte, bitmapSize)
		for i, wf := range wfs {
			if !wf.field.Nullable {
				continue
			}
			if nullMask[wf.spec.Name] {
				encoding.BitmapSetNull(bitmap, i)
			}
		}
		if err := encoding.WriteBitmap(buf, bitmap); err != nil {
			return err
		}
	}
	return nil
}

func writeFieldValue(buf *bytes.Buffer, wf *writerField, val any, isNull bool) error {
	return writeFieldValueForField(buf, wf.field, val, isNull)
}

// writeFieldValueForField encodes a single field's value into buf per
// field.Type, using field.Dictionary for categorical AddWithLimit. It is
// the field-shaped twin of writeFieldValue (which threads through a
// spec-bound *writerField); both funnel through here so the merge/augment
// path (synth/augment.go, which re-encodes decoded records rather than
// sampler-drawn values) shares exactly one encode implementation with
// ordinary spec-driven generation.
func writeFieldValueForField(buf *bytes.Buffer, field *encoding.Field, val any, isNull bool) error {
	ft := field.Type
	switch ft {
	case encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		f := toFloat64(val)
		if f < 0 {
			f = 0
		}
		if math.IsNaN(f) {
			f = 0
		}
		raw := uint64(math.Floor(f + 0.5))
		raw = clampUnsigned(raw, ft)
		return encoding.WriteFieldValue(buf, ft, raw)
	case encoding.FieldTypeF32:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		f := toFloat32(val)
		return encoding.WriteFieldValue(buf, ft, uint64(math.Float32bits(f)))
	case encoding.FieldTypeF64:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		f := toFloat64(val)
		return encoding.WriteFieldValue(buf, ft, math.Float64bits(f))
	case encoding.FieldTypeDate:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		f := toFloat64(val)
		days := uint32(int64(math.Floor(f + 0.5)))
		return encoding.WriteFieldValue(buf, ft, uint64(days))
	case encoding.FieldTypePackedBool:
		// Single byte holding the flag in bit 0. Matches reader's ReadBit.
		var b byte
		if !isNull && toBool(val) {
			b = 1
		}
		_, err := buf.Write([]byte{b})
		return err
	case encoding.FieldTypeU4:
		var v uint8
		if !isNull {
			f := toFloat64(val)
			if math.IsNaN(f) {
				f = 0
			}
			v = uint8(math.Floor(f+0.5)) & 0x0F
		}
		_, err := buf.Write([]byte{v})
		return err
	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		s, ok := val.(string)
		if !ok {
			return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
				fmt.Sprintf("field %q: categorical value must be a string", field.Name), nil)
		}
		id, err := field.Dictionary.AddWithLimit(s, ft.MaxCategoricalEntries())
		if err != nil {
			return err
		}
		return encoding.WriteFieldValue(buf, ft, uint64(id))
	case encoding.FieldTypeSetU8, encoding.FieldTypeSetU16, encoding.FieldTypeSetU32, encoding.FieldTypeSetU64:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		if field.Dictionary == nil {
			return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
				fmt.Sprintf("field %q: set field missing dictionary", field.Name), nil)
		}
		var mask uint64
		switch sel := val.(type) {
		case map[string]bool:
			// Iterate the dictionary's OWN fixed order (never a Go map
			// range) so bit assignment cannot depend on map iteration
			// randomization — see buildSchema's pre-registration
			// comment for the full determinism rationale.
			for _, opt := range field.Dictionary.Values() {
				if !sel[opt] {
					continue
				}
				id, ok := field.Dictionary.IDFor(opt)
				if !ok {
					continue
				}
				mask |= uint64(1) << id
			}
		case []string:
			// The re-encode (AugmentFromProfile) path: a decoded row's
			// currently-selected labels, already ordered by the source
			// dictionary's own ascending bit order (see
			// decodedFieldValue in augment.go). AddWithLimit is a no-op
			// lookup for every label the merged dictionary was already
			// pre-populated with at buildMergedSchema time.
			for _, opt := range sel {
				id, aerr := field.Dictionary.AddWithLimit(opt, ft.MaxSetEntries())
				if aerr != nil {
					return errors.WrapCodedError(aerr, errors.PULSE_IMPORT_SET_OVERFLOW,
						fmt.Sprintf("field %q: encoding set value", field.Name))
				}
				mask |= uint64(1) << id
			}
		default:
			return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
				fmt.Sprintf("field %q: set value must be map[string]bool or []string", field.Name), nil)
		}
		return encoding.WriteFieldValue(buf, ft, mask)
	case encoding.FieldTypeDecimal128:
		if isNull {
			return encoding.WriteDecimal128(buf, encoding.ZeroDecimal128())
		}
		// A decoded record carries the exact Decimal128 already (see
		// synth/augment.go's decodedFieldValue) — write it straight
		// through rather than round-tripping via decimalFromValue's
		// string/float paths, which would risk precision loss.
		if dec, ok := val.(encoding.Decimal128); ok {
			return encoding.WriteDecimal128(buf, dec)
		}
		dec, err := decimalFromValue(val, field.Scale)
		if err != nil {
			return errors.WrapCodedError(err, errors.PULSE_DECIMAL_OVERFLOW,
				fmt.Sprintf("field %q: encoding decimal", field.Name))
		}
		return encoding.WriteDecimal128(buf, dec)
	}
	return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
		fmt.Sprintf("field %q: cannot encode type %s", field.Name, ft), nil)
}

func toFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		return 0
	}
	return 0
}

func toFloat32(v any) float32 {
	return float32(toFloat64(v))
}

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case int:
		return x != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		return s == "true" || s == "1" || s == "yes" || s == "y" || s == "t"
	}
	return false
}

func clampUnsigned(v uint64, ft encoding.FieldType) uint64 {
	switch ft {
	case encoding.FieldTypeU8:
		if v > 0xFF {
			return 0xFF
		}
	case encoding.FieldTypeU16:
		if v > 0xFFFF {
			return 0xFFFF
		}
	case encoding.FieldTypeU32:
		if v > 0xFFFFFFFF {
			return 0xFFFFFFFF
		}
	}
	return v
}

func decimalFromValue(v any, scale uint8) (encoding.Decimal128, error) {
	switch x := v.(type) {
	case string:
		dec, srcScale, err := encoding.ParseDecimal128(x)
		if err != nil {
			return encoding.Decimal128{}, err
		}
		if srcScale != scale {
			return dec.Rescale(srcScale, scale)
		}
		return dec, nil
	case float64:
		return decimalFromFloat(x, scale)
	case int:
		return decimalFromFloat(float64(x), scale)
	case int64:
		return decimalFromFloat(float64(x), scale)
	}
	return encoding.Decimal128{}, errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
		"unsupported value type for decimal128")
}

func decimalFromFloat(f float64, scale uint8) (encoding.Decimal128, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return encoding.Decimal128{}, errors.NewCodedError(errors.PULSE_DECIMAL_OVERFLOW,
			"cannot encode NaN/Inf as decimal128")
	}
	mult := math.Pow10(int(scale))
	scaled := f * mult
	if scaled < 0 {
		scaled = math.Ceil(scaled - 0.5)
	} else {
		scaled = math.Floor(scaled + 0.5)
	}
	bf := new(big.Float).SetFloat64(scaled)
	m := new(big.Int)
	bf.Int(m)
	return encoding.NewDecimal128FromBigInt(m)
}

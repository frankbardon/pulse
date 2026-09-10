package synth

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing/regression"
)

// This file is the synth-side bridge onto the shipped OLS engine in
// processing/regression. It exists because the linear predictor this
// package wants to fit — one coefficient per categorical LEVEL and per
// set OPTION — has no numeric column behind it in the cohort. A cohort
// stores `dma` as a single categorical dictionary ID and `brandAssets`
// as a single bitmask; the design matrix needs one 0/1 column per level
// and per option. Materialising those columns as real fields would mean
// rewriting the cohort, so instead they are synthesised at read time by
// the Record adapter below.
//
// The whole construction rests on regression.Record being a ONE-METHOD
// interface (NumericValue(name) (float64, bool)): the engine pulls each
// predictor BY NAME and never asks where the number came from. Dummy
// coding is therefore entirely a caller-side concern and needs no change
// in processing/regression — which is load-bearing, since that package's
// solvers carry the golden/property test coverage this package is
// deliberately reusing rather than reimplementing.
//
// # Schema validation (PRD FR-3 — the effort's main architectural unknown)
//
// regression.BuildStreaming takes an *encoding.Schema alongside the
// specs, and the dummy columns named in RegressionSpec.Predictors do not
// exist in the real cohort schema. The question was whether that schema
// is used to VALIDATE predictor names, which would reject every dummy
// outright.
//
// It is not. Field-existence and field-type checking for a
// RegressionSpec lives in descriptor.validateRegressions — the
// no-execute predict path — and processing/regression states the
// division explicitly on newOLSEngine: "The spec is assumed already to
// be type-checked against the schema by descriptor.validateRegressions."
// The engine stores the schema and, for REG_OLS, never reads it; every
// value it consumes arrives through Record.NumericValue. synth does not
// route through descriptor (it cannot — descriptor imports synth), so
// nothing on this path ever validates a predictor name against a schema.
//
// The FALLBACK named in the story — a hand-rolled normal-equations solve
// inside synth/ — is therefore not needed and was not taken.
//
// A view schema is still built and passed (viewSchema below) rather than
// passing the real cohort schema or nil, for three reasons: it is honest
// about the columns the engine is actually being driven with, it keeps
// the call site working unchanged if a future release does start
// validating names there, and it follows the precedent already set in
// this package by SyntheticAsCategoricalSchema (synth/fidelity.go) —
// a throwaway presentation-only schema, built for one call, never used
// to decode bytes and never written to disk.

// dummyColumnKind tags how a dummyColumn derives its 0/1 (or scalar)
// value from a decoded row.
type dummyColumnKind uint8

const (
	// dummyNumeric passes a numeric field's own value through unchanged.
	// Used for the target and for any predictor that is already a scalar.
	dummyNumeric dummyColumnKind = iota
	// dummyCategoricalLevel is 1 when the row's categorical field holds
	// this column's level, 0 when it holds a different one.
	dummyCategoricalLevel
	// dummySetOption is the row's set_* bitmask bit for this column's
	// option. A set option is ALREADY a binary indicator, which is why it
	// folds into the same design matrix as a categorical level with no
	// separate mechanism.
	dummySetOption
)

// dummyColumn is one column of the synthesised design matrix.
type dummyColumn struct {
	// Name is the generated, collision-safe column name — the string the
	// engine passes to NumericValue. See dummyCategoricalName /
	// dummySetName for the encoding.
	Name string
	Kind dummyColumnKind
	// Field is the REAL cohort field this column reads from. Nullity is
	// always decided on Field, never on Name.
	Field string
	// Level is the categorical level text or set option text this column
	// indicates. Empty for dummyNumeric.
	Level string
	// LevelID is the categorical dictionary ID matching Level. The
	// per-row comparison is done on the ID, not the text, so no string
	// compare happens in the inner loop.
	LevelID uint32
	// Bit is the set_* mask bit index matching Level.
	Bit uint
	// dict is the source field's dictionary, retained so the row reader
	// can tell an in-dictionary ID from an unresolvable one.
	dict *encoding.Dictionary
}

// dummyPlan is the fixed, deterministic expansion of one target plus a
// list of predictor SOURCE fields into design-matrix columns. Built once
// per fitted field, then reused across every row of the scan.
//
// Column order is deterministic and depends only on the caller's
// predictor field order and each dictionary's own ID order — no map
// iteration participates, so two runs over the same schema produce the
// same column list in the same order.
type dummyPlan struct {
	target  string
	columns []dummyColumn
	byName  map[string]dummyColumn
}

// dummyCategoricalName encodes a (field, level) pair into a column name.
// dummySetName does the same for (field, option).
//
// Both are LENGTH-PREFIXED on the field name, which is what makes them
// injective: a reader takes the digits up to the first ':' as a byte
// count, the next that-many bytes as the field name, and everything
// after the single delimiter as the level. A level whose literal text
// contains '=', '[', ']' or ':' therefore cannot forge another column's
// name, and no (field, level) pair can collide with a different one.
// The kind tag ('c'/'s') keeps a categorical column from ever equalling
// a set column.
//
// Nothing actually parses these names — resolution is a map lookup on
// dummyPlan.byName. Injectivity is what matters: it is what guarantees
// the map has one entry per distinct column, so a forged name resolves
// to nothing rather than to somebody else's coefficient. The names are
// an internal encoding and never reach a user-facing surface.
func dummyCategoricalName(field, level string) string {
	var b strings.Builder
	b.WriteString("c:")
	b.WriteString(strconv.Itoa(len(field)))
	b.WriteByte(':')
	b.WriteString(field)
	b.WriteByte('=')
	b.WriteString(level)
	return b.String()
}

func dummySetName(field, option string) string {
	var b strings.Builder
	b.WriteString("s:")
	b.WriteString(strconv.Itoa(len(field)))
	b.WriteByte(':')
	b.WriteString(field)
	b.WriteByte('[')
	b.WriteString(option)
	b.WriteByte(']')
	return b.String()
}

// newDummyPlan expands target + predictorFields against schema.
//
// target must be a scalar field (a categorical or set target has no
// single number to regress and is rejected rather than silently coerced
// from its dictionary ID, which would fit a model on arbitrary
// dictionary ordering). Each predictor field expands per its schema
// type: a categorical into one column per dictionary entry, a set into
// one column per option bit, anything else scalar into itself.
//
// NOTE the expansion is FULL — every level of every categorical gets a
// column. A full level set plus an intercept is rank-deficient (the
// dummy trap), so the caller choosing which columns to hand
// RegressionSpec.Predictors is responsible for dropping a reference
// level. That choice is a modelling decision and deliberately does not
// live here; because the Record resolves purely by name, any SUBSET of
// PredictorNames() works without rebuilding the plan.
func newDummyPlan(schema *encoding.Schema, target string, predictorFields []string) (*dummyPlan, error) {
	if schema == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"synth: dummy plan requires a schema")
	}
	tf := schema.Field(target)
	if tf == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"synth: regression target field not in schema",
			map[string]any{"field": target})
	}
	if tf.Type.IsCategorical() || tf.Type.IsSet() || !tf.Type.IsNumericForAnalytics() {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"synth: regression target must be a scalar numeric field",
			map[string]any{"field": target, "type": tf.Type.String()})
	}

	p := &dummyPlan{
		target: target,
		byName: make(map[string]dummyColumn, len(predictorFields)+1),
	}
	// The target resolves through the same Record, under its own plain
	// name — the engine reads spec.Target via NumericValue exactly as it
	// reads a predictor. It is registered in byName but NOT in columns,
	// so PredictorNames() never offers the target as its own regressor.
	p.byName[target] = dummyColumn{Name: target, Kind: dummyNumeric, Field: target}

	seen := make(map[string]bool, len(predictorFields))
	for _, name := range predictorFields {
		if name == target {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"synth: regression target cannot also be a predictor",
				map[string]any{"field": name})
		}
		if seen[name] {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"synth: duplicate regression predictor field",
				map[string]any{"field": name})
		}
		seen[name] = true

		f := schema.Field(name)
		if f == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"synth: regression predictor field not in schema",
				map[string]any{"field": name})
		}
		switch {
		case f.Type.IsCategorical():
			if f.Dictionary == nil {
				return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					"synth: categorical predictor carries no dictionary",
					map[string]any{"field": name})
			}
			for id := 0; id < f.Dictionary.Count(); id++ {
				level := f.Dictionary.Resolve(uint32(id))
				p.columns = append(p.columns, dummyColumn{
					Name:    dummyCategoricalName(name, level),
					Kind:    dummyCategoricalLevel,
					Field:   name,
					Level:   level,
					LevelID: uint32(id),
					dict:    f.Dictionary,
				})
			}
		case f.Type.IsSet():
			if f.Dictionary == nil {
				return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					"synth: set predictor carries no dictionary",
					map[string]any{"field": name})
			}
			n := f.Dictionary.Count()
			// A dictionary can outgrow the mask width only through a
			// corrupt file; clamp so a bit index can never exceed the
			// on-wire mask, matching profileRecords' own clamp.
			if max := int(f.Type.MaxSetEntries()); n > max {
				n = max
			}
			for bit := 0; bit < n; bit++ {
				option := f.Dictionary.Resolve(uint32(bit))
				p.columns = append(p.columns, dummyColumn{
					Name:  dummySetName(name, option),
					Kind:  dummySetOption,
					Field: name,
					Level: option,
					Bit:   uint(bit),
					dict:  f.Dictionary,
				})
			}
		case f.Type.IsNumericForAnalytics():
			p.columns = append(p.columns, dummyColumn{
				Name: name, Kind: dummyNumeric, Field: name,
			})
		default:
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"synth: unsupported regression predictor field type",
				map[string]any{"field": name, "type": f.Type.String()})
		}
	}

	for _, c := range p.columns {
		if _, clash := p.byName[c.Name]; clash {
			// Unreachable through the encoding's injectivity for two
			// dummies; reachable only if a REAL field is literally named
			// like a generated column. Refusing beats silently letting
			// one column shadow another's coefficient.
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"synth: dummy column name collides with an existing column",
				map[string]any{"column": c.Name, "field": c.Field})
		}
		if schema.Field(c.Name) != nil && c.Kind != dummyNumeric {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"synth: dummy column name collides with a real schema field",
				map[string]any{"column": c.Name, "field": c.Field})
		}
		p.byName[c.Name] = c
	}
	return p, nil
}

// PredictorNames returns every expanded column name in plan order. The
// caller normally hands a SUBSET of this to RegressionSpec.Predictors
// (see newDummyPlan on reference levels).
func (p *dummyPlan) PredictorNames() []string {
	out := make([]string, len(p.columns))
	for i := range p.columns {
		out[i] = p.columns[i].Name
	}
	return out
}

// Target returns the target field name, which doubles as its column name.
func (p *dummyPlan) Target() string { return p.target }

// viewSchema builds the throwaway *encoding.Schema handed to
// regression.Build / BuildStreaming beside the spec.
//
// Every column — target included — is declared f64 and non-nullable,
// because f64 is precisely what the engine consumes through
// NumericValue; nullity is carried by that method's ok return, not by a
// bitmap, since this schema NEVER decodes bytes. Offsets are laid out on
// a plain 8-byte stride purely so RecordByteSize() stays self-consistent
// if anything ever asks; nothing reads them.
//
// This mirrors SyntheticAsCategoricalSchema (synth/fidelity.go): a
// presentation-only schema constructed for one call and discarded, never
// written to disk. See the FR-3 note at the top of this file for why the
// engine does not currently look at it at all.
func (p *dummyPlan) viewSchema() *encoding.Schema {
	fields := make([]encoding.Field, 0, len(p.columns)+1)
	add := func(name string) {
		fields = append(fields, encoding.Field{
			Name:         name,
			Type:         encoding.FieldTypeF64,
			Nullable:     false,
			ByteOffset:   len(fields) * 8,
			CsvColumnIdx: len(fields),
		})
	}
	add(p.target)
	for i := range p.columns {
		add(p.columns[i].Name)
	}
	return &encoding.Schema{Fields: fields}
}

// dummyRecord adapts one decoded cohort row to regression.Record,
// expanding dummy columns on demand.
//
// It is bound to the SAME three maps profileRecords already fills per
// row (values / nulls / wide) and holds no copy of them, so folding a
// fit into that existing single scan costs no extra decode and no
// per-row allocation. The maps are cleared and repopulated in place by
// each RecordReader read — see the reuse contract on
// encoding.RecordReader.ReadRecord — so a dummyRecord is only valid
// until the next read, which is exactly the lifetime a streaming
// UpdateRow needs.
type dummyRecord struct {
	plan   *dummyPlan
	values map[string]float64
	nulls  map[string]bool
	wide   map[string]any
}

var _ regression.Record = (*dummyRecord)(nil)

// newRecord returns a reusable adapter bound to this plan.
func (p *dummyPlan) newRecord() *dummyRecord { return &dummyRecord{plan: p} }

// bind points the adapter at the current row's decoded maps. wide may be
// nil when the plan expands no set predictor; a set column then reads as
// null rather than as an unselected 0, which is the honest answer — the
// mask was never decoded.
func (r *dummyRecord) bind(values map[string]float64, nulls map[string]bool, wide map[string]any) {
	r.values = values
	r.nulls = nulls
	r.wide = wide
}

// NumericValue implements regression.Record.
//
// Null propagation is the load-bearing part. A null SOURCE field makes
// every column derived from it answer (0, false) — never (0, true) —
// because "this row has no dma" is not the same statement as "this row
// is not dma=501", and the second would quietly enter a real zero into
// the design matrix. The engine applies listwise deletion on any
// (0, false), so the row drops out of the fit entirely, which is the
// intended treatment. An unknown name answers (0, false) for the same
// reason.
//
// A level simply ABSENT from the row is different and does answer
// (0, true): the row has a dma, it is just not this one. That is a
// genuine measured zero and the row must contribute.
func (r *dummyRecord) NumericValue(name string) (float64, bool) {
	col, ok := r.plan.byName[name]
	if !ok {
		return 0, false
	}
	if r.values == nil || r.nulls[col.Field] {
		return 0, false
	}
	switch col.Kind {
	case dummyCategoricalLevel:
		raw, present := r.values[col.Field]
		if !present {
			return 0, false
		}
		id := uint32(raw)
		// An ID the dictionary cannot resolve is treated as missing
		// rather than as "not this level": the row's category is
		// genuinely unknown, so no level column may claim a measured
		// zero from it. This matches profileRecords, which skips the
		// row for that field on the same empty-Resolve condition.
		if col.dict == nil || col.dict.Resolve(id) == "" {
			return 0, false
		}
		if id == col.LevelID {
			return 1, true
		}
		return 0, true
	case dummySetOption:
		// wide carries the exact uint64 mask (see
		// encoding.RecordReader.readRecord); values' float64 echo is not
		// used because set_u64 bits exceed float64's 2^53 exact range.
		// A missing entry means the mask was not decoded (null field or
		// no wide map) — missing, not empty.
		mask, present := r.wide[col.Field].(uint64)
		if !present {
			return 0, false
		}
		if mask&(uint64(1)<<col.Bit) != 0 {
			return 1, true
		}
		return 0, true
	default:
		v, present := r.values[col.Field]
		if !present {
			return 0, false
		}
		return v, true
	}
}

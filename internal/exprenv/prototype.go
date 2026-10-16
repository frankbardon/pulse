package exprenv

import (
	"reflect"

	exprtypes "github.com/expr-lang/expr/types"

	"github.com/frankbardon/pulse/encoding"
)

// Prototype declares every schema field typed as a decoded record
// surfaces it (internal/processing's Record.AllValues; [ValueType]), so
// an expression type-checks against the schema alone — no record and no
// engine. A duplicated name keeps its first occurrence, as Schema.Field
// does. A nil schema declares nothing.
//
// The compile-once runtime builds its declaration here, and a no-engine
// caller compiles with the same three parts:
//
//	opts := append([]expr.Option{expr.Env(exprenv.Prototype(schema))}, exprenv.Options()...)
//	opts = append(opts, exprenv.ExtensionFunctions(fns)...)
//	_, err := expr.Compile(src, opts...)
func Prototype(schema *encoding.Schema) exprtypes.Map {
	decl := exprtypes.Map{}
	if schema == nil {
		return decl
	}
	for i := range schema.Fields {
		f := &schema.Fields[i]
		if _, dup := decl[f.Name]; dup {
			continue
		}
		decl[f.Name] = exprtypes.TypeOf(reflect.Zero(ValueType(f)).Interface())
	}
	return decl
}

var (
	exprAnyType      = reflect.TypeOf((*any)(nil)).Elem()
	exprFloat64Type  = reflect.TypeOf(float64(0))
	exprStringType   = reflect.TypeOf("")
	exprStringsType  = reflect.TypeOf([]string(nil))
	exprUint64Type   = reflect.TypeOf(uint64(0))
	exprSetMaskType  = reflect.TypeOf(encoding.SetMask{})
	exprFloat64sType = reflect.TypeOf([]float64(nil))
)

// ValueType is the Go type field f's value binds as in the env — a
// decoded record's value (Record.AllValues) through [Bind]: a
// dictionary-bearing categorical resolves to its label, a
// dictionary-bearing set to its label slice, a decimal to a [Decimal]
// (mantissa + the field's scale), a dictionary-less set rides its wide
// value, a vector its []float64 elements, everything else is the
// float64 slot. Pinned against every decode path by
// internal/processing's TestExprValueType_MatchesAllValues.
func ValueType(f *encoding.Field) reflect.Type {
	switch {
	case f.Type.IsCategorical():
		if f.Dictionary != nil {
			return exprStringType
		}
	case f.Type.IsSet():
		if f.Dictionary != nil {
			return exprStringsType
		}
		if f.Type.IsWideSet() {
			return exprSetMaskType
		}
		return exprUint64Type
	case f.Type == encoding.FieldTypeDecimal128:
		return exprDecimalType
	case f.Type.IsVector():
		return exprFloat64sType
	}
	return exprFloat64Type
}

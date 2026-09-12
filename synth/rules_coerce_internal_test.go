package synth

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// THE COERCION MATRIX IS THE STORY, so this file is its gate.
//
// A `set_expr` expression returns one of a handful of Go shapes and the
// target field is one of the seventeen types fieldTypeFromName accepts.
// A wrong cell is a SILENT wrong value — a bool landing on a u8 as a
// Go-formatted string encodes as 0, a number landing on a categorical
// grows the dictionary with a value that looks like data — so every cell
// is asserted here rather than inferred from the implementation.
//
// The row axis is DERIVED, never hand-listed: declarableTypeNames walks
// the encoding.FieldType enum and representativeFieldSpecs supplies one
// minimal valid spec per type (both from constraints_internal_test.go),
// so a field type added to fieldTypeFromName fails this test instead of
// acquiring a silent default cell. That is the same construction
// TestSentinelFor_MatchesDrawnRowValueForEveryDeclarableType uses, and
// E1-S6 exists because two tables that had to agree did not.

// exprResultKinds is the COLUMN axis: one representative value per Go
// shape an expr-lang expression compiled against rowExprEnv can return.
//
// The shapes are not guesses. With a map[string]any environment whose
// values are the row's own sentinels, expr yields: bool from a
// comparison, float64 from arithmetic over a scalar field, int from an
// integer literal or len(), string from a categorical field or a string
// literal, map[string]bool from a set_* field, []any from an array
// literal and []string from split(). Everything else is either
// interface{} (statically unknowable — deferred to run time) or a shape
// no target can hold.
var exprResultKinds = []struct {
	name  string
	value any
}{
	{"bool", true},
	// 1, not an arbitrary number: this axis tests TYPE admission and 1
	// is in range for every bounded scalar type including packed_bool,
	// so a refusal here is always about the shape and never about the
	// value. Range is asserted separately, below.
	{"float64", float64(1)},
	{"int", 1},
	{"string", "a"},
	{"map[string]bool", map[string]bool{"a": true}},
	{"[]any", []any{"a"}},
	{"[]string", []string{"a"}},
	{"[]float64", []float64{1}},
}

// wantAdmitted is the expected verdict per (target class, result shape).
// Written as the THREE row-value classes sentinelFor derives rather than
// per type name, because the coercion is a property of the class: every
// scalar takes a number or a bool, a categorical takes a string, a set_*
// takes a selection. The per-TYPE table below then expands this over
// every declarable type name, so adding a type cannot skip a cell.
func wantAdmitted(ft encoding.FieldType, kind string) bool {
	switch {
	case ft.IsCategorical():
		return kind == "string"
	case ft.IsSet():
		return kind == "map[string]bool" || kind == "[]any" || kind == "[]string"
	case ft.IsDecimal():
		// decimal128 is the ONE cell where set_expr is deliberately
		// narrower than a `set` literal: a literal string is kept
		// verbatim and parsed exactly, an expression's string is not.
		// See ruleValueFault for why.
		return kind == "bool" || kind == "float64" || kind == "int"
	default:
		return kind == "bool" || kind == "float64" || kind == "int"
	}
}

// TestRuleCoercion_MatrixCoversEveryDeclarableTargetType is the matrix.
func TestRuleCoercion_MatrixCoversEveryDeclarableTargetType(t *testing.T) {
	specs := representativeFieldSpecs()
	names := declarableTypeNames(t)
	if len(names) < 17 {
		t.Fatalf("expected at least 17 declarable field types, got %d: %v", len(names), names)
	}
	for _, typeName := range names {
		fs, ok := specs[typeName]
		if !ok {
			t.Fatalf("declarable field type %q has no representative FieldSpec; add one", typeName)
		}
		ft, ok := fieldTypeFromName(typeName)
		if !ok {
			t.Fatalf("fieldTypeFromName(%q) failed", typeName)
		}
		for _, kind := range exprResultKinds {
			t.Run(typeName+"/"+kind.name, func(t *testing.T) {
				value, err := ruleExprRowValue("x", fs, ft, kind.value)
				admitted := err == nil
				if want := wantAdmitted(ft, kind.name); admitted != want {
					t.Fatalf("target %s <- %s: admitted = %v, want %v (err %v)",
						typeName, kind.name, admitted, want, err)
				}
				if !admitted {
					return
				}
				// An ADMITTED value must land in the row as the Go shape
				// sentinelFor promises for that type, or a later rule's
				// expression over the same field fails at run time with
				// "invalid operation" — issue #258's exact failure mode,
				// reachable per-row and only on gated rows.
				if got, want := reflect.TypeOf(value), reflect.TypeOf(sentinelFor(typeName)); got != want {
					t.Fatalf("target %s <- %s coerced to %v, but sentinelFor(%q) promises %v",
						typeName, kind.name, got, typeName, want)
				}
			})
		}
	}
}

// TestRuleCoercion_BoolIsOneOrZeroOnEveryScalarTarget is the acceptance
// criterion stated on its own, because it is the cell an author is most
// likely to get silently: a Go bool reaching a numeric target must land
// as the NUMBER 1 or 0, never as a formatted string and never as a Go
// bool the encoder's toFloat64 would have to rescue.
func TestRuleCoercion_BoolIsOneOrZeroOnEveryScalarTarget(t *testing.T) {
	specs := representativeFieldSpecs()
	for _, typeName := range declarableTypeNames(t) {
		ft, _ := fieldTypeFromName(typeName)
		if ft.IsCategorical() || ft.IsSet() {
			continue
		}
		for _, tc := range []struct {
			in   bool
			want float64
		}{{true, 1}, {false, 0}} {
			t.Run(typeName, func(t *testing.T) {
				got, err := ruleExprRowValue("x", specs[typeName], ft, tc.in)
				if err != nil {
					t.Fatalf("target %s <- %v: %v", typeName, tc.in, err)
				}
				f, isFloat := got.(float64)
				if !isFloat {
					t.Fatalf("target %s <- %v coerced to %T, want float64", typeName, tc.in, got)
				}
				if f != tc.want {
					t.Fatalf("target %s <- %v = %v, want %v", typeName, tc.in, f, tc.want)
				}
			})
		}
	}
}

// TestRuleCoercion_NumberObeysTheSameRangeRulesAsALiteral asserts the
// value half of the matrix: a computed number is held to exactly the
// range a `set` literal is held to, because the reason for the refusal
// is the same in both cases — writeFieldValueForField CLAMPS an
// out-of-range unsigned and MASKS a u4 to its low nibble, so an
// un-refused value is silently something other than what was asked for.
//
// Both halves are driven through the SAME function the literal path
// uses (ruleValueFault), so the two cannot drift.
func TestRuleCoercion_NumberObeysTheSameRangeRulesAsALiteral(t *testing.T) {
	specs := representativeFieldSpecs()
	cases := []struct {
		typeName string
		value    float64
		ok       bool
	}{
		{"u4", 15, true},
		{"u4", 16, false},
		{"u4", -1, false},
		{"u8", 255, true},
		{"u8", 256, false},
		{"u16", 65535, true},
		{"u16", 65536, false},
		{"u32", 4294967295, true},
		{"u32", 4294967296, false},
		{"packed_bool", 1, true},
		{"packed_bool", 2, false},
		{"date", 0, true},
		{"date", -1, false},
		{"f32", -1e30, true},
		{"f64", -1e300, true},
		{"decimal128", -12.5, true},
	}
	for _, tc := range cases {
		t.Run(tc.typeName, func(t *testing.T) {
			ft, _ := fieldTypeFromName(tc.typeName)
			_, err := ruleExprRowValue("x", specs[tc.typeName], ft, tc.value)
			if (err == nil) != tc.ok {
				t.Fatalf("target %s <- %v: err = %v, want ok = %v", tc.typeName, tc.value, err, tc.ok)
			}
			// The literal path must agree, cell for cell.
			litErr := validateRuleLiteral(0, "x", tc.value, specs[tc.typeName])
			if (litErr == nil) != tc.ok {
				t.Fatalf("literal path disagrees for %s <- %v: expr ok = %v, literal err = %v",
					tc.typeName, tc.value, tc.ok, litErr)
			}
		})
	}
}

// TestRuleCoercion_NonFiniteIsRefused pins that a NaN or an infinity
// never reaches the encoder. writeFieldValueForField turns a NaN into 0
// on an unsigned target, which is a real value and indistinguishable
// from a measured 0.
func TestRuleCoercion_NonFiniteIsRefused(t *testing.T) {
	specs := representativeFieldSpecs()
	for _, typeName := range []string{"u8", "f64", "decimal128"} {
		ft, _ := fieldTypeFromName(typeName)
		for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			if _, err := ruleExprRowValue("x", specs[typeName], ft, v); err == nil {
				t.Fatalf("target %s accepted %v", typeName, v)
			}
		}
	}
}

// TestRuleCoercion_StringMustBeInTheTargetsDeclaredDomain is the
// acceptance criterion for a computed category. A weighted_categorical
// declares its domain, so a computed value outside it is a typo that
// would otherwise add a silent extra dictionary entry looking like data.
// A regex categorical's domain is a language, so it is unbounded and any
// string is accepted — the same split the `set` literal path makes.
func TestRuleCoercion_StringMustBeInTheTargetsDeclaredDomain(t *testing.T) {
	bounded := FieldSpec{Name: "region", Type: "categorical_u8",
		Distribution: DistWeightedCategorical,
		Params:       map[string]any{"values": []any{"east", "west"}}}
	unbounded := FieldSpec{Name: "freeform", Type: "categorical_u8",
		Distribution: DistRegex, Params: map[string]any{"pattern": "[a-z]{3}"}}
	ft, _ := fieldTypeFromName("categorical_u8")

	if _, err := ruleExprRowValue("region", bounded, ft, "west"); err != nil {
		t.Fatalf("declared category refused: %v", err)
	}
	_, err := ruleExprRowValue("region", bounded, ft, "north")
	if err == nil {
		t.Fatal("want a refusal for a computed category outside the declared domain")
	}
	if !strings.Contains(err.Error(), "north") || !strings.Contains(err.Error(), "region") {
		t.Fatalf("the refusal must name the field and the value, got %q", err.Error())
	}
	if _, err := ruleExprRowValue("freeform", unbounded, ft, "zzz"); err != nil {
		t.Fatalf("an unbounded categorical must accept any string: %v", err)
	}
}

// TestRuleCoercion_DecimalStringIsRefusedFromAnExpression pins the one
// asymmetry between a `set` literal and a `set_expr` result, which is a
// DECISION and not an oversight. See ruleValueFault's decimal arm.
func TestRuleCoercion_DecimalStringIsRefusedFromAnExpression(t *testing.T) {
	f := representativeFieldSpecs()["decimal128"]
	ft, _ := fieldTypeFromName("decimal128")

	// The literal path keeps the string verbatim — exact, no float round
	// trip — and must go on doing so.
	if err := validateRuleLiteral(0, "cents", "1.25", f); err != nil {
		t.Fatalf("a decimal128 `set` literal string must stay exact: %v", err)
	}
	norm, err := constantRowValue(f, "1.25")
	if err != nil {
		t.Fatalf("constantRowValue: %v", err)
	}
	if _, isStr := norm.(string); !isStr {
		t.Fatalf("a decimal128 literal string was normalised to %T; exactness is gone", norm)
	}

	// The expression path refuses it, and the refusal points at `set`.
	_, err = ruleExprRowValue("cents", f, ft, "1.25")
	if err == nil {
		t.Fatal("want a refusal for a string-returning expression on a decimal128 target")
	}
	if !strings.Contains(err.Error(), "set") {
		t.Fatalf("the refusal should point the author at `set`, got %q", err.Error())
	}
}

// TestRuleCoercion_StaticRefusalAgreesWithTheRuntimeMatrix is the gate
// on the story's two-timing requirement: a return type that CANNOT
// coerce is refused at SPEC PARSE, and one that merely might not is
// refused at row time. Those are two call sites over one matrix, so the
// only way they can disagree is if the static side grows its own table
// — which this test makes impossible by deriving the expectation from
// the runtime side.
func TestRuleCoercion_StaticRefusalAgreesWithTheRuntimeMatrix(t *testing.T) {
	specs := representativeFieldSpecs()
	for _, typeName := range declarableTypeNames(t) {
		fs := specs[typeName]
		ft, _ := fieldTypeFromName(typeName)
		for _, kind := range exprResultKinds {
			t.Run(typeName+"/"+kind.name, func(t *testing.T) {
				rt := reflect.TypeOf(kind.value)
				staticFault := ruleExprStaticFault("x", fs, ft, rt)
				_, runtimeErr := ruleExprRowValue("x", fs, ft, kind.value)
				// A TYPE fault must be caught statically; a value fault
				// must NOT be (the static side sees no value).
				if wantAdmitted(ft, kind.name) {
					if staticFault != nil {
						t.Fatalf("target %s <- %s refused at parse but admitted at run time: %v",
							typeName, kind.name, staticFault)
					}
					return
				}
				if staticFault == nil {
					t.Fatalf("target %s <- %s is refused at run time (%v) but survives parse",
						typeName, kind.name, runtimeErr)
				}
			})
		}
	}
}

// TestRuleCoercion_UnknowableReturnTypeDefersToRunTime pins the other
// half of the timing rule. expr reports interface{} for an expression
// whose branches disagree, and a parse-time refusal there would refuse
// a rule that is correct on every row it actually fires on.
func TestRuleCoercion_UnknowableReturnTypeDefersToRunTime(t *testing.T) {
	fs := representativeFieldSpecs()["u8"]
	ft, _ := fieldTypeFromName("u8")
	var iface any
	rt := reflect.TypeOf(&iface).Elem()
	if err := ruleExprStaticFault("x", fs, ft, rt); err != nil {
		t.Fatalf("an interface{} return type must defer to run time, got %v", err)
	}
	if err := ruleExprStaticFault("x", fs, ft, nil); err != nil {
		t.Fatalf("an untyped return must defer to run time, got %v", err)
	}
}

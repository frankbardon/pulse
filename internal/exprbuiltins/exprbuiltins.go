// Package exprbuiltins is the one list of the reserved built-in
// expression functions Pulse registers in the shared expr-lang
// environment (ATTR_FORMULA, FILTER_EXPRESSION, total() arguments and
// OVERLAY_FORMULA).
//
// Three sites read it and must never drift apart:
//
//   - internal/processing registers one expr.Function per name (and its
//     tests pin that every name has an implementation);
//   - internal/processing's filter-precompute purity check treats every
//     name as a pure function, so a filter calling one keeps the
//     precompute fast path;
//   - the root package's embedder validation refuses an ExprFunction
//     claiming any name with PULSE_EXTENSION_NAME_RESERVED.
//
// It is a stdlib-free leaf, so the root package and internal/processing
// both import it without a cycle and without the root reaching the
// engine.
//
// Adding a function: append its name to [reserved] (keep the group
// comments), implement it in internal/processing's built-in table, and
// document it in the expression-language skill. The earlier set helpers
// (has_any, set_union, ...) and coef / lookup are deliberately absent:
// they predate the reservation and keep their shadowing semantics.
package exprbuiltins

// reserved lists every reserved built-in name, grouped by family.
var reserved = []string{
	// Vector reductions (NaN-skipping).
	"vsum", "vmean", "vmin", "vmax", "vsd", "vcount",
	// Vector geometry.
	"dot", "norm", "dist", "cosine",
	// Vector position.
	"argmax", "argmin",
	// Vector-returning transforms (usable inside an expression only).
	"vcenter", "vzscore", "vnormalize",
	// Scalar maths.
	"sqrt", "log", "exp", "pow",
}

// reservedSet indexes reserved for IsReserved.
var reservedSet = func() map[string]bool {
	m := make(map[string]bool, len(reserved))
	for _, n := range reserved {
		m[n] = true
	}
	return m
}()

// Names returns a copy of every reserved built-in name, in declaration
// order.
func Names() []string {
	out := make([]string, len(reserved))
	copy(out, reserved)
	return out
}

// IsReserved reports whether name is a reserved built-in expression
// function.
func IsReserved(name string) bool { return reservedSet[name] }

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

// overlayAllowed is the scalar-maths subset of [reserved] OVERLAY_FORMULA
// may call. Overlay formulas see only scalar host figures (cell,
// margins, value, ...), so the vector reductions, geometry, position
// and transform families are refused there; both the predict gate
// (internal/descriptor) and the runtime compile (internal/processing)
// read this one list so they never disagree.
var overlayAllowed = []string{"sqrt", "log", "exp", "pow"}

// overlayAllowedSet indexes overlayAllowed for OverlayAllowed.
var overlayAllowedSet = func() map[string]bool {
	m := make(map[string]bool, len(overlayAllowed))
	for _, n := range overlayAllowed {
		m[n] = true
	}
	return m
}()

// OverlayNames returns a copy of the reserved built-ins OVERLAY_FORMULA
// may call, in declaration order.
func OverlayNames() []string {
	out := make([]string, len(overlayAllowed))
	copy(out, overlayAllowed)
	return out
}

// OverlayAllowed reports whether OVERLAY_FORMULA may call the reserved
// built-in name. It is false for every non-reserved name.
func OverlayAllowed(name string) bool { return overlayAllowedSet[name] }

// OverlayRefused reports whether name is a reserved built-in that
// OVERLAY_FORMULA must refuse (reserved, but outside the overlay subset).
func OverlayRefused(name string) bool { return reservedSet[name] && !overlayAllowedSet[name] }

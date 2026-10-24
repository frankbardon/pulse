// Package exprenv is the one shared expr-lang formula environment Pulse
// compiles ATTR_FORMULA, FILTER_EXPRESSION, total() arguments and
// OVERLAY_FORMULA against. It owns:
//
//   - the reserved built-in names (this file) and the unreserved set
//     helpers, with their implementations (vector.go, setsim.go, set.go)
//     and ref() (ref.go);
//   - the `%` patcher (mod.go);
//   - the Hill saturation curve (hill.go);
//   - trigonometry and the constants pi() / e() (trig.go); every
//     transcendental (exp, log, log1p, pow, hill's power, the trig set)
//     runs on internal/detmath's portable kernels via internal/vecmath;
//   - the calendar helpers and log1p (calendar.go): weekday,
//     month_index and date_trunc over epoch days through
//     internal/temporal, to_day in the request zone ([Bound.Zone]),
//     and the compile-time datetime guard ([CalendarGuard]);
//   - the exact decimal operators (decimal.go): a decimal128 field binds
//     as a [Decimal] (mantissa + scale, [Bind]), a patcher rewrites
//     `+ - * /`, comparisons, unary `-`, `??` / ternary branches,
//     `float()` and the numeric built-ins (decimal_builtins.go) over one
//     into exact builtins, and the null-row twin declares it through
//     [Untyped];
//   - the single always-on option hook, [Options], every expression
//     site prepends to its own options;
//   - embedder ExprFunction bridging ([ExtensionFunctions], [Func]);
//   - the schema-derived prototype env ([Prototype], [ValueType]): every
//     schema field typed exactly as a decoded record surfaces it, so a
//     caller can expr.Compile without a record or the engine.
//   - the request-bound declarations ([Bound]: coef's typed overloads,
//     the embedder functions, lookup) and the derived-column and
//     named-vector types ([DerivedType], [VectorType]), so predict and
//     the engine type-check every expression against the same env;
//   - the compile failure, PULSE_FORMULA_COMPILE_ERROR ([CompileError],
//     the site nouns [SiteFormula], [SiteFilter], [SiteTotalArgument]).
//
// Several sites read the name lists and must never drift apart:
//
//   - [Options] registers one expr.Function per name (and its tests pin
//     that every name has an implementation);
//   - internal/processing's filter-precompute purity check treats every
//     name as a pure function, so a filter calling one keeps the
//     precompute fast path;
//   - the root package's embedder validation refuses an ExprFunction
//     claiming any name with PULSE_EXTENSION_NAME_RESERVED;
//   - internal/vectors, internal/formulaident and internal/descriptor
//     (predict) judge calls statically against them.
//
// It is a no-execute leaf: it never imports internal/processing,
// internal/service, internal/descriptor or internal/vectors (which
// imports it), so predict (internal/descriptor) and the engine both
// import it (TestExprenvImportBoundary). The record-binding runtime —
// compile-once programs, the null-row twin, named-vector binding, the
// ref() plan patcher and the bodies of coef() and lookup() — stays in
// internal/processing; total()'s declaration is internal/totalcall's
// (Options).
//
// Adding a function: append its name to [reserved] (keep the group
// comments), implement it in [builtinImpls], and document it in the
// expression-language skill. The earlier set helpers (has_any,
// set_union, ...) and coef / lookup are deliberately absent from
// [reserved]: they predate the reservation and keep their shadowing
// semantics.
package exprenv

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
	"sqrt", "log", "exp", "pow", "log1p",
	// Media saturation (hill.go): hill(x, half_saturation, shape).
	"hill",
	// Trigonometry in radians and the zero-argument constants pi(), e()
	// (trig.go).
	"sin", "cos", "tan", "atan2", "pi", "e",
	// Calendar over epoch days (calendar.go); to_day converts a datetime.
	"weekday", "month_index", "date_trunc", "to_day",
	// Set similarity over two label sets.
	"jaccard", "dice", "hamming", "overlap",
	// Reference vectors (Options.Extensions.ReferenceVectors).
	"ref",
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
//
// The trigonometry set and the constants (sin, cos, tan, atan2, pi, e)
// are deliberately NOT in it (U58): an overlay formula post-processes
// counts, shares and statistics of host cells, where an angle has no
// meaning, and widening the subset later is additive while narrowing
// it would break a request. TestOverlaySubset pins the refusal.
var overlayAllowed = []string{"sqrt", "log", "exp", "pow", "log1p"}

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

// setHelpers are the multi-select set helpers the shared expression
// environment registers beside the reserved built-ins. They predate the
// reservation and stay UNRESERVED (an embedder ExprFunction of the same
// name shadows one), so they are not in [reserved]; every expression
// site — ATTR_FORMULA, FILTER_EXPRESSION, total() arguments and
// OVERLAY_FORMULA — may call them. The overlay predict gate and the
// filter-precompute purity table read this list; internal/processing
// pins that it registers every name.
var setHelpers = []string{
	"has_any", "has_all", "has_none", "popcount",
	"set_union", "set_intersect", "set_diff", "set_xor",
}

// SetHelperNames returns a copy of the unreserved set helper names, in
// declaration order.
func SetHelperNames() []string {
	out := make([]string, len(setHelpers))
	copy(out, setHelpers)
	return out
}

// OverlayRefused reports whether name is a reserved built-in that
// OVERLAY_FORMULA must refuse (reserved, but outside the overlay subset).
func OverlayRefused(name string) bool { return reservedSet[name] && !overlayAllowedSet[name] }

// MetricFunction is the linalg/metric registry metric a reserved
// built-in evaluates and the form it answers in ("distance" or
// "similarity", the metric.Form spellings, kept as strings).
type MetricFunction struct {
	Metric string
	Form   string
}

// metricFunctions maps every reserved built-in that evaluates a
// registry metric to it. internal/processing registers each through
// this table (so the function and the metric cannot drift) and the
// vector-kind fit check (internal/vectors) reads it to judge
// `cosine(v, w)` on a kind-declared vector.
var metricFunctions = map[string]MetricFunction{
	"dist":    {Metric: "euclidean", Form: "distance"},
	"cosine":  {Metric: "cosine", Form: "similarity"},
	"jaccard": {Metric: "jaccard", Form: "similarity"},
	"dice":    {Metric: "dice", Form: "similarity"},
	"hamming": {Metric: "hamming", Form: "distance"},
	"overlap": {Metric: "overlap", Form: "similarity"},
}

// Metric returns the registry metric the reserved built-in name
// evaluates; false for a name that evaluates none.
func Metric(name string) (MetricFunction, bool) {
	m, ok := metricFunctions[name]
	return m, ok
}

// MetricFunctionNames returns, in declaration order of [reserved], every
// reserved built-in that evaluates a registry metric.
func MetricFunctionNames() []string {
	var out []string
	for _, n := range reserved {
		if _, ok := metricFunctions[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// vectorPairs are the reserved built-ins that read two numeric vectors
// position by position. A ref() argument of one (directly, or under the
// label-preserving transforms) is aligned BY LABEL to the other
// argument before the call: internal/vectors judges the alignment
// (predict and the runtime build) and internal/processing patches it
// in, both from this one list.
var vectorPairs = map[string]bool{"dot": true, "dist": true, "cosine": true}

// IsVectorPair reports whether name is a reserved built-in that reads
// two numeric vectors position by position (dot, dist, cosine).
func IsVectorPair(name string) bool { return vectorPairs[name] }

// labelPreserving are the reserved vector-returning built-ins whose
// first argument's labels carry through to their result unchanged
// (element i of the result is element i of the argument, transformed).
var labelPreserving = map[string]bool{"vcenter": true, "vzscore": true, "vnormalize": true}

// PreservesLabels reports whether the reserved built-in name returns a
// vector labelled exactly like its first argument (vcenter, vzscore,
// vnormalize).
func PreservesLabels(name string) bool { return labelPreserving[name] }

// RefFunc is the reserved built-in that reads a registered reference
// vector: ref("table", key).
const RefFunc = "ref"

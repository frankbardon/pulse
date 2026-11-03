// Package exprslots is the one list of request string slots that carry
// an expr-lang expression, shared by the engine and the request-template
// renderer.
//
// The engine names its compile sites through the Site* constants here
// (internal/exprenv re-exports them, so CompileError messages keep the
// same nouns) and reads the OVERLAY_FORMULA source through
// OverlayFormulaParam. The template package (internal/template) reads
// All / Match to refuse a `{{token}}` spliced into one of these slots
// (PULSE_TEMPLATE_EXPR_SPLICE): request params carry values into an
// expression, a template never splices text into it.
//
// Leaf: stdlib only, so the template package's import ceiling
// (TestTemplatePackage_ImportBoundary) admits it. The coverage gates —
// every exprenv compile site is one of these (TestTemplateExprSplice_
// CompileSitesShared, internal/exprenv), every engine expression path
// matches (TestTemplateExprSplice_EngineExprSlotsMatch,
// internal/vectors), every owner position in the request roots matches
// (TestTemplateExprSplice_SlotsCoverRequestRoots, here) — keep it from
// becoming a second hand-kept list.
package exprslots

// The compile sites: the what of an expression compile error. The
// runtime build and predict both name a site through these (via
// internal/exprenv), so their messages agree to the byte.
const (
	SiteFormula       = "formula"                  // an ATTR_FORMULA expression
	SiteFilter        = "filter"                   // a FILTER_EXPRESSION expression
	SiteTotalArgument = "formula total() argument" // one total(expr) argument
	SiteHoldout       = "regression holdout"       // a RegressionSpec.Holdout predicate
)

// OverlayFormulaParam is the OVERLAY_FORMULA params key carrying the
// formula source, on a Request overlay and a Compose overlay alike.
const OverlayFormulaParam = "formula"

// Slot is one expression-bearing request string slot.
type Slot struct {
	// Name identifies the slot in a refusal's details.
	Name string
	// Owners are the types structs the slot lives on.
	Owners []string
	// Tail is the JSON key path the expression string sits at, array
	// indices dropped, matched as a suffix of the string's key path from
	// any request root: ["expression"] matches every filterer and
	// attribute expression wherever it nests (a Compose request, a chain
	// stage, a sweep request, a params-borne fit_on filterer list).
	Tail []string
	// Sites are the compile sites the engine reads the slot under; nil
	// for OVERLAY_FORMULA, which compiles through the overlay subset, and
	// for the optimize slots, which internal/optblock compiles (its own
	// PULSE_OPTIMIZE_EXPRESSION_INVALID, no exprenv compile site).
	Sites []string
	// Scalar marks a scalar-expression slot (internal/scalarslot): a
	// JSON number or an expression string, its owner field a
	// number-or-expression type (types.OptimizeBound) or a params
	// object key (a window param, an AGG_CONSTANT expr) rather than a
	// Go string. A `{{token}}` in its string form is refused like any
	// expression's; a number there is no expression.
	Scalar bool
}

// The slot names.
const (
	SlotExpression         = "expression"
	SlotHoldout            = "holdout"
	SlotOverlayFormula     = "overlay_formula"
	SlotFeatureFitOn       = "feature_fit_on"
	SlotOptimizeObjective  = "optimize_objective"
	SlotOptimizeConstraint = "optimize_constraint"
	SlotOptimizeBound      = "optimize_bound"
	SlotWindowParam        = "window_param"
	SlotConstantExpr       = "constant_expr"
	SlotInlineModelNumber  = "inline_model_number"
	// SlotExtensionParam names an embedder extension's
	// expression-capable params key (pulse.ParamMeta.Expression). It has
	// no row in the static table: which keys are flagged is an instance
	// fact, handed to the template validator by the facade
	// (internal/template.ValidateOptions.ExprParams) and read by the
	// request reads walker from the extensions snapshot.
	SlotExtensionParam = "extension_param"
)

var slots = []Slot{
	{
		Name:   SlotExpression,
		Owners: []string{"Filterer", "Attribute"},
		Tail:   []string{"expression"},
		Sites:  []string{SiteFormula, SiteFilter, SiteTotalArgument},
	},
	{
		Name:   SlotHoldout,
		Owners: []string{"RegressionSpec"},
		Tail:   []string{"holdout"},
		Sites:  []string{SiteHoldout},
	},
	{
		Name:   SlotOverlayFormula,
		Owners: []string{"OverlaySpec", "ComposeOverlaySpec"},
		Tail:   []string{"overlays", "params", OverlayFormulaParam},
	},
	{
		// A feature's params.fit_on row expression (FEAT_TARGET_ENCODE),
		// compiled as an expression filter. The tail is rooted at the
		// `features` list so an attribute's params.fit_on — the
		// "filtered" keyword or a filterer list — never matches.
		Name:   SlotFeatureFitOn,
		Owners: []string{"Feature"},
		Tail:   []string{"features", ParamsKey, FeatureFitOnParam},
		Sites:  []string{SiteFilter},
	},
	{
		// A Compose optimize block's objective, compiled against the
		// block's decision variables (internal/optblock).
		Name:   SlotOptimizeObjective,
		Owners: []string{"OptimizeSpec"},
		Tail:   []string{"optimize", "objective"},
	},
	{
		// One optimize constraint's expression.
		Name:   SlotOptimizeConstraint,
		Owners: []string{"OptimizeConstraint"},
		Tail:   []string{"optimize", "constraints", "expr"},
	},
	// An optimize decision variable's lower / upper / start: a number
	// or a scalar expression (internal/optblock through
	// internal/scalarslot).
	{Name: SlotOptimizeBound, Owners: []string{"OptimizeVariable"}, Tail: []string{"optimize", "variables", "lower"}, Scalar: true},
	{Name: SlotOptimizeBound, Owners: []string{"OptimizeVariable"}, Tail: []string{"optimize", "variables", "upper"}, Scalar: true},
	{Name: SlotOptimizeBound, Owners: []string{"OptimizeVariable"}, Tail: []string{"optimize", "variables", "start"}, Scalar: true},
	// A built-in window's scalar params (internal/windowscalar):
	// WIN_EWMA alpha / decay, WIN_DISTRIBUTED_LAG decay / shape /
	// scale / max_lag — a number or a scalar expression, resolved before
	// the window's own reading. Rooted at the `windows` list.
	{Name: SlotWindowParam, Owners: []string{"Window"}, Tail: []string{"windows", ParamsKey, "alpha"}, Scalar: true},
	{Name: SlotWindowParam, Owners: []string{"Window"}, Tail: []string{"windows", ParamsKey, "decay"}, Scalar: true},
	{Name: SlotWindowParam, Owners: []string{"Window"}, Tail: []string{"windows", ParamsKey, "shape"}, Scalar: true},
	{Name: SlotWindowParam, Owners: []string{"Window"}, Tail: []string{"windows", ParamsKey, "scale"}, Scalar: true},
	{Name: SlotWindowParam, Owners: []string{"Window"}, Tail: []string{"windows", ParamsKey, "max_lag"}, Scalar: true},
	// An AGG_CONSTANT slot's params.expr (internal/aggconst): a scalar
	// expression resolved into the slot's literal value before any
	// operator is built. Rooted at every aggregation position: the
	// aggregation lists, and a crosstab's or banner section's cell /
	// cells.
	{Name: SlotConstantExpr, Owners: []string{"Aggregation"}, Tail: []string{"aggregations", ParamsKey, "expr"}, Scalar: true},
	{Name: SlotConstantExpr, Owners: []string{"Aggregation"}, Tail: []string{"cell", ParamsKey, "expr"}, Scalar: true},
	{Name: SlotConstantExpr, Owners: []string{"Aggregation"}, Tail: []string{"cells", ParamsKey, "expr"}, Scalar: true},
	{Name: SlotConstantExpr, Owners: []string{"Aggregation"}, Tail: []string{"crosstab", "margin_aggregations", ParamsKey, "expr"}, Scalar: true},
	// An inline model's numbers (a `models` entry's `inline` body,
	// internal/model's InlineNumberPaths): each a number or a scalar
	// expression over params, resolved before the body is judged as a
	// model file. Rooted at the `models` list; Wildcard keys (a
	// coefficient's term, an adstock record's channel) match any key.
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "ci_lower", Wildcard}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "ci_upper", Wildcard}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "coefficients", Wildcard}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "std_errors", Wildcard}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "conf_level"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "df"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "dispersion"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "n_eff"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "n_obs"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "sum_weights"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "theta"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "vcov", "values"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "adstock", Wildcard, "decay"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "adstock", Wildcard, "max_lag"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "adstock", Wildcard, "scale"}, Scalar: true},
	{Name: SlotInlineModelNumber, Owners: []string{"ModelRef"}, Tail: []string{"models", "inline", "adstock", Wildcard, "shape"}, Scalar: true},
}

// Wildcard in a Tail matches any one key (an inline model's
// coefficient term).
const Wildcard = "*"

// FeatureFitOnParam is the feature params key carrying a row
// expression (FEAT_TARGET_ENCODE params.fit_on).
const FeatureFitOnParam = "fit_on"

// All returns every expression-bearing slot, in a fixed order. The
// result is a copy.
func All() []Slot {
	out := make([]Slot, len(slots))
	for i, s := range slots {
		s.Owners = append([]string(nil), s.Owners...)
		s.Tail = append([]string(nil), s.Tail...)
		s.Sites = append([]string(nil), s.Sites...)
		out[i] = s
	}
	return out
}

// Sites returns every compile site, in declaration order.
func Sites() []string {
	return []string{SiteFormula, SiteFilter, SiteTotalArgument, SiteHoldout}
}

// ParamsKey is the key of a params object: the request's own
// constants (Request.Params) or an operator's configuration. A tail
// directly under one is a params entry that happens to share the slot's
// key — `"params": {"expression": "…"}` is data, not an expression
// slot — and never matches.
const ParamsKey = "params"

// Match reports the slot a string at JSON key path keys (array indices
// dropped, root first) sits in, if any.
func Match(keys []string) (Slot, bool) {
	for _, s := range slots {
		if matches(keys, s.Tail) {
			return s, true
		}
	}
	return Slot{}, false
}

func matches(keys, tail []string) bool {
	if len(keys) < len(tail) {
		return false
	}
	off := len(keys) - len(tail)
	for i, k := range tail {
		if k != Wildcard && keys[off+i] != k {
			return false
		}
	}
	return off == 0 || keys[off-1] != ParamsKey
}

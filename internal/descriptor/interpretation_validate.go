package descriptor

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// InterpretationRule names one validity rule ValidateInterpretations
// enforces (binding for built-ins via TestInterpretationCoversOutputs,
// and for extension registrations through the same function).
type InterpretationRule string

// The validity rules.
const (
	// InterpretationRuleField: Field is a well-formed normalised path
	// (dot-separated lowercase snake_case segments, "*" only as the last
	// segment) and is declared once per operator.
	InterpretationRuleField InterpretationRule = "field"
	// InterpretationRuleFieldUnknown: Field names an output the operator
	// emits (struct tag, ComponentSchema key, declared overlay shape or
	// per-regression-type applicability).
	InterpretationRuleFieldUnknown InterpretationRule = "field_unknown"
	// InterpretationRuleMeans: an Interpretation says what the value
	// means — a non-empty Means, or a Shared rule set.
	InterpretationRuleMeans InterpretationRule = "means"
	// InterpretationRuleConvention: Bands and Convention come together —
	// bands are a labelled convention, never authoritative.
	InterpretationRuleConvention InterpretationRule = "convention"
	// InterpretationRuleBands: every band has a label and Min < Max; the
	// bands are ordered ascending and do not overlap (only the first may
	// be open below, only the last open above).
	InterpretationRuleBands InterpretationRule = "bands"
	// InterpretationRuleShared: Shared names a registered shared rule set.
	InterpretationRuleShared InterpretationRule = "shared"
	// InterpretationRuleSign: Sign keys are "+" / "-" with non-empty
	// meanings.
	InterpretationRuleSign InterpretationRule = "sign"
)

// InterpretationViolation is one broken validity rule on one
// Interpretation.
type InterpretationViolation struct {
	// Name is the operator (or registry key) the Interpretation belongs to.
	Name string
	// Field is the Interpretation's Field as declared.
	Field string
	// Rule is the broken rule.
	Rule InterpretationRule
	// Detail says what is wrong, in a sentence.
	Detail string
}

func (v InterpretationViolation) String() string {
	return fmt.Sprintf("%s: interpretation %q %s: %s", v.Name, v.Field, v.Rule, v.Detail)
}

// FieldCheck is the static verdict on one Interpretation.Field.
type FieldCheck int

// The static verdicts.
const (
	// FieldUnknown: the path names nothing the operator emits.
	FieldUnknown FieldCheck = iota
	// FieldStatic: the path is anchored to a declared output — a result
	// struct tag, a ComponentSchema key, a declared overlay shape, a
	// descriptive operator's value / value.* primary result.
	FieldStatic
	// FieldDeferred: the path descends into a map-valued slot
	// (details.*, summary.parameters.*) whose keys exist only at run
	// time. It passes the static check; the runtime probe in
	// internal/processing / internal/service proves the key is emitted.
	FieldDeferred
)

// OutputResolver judges a normalised Field path for one operator,
// returning the verdict and, for FieldUnknown, why.
type OutputResolver func(field string) (FieldCheck, string)

// ValidateInterpretations checks ins, declared for name, against every
// validity rule and returns the violations (nil when valid). outputs
// judges each Field; a nil resolver is STRUCTURE-ONLY mode — path
// syntax is checked but existence is not (the extension registration
// path, where no emitted-key declaration exists).
func ValidateInterpretations(name string, ins []descriptor.Interpretation, outputs OutputResolver) []InterpretationViolation {
	var out []InterpretationViolation
	seen := map[string]bool{}
	for _, in := range ins {
		bad := func(rule InterpretationRule, format string, args ...any) {
			out = append(out, InterpretationViolation{Name: name, Field: in.Field, Rule: rule, Detail: fmt.Sprintf(format, args...)})
		}

		// Field.
		switch err := interpretationPathSyntax(in.Field); {
		case err != "":
			bad(InterpretationRuleField, "%s", err)
		case seen[in.Field]:
			bad(InterpretationRuleField, "Field declared twice")
		case outputs != nil:
			if check, why := outputs(in.Field); check == FieldUnknown {
				bad(InterpretationRuleFieldUnknown, "%s", why)
			}
		}
		seen[in.Field] = true

		// Content.
		if strings.TrimSpace(in.Means) == "" && in.Shared == "" {
			bad(InterpretationRuleMeans, "declares neither Means nor a Shared rule set")
		}
		if in.Shared != "" {
			if _, ok := sharedInterpretations[in.Shared]; !ok {
				bad(InterpretationRuleShared, "Shared %q is not a shared rule set (known: %s)", in.Shared, strings.Join(SharedInterpretationKeys(), ", "))
			}
		}
		for _, k := range sortedSignKeys(in.Sign) {
			if k != "+" && k != "-" {
				bad(InterpretationRuleSign, "Sign key %q is not \"+\" or \"-\"", k)
			}
			if strings.TrimSpace(in.Sign[k]) == "" {
				bad(InterpretationRuleSign, "Sign[%q] is empty", k)
			}
		}

		// Bands.
		hasConv := strings.TrimSpace(in.Convention) != ""
		switch {
		case len(in.Bands) > 0 && !hasConv:
			bad(InterpretationRuleConvention, "declares Bands without a Convention attributing them")
		case len(in.Bands) == 0 && hasConv:
			bad(InterpretationRuleConvention, "declares a Convention but no Bands")
		}
		for _, d := range bandProblems(in.Bands) {
			bad(InterpretationRuleBands, "%s", d)
		}
	}
	return out
}

// interpretationPathSyntax returns "" when field is a well-formed
// normalised path, else why it is not.
func interpretationPathSyntax(field string) string {
	if field == "" {
		return "Field is empty"
	}
	segs := strings.Split(field, ".")
	for i, s := range segs {
		if s == "*" {
			if i == 0 || i != len(segs)-1 {
				return "\"*\" may only be the last segment of a nested path"
			}
			continue
		}
		if !isSnakeSegment(s) {
			return fmt.Sprintf("segment %q is not lowercase snake_case", s)
		}
	}
	return ""
}

func isSnakeSegment(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

func sortedSignKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// bandProblems returns one sentence per band defect: missing label,
// Min >= Max, an open end anywhere but the outside, or a band that does
// not start at or after the previous band's end.
func bandProblems(bands []descriptor.Band) []string {
	var out []string
	for i, b := range bands {
		if strings.TrimSpace(b.Label) == "" {
			out = append(out, fmt.Sprintf("Bands[%d] has no label", i))
		}
		if b.Min != nil && b.Max != nil && *b.Min >= *b.Max {
			out = append(out, fmt.Sprintf("Bands[%d] has Min %g >= Max %g", i, *b.Min, *b.Max))
		}
		if b.Min == nil && i > 0 {
			out = append(out, fmt.Sprintf("Bands[%d] is open below but is not the first band, so it overlaps Bands[%d]", i, i-1))
		}
		if b.Max == nil && i < len(bands)-1 {
			out = append(out, fmt.Sprintf("Bands[%d] is open above but is not the last band, so it overlaps Bands[%d]", i, i+1))
		}
		if i > 0 {
			prev := bands[i-1]
			if prev.Max != nil && b.Min != nil && *b.Min < *prev.Max {
				out = append(out, fmt.Sprintf("Bands[%d] starts at %g, before Bands[%d] ends at %g (out of order or overlapping)", i, *b.Min, i-1, *prev.Max))
			}
		}
	}
	return out
}

// jsonTags returns the JSON names of t's exported fields, mapped to the
// field's reflect.Type.
func jsonTags(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if !f.IsExported() || tag == "" || tag == "-" {
			continue
		}
		out[tag] = f.Type
	}
	return out
}

// isScalarOutput reports whether t is a number or bool (through one
// pointer) — a value an Interpretation can read directly.
func isScalarOutput(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// regressionOutputs is the per-regression-type applicability table: the
// RegressionResult JSON keys each REG_* type populates on its plain fit
// path. TestRegressionOutputs_AreResultTags pins every key to a real
// struct tag.
var regressionOutputs = map[string][]string{
	string(types.REG_OLS):          {"adj_r2", "ci_lower", "ci_upper", "coefficients", "conf_level", "converged_iters", "df", "n_obs", "p_values", "r2", "residual_std_err", "std_errors"},
	string(types.REG_GLM):          {"ci_lower", "ci_upper", "coefficients", "conf_level", "converged_iters", "deviance", "df", "n_obs", "null_deviance", "p_values", "pseudo_r2", "std_errors"},
	string(types.REG_BAYES_LINEAR): {"adj_r2", "ci_lower", "ci_upper", "coefficients", "conf_level", "credible_intervals", "df", "n_obs", "r2", "residual_std_err", "std_errors"},
	string(types.REG_MMM_BAYES):    {"ci_lower", "ci_upper", "coefficients", "conf_level", "n_obs", "outputs", "p_values", "statistics", "std_errors"},
	string(types.REG_MMM_HILL):     {"ci_lower", "ci_upper", "coefficients", "conf_level", "converged_iters", "deviance", "df", "n_obs", "outputs", "p_values", "r2", "residual_std_err", "statistics", "std_errors"},
}

// RegressionOutputKeys returns a copy of the applicability row for
// regression type name: the RegressionResult JSON keys it populates on
// its plain fit path (nil for an unknown type). The runtime probe in
// internal/processing/regression holds it to what each engine emits.
func RegressionOutputKeys(name string) []string {
	return append([]string(nil), regressionOutputs[name]...)
}

// componentFloors are the universal-floor component keys the
// orchestrator fills for every operator of a category (not listed in a
// ComponentSchema).
var componentFloors = map[string][]string{
	"aggregator": {"n", "n_null"},
	"grouper":    {"total_n", "n_null"},
	"filterer":   {"n_in", "n_out", "n_null_input"},
}

// surfaceCategory returns the PurposeSurfaces category name belongs to.
func surfaceCategory(name string) (string, bool) {
	for _, s := range PurposeSurfaces() {
		i := sort.SearchStrings(s.Names, name)
		if i < len(s.Names) && s.Names[i] == name {
			return s.Category, true
		}
	}
	return "", false
}

// BuiltinOutputResolver returns the static OutputResolver for the
// built-in manifest entry name (tests by family). Tests resolve against
// types.TestResult; regressions against types.RegressionResult filtered
// by the per-type applicability table; overlays against the kind's
// declared shapes plus types.OverlaySummary; aggregators, groupers and
// filterers against components.<key> from their ComponentSchema plus
// the universal floor; aggregators, attributes, windows and features
// also against their primary result as `value` / `value.*`
// (interpretation_reading.go); matrices against their MatrixResult as
// primary.values and scalars.<key> from the manifest output_keys. Every
// other category (and an unknown name) emits nothing an Interpretation
// can read.
func BuiltinOutputResolver(name string) OutputResolver {
	cat, ok := surfaceCategory(name)
	if !ok {
		return func(string) (FieldCheck, string) {
			return FieldUnknown, fmt.Sprintf("%s is not a registered built-in", name)
		}
	}
	switch cat {
	case "test":
		return testOutputResolver
	case "regression":
		return regressionOutputResolver(name)
	case "overlay":
		return overlayOutputResolver(types.OverlayKind(name))
	case "aggregator", "grouper", "filterer":
		return componentOutputResolver(name, cat)
	case "attribute", "window", "feature":
		return valueOutputResolver(name, cat)
	case "matrix":
		return matrixOutputResolver(name)
	}
	return func(string) (FieldCheck, string) {
		return FieldUnknown, fmt.Sprintf("%s operators emit no output an Interpretation can read", cat)
	}
}

// matrixOutputResolver reads a MAT_* operator's MatrixResult: every
// cell of the primary matrix as primary.values, each declared
// whole-matrix figure as scalars.<key> (MatrixOutputKeys.Scalars) and
// each declared auxiliary matrix as auxiliary.<key>
// (MatrixOutputKeys.Auxiliary — its cells, e.g. params.inference's p).
func matrixOutputResolver(name string) OutputResolver {
	scalars, aux := map[string]bool{}, map[string]bool{}
	for _, m := range matrixCapabilities() {
		if m.Name == name {
			for _, k := range m.OutputKeys.Scalars {
				scalars[k] = true
			}
			for _, k := range m.OutputKeys.Auxiliary {
				aux[k] = true
			}
		}
	}
	return func(field string) (FieldCheck, string) {
		if field == "primary.values" {
			return FieldStatic, ""
		}
		if k, ok := strings.CutPrefix(field, "scalars."); ok && scalars[k] {
			return FieldStatic, ""
		}
		if k, ok := strings.CutPrefix(field, "auxiliary."); ok && aux[k] {
			return FieldStatic, ""
		}
		// A per-level key (MAT_SEM's implied_means.<level>) is read as
		// auxiliary.<key>.*: every level's matrix.
		if k, ok := strings.CutPrefix(field, "auxiliary."); ok {
			if pre, ok := strings.CutSuffix(k, ".*"); ok && aux[pre+"."+vectors.SEMOutputKeyLevel] {
				return FieldStatic, ""
			}
		}
		return FieldUnknown, fmt.Sprintf("%s does not emit %q (want primary.values, a declared scalars.<key>, auxiliary.<key> or a per-level auxiliary.<key>.*)", name, field)
	}
}

func testOutputResolver(field string) (FieldCheck, string) {
	head, rest, nested := strings.Cut(field, ".")
	if head == "details" && nested && rest != "" {
		return FieldDeferred, ""
	}
	t, ok := jsonTags(reflect.TypeOf(types.TestResult{}))[head]
	if !ok || nested || !isScalarOutput(t) {
		return FieldUnknown, fmt.Sprintf("test results carry no %q (want a numeric TestResult key such as statistic / p_value / df, or details.<key>)", field)
	}
	return FieldStatic, ""
}

// extensionRegressionOutputs are the RegressionResult JSON keys an
// extension REG_* model can fill (extend.RegressionOutput: the
// coefficient table and the fixed fit statistics) plus the engine's
// n_obs — what an extension registration's Interpretation may name
// beside outputs.<key>.
var extensionRegressionOutputs = []string{
	"adj_r2", "coefficients", "converged_iters", "deviance", "df", "n_obs", "null_deviance",
	"p_values", "pseudo_r2", "r2", "residual_std_err", "statistics", "std_errors",
}

// ExtensionRegressionOutputResolver is the OutputResolver of an
// extension REG_* model named name whose OutputSchema declares
// outputKeys: a figure the model can report
// (extensionRegressionOutputs, a per-coefficient map read as
// "<key>.*") or outputs.<key> for a declared key. Every other path is
// FieldUnknown.
func ExtensionRegressionOutputResolver(name string, outputKeys []string) OutputResolver {
	figures := regressionResultResolver(name, extensionRegressionOutputs)
	return func(field string) (FieldCheck, string) {
		if k, ok := strings.CutPrefix(field, "outputs."); ok {
			if slices.Contains(outputKeys, k) {
				return FieldStatic, ""
			}
			return FieldUnknown, fmt.Sprintf("%s declares no output key %q (OutputSchema: %s)", name, k, strings.Join(outputKeys, ", "))
		}
		return figures(field)
	}
}

func regressionOutputResolver(name string) OutputResolver {
	return regressionResultResolver(name, regressionOutputs[name])
}

// regressionResultResolver resolves a path against the RegressionResult
// keys listed in keys: a scalar figure by its key, a per-coefficient map
// by "<key>.*".
func regressionResultResolver(name string, keys []string) OutputResolver {
	tags := jsonTags(reflect.TypeOf(types.RegressionResult{}))
	applicable := map[string]bool{}
	for _, k := range keys {
		applicable[k] = true
	}
	return func(field string) (FieldCheck, string) {
		head, rest, nested := strings.Cut(field, ".")
		t, ok := tags[head]
		if !ok || !applicable[head] {
			return FieldUnknown, fmt.Sprintf("%s does not emit %q", name, head)
		}
		if t.Kind() == reflect.Map {
			if !nested || rest != "*" {
				return FieldUnknown, fmt.Sprintf("%q is keyed by predictor; use the pattern %q", head, head+".*")
			}
			return FieldStatic, ""
		}
		if nested || !isScalarOutput(t) {
			return FieldUnknown, fmt.Sprintf("%q is not a readable scalar output", field)
		}
		return FieldStatic, ""
	}
}

func overlayOutputResolver(kind types.OverlayKind) OutputResolver {
	shapes := map[types.OverlayShape]bool{}
	for _, c := range OverlayCapabilities() {
		if c.Kind == kind {
			for _, s := range c.Shapes {
				shapes[s] = true
			}
		}
	}
	summary := jsonTags(reflect.TypeOf(types.OverlaySummary{}))
	return func(field string) (FieldCheck, string) {
		switch field {
		case "scalar":
			if shapes[types.OverlayShapeScalar] {
				return FieldStatic, ""
			}
			return FieldUnknown, fmt.Sprintf("%s declares no scalar shape", kind)
		case "cells.value":
			if shapes[types.OverlayShapeMatrix] {
				return FieldStatic, ""
			}
			return FieldUnknown, fmt.Sprintf("%s declares no matrix shape", kind)
		}
		if rest, ok := strings.CutPrefix(field, "parts."); ok {
			// A declared fixed-name payload part (overlayPartShape): its own
			// cells.value (MATRIX part), scalar (SCALAR part) or an
			// entry's scalar summary.<key> (SERIES part).
			name, sub, _ := strings.Cut(rest, ".")
			sh, ok := overlayPartShape(kind, name)
			key, seriesKey := strings.CutPrefix(sub, "summary.")
			if t, known := summary[key]; ok && seriesKey && sh == types.OverlayShapeSeries {
				if known && isScalarOutput(t) {
					return FieldStatic, ""
				}
				if strings.HasPrefix(key, "parameters.") && key != "parameters." {
					return FieldDeferred, ""
				}
			}
			if ok && ((sh == types.OverlayShapeMatrix && sub == "cells.value") || (sh == types.OverlayShapeScalar && sub == "scalar")) {
				return FieldStatic, ""
			}
			return FieldUnknown, fmt.Sprintf("%s declares no part path %q", kind, field)
		}
		if rest, ok := strings.CutPrefix(field, "summary.parameters."); ok && rest != "" {
			return FieldDeferred, ""
		}
		if key, ok := strings.CutPrefix(field, "summary."); ok {
			if t, ok := summary[key]; ok && isScalarOutput(t) {
				return FieldStatic, ""
			}
		}
		return FieldUnknown, fmt.Sprintf("overlay layers carry no %q (want scalar, cells.value, summary.<key> or summary.parameters.<key>)", field)
	}
}

func componentOutputResolver(name, cat string) OutputResolver {
	var ops []descriptor.Operator
	switch cat {
	case "aggregator":
		ops = aggregatorCapabilities()
	case "grouper":
		ops = grouperCapabilities()
	case "filterer":
		ops = filtererCapabilities()
	}
	keys := map[string]bool{}
	for _, k := range componentFloors[cat] {
		keys[k] = true
	}
	for _, o := range ops {
		if o.Name == name {
			for _, k := range o.ComponentSchema.Keys {
				keys[k.Name] = true
			}
		}
	}
	return func(field string) (FieldCheck, string) {
		if key, ok := strings.CutPrefix(field, "components."); ok && keys[key] {
			return FieldStatic, ""
		}
		if valuePathCategories[cat] {
			if check, why, ok := valueOutputCheck(name, field); ok {
				return check, why
			}
			return FieldUnknown, fmt.Sprintf("%s emits no %q (want %s, or components.<key> from its ComponentSchema or the universal floor)", name, field, valueField(name))
		}
		return FieldUnknown, fmt.Sprintf("%s emits no %q (want components.<key> from its ComponentSchema or the universal floor)", name, field)
	}
}

// InterpretationField is one declared (operator, field) pair of the
// built-in Interpretation registry.
type InterpretationField struct {
	// Name is the registry key (TEST_* family, REG_*, OVERLAY_*, ...).
	Name string
	// Category is the PurposeSurfaces category Name belongs to.
	Category string
	// Field is the declared normalised path.
	Field string
	// Deferred is true for a map-valued path (details.*,
	// summary.parameters.*) only a runtime probe can confirm.
	Deferred bool
}

// DeclaredInterpretationFields returns every declared (operator, field)
// pair of the built-in registry, sorted by name then field. It is the
// seam the runtime probes (internal/processing for tests and
// regressions, internal/service for overlays) hold two-way complete:
// every Deferred pair must be probed against the real emitted value.
func DeclaredInterpretationFields() []InterpretationField {
	var out []InterpretationField
	for name, ins := range builtinInterpretations {
		cat, _ := surfaceCategory(name)
		resolve := BuiltinOutputResolver(name)
		for _, in := range ins {
			check, _ := resolve(in.Field)
			out = append(out, InterpretationField{Name: name, Category: cat, Field: in.Field, Deferred: check == FieldDeferred})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Field < out[j].Field
	})
	return out
}

// interpretationRegistryViolations validates every entry of reg in
// sorted key order with the built-in resolvers.
func interpretationRegistryViolations(reg map[string][]descriptor.Interpretation) []InterpretationViolation {
	keys := make([]string, 0, len(reg))
	for k := range reg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []InterpretationViolation
	for _, k := range keys {
		out = append(out, ValidateInterpretations(k, reg[k], BuiltinOutputResolver(k))...)
	}
	return out
}

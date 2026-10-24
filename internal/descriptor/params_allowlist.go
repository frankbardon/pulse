package descriptor

import (
	"bytes"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"sync"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/strictjson"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// params_allowlist.go makes the manifest's per-operator Param lists the
// AUTHORITATIVE allowlist for a slot's `params` object (U44 #301): a
// params key the operator does not declare is refused
// PULSE_REQUEST_UNKNOWN_FIELD with details.path locating it
// (`groups[0].params.unit`), never silently ignored. The lists are held
// to what the runtime actually reads by the declared-vs-read gate
// (TestParamsDeclaredMatchRead, root package, over internal/paramread).
//
// A Param marked Slot is a field of the slot object (Group.Interval,
// Test.Alpha) listed for discovery: the strict request decode checks
// it as a slot key, and here it is NOT a params key. An extension
// operator whose registration declares no params is not validated (its
// params pass through to the embedder's factory, as before); predict
// says so with PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED.
//
// An extension operator that declares params also has each declared
// value checked against its ParamMeta (U53 E1-S3), in every one of the
// nine extension categories: a value whose JSON type is not the
// declared JSONType, or a Required key the params object omits, is
// refused PULSE_REQUEST_PARAM_INVALID (details.reason wrong_type /
// missing_required, details.path `<slot>.params.<key>`), and so is a
// params object carrying none, or more than one, of the members of a
// declared ExactlyOneOf group (U53 decision #12; details.reason
// exactly_one_of, details.path `<slot>.params`, details.group /
// members / present). The faults are ordered: an unknown key first
// (PULSE_REQUEST_UNKNOWN_FIELD), then a wrong type, then a missing
// Required param, then an ExactlyOneOf group. The check is declarative
// — no operator code runs — so predict and runtime refuse identically.
// A Default is never filled in here: it stays the factory's to apply.
// Built-ins keep their own decode path.

// ParamsDecl is one operator's params allowlist.
type ParamsDecl struct {
	// Keys are the params-object keys the operator reads, sorted.
	Keys []string
	// Slot are the declared keys that live on the slot object
	// (descriptor.Param.Slot), sorted.
	Slot []string
	// OwnRefusal are keys the operator decodes only to refuse them with
	// its own pointed code (paramsOwnRefusals): not in its manifest
	// list, and passed through here so that refusal — which names the
	// alternative — is the one the caller sees.
	OwnRefusal []string
	// Declared is false for an extension operator whose registration
	// declares no params: its params object is not checked.
	Declared bool
	// Types maps each extension param key to its declared JSON type
	// (ParamMeta.JSONType: string, number, boolean, array, object); a
	// present value of another type is refused. Nil for a built-in.
	Types map[string]string
	// Required are the extension param keys a request must supply,
	// sorted. Nil for a built-in.
	Required []string
	// OneOf are the extension operator's ExactlyOneOf groups, in the
	// order each group is first declared: a request supplies exactly
	// one member of each. Nil for a built-in.
	OneOf []OneOfGroup
}

// OneOfGroup is one declared ExactlyOneOf group: its name and its
// members in declaration order.
type OneOfGroup struct {
	Name    string
	Members []string
}

// paramsOwnRefusals are the keys a built-in reads only to refuse it
// with its own, more useful, code — the key is inert on that operator
// and the refusal says what to use instead:
//   - MAT_COVARIANCE params.summary: top_pairs ranks correlations, a
//     covariance is in its members' units (PULSE_MATRIX_INVALID);
//   - MAT_DISTANCE params.fit: per-group centroids are not one listwise
//     state (PULSE_MATRIX_INVALID fit_unsupported);
//   - a pairwise overlay's mode selector the kind does not take
//     (PULSE_OVERLAY_PARAM_MISSING): n_source / p_source on the Welford
//     and weighted-moments kinds, n_basis on every kind but the
//     weighted one.
func paramsOwnRefusals(k paramread.Key) []string {
	switch {
	case k == paramread.Key{Category: paramread.Matrix, Op: string(types.MAT_COVARIANCE)}:
		return []string{"summary"}
	case k == paramread.Key{Category: paramread.Matrix, Op: string(types.MAT_DISTANCE)}:
		return []string{"fit"}
	case k.Category == paramread.Overlay && types.IsPairwiseOverlayKind(types.OverlayKind(k.Op)):
		kind := types.OverlayKind(k.Op)
		switch {
		case types.PairwiseKindUsesWeightedMoments(kind):
			return []string{"n_source", "p_source"}
		case types.PairwiseKindUsesWelford(kind):
			return []string{"n_basis", "n_source", "p_source"}
		}
		return []string{"n_basis"}
	}
	return nil
}

// paramsAliases are known params misnomers per operator: the key a
// caller reaches for, mapped to the declared key that does the job,
// suggested where no edit distance would find it.
var paramsAliases = map[paramread.Key]map[string]string{
	{Category: paramread.Grouper, Op: string(types.GROUP_DATE)}: {"unit": "component", "granularity": "component"},
}

// paramsDeclOf splits a manifest Param list.
func paramsDeclOf(ps []descriptor.Param) ParamsDecl {
	d := ParamsDecl{Declared: true, Keys: []string{}}
	for _, p := range ps {
		if p.Slot {
			d.Slot = append(d.Slot, p.Name)
		} else {
			d.Keys = append(d.Keys, p.Name)
		}
	}
	sort.Strings(d.Keys)
	sort.Strings(d.Slot)
	d.Keys = slices.Compact(d.Keys)
	return d
}

// mergeDecl unions b into a (post-test variants sharing a family).
func mergeDecl(a, b ParamsDecl) ParamsDecl {
	keys := slices.Compact(slices.Sorted(slices.Values(append(append([]string(nil), a.Keys...), b.Keys...))))
	slot := slices.Compact(slices.Sorted(slices.Values(append(append([]string(nil), a.Slot...), b.Slot...))))
	return ParamsDecl{Keys: keys, Slot: slot, Declared: true}
}

// builtinParamsAllowlist is every built-in operator's allowlist, keyed
// by the paramread category the runtime records its reads under.
var builtinParamsAllowlist = sync.OnceValue(func() map[paramread.Key]ParamsDecl {
	out := map[paramread.Key]ParamsDecl{}
	ops := func(cat paramread.Category, list []descriptor.Operator) {
		for _, o := range list {
			out[paramread.Key{Category: cat, Op: o.Name}] = paramsDeclOf(o.Params)
		}
	}
	ops(paramread.Aggregator, aggregatorCapabilityTable())
	ops(paramread.Attribute, attributeCapabilities())
	ops(paramread.Filterer, filtererCapabilities())
	ops(paramread.Grouper, grouperCapabilities())
	ops(paramread.Window, windowCapabilities())
	ops(paramread.Feature, featureCapabilities())
	for _, t := range append(testCapabilities(), postTestCapabilities()...) {
		if t.Tier == 1 {
			out[paramread.Key{Category: paramread.Test, Op: t.Name}] = paramsDeclOf(t.Params)
			continue
		}
		k := paramread.Key{Category: paramread.PostTest, Op: t.Family}
		if prev, ok := out[k]; ok {
			out[k] = mergeDecl(prev, paramsDeclOf(t.Params))
		} else {
			out[k] = paramsDeclOf(t.Params)
		}
	}
	for _, m := range matrixCapabilities() {
		out[paramread.Key{Category: paramread.Matrix, Op: m.Name}] = paramsDeclOf(m.Params)
	}
	for _, o := range OverlayCapabilities() {
		out[paramread.Key{Category: paramread.Overlay, Op: string(o.Kind)}] = paramsDeclOf(o.Params)
	}
	// A built-in regression takes every knob as a typed RegressionSpec
	// field (its manifest Param list names those slot fields, each
	// marked Slot) and reads no params object: the allowlist has no
	// params key, so any params key on REG_OLS / REG_GLM /
	// REG_BAYES_LINEAR is refused.
	for _, r := range regressionCapabilities() {
		out[paramread.Key{Category: paramread.Regression, Op: r.Name}] = paramsDeclOf(r.Params)
	}
	for k, d := range out {
		d.OwnRefusal = paramsOwnRefusals(k)
		out[k] = d
	}
	return out
})

// instanceBuiltinDecl is built-in k's allowlist d on inst: a MAT_SEM
// params surface the instance hides (vectors.SEMFeatureParams: the
// mean structure, the multi-group fit) is no key at all — refused, and
// absent from valid_keys, exactly as a key MAT_SEM never read. d itself
// is never mutated (the built-in table is shared).
func instanceBuiltinDecl(k paramread.Key, d ParamsDecl, inst *InstanceSnapshot) ParamsDecl {
	if k.Category != paramread.Matrix || k.Op != string(types.MAT_SEM) || !inst.Scoped() {
		return d
	}
	for feat, keys := range vectors.SEMFeatureParams() {
		if inst.Hidden(feat) {
			d.Keys = slices.DeleteFunc(slices.Clone(d.Keys), func(k string) bool { return slices.Contains(keys, k) })
		}
	}
	return d
}

// extensionVecAggEngineParams are the engine-owned params keys of an
// extension vector-input aggregator (vectors.DecodeExtensionVecAggParams).
var extensionVecAggEngineParams = []string{"expand", "labels"}

// ParamsAllowlist returns every operator's params allowlist on inst:
// the built-ins plus inst's (visible) extension registrations. A fresh
// map; the caller may keep it.
func ParamsAllowlist(inst *InstanceSnapshot) map[paramread.Key]ParamsDecl {
	out := make(map[paramread.Key]ParamsDecl, len(builtinParamsAllowlist()))
	for k, v := range builtinParamsAllowlist() {
		out[k] = instanceBuiltinDecl(k, v, inst)
	}
	snap := inst.Extensions()
	if snap == nil {
		return out
	}
	ext := func(cat paramread.Category, metas []descriptor.OperatorMeta) {
		for _, m := range metas {
			d := ParamsDecl{Declared: len(m.Params) > 0, Keys: []string{}}
			for _, p := range m.Params {
				d.Keys = append(d.Keys, p.Name)
				d.declareValue(p.Name, p.JSONType, p.Required, p.ExactlyOneOf)
			}
			if d.Declared && cat == paramread.Aggregator && m.VectorInput {
				d.Keys = append(d.Keys, extensionVecAggEngineParams...)
			}
			sort.Strings(d.Keys)
			d.Keys = slices.Compact(d.Keys)
			out[paramread.Key{Category: cat, Op: m.Name}] = d
		}
	}
	ext(paramread.Aggregator, snap.Aggregators)
	ext(paramread.Attribute, snap.Attributes)
	ext(paramread.Filterer, snap.Filterers)
	ext(paramread.Grouper, snap.Groupers)
	ext(paramread.Window, snap.Windows)
	ext(paramread.Feature, snap.Features)
	ext(paramread.Test, snap.Tests)
	ext(paramread.Regression, snap.Regressions)
	for _, m := range snap.MatrixOps {
		facts := snap.MatrixDetails[m.Name]
		d := paramsDeclOf(extensionMatrixParams(m.Streamable, facts.Params))
		d.Declared = len(facts.Params) > 0
		// Only the operator's own params carry a registration
		// declaration; the engine-owned member / fit params decode in
		// the engine. facts.Params are in the manifest Param
		// vocabulary, mapped back to the declared JSON type.
		for _, p := range facts.Params {
			d.declareValue(p.Name, paramTypeJSON[p.Type], p.Required, p.ExactlyOneOf)
		}
		out[paramread.Key{Category: paramread.Matrix, Op: m.Name}] = d
	}
	return out
}

// paramTypeJSON maps the manifest Param type vocabulary an extension
// matrix operator's own params are projected into back onto the
// registration's JSONType (the inverse of the root package's
// matrixParamSnapshot mapping).
var paramTypeJSON = map[string]string{"float": "number", "bool": "boolean", "list": "array", "string": "string", "object": "object"}

// declareValue records key's declared JSON type, required flag and
// ExactlyOneOf group (empty = ungrouped).
func (d *ParamsDecl) declareValue(key, jsonType string, required bool, group string) {
	if group != "" {
		i := slices.IndexFunc(d.OneOf, func(g OneOfGroup) bool { return g.Name == group })
		if i < 0 {
			d.OneOf = append(d.OneOf, OneOfGroup{Name: group})
			i = len(d.OneOf) - 1
		}
		d.OneOf[i].Members = append(d.OneOf[i].Members, key)
	}
	if jsonType != "" {
		if d.Types == nil {
			d.Types = map[string]string{}
		}
		d.Types[key] = jsonType
	}
	if required {
		d.Required = append(d.Required, key)
		sort.Strings(d.Required)
	}
}

// JSONValueType is the JSON type of one raw value: string, number,
// boolean, array, object or null. It is the one value checker the
// request params check and the registration-time ParamMeta.Example
// check (pulse.New) share.
func JSONValueType(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "null"
	}
	switch raw[0] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	}
	return "number"
}

// valueRefusal checks the declared values of an extension operator's
// params object m (nil when the slot carries none): the first key, in
// sorted order, whose value is not its declared JSON type, else the
// first Required key m omits, else the first ExactlyOneOf group (in
// declaration order) of which m carries no member or more than one. A
// null value is not of any declared type. Nil when every value
// conforms.
func valueRefusal(path, op string, decl ParamsDecl, m map[string]json.RawMessage) *errors.CodedError {
	if len(decl.Types) == 0 && len(decl.Required) == 0 && len(decl.OneOf) == 0 {
		return nil
	}
	for _, k := range paramKeys(m) {
		want, ok := decl.Types[k]
		if !ok {
			continue
		}
		if got := JSONValueType(m[k]); got != want {
			return errors.NewCodedErrorWithDetails(errors.PULSE_REQUEST_PARAM_INVALID,
				"operator "+op+" param "+strconv.Quote(k)+" at "+path+".params."+k+" must be a JSON "+want+", got "+got,
				map[string]any{"path": path + ".params." + k, "operator": op, "param": k,
					"reason": "wrong_type", "expected": want, "got": got})
		}
	}
	for _, k := range decl.Required {
		if _, ok := m[k]; !ok {
			return errors.NewCodedErrorWithDetails(errors.PULSE_REQUEST_PARAM_INVALID,
				"operator "+op+" requires param "+strconv.Quote(k)+" at "+path+".params."+k+", which the params object omits",
				map[string]any{"path": path + ".params." + k, "operator": op, "param": k,
					"reason": "missing_required", "expected": decl.Types[k]})
		}
	}
	for _, g := range decl.OneOf {
		present := []string{}
		for _, k := range g.Members {
			if _, ok := m[k]; ok {
				present = append(present, k)
			}
		}
		if len(present) == 1 {
			continue
		}
		carries := "none of them"
		if len(present) > 0 {
			carries = quotedList(present)
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_REQUEST_PARAM_INVALID,
			"operator "+op+" takes exactly one of params "+quotedList(g.Members)+" (group "+strconv.Quote(g.Name)+") at "+path+".params, which carries "+carries,
			map[string]any{"path": path + ".params", "operator": op, "reason": "exactly_one_of",
				"group": g.Name, "members": append([]string(nil), g.Members...), "present": present})
	}
	return nil
}

// quotedList is names quoted and comma-separated.
func quotedList(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += strconv.Quote(n)
	}
	return out
}

// paramsChecker walks one request root's slots against an allowlist.
// It allocates nothing on a request whose slots carry no params: slot
// paths are built only on a refusal, and the extension allowlist only
// when a non-built-in operator carries params.
type paramsChecker struct {
	inst *InstanceSnapshot
	ext  map[paramread.Key]ParamsDecl
	// undeclared, when set, turns the walk into the advisory pass: it
	// is called for every slot whose extension operator declares no
	// params yet carries a non-empty params object, and no slot is
	// refused (extensionParamsAdvisories).
	undeclared func(path, op string, keys []string)
}

// lookup returns op's allowlist: the built-in table first (an extension
// name never collides with a built-in), then inst's extensions.
func (c *paramsChecker) lookup(k paramread.Key) (ParamsDecl, bool) {
	if d, ok := builtinParamsAllowlist()[k]; ok {
		return instanceBuiltinDecl(k, d, c.inst), true
	}
	if c.ext == nil {
		c.ext = ParamsAllowlist(c.inst)
	}
	d, ok := c.ext[k]
	return d, ok
}

// slotPath is "<prefix>[<i>]", or prefix alone when i < 0.
func slotPath(prefix string, i int) string {
	if i < 0 {
		return prefix
	}
	return prefix + "[" + strconv.Itoa(i) + "]"
}

// check refuses raw (the params object of slot prefix[i], e.g.
// groups[0]; i < 0 for an unindexed slot) when it carries a key op does
// not declare, or — on an extension operator that declares params — a
// value of the wrong JSON type, no value for a Required key (absent
// params included), or not exactly one member of an ExactlyOneOf
// group. A slot whose operator is unknown, hidden or
// undeclared, or whose params are not an object, is left to the
// operator's own validation.
func (c *paramsChecker) check(prefix string, i int, cat paramread.Category, op string, raw []byte) *errors.CodedError {
	if op == "" {
		return nil
	}
	var m map[string]json.RawMessage
	if len(bytes.TrimSpace(raw)) > 0 {
		var ok bool
		if m, ok = objectMap(raw); !ok {
			return nil
		}
	}
	k := paramread.Key{Category: cat, Op: op}
	if len(m) == 0 {
		// A params-free slot: only an extension operator's Required
		// params can refuse it. Built-ins (and instances without
		// extensions) return before any allocation.
		if _, builtin := builtinParamsAllowlist()[k]; builtin || c.undeclared != nil || c.inst.Extensions() == nil {
			return nil
		}
	}
	if c.inst.Hidden(op) {
		return nil
	}
	decl, known := c.lookup(k)
	if c.undeclared != nil {
		if known && !decl.Declared {
			c.undeclared(slotPath(prefix, i), op, paramKeys(m))
		}
		return nil
	}
	if !known || !decl.Declared {
		return nil
	}
	var unknown []string
	for _, k := range paramKeys(m) {
		if !slices.Contains(decl.Keys, k) && !slices.Contains(decl.OwnRefusal, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return valueRefusal(slotPath(prefix, i), op, decl, m)
	}
	// params.covariate on a test that does not read it is the pointed
	// refusal naming the tests that do (the covariate adjustment would
	// otherwise be silently absent).
	if (cat == paramread.Test || cat == paramread.PostTest) && slices.Contains(unknown, fieldaccept.CovariateKey) {
		return fieldaccept.CovariateUnsupportedRefusal(slotPath(prefix, i), op)
	}
	return strictjson.UnknownFieldErrorAliased(slotPath(prefix, i)+".params", unknown, decl.Keys, paramsAliases[k])
}

// checkMap is check over a map-valued params object (Compose / chain
// overlays).
func (c *paramsChecker) checkMap(prefix string, i int, cat paramread.Category, op string, m map[string]any) *errors.CodedError {
	if len(m) == 0 {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return c.check(prefix, i, cat, op, raw)
}

// objectMap returns raw's top-level members when raw is a JSON object;
// false for null or any non-object (the operator's own decode refuses
// those).
func objectMap(raw []byte) (map[string]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	return m, true
}

// paramKeys returns m's keys, sorted.
func paramKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// request walks one Request's slots in declaration order.
func (c *paramsChecker) request(req *types.Request) *errors.CodedError {
	if req == nil {
		return nil
	}
	if ce := c.filterers("filterers", req.Filterers); ce != nil {
		return ce
	}
	if ce := c.aggregations("aggregations", req.Aggregations); ce != nil {
		return ce
	}
	for i, a := range req.Attributes {
		if a != nil {
			if ce := c.check("attributes", i, paramread.Attribute, string(a.Type), a.Params); ce != nil {
				return ce
			}
		}
	}
	if ce := c.groups("groups", req.Groups); ce != nil {
		return ce
	}
	for i, w := range req.Windows {
		if w != nil {
			if ce := c.check("windows", i, paramread.Window, string(w.Type), w.Params); ce != nil {
				return ce
			}
		}
	}
	for i, f := range req.Features {
		if f != nil {
			if ce := c.check("features", i, paramread.Feature, string(f.Type), f.Params); ce != nil {
				return ce
			}
		}
	}
	for i, t := range req.Tests {
		if t != nil {
			if ce := c.check("tests", i, paramread.Test, string(t.Type), t.Params); ce != nil {
				return ce
			}
		}
	}
	for i, t := range req.PostTests {
		if t != nil {
			if ce := c.check("post_tests", i, paramread.PostTest, string(t.Type), t.Params); ce != nil {
				return ce
			}
		}
	}
	for i := range req.Matrices {
		if ce := c.matrix(i, &req.Matrices[i]); ce != nil {
			return ce
		}
	}
	for i, r := range req.Regressions {
		if r != nil {
			if ce := c.check("regressions", i, paramread.Regression, string(r.Type), r.Params); ce != nil {
				return ce
			}
		}
	}
	if ct := req.Crosstab; ct != nil {
		if ce := c.groups("crosstab.rows", ct.Rows); ce != nil {
			return ce
		}
		if ce := c.groups("crosstab.columns", ct.Columns); ce != nil {
			return ce
		}
		if ct.HasMeasures() {
			if ce := c.aggregations("crosstab.cells", ct.Cells); ce != nil {
				return ce
			}
		} else if ct.Cell != nil {
			if ce := c.check("crosstab.cell", -1, paramread.Aggregator, string(ct.Cell.Type), ct.Cell.Params); ce != nil {
				return ce
			}
		}
		if ce := c.aggregations("crosstab.margin_aggregations", ct.MarginAggregations); ce != nil {
			return ce
		}
	}
	return c.overlays("overlays", req.Overlays)
}

func (c *paramsChecker) filterers(prefix string, fs []*types.Filterer) *errors.CodedError {
	for i, f := range fs {
		if f != nil {
			if ce := c.check(prefix, i, paramread.Filterer, string(f.Type), f.Params); ce != nil {
				return ce
			}
		}
	}
	return nil
}

func (c *paramsChecker) aggregations(prefix string, as []*types.Aggregation) *errors.CodedError {
	for i, a := range as {
		if a != nil {
			if ce := c.check(prefix, i, paramread.Aggregator, string(a.Type), a.Params); ce != nil {
				return ce
			}
		}
	}
	return nil
}

func (c *paramsChecker) groups(prefix string, gs []*types.Group) *errors.CodedError {
	for i, g := range gs {
		if g != nil {
			if ce := c.check(prefix, i, paramread.Grouper, string(g.Type), g.Params); ce != nil {
				return ce
			}
		}
	}
	return nil
}

// matrix checks a matrix spec's params, then a MAT_DISTANCE
// params.between grouper's own params.
func (c *paramsChecker) matrix(i int, m *types.MatrixSpec) *errors.CodedError {
	if ce := c.check("matrices", i, paramread.Matrix, string(m.Type), m.Params); ce != nil {
		return ce
	}
	var p struct {
		Between *types.Group `json:"between"`
	}
	if m.Type != types.MAT_DISTANCE || len(m.Params) == 0 || paramread.Probe(m.Params, &p) != nil || p.Between == nil {
		return nil
	}
	return c.check(slotPath("matrices", i)+".params.between", -1, paramread.Grouper, string(p.Between.Type), p.Between.Params)
}

func (c *paramsChecker) overlays(prefix string, os []types.OverlaySpec) *errors.CodedError {
	for i := range os {
		if ce := c.check(prefix, i, paramread.Overlay, string(os[i].Kind), os[i].Params); ce != nil {
			return ce
		}
	}
	return nil
}

// ParamsRefusal checks every slot params object in v (a *types.Request,
// *types.ComposedRequest, *types.ChainRequest or *types.FacetRequest;
// anything else passes) against inst's ParamsAllowlist, refusing the
// first unknown key PULSE_REQUEST_UNKNOWN_FIELD: details.path
// `<slot>.params.<key>`, unknown_keys, valid_keys (the operator's
// params keys) and a nearest-key suggestion; inside a Compose request,
// fit or chain stage, details.request / fit / stage locate it.
// SlotRefusal runs it, so every service funnel and every predict root
// refuses the same key.
func ParamsRefusal(v any, inst *InstanceSnapshot) error {
	c := paramsChecker{inst: inst}
	switch r := v.(type) {
	case *types.Request:
		if ce := c.request(r); ce != nil {
			return ce
		}
	case *types.FacetRequest:
		if r == nil {
			return nil
		}
		if ce := c.filterers("filterers", r.Filterers); ce != nil {
			return ce
		}
		if ce := c.overlays("overlays", r.Overlays); ce != nil {
			return ce
		}
	case *types.ComposedRequest:
		if r == nil {
			return nil
		}
		for i := range r.Overlays {
			if ce := c.checkMap("overlays", i, paramread.Overlay, string(r.Overlays[i].Kind), r.Overlays[i].Params); ce != nil {
				return ce
			}
		}
		for i, req := range r.Requests {
			if ce := c.request(req); ce != nil {
				return RefusalAt(ce, strictjson.LocationRequest, i)
			}
		}
		for i := range r.Fits {
			if ce := c.request(r.Fits[i].Request); ce != nil {
				return RefusalAt(ce, strictjson.LocationFit, i)
			}
		}
	case *types.ChainRequest:
		if r == nil {
			return nil
		}
		for i, st := range r.Stages {
			if st == nil {
				continue
			}
			if ce := c.request(st.Request); ce != nil {
				return RefusalAt(ce, strictjson.LocationStage, i)
			}
		}
		for i, o := range r.Overlays {
			if o == nil {
				continue
			}
			if ce := c.checkMap("overlays", i, paramread.Overlay, string(o.Kind), o.Params); ce != nil {
				return ce
			}
		}
	}
	return nil
}

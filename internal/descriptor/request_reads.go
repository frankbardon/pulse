package descriptor

// Request read maps (expression-wires E2-S3).
//
// RequestReads walks a request document as raw JSON — never decoded
// into types, so a document that does not validate still yields every
// slot it can read — and reports, per reading slot, the component's
// JSON path, the key inside it and the names it reads. The slot lists
// are Pulse's own, never a hand copy:
//
//   - expression slots: every string whose key path internal/exprslots
//     matches (filterer / attribute expression, regression holdout,
//     OVERLAY_FORMULA, a feature's fit_on, the optimize objective and
//     constraints), read by internal/exprreads — the engine's walk;
//   - params naming a column: the instance manifest's Param entries
//     marked names_column (a Slot entry on the component, else under
//     its params object);
//   - an extension operator's params: its registration's FieldInputs
//     hook, the one the runtime projection consults;
//   - the typed slots of the request roots (field, fields, vector,
//     target, predictors, split_by, order_by, partition_by, weight,
//     covariance, bounds, sort, labels, joins, the crosstab and banner
//     groupers and cells), from the types structs' json tags.
//
// No-execute: no compile, no engine, no cohort (TestPredictNoExecution
// Imports lists this file).

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprreads"
	"github.com/frankbardon/pulse/internal/exprslots"
)

// Component categories: the manifest section an operator's Param list
// is read from, spelled as the extension FieldInputs keys spell them
// (post_test aside, which only the manifest knows).
const (
	catAggregator = "aggregator"
	catAttribute  = "attribute"
	catFilterer   = "filterer"
	catGrouper    = "grouper"
	catWindow     = "window"
	catFeature    = "feature"
	catTest       = "test"
	catPostTest   = "post_test"
	catRegression = "regression"
	catMatrix     = "matrix"
	catOverlay    = "overlay"
	// The typed-only slots: no operator type, no manifest entry.
	catVector  = "vector"
	catSortKey = "sort"
	catLabel   = "label"
	catJoin    = "join"
)

// slotShape is how a typed slot's value names columns.
type slotShape int

const (
	shapeOne    slotShape = iota // a field-name string
	shapeList                    // a list of field-name strings
	shapeOrder                   // a list of {field, desc} order keys
	shapeWeight                  // a field-name string, {field, kind} or null
	shapeOn                      // a join's [{left_field, right_field}]: the left side
)

type typedSlot struct {
	key   string // dotted key path inside the component
	shape slotShape
}

// typedSlots are each component's typed keys that name a column or a
// vector, from the types structs (types.Filterer, Attribute, ...). Keys
// the manifest also declares (a Slot param such as a regression's
// target) are read once.
var typedSlots = map[string][]typedSlot{
	catFilterer:   {{"field", shapeOne}, {"tz_field", shapeOne}},
	catAttribute:  {{"field", shapeOne}, {"vector", shapeOne}, {"fields", shapeList}, {"target", shapeOne}, {"predictors", shapeList}, {"subject_field", shapeOne}, {"order_field", shapeOne}, {"tz_field", shapeOne}, {"weight", shapeWeight}},
	catAggregator: {{"field", shapeOne}, {"vector", shapeOne}, {"fields", shapeList}, {"weight", shapeWeight}},
	catGrouper:    {{"field", shapeOne}, {"vector", shapeOne}, {"fields", shapeList}, {"tz_field", shapeOne}, {"weight", shapeWeight}},
	catWindow:     {{"field", shapeOne}, {"partition_by", shapeList}, {"order_by", shapeOrder}},
	catFeature:    {{"field", shapeOne}, {"tz_field", shapeOne}},
	catTest:       {{"field", shapeOne}, {"field2", shapeOne}, {"vector", shapeOne}, {"fields", shapeList}, {"vector2", shapeOne}, {"fields2", shapeList}, {"split_by", shapeOne}, {"rows", shapeOne}, {"cols", shapeOne}, {"subject_field", shapeOne}, {"order_by", shapeOrder}, {"weight", shapeWeight}},
	catRegression: {{"target", shapeOne}, {"predictors", shapeList}, {"unpenalized", shapeList}, {"bounds.nonnegative", shapeList}, {"jackknife_by", shapeOne}, {"covariance.cluster", shapeOne}, {"covariance.order_by", shapeOne}, {"weight", shapeWeight}},
	catMatrix:     {{"vector", shapeOne}, {"fields", shapeList}, {"weight", shapeWeight}},
	catVector:     {{"fields", shapeList}},
	catSortKey:    {{"field", shapeOne}},
	catLabel:      {{"field", shapeOne}},
	catJoin:       {{"on", shapeOn}},
}

func init() { typedSlots[catPostTest] = typedSlots[catTest] }

// readsIndex is the instance manifest's names_column Param entries,
// by category then operator name.
type readsIndex map[string]map[string][]descriptor.Param

func buildReadsIndex(inst *InstanceSnapshot) readsIndex {
	m := BuildManifestForInstance(inst)
	idx := readsIndex{}
	add := func(cat, name string, params []descriptor.Param) {
		var marked []descriptor.Param
		for _, p := range params {
			if p.NamesColumn {
				marked = append(marked, p)
			}
		}
		if len(marked) == 0 {
			return
		}
		if idx[cat] == nil {
			idx[cat] = map[string][]descriptor.Param{}
		}
		idx[cat][name] = marked
	}
	ops := func(cat string, list []descriptor.Operator) {
		for _, o := range list {
			add(cat, o.Name, o.Params)
		}
	}
	ops(catAggregator, m.Components.Aggregators)
	ops(catAttribute, m.Components.Attributes)
	ops(catFilterer, m.Components.Filterers)
	ops(catGrouper, m.Components.Groupers)
	ops(catWindow, m.Components.Windows)
	ops(catFeature, m.Components.Features)
	for _, t := range m.Tests {
		add(catTest, t.Name, t.Params)
	}
	for _, t := range m.PostTests {
		add(catPostTest, t.Name, t.Params)
	}
	for _, r := range m.Regressions {
		add(catRegression, r.Name, r.Params)
	}
	for _, x := range m.Matrices {
		add(catMatrix, x.Name, x.Params)
	}
	for _, o := range m.Overlays {
		add(catOverlay, string(o.Kind), o.Params)
	}
	return idx
}

// namesColumnKeyword are the marked params' keyword values that name
// no column (MAT_PARTIAL_CORRELATION control "all": partial on every
// other member).
var namesColumnKeyword = map[string]string{"control": "all"}

// RequestReads returns doc's read map. root names the document kind;
// "" detects it from the top-level keys (stages: chain; requests,
// sweep, banner, fits or optimize: composed; fields: facet; else
// request). A doc that is not a JSON object, or an unknown root, is
// SERVICE_VALIDATION; every other failure is one slot's Error. A
// templated `{{token}}` is not rendered here: the caller renders the
// template first (an unrendered token in an expression slot is that
// slot's PULSE_FORMULA_COMPILE_ERROR; elsewhere it reads as written).
func RequestReads(doc []byte, root descriptor.ReadsRoot, inst *InstanceSnapshot) (descriptor.RequestReads, *errors.CodedError) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil || top == nil {
		reason := "not a JSON object"
		if err != nil {
			reason = err.Error()
		}
		return descriptor.RequestReads{Slots: []descriptor.SlotReads{}}, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"request reads: the document is not a JSON object: "+reason, map[string]any{"reason": "document"})
	}
	if root == "" {
		root = detectReadsRoot(top)
	}
	w := &readsWalker{ext: inst.Extensions()}
	if inst != nil && inst.reads != nil {
		w.idx = inst.reads()
	} else {
		w.idx = defaultReadsIndex()
	}
	switch root {
	case descriptor.ReadsRootRequest:
		w.request(top, nil)
	case descriptor.ReadsRootComposed:
		w.compose(top)
	case descriptor.ReadsRootChain:
		w.chain(top)
	case descriptor.ReadsRootFacet:
		w.facet(top)
	default:
		return descriptor.RequestReads{Slots: []descriptor.SlotReads{}}, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"request reads: unknown root "+strconv.Quote(string(root))+"; want request, composed, chain or facet",
			map[string]any{"reason": "root", "root": string(root), "valid": []string{"request", "composed", "chain", "facet"}})
	}
	sort.SliceStable(w.out, func(i, j int) bool { return w.out[i].less(&w.out[j]) })
	out := descriptor.RequestReads{Root: root, Slots: make([]descriptor.SlotReads, len(w.out))}
	for i := range w.out {
		out.Slots[i] = w.out[i].SlotReads
	}
	return out, nil
}

// defaultReadsIndex is the unscoped built-in index, for a nil instance.
var defaultReadsIndex = sync.OnceValue(func() readsIndex { return buildReadsIndex(nil) })

func detectReadsRoot(top map[string]any) descriptor.ReadsRoot {
	has := func(k string) bool { _, ok := top[k]; return ok }
	switch {
	case has("stages"):
		return descriptor.ReadsRootChain
	case has("requests"), has("sweep"), has("banner"), has("fits"), has("optimize"):
		return descriptor.ReadsRootComposed
	case has("fields"):
		return descriptor.ReadsRootFacet
	}
	return descriptor.ReadsRootRequest
}

// seg is one JSON path step: a key, or an array index (idx >= 0).
type seg struct {
	key string
	idx int
}

type readsPath []seg

func (p readsPath) key(k string) readsPath { return append(slices.Clip(p), seg{key: k, idx: -1}) }
func (p readsPath) at(i int) readsPath     { return append(slices.Clip(p), seg{idx: i}) }

func (p readsPath) String() string {
	var b strings.Builder
	for _, s := range p {
		if s.idx >= 0 {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(s.idx))
			b.WriteByte(']')
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.key)
	}
	return b.String()
}

// keys is the key path with indices dropped, as exprslots.Match reads.
func (p readsPath) keys() []string {
	out := make([]string, 0, len(p))
	for _, s := range p {
		if s.idx < 0 {
			out = append(out, s.key)
		}
	}
	return out
}

// split cuts a value's path into its component (through the last array
// index, else the scope) and the dotted key inside it.
func (p readsPath) split(scope readsPath) (readsPath, string) {
	cut := len(scope)
	for i := len(p) - 1; i >= len(scope); i-- {
		if p[i].idx >= 0 {
			cut = i + 1
			break
		}
	}
	return p[:cut], p[cut:].String()
}

type slotEntry struct {
	descriptor.SlotReads
	at readsPath
}

func (a *slotEntry) less(b *slotEntry) bool {
	for i := 0; i < len(a.at) && i < len(b.at); i++ {
		x, y := a.at[i], b.at[i]
		switch {
		case x.idx >= 0 && y.idx >= 0:
			if x.idx != y.idx {
				return x.idx < y.idx
			}
		case x.idx >= 0 || y.idx >= 0:
			return y.idx >= 0
		case x.key != y.key:
			return x.key < y.key
		}
	}
	if len(a.at) != len(b.at) {
		return len(a.at) < len(b.at)
	}
	return a.Key < b.Key
}

type readsWalker struct {
	idx readsIndex
	ext *ExtensionsSnapshot
	out []slotEntry
}

func (w *readsWalker) emit(at readsPath, key string, req readsPath, columns, params []string) *slotEntry {
	if columns == nil {
		columns = []string{}
	}
	if params == nil {
		params = []string{}
	}
	w.out = append(w.out, slotEntry{SlotReads: descriptor.SlotReads{
		Path: at.String(), Key: key, Request: req.String(), Columns: columns, Params: params,
	}, at: at})
	return &w.out[len(w.out)-1]
}

func (w *readsWalker) fail(at readsPath, key string, req readsPath, reason string) {
	e := w.emit(at, key, req, nil, nil)
	where := key
	if p := at.String(); p != "" {
		where = p + "." + key
	}
	e.Error = &descriptor.EnvelopeEntry{
		Code:    string(errors.SERVICE_VALIDATION),
		Message: where + ": " + reason,
		Details: map[string]any{"path": e.Path, "key": key, "reason": reason},
	}
}

// ---- roots ----------------------------------------------------------

// request walks one Request object at at; req is the params scope.
func (w *readsWalker) request(v any, at readsPath) {
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	req := at
	w.scanExpr(obj, at, req, "params", "cohort", "return", "outputs")
	sections := []struct{ key, cat string }{
		{"filterers", catFilterer}, {"attributes", catAttribute}, {"aggregations", catAggregator},
		{"groups", catGrouper}, {"windows", catWindow}, {"features", catFeature}, {"tests", catTest},
		{"post_tests", catPostTest}, {"regressions", catRegression}, {"matrices", catMatrix},
		{"vectors", catVector}, {"sort", catSortKey}, {"labels", catLabel}, {"joins", catJoin},
	}
	for _, s := range sections {
		w.components(obj[s.key], at.key(s.key), req, s.cat)
	}
	if ct, ok := obj["crosstab"].(map[string]any); ok {
		w.cells(ct, at.key("crosstab"), req)
		w.components(ct["margin_aggregations"], at.key("crosstab").key("margin_aggregations"), req, catAggregator)
	}
	w.typed(obj, at, req, []typedSlot{{"time_zone_field", shapeOne}, {"weight", shapeWeight}}, nil)
}

// cells walks the grouper / cell slots a crosstab and a banner section
// share: rows, columns, cell, cells.
func (w *readsWalker) cells(obj map[string]any, at, req readsPath) {
	w.components(obj["rows"], at.key("rows"), req, catGrouper)
	w.components(obj["columns"], at.key("columns"), req, catGrouper)
	if cell, ok := obj["cell"].(map[string]any); ok {
		w.component(cell, at.key("cell"), req, catAggregator)
	}
	w.components(obj["cells"], at.key("cells"), req, catAggregator)
}

func (w *readsWalker) compose(top map[string]any) {
	var root readsPath
	w.scanExpr(top, root, root, "requests", "fits", "sweep", "banner", "params", "optimize")
	if list, ok := top["requests"].([]any); ok {
		for i, r := range list {
			w.request(r, root.key("requests").at(i))
		}
	}
	if list, ok := top["fits"].([]any); ok {
		for i, f := range list {
			if fo, ok := f.(map[string]any); ok {
				w.request(fo["request"], root.key("fits").at(i).key("request"))
			}
		}
	}
	if sw, ok := top["sweep"].(map[string]any); ok {
		w.request(sw["request"], root.key("sweep").key("request"))
		w.scanExpr(sw, root.key("sweep"), root, "request")
	}
	if bn, ok := top["banner"].(map[string]any); ok {
		breq := root.key("banner").key("request")
		w.request(bn["request"], breq)
		if list, ok := bn["sections"].([]any); ok {
			for i, s := range list {
				so, ok := s.(map[string]any)
				if !ok {
					continue
				}
				at := root.key("banner").key("sections").at(i)
				w.scanExpr(so, at, breq)
				w.components(so["filterers"], at.key("filterers"), breq, catFilterer)
				w.cells(so, at, breq)
			}
		}
		if list, ok := bn["groups"].([]any); ok {
			for i, g := range list {
				if gobj, ok := g.(map[string]any); ok {
					at := root.key("banner").key("groups").at(i)
					w.components(gobj["columns"], at.key("columns"), breq, catGrouper)
				}
			}
		}
	}
	if list, ok := top["optimize"].([]any); ok {
		for i, o := range list {
			oo, ok := o.(map[string]any)
			if !ok {
				continue
			}
			at := root.key("optimize").at(i)
			w.scanExpr(oo, at, root, "variables", "models")
			w.optimizeBounds(oo, at, root)
		}
	}
}

// optimizeBounds reads each decision variable's `"params.<name>"`
// lower / upper / start.
func (w *readsWalker) optimizeBounds(o map[string]any, at, req readsPath) {
	vars, ok := o["variables"].([]any)
	if !ok {
		return
	}
	for j, v := range vars {
		vo, ok := v.(map[string]any)
		if !ok {
			continue
		}
		vat := at.key("variables").at(j)
		for _, k := range []string{"lower", "upper", "start"} {
			s, ok := vo[k].(string)
			if !ok {
				continue
			}
			name, ok := strings.CutPrefix(s, "params.")
			if !ok || name == "" {
				w.fail(vat, k, req, "want a number or \"params.<name>\"")
				continue
			}
			w.emit(vat, k, req, nil, []string{name})
		}
	}
}

func (w *readsWalker) chain(top map[string]any) {
	var root readsPath
	w.scanExpr(top, root, root, "stages", "params", "cohort")
	if list, ok := top["stages"].([]any); ok {
		for i, s := range list {
			if so, ok := s.(map[string]any); ok {
				w.request(so["request"], root.key("stages").at(i).key("request"))
			}
		}
	}
}

func (w *readsWalker) facet(top map[string]any) {
	var root readsPath
	w.scanExpr(top, root, root, "params", "cohort")
	w.components(top["filterers"], root.key("filterers"), root, catFilterer)
	w.components(top["labels"], root.key("labels"), root, catLabel)
	w.typed(top, root, root, []typedSlot{{"fields", shapeList}, {"additive_fields", shapeList}, {"time_zone_field", shapeOne}}, nil)
}

// ---- components -----------------------------------------------------

func (w *readsWalker) components(v any, at, req readsPath, cat string) {
	list, ok := v.([]any)
	if !ok {
		return
	}
	for i, c := range list {
		if obj, ok := c.(map[string]any); ok {
			w.component(obj, at.at(i), req, cat)
		}
	}
}

// component reads one component's typed slots, its manifest-marked
// params (or, for an extension operator, its FieldInputs hook) and the
// components nested in its params (an attribute's fit_on filterers,
// MAT_DISTANCE's between grouper).
func (w *readsWalker) component(obj map[string]any, at, req readsPath, cat string) {
	seen := map[string]bool{}
	w.typed(obj, at, req, typedSlots[cat], seen)
	typ, _ := obj["type"].(string)
	if typ == "" {
		return
	}
	params, _ := obj["params"].(map[string]any)
	for _, p := range w.idx[cat][typ] {
		if p.Slot {
			if !seen[p.Name] {
				seen[p.Name] = true
				w.value(obj[p.Name], at, p.Name, req, shapeAuto)
			}
			continue
		}
		if params == nil {
			continue
		}
		v, ok := params[p.Name]
		if !ok {
			continue
		}
		if kw, ok := namesColumnKeyword[p.Name]; ok && v == kw {
			continue
		}
		w.value(v, at, "params."+p.Name, req, shapeAuto)
	}
	w.fieldInputs(obj, at, req, cat, typ)
	if params == nil {
		return
	}
	switch cat {
	case catAttribute:
		w.components(params["fit_on"], at.key("params").key("fit_on"), req, catFilterer)
	case catMatrix:
		if b, ok := params["between"].(map[string]any); ok {
			sub := []typedSlot{}
			for _, s := range typedSlots[catGrouper] {
				sub = append(sub, typedSlot{"params.between." + s.key, s.shape})
			}
			w.typed(obj, at, req, sub, nil)
			if bt, _ := b["type"].(string); bt != "" {
				w.fieldInputsAt(b, at, "params.between.params", req, catGrouper, bt)
			}
		}
	}
}

// fieldInputs reads an extension operator's params through its
// registration's FieldInputs hook.
func (w *readsWalker) fieldInputs(obj map[string]any, at, req readsPath, cat, typ string) {
	w.fieldInputsAt(obj, at, "params", req, cat, typ)
}

func (w *readsWalker) fieldInputsAt(obj map[string]any, at readsPath, key string, req readsPath, cat, typ string) {
	if w.ext == nil || w.ext.FieldInputs == nil {
		return
	}
	hookCat := cat
	if cat == catPostTest {
		hookCat = catTest
	}
	if w.ext.FieldInputs[hookCat+"|"+typ] == nil {
		return
	}
	raw := json.RawMessage("{}")
	if p, ok := obj["params"]; ok {
		b, err := json.Marshal(p)
		if err != nil {
			w.fail(at, key, req, "params do not encode: "+err.Error())
			return
		}
		raw = b
	}
	names, ok := w.ext.DeclaredFieldInputs(hookCat, typ, raw)
	if !ok || len(names) == 0 {
		return
	}
	w.emit(at, key, req, dedupe(names), nil)
}

// typed reads the typed slots of obj at at; seen (may be nil) records
// the keys read.
func (w *readsWalker) typed(obj map[string]any, at, req readsPath, slots []typedSlot, seen map[string]bool) {
	for _, s := range slots {
		if seen != nil {
			seen[s.key] = true
		}
		v, ok := lookupDotted(obj, s.key)
		if !ok {
			continue
		}
		w.value(v, at, s.key, req, s.shape)
	}
}

func lookupDotted(obj map[string]any, key string) (any, bool) {
	cur := any(obj)
	for part := range strings.SplitSeq(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// shapeAuto reads a manifest-marked value: one name or a list of names.
const shapeAuto slotShape = -1

// value emits the slot at (at, key) for v read as shape.
func (w *readsWalker) value(v any, at readsPath, key string, req readsPath, shape slotShape) {
	if v == nil {
		return
	}
	var names []string
	bad := ""
	switch shape {
	case shapeOne:
		s, ok := v.(string)
		if !ok {
			bad = "want a field-name string"
		}
		names = nonEmpty(s)
	case shapeList:
		names, bad = stringList(v, "")
	case shapeOrder:
		names, bad = stringList(v, "field")
	case shapeOn:
		names, bad = stringList(v, "left_field")
	case shapeWeight:
		switch x := v.(type) {
		case string:
			names = nonEmpty(x)
		case map[string]any:
			f, ok := x["field"].(string)
			if !ok {
				bad = "want weight.field, a field-name string"
				break
			}
			key += ".field"
			names = nonEmpty(f)
		default:
			bad = "want a field-name string, a {\"field\", \"kind\"} object or null"
		}
	case shapeAuto:
		switch x := v.(type) {
		case string:
			names = nonEmpty(x)
		case []any:
			names, bad = stringList(x, "")
		default:
			bad = "want a field-name string or a list of them"
		}
	}
	if bad != "" {
		w.fail(at, key, req, bad)
		return
	}
	if len(names) == 0 {
		return
	}
	w.emit(at, key, req, dedupe(names), nil)
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// stringList reads a list of names: strings, or (member != "") objects
// carrying the name under member.
func stringList(v any, member string) ([]string, string) {
	list, ok := v.([]any)
	if !ok {
		return nil, "want a list"
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		if member != "" {
			o, ok := e.(map[string]any)
			if !ok {
				return nil, "want a list of objects with " + member
			}
			e = o[member]
		}
		s, ok := e.(string)
		if !ok {
			return nil, "want a list of field-name strings"
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out, ""
}

func dedupe(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// ---- expressions ----------------------------------------------------

// scanExpr reads every expression slot under v (at path at), skipping
// the named top-level keys of v — the nested requests and the data
// blocks (`params`) a caller walks separately or never.
func (w *readsWalker) scanExpr(v map[string]any, at, req readsPath, skip ...string) {
	for _, k := range slices.Sorted(maps.Keys(v)) {
		if slices.Contains(skip, k) {
			continue
		}
		w.scanValue(v[k], at.key(k), at, req)
	}
}

func (w *readsWalker) scanValue(v any, at, scope, req readsPath) {
	switch x := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			w.scanValue(x[k], at.key(k), scope, req)
		}
	case []any:
		for i, e := range x {
			w.scanValue(e, at.at(i), scope, req)
		}
	case string:
		slot, ok := exprslots.Match(at.keys())
		if !ok {
			return
		}
		comp, key := at.split(scope)
		w.expression(x, comp, key, req, slot.Name)
	}
}

func (w *readsWalker) expression(src string, at readsPath, key string, req readsPath, slot string) {
	reads, ce := exprreads.Read(src)
	e := w.emit(at, key, req, nil, nil)
	if ce != nil {
		e.Error = &descriptor.EnvelopeEntry{Code: string(ce.Code), Message: ce.Message, Details: ce.Details}
		return
	}
	e.Params = reads.Params
	e.UsesEnv = reads.UsesEnv
	if len(reads.Funcs) > 0 {
		e.Funcs = reads.Funcs
	}
	if slot == exprslots.SlotOptimizeObjective || slot == exprslots.SlotOptimizeConstraint {
		e.Variables = reads.Vars
		return
	}
	e.Columns = reads.Vars
}

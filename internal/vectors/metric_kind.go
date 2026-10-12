package vectors

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprbuiltins"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/linalg/metric"
	"github.com/frankbardon/pulse/types"
)

// metric_kind.go is the one reading of a vector's `kind` against the
// metrics that read it (PRD FR-10..FR-12), shared by predict
// (internal/descriptor) and the runtime (internal/service):
//
//   - MatrixMetricDefaults: the smart-defaults fill — a metric-taking
//     operator slot reading a kind-declared vector by name and naming no
//     metric gets the kind's default for its consumer (never a poor
//     fit). descriptor.ResolveDefaults applies it, so it honours
//     DisableDefaults and lands in DefaultsApplied exactly as the
//     runtime executes it.
//   - ResolveKindMetric: an operator's metric resolution — the explicit
//     name, else the kind default, else PULSE_VECTOR_METRIC_REQUIRED.
//   - MetricUnsuitedIssues: the PULSE_VECTOR_METRIC_UNSUITED issue list
//     (explicit poor-fit pairs only); the caller decides the channel
//     (warning, or an error under strict).
//
// No kind means no default and no warning.

// MatrixMetricConsumer returns the registry consumer a matrix type
// resolves its params.metric as, and the form it asks for; false for a
// type that takes no metric.
func MatrixMetricConsumer(t types.MatrixType) (metric.Consumer, metric.Form, bool) {
	if t == types.MAT_DISTANCE {
		return metric.ConsumerMatDistance, metric.Distance, true
	}
	return "", "", false
}

// KindOf returns the declared kind of the vector named name in specs
// ("" when it declares none or no spec has that name).
func KindOf(specs []types.VectorSpec, name string) types.VectorKind {
	if name == "" {
		return ""
	}
	for _, s := range specs {
		if s.Name == name {
			return s.Kind
		}
	}
	return ""
}

// KindDefault returns the metric consumer c defaults to for a vector of
// kind k in form f (metric.DefaultFor; admit nil admits every metric):
// the kind's table default when c accepts it, else the first metric c
// accepts that suits the kind. False with no kind or no suitable
// metric.
func KindDefault(c metric.Consumer, k types.VectorKind, f metric.Form, admit func(metric.Metric) bool) (metric.Metric, bool) {
	if k == "" {
		return metric.Metric{}, false
	}
	return metric.DefaultFor(c, metric.Kind(k), f, admit)
}

// ResolveKindMetric resolves an operator's metric for consumer c in
// form f: an explicit name through the registry (metric.ResolveFor —
// PULSE_VECTOR_METRIC_UNKNOWN), else the kind default (defaulted true),
// else PULSE_VECTOR_METRIC_REQUIRED (no kind, or no metric suits it).
func ResolveKindMetric(c metric.Consumer, explicit string, k types.VectorKind, f metric.Form) (m metric.Metric, defaulted bool, cerr *errors.CodedError) {
	if explicit != "" {
		m, err := metric.ResolveFor(c, explicit, f)
		if err != nil {
			return metric.Metric{}, false, asCoded(err)
		}
		return m, false, nil
	}
	if m, ok := KindDefault(c, k, f, nil); ok {
		return m, true, nil
	}
	_, err := metric.ResolveFor(c, "", f)
	return metric.Metric{}, false, asCoded(err)
}

func asCoded(err error) *errors.CodedError {
	if ce, ok := err.(*errors.CodedError); ok {
		return ce
	}
	return errors.NewCodedError(errors.PROCESSING_INTERNAL, err.Error())
}

// MetricDefault is one kind-filled metric on a matrix slot.
type MetricDefault struct {
	// Index is the slot's position in Request.Matrices.
	Index int
	// Vector and Kind are the vector the slot reads and its kind.
	Vector string
	Kind   types.VectorKind
	// Consumer is the registry consumer the slot resolves as.
	Consumer metric.Consumer
	// Metric is the filled metric; TableDefault the kind's own default
	// in the slot's form (they differ when the consumer does not accept
	// the table default).
	Metric, TableDefault string
	// Params are the slot's params with "metric" set.
	Params json.RawMessage
}

// MatrixMetricDefaults returns, in request order, the metric each
// matrix slot defaults to: a metric-taking type (MatrixMetricConsumer)
// reading a kind-declared vector by `vector` whose params are a JSON
// object naming no metric (absent or null). Inline `fields` carry no
// kind and are never filled; malformed params are left for decode to
// refuse. Under MAT_DISTANCE's standardize a non-negative-domain metric
// (aitchison, hellinger) is not admitted — z-scored centroids are
// signed — so the next suitable one is filled.
func MatrixMetricDefaults(req *types.Request) []MetricDefault {
	if req == nil || len(req.Vectors) == 0 {
		return nil
	}
	var out []MetricDefault
	for i, spec := range req.Matrices {
		c, f, ok := MatrixMetricConsumer(spec.Type)
		if !ok || spec.Vector == "" {
			continue
		}
		kind := KindOf(req.Vectors, spec.Vector)
		if kind == "" {
			continue
		}
		params := map[string]json.RawMessage{}
		if raw := bytes.TrimSpace(spec.Params); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
			if json.Unmarshal(raw, &params) != nil {
				continue
			}
		}
		if v, set := params["metric"]; set && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			continue
		}
		var standardize bool
		if v, set := params["standardize"]; set {
			_ = json.Unmarshal(v, &standardize)
		}
		var admit func(metric.Metric) bool
		if standardize {
			admit = func(m metric.Metric) bool { return m.Domain() != metric.DomainNonNegative }
		}
		m, ok := KindDefault(c, kind, f, admit)
		if !ok {
			continue
		}
		name, _ := json.Marshal(m.Name())
		params["metric"] = name
		raw, err := json.Marshal(params)
		if err != nil {
			continue
		}
		d := MetricDefault{Index: i, Vector: spec.Vector, Kind: kind, Consumer: c, Metric: m.Name(), Params: raw}
		if td, ok := metric.Default(metric.Kind(kind), f); ok {
			d.TableDefault = td.Name()
		}
		out = append(out, d)
	}
	return out
}

// AttributeMetricDefault is one kind-filled metric on a comparison
// attribute slot.
type AttributeMetricDefault struct {
	// Index is the slot's position in Request.Attributes.
	Index int
	// Vector and Kind are the vector the slot reads and its kind.
	Vector string
	Kind   types.VectorKind
	// Metric is the filled metric; TableDefault the kind's own default
	// in the slot's form (they differ when the operator does not accept
	// the table default).
	Metric, TableDefault string
	// Params are the slot's params with "metric" set.
	Params json.RawMessage
}

// AttributeMetricDefaults returns, in request order, the metric each
// comparison attribute slot (ATTR_PROFILE_SIMILARITY, ATTR_VEC_DISTANCE)
// defaults to: one reading a kind-declared vector by `vector` whose
// params are a JSON object naming no metric (absent or null). The fill
// is ResolveKindMetric's own default, so the slot executes the metric
// reported whether or not the fill is applied. Inline `fields` carry no
// kind and are never filled; malformed params are left for decode to
// refuse.
func AttributeMetricDefaults(req *types.Request) []AttributeMetricDefault {
	if req == nil || len(req.Vectors) == 0 {
		return nil
	}
	var out []AttributeMetricDefault
	for i, a := range req.Attributes {
		if a == nil || !IsComparisonAttribute(a.Type) || a.Vector == "" {
			continue
		}
		kind := KindOf(req.Vectors, a.Vector)
		if kind == "" {
			continue
		}
		params := map[string]json.RawMessage{}
		if raw := bytes.TrimSpace(a.Params); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
			if json.Unmarshal(raw, &params) != nil {
				continue
			}
		}
		if v, set := params["metric"]; set && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			continue
		}
		c, f, _ := ComparisonConsumer(a.Type)
		m, ok := KindDefault(c, kind, f, nil)
		if !ok {
			continue
		}
		name, _ := json.Marshal(m.Name())
		params["metric"] = name
		raw, err := json.Marshal(params)
		if err != nil {
			continue
		}
		d := AttributeMetricDefault{Index: i, Vector: a.Vector, Kind: kind, Metric: m.Name(), Params: raw}
		if td, ok := metric.Default(metric.Kind(kind), f); ok {
			d.TableDefault = td.Name()
		}
		out = append(out, d)
	}
	return out
}

// MetricUnsuitedIssues returns one PULSE_VECTOR_METRIC_UNSUITED issue
// per explicit poor-fit pair in req (metric.Metric.PoorFitFor: cosine
// on scale / measure, the Euclidean family on composition), in request
// order: every metric-taking matrix slot reading a kind-declared vector
// by name, every comparison attribute (ATTR_PROFILE_SIMILARITY,
// ATTR_VEC_DISTANCE) doing the same, then every expression call of a metric built-in
// (exprbuiltins.Metric) whose argument IS a kind-declared vector — the
// filterers' FILTER_EXPRESSION, each attribute's ATTR_FORMULA and its
// params.fit_on filterers, the slots exprReferenced scans. A wrapped
// argument (cosine(vcenter(v), …)) is a transform of the vector, not
// the vector, and is not judged. A defaulted metric is never a poor fit
// (KindDefault), and a vector without a kind raises nothing. The caller
// decides the channel: a warning, or under strict an error.
func MetricUnsuitedIssues(req *types.Request) []*errors.CodedError {
	if req == nil || len(req.Vectors) == 0 {
		return nil
	}
	kinds := map[string]types.VectorKind{}
	for _, v := range req.Vectors {
		if v.Kind != "" {
			kinds[v.Name] = v.Kind
		}
	}
	if len(kinds) == 0 {
		return nil
	}
	var out []*errors.CodedError
	for i, spec := range req.Matrices {
		c, f, ok := MatrixMetricConsumer(spec.Type)
		kind := kinds[spec.Vector]
		if !ok || kind == "" {
			continue
		}
		var p struct {
			Metric *string `json:"metric"`
		}
		if raw := bytes.TrimSpace(spec.Params); len(raw) == 0 || json.Unmarshal(raw, &p) != nil || p.Metric == nil {
			continue
		}
		if w := unsuited(matrixPath(i)+".params.metric", spec.Vector, kind, *p.Metric, f, c, ""); w != nil {
			out = append(out, w)
		}
	}
	for i, a := range req.Attributes {
		if a == nil || !IsComparisonAttribute(a.Type) {
			continue
		}
		kind := kinds[a.Vector]
		if kind == "" {
			continue
		}
		c, f, _ := ComparisonConsumer(a.Type)
		var p struct {
			Metric *string `json:"metric"`
		}
		if raw := bytes.TrimSpace(a.Params); len(raw) == 0 || json.Unmarshal(raw, &p) != nil || p.Metric == nil {
			continue
		}
		if w := unsuited(attributePath(i)+".params.metric", a.Vector, kind, *p.Metric, f, c, ""); w != nil {
			out = append(out, w)
		}
	}
	scan := func(slot, src string) {
		if src == "" {
			return
		}
		calls, _ := exprident.Calls(src)
		seen := map[string]bool{}
		for _, call := range calls {
			mf, ok := exprbuiltins.Metric(call.Name)
			if !ok {
				continue
			}
			for _, arg := range call.Args {
				kind := kinds[arg]
				key := call.Name + "\x00" + arg
				if kind == "" || seen[key] {
					continue
				}
				seen[key] = true
				if w := unsuited(slot, arg, kind, mf.Metric, metric.Form(mf.Form), metric.ConsumerExpr, call.Name); w != nil {
					out = append(out, w)
				}
			}
		}
	}
	for _, slot := range ExprSlots(req) {
		scan(slot.Path, slot.Src)
	}
	return out
}

// unsuited builds the PULSE_VECTOR_METRIC_UNSUITED issue for metric
// name read over vector (of kind) at slot, or nil when it fits (or is
// not a registered metric — the consumer's own resolution refuses
// that). fn is the expression function ("" for an operator slot).
func unsuited(slot, vector string, kind types.VectorKind, name string, f metric.Form, c metric.Consumer, fn string) *errors.CodedError {
	m, ok := metric.Lookup(name)
	if !ok || !m.PoorFitFor(metric.Kind(kind)) {
		return nil
	}
	details := map[string]any{
		"vector":   vector,
		"kind":     string(kind),
		"metric":   name,
		"slot":     slot,
		"form":     string(f),
		"consumer": string(c),
	}
	var suggested string
	if c == metric.ConsumerExpr {
		details["function"] = fn
		if d, ok := metric.Default(metric.Kind(kind), f); ok {
			suggested = d.Name()
		}
	} else if d, ok := KindDefault(c, kind, f, nil); ok {
		suggested = d.Name()
	}
	what := slot + " metric " + strconv.Quote(name)
	if fn != "" {
		what = slot + ": " + fn + "(" + vector + ", …) evaluates " + strconv.Quote(name) + ", which"
	}
	msg := what + " fits kind " + strconv.Quote(string(kind)) + " vector " + strconv.Quote(vector) + " poorly"
	if suggested != "" {
		details["suggested"] = suggested
		msg += "; the " + string(kind) + " " + string(f) + " default is " + strconv.Quote(suggested)
	} else {
		msg += "; kind " + strconv.Quote(string(kind)) + " has no " + string(f) + " default"
	}
	if fn == "cosine" && kind == types.VectorKindScale {
		msg += " (in an expression, cosine(vcenter(a), vcenter(b)) is that correlation)"
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_METRIC_UNSUITED, msg, details)
}

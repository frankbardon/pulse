package vectors

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/linalg/metric"
	"github.com/frankbardon/pulse/types"
)

// compare.go is the one reading of the reference-comparison attributes
// — ATTR_PROFILE_SIMILARITY and ATTR_VEC_DISTANCE, one engine split by
// direction — shared by predict (the field-reference walk and
// ReferenceIssues), the runtime (Processor.prepare's canonical rewrite,
// the factory) and projection (ComparisonKeyField):
//
//   - The metric resolves through the linalg/metric registry for the
//     operator's consumer in its one form (similarity / distance): the
//     explicit params.metric, else the vector's kind default
//     (ResolveKindMetric), else PULSE_VECTOR_METRIC_REQUIRED. A metric of
//     the other direction is PULSE_VECTOR_METRIC_UNKNOWN naming the
//     other operator (details.use) — never transformed silently.
//   - params.reference is a literal array (member order), a label map
//     (matched by label), {table, key} or {table, key_field} over
//     Extensions.ReferenceVectors (matched by label: ReferenceTable.Match),
//     a centroid {centroid: {filter?, by?}} (the per-element weighted
//     mean of the filtered rows params.reference.centroid.filter admits —
//     AGG_VEC_MEAN's figure — one per by value when by names a
//     categorical / unsigned-integer column, each row compared with its
//     own key's), {population: name} (a bound vector fit's stored
//     mean, aligned by label: AlignFit), or {record: {cohort?, keys}}
//     (one stored record, point-looked-up through the cohort's sidecar
//     index by the Service before the processor builds and bound from
//     the request's model.Set — matched by member column name, a null
//     member a missing element). A literal of the wrong length
//     or a label map / table / population whose labels are not the
//     vector's is PULSE_VECTOR_REFERENCE_MISMATCH; an unregistered table
//     or key, or an unbound population, PULSE_VECTOR_REFERENCE_UNKNOWN.
//     The table checks need the instance's tables (ComparisonTableIssue),
//     the population checks the bound fits (PopulationRefusal, AlignFit);
//     the rest only the members. A centroid reference needs a pre-pass
//     (ComparisonTwoPass) for either operator and any metric.
//   - params.min_valid is the fewest complete member pairs a row needs
//     (default 1); below it the row is null.
//   - ATTR_VEC_DISTANCE's fitted metrics (std_euclidean, mahalanobis)
//     read ATTR_MAHALANOBIS's fit pass: params.fit_on / missing
//     (FittedCommon), refused beside a row-local metric.
//   - params.labels names inline fields' elements (one per member,
//     unique) so a label map or table can align to them; refused beside
//     a named vector, whose own labels apply.
//
// Shape refusals are SERVICE_VALIDATION naming `attribute` (the slot
// path) and `reason`.

// ComparisonTypes returns the reference-comparison attribute types, in
// AllAttributeTypes order.
func ComparisonTypes() []types.AttributeType {
	return []types.AttributeType{types.ATTR_PROFILE_SIMILARITY, types.ATTR_VEC_DISTANCE}
}

// IsComparisonAttribute reports whether t is a reference-comparison
// attribute type.
func IsComparisonAttribute(t types.AttributeType) bool {
	return t == types.ATTR_PROFILE_SIMILARITY || t == types.ATTR_VEC_DISTANCE
}

// ComparisonConsumer returns the registry consumer comparison type t
// resolves its metric as, the form it asks for, and the operator that
// serves the other direction.
func ComparisonConsumer(t types.AttributeType) (metric.Consumer, metric.Form, types.AttributeType) {
	if t == types.ATTR_VEC_DISTANCE {
		return metric.ConsumerVecDistance, metric.Distance, types.ATTR_PROFILE_SIMILARITY
	}
	return metric.ConsumerProfileSimilarity, metric.Similarity, types.ATTR_VEC_DISTANCE
}

// The params.reference forms.
const (
	// ReferenceValues: a literal array, one value per member in member
	// order.
	ReferenceValues = "values"
	// ReferenceLabelMap: an object of label → value, matched by label.
	ReferenceLabelMap = "label_map"
	// ReferenceTableKey: {table, key} — one registered profile.
	ReferenceTableKey = "table_key"
	// ReferenceTableKeyField: {table, key_field} — each row's own
	// profile, keyed by its key_field value (a categorical label, or an
	// unsigned integer's base-10 form).
	ReferenceTableKeyField = "table_key_field"
	// ReferenceCentroid: {centroid: {filter?, by?}} — the per-element
	// weighted mean of the filtered rows the centroid filter admits
	// (a pre-pass); with by, one centroid per by value, each row read
	// against its own key's.
	ReferenceCentroid = "centroid"
	// ReferencePopulation: {population: name} — a bound vector fit's
	// stored mean (a request / registered population or an in-batch
	// Compose vector fit), aligned by label.
	ReferencePopulation = "population"
	// ReferenceRecord: {record: {cohort?, keys}} — one stored record of
	// a cohort (the request's own when cohort is omitted), resolved by
	// a point lookup over the cohort's sidecar index before the
	// processor builds; its member columns are the reference.
	ReferenceRecord = "record"
)

// ReferenceReasonRecordMembersAbsent is the
// PULSE_VECTOR_REFERENCE_MISMATCH reason of a {record} reference whose
// cohort lacks a member column (or holds it as a type no vector member
// may be).
const ReferenceReasonRecordMembersAbsent = "record_members_absent"

// ReferenceReasonPopulationUnknown is the PULSE_VECTOR_REFERENCE_UNKNOWN
// reason of a {population} reference naming no bound vector fit.
const ReferenceReasonPopulationUnknown = "population_unknown"

// ReferenceReasonLengthDiffers is the PULSE_VECTOR_REFERENCE_MISMATCH
// reason of a literal reference whose length is not the member count.
const ReferenceReasonLengthDiffers = "length_differs"

// ComparisonReference is a decoded params.reference.
type ComparisonReference struct {
	// Form is one of ReferenceValues, ReferenceLabelMap,
	// ReferenceTableKey, ReferenceTableKeyField.
	Form string
	// Values is the reference in member order (ReferenceValues and
	// ReferenceLabelMap); nil for a table form.
	Values []float64
	// Table, Key and KeyField are the table forms' operands.
	Table, Key, KeyField string
	// Filter is a centroid's decoded filter (nil: every filtered row),
	// FilterRaw it as written; By the column a per-key centroid reads.
	Filter    []*types.Filterer
	FilterRaw json.RawMessage
	By        string
	// Population names the bound vector fit a {population} form reads.
	Population string
	// Cohort and Keys are a {record} form's operands: the cohort the
	// record is looked up in (nil: the request's own) and the ordered
	// key tuple of its sidecar index.
	Cohort *types.Cohort
	Keys   []types.LookupKey
}

// RecordKey is the key a {record} reference's resolved record binds
// under on the request's model.Set (model.Set.LookupRecord): the cohort
// as written (absent when omitted) and the keys in order, canonical
// JSON. The Service (which resolves) and the engine (which binds) both
// derive it from the decoded reference.
func (r ComparisonReference) RecordKey() string {
	b, err := json.Marshal(recordWire{Cohort: r.Cohort, Keys: r.Keys})
	if err != nil {
		// Unreachable: strings only.
		return ""
	}
	return string(b)
}

// recordWire is the {record} operand (strict decode; CanonicalParams
// writes it back).
type recordWire struct {
	Cohort *types.Cohort     `json:"cohort,omitempty"`
	Keys   []types.LookupKey `json:"keys"`
}

// Static reports whether the reference is the same for every row (every
// form but ReferenceTableKeyField and a centroid with by).
func (r ComparisonReference) Static() bool {
	return r.Form != ReferenceTableKeyField && !(r.Form == ReferenceCentroid && r.By != "")
}

// KeyColumn is the column a per-row reference keys on: the table form's
// key_field, or a centroid's by ("" for every static form).
func (r ComparisonReference) KeyColumn() string {
	switch r.Form {
	case ReferenceTableKeyField:
		return r.KeyField
	case ReferenceCentroid:
		return r.By
	}
	return ""
}

// ComparisonConfig is a comparison attribute's params judged against
// its resolved members.
type ComparisonConfig struct {
	// Type is ATTR_PROFILE_SIMILARITY or ATTR_VEC_DISTANCE.
	Type types.AttributeType
	// Metric is the resolved metric; Defaulted reports it came from the
	// vector's kind.
	Metric    metric.Metric
	Defaulted bool
	// MinValid is the fewest complete member pairs a row needs (default
	// 1).
	MinValid int
	// Labels are the element labels the reference aligns to: params.labels,
	// else the named vector's labels (the member names for inline fields).
	Labels []string
	// Vector is the named vector compared ("" for inline fields).
	Vector string
	// Reference is the decoded params.reference.
	Reference ComparisonReference
	// FitOn is the decoded params.fit_on (a fitted metric only); nil
	// fits on every filtered row. FitOnRaw is it as written.
	FitOn    []*types.Filterer
	FitOnRaw json.RawMessage
	// Fit names the bound vector fit a fitted metric reads instead of
	// fitting ("" folds this request's own fit); it aligns to Labels.
	Fit string
}

// Fitted reports whether the metric reads a fit pass (std_euclidean,
// mahalanobis).
func (cfg ComparisonConfig) Fitted() bool { return cfg.Metric.Fitted() }

// comparisonParams is the wire params (strict decode).
type comparisonParams struct {
	Metric    *string         `json:"metric"`
	MinValid  *json.Number    `json:"min_valid"`
	Reference json.RawMessage `json:"reference"`
	Labels    []string        `json:"labels"`
}

// vecDistanceParams adds the fit keys ATTR_VEC_DISTANCE takes.
type vecDistanceParams struct {
	comparisonParams
	FittedCommon
}

// ComparisonParams judges comparison type typ's params against members
// (the resolved vector or inline fields) and schema (key_field must be a
// categorical or unsigned-integer source column,
// ComparisonKeyFieldAccepts; nil skips that check): metric, min_valid,
// reference, labels and — ATTR_VEC_DISTANCE beside a fitted metric —
// fit_on / missing. Unknown keys are refused. The table checks are
// ComparisonTableIssue's. Predict (the field-reference walk) and the
// runtime (Processor.prepare, the factory) both call it.
func ComparisonParams(at string, typ types.AttributeType, raw json.RawMessage, members Resolved, schema *encoding.Schema) (ComparisonConfig, *errors.CodedError) {
	p := len(members.Members)
	cfg := ComparisonConfig{Type: typ, MinValid: 1, Vector: members.Name}
	var params vecDistanceParams
	if typ == types.ATTR_VEC_DISTANCE {
		paramread.Declare(paramread.Attribute, string(typ), &params)
	} else {
		paramread.Declare(paramread.Attribute, string(typ), &params.comparisonParams)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		var err error
		if typ == types.ATTR_VEC_DISTANCE {
			err = paramread.DecodeStrict(paramread.Attribute, string(typ), raw, &params)
		} else {
			err = paramread.DecodeStrict(paramread.Attribute, string(typ), raw, &params.comparisonParams)
		}
		if err != nil {
			keys := "metric, min_valid, reference, labels"
			if typ == types.ATTR_VEC_DISTANCE {
				keys += ", fit_on, fit, missing"
			}
			return ComparisonConfig{}, attributeInvalid(at, "bad_params",
				at+" "+string(typ)+" params: "+err.Error()+" (keys: "+keys+")", nil)
		}
	}
	// Labels: a named vector's own, or params.labels over inline fields.
	labels, lerr := slotLabels(at, params.Labels, members)
	if lerr != nil {
		return ComparisonConfig{}, lerr
	}
	cfg.Labels = labels

	// The reference first (a draft without one is refused naming it),
	// then the metric.
	ref, rerr := decodeComparisonReference(at, params.Reference, cfg.Labels, members.Name, schema)
	if rerr != nil {
		return ComparisonConfig{}, rerr
	}
	cfg.Reference = ref

	m, defaulted, merr := comparisonMetric(at, typ, params.Metric, members.Kind)
	if merr != nil {
		return ComparisonConfig{}, merr
	}
	cfg.Metric, cfg.Defaulted = m, defaulted

	if params.MinValid != nil {
		k, ok := positiveInt(*params.MinValid)
		if !ok || k > p {
			return ComparisonConfig{}, attributeInvalid(at, "bad_params",
				at+" params.min_valid must be an integer from 1 to the member count ("+strconv.Itoa(p)+")",
				map[string]any{"param": "min_valid", "value": params.MinValid.String(), "members": p})
		}
		cfg.MinValid = k
	}

	if typ == types.ATTR_VEC_DISTANCE {
		fitOn, err := params.resolve(at)
		if err != nil {
			return ComparisonConfig{}, err
		}
		fit, err := params.resolveFit(at)
		if err != nil {
			return ComparisonConfig{}, err
		}
		set := len(bytes.TrimSpace(params.FitOn)) > 0 || params.Missing != nil || params.Fit != nil
		if set && !m.Fitted() {
			return ComparisonConfig{}, attributeInvalid(at, "fit_on_row_local",
				at+" params.fit_on / fit / missing apply only to a fitted metric (std_euclidean, mahalanobis); metric "+strconv.Quote(m.Name())+" reads no fit",
				map[string]any{"param": "fit_on", "metric": m.Name()})
		}
		cfg.FitOn, cfg.Fit = fitOn, fit
		if fr := bytes.TrimSpace(params.FitOn); len(fr) > 0 && !bytes.Equal(fr, []byte("null")) {
			cfg.FitOnRaw = slices.Clone(json.RawMessage(fr))
		}
	}

	return cfg, nil
}

// comparisonMetric resolves typ's metric (explicit, else the kind
// default), naming the other operator when the explicit name serves the
// other direction.
func comparisonMetric(at string, typ types.AttributeType, explicit *string, kind types.VectorKind) (metric.Metric, bool, *errors.CodedError) {
	c, f, other := ComparisonConsumer(typ)
	name := ""
	if explicit != nil {
		name = *explicit
		if name == "" {
			return metric.Metric{}, false, attributeInvalid(at, "bad_params", at+" params.metric is empty; omit it to take the vector kind's default",
				map[string]any{"param": "metric"})
		}
	}
	m, defaulted, cerr := ResolveKindMetric(c, name, kind, f)
	if cerr == nil {
		return m, defaulted, nil
	}
	if cerr.Details == nil {
		cerr.Details = map[string]any{}
	}
	cerr.Details["attribute"] = at
	cerr.Details["operator"] = string(typ)
	if name == "" {
		cerr.Message = at + " (" + string(typ) + ") needs params.metric, or a vector declaring a kind whose " + string(f) + " default applies: " + cerr.Message
		if kind != "" {
			cerr.Details["kind"] = string(kind)
		}
		return metric.Metric{}, false, cerr
	}
	oc, of, _ := ComparisonConsumer(other)
	if _, err := metric.ResolveFor(oc, name, of); err == nil {
		cerr.Details["use"] = string(other)
		cerr.Message = at + " metric " + strconv.Quote(name) + " is a " + string(of) + " here, and " + string(typ) +
			" answers only in the " + string(f) + " direction (never converted); use " + string(other) + " for it — " + cerr.Message
		return metric.Metric{}, false, cerr
	}
	cerr.Message = at + " (" + string(typ) + "): " + cerr.Message
	return metric.Metric{}, false, cerr
}

// decodeComparisonReference decodes params.reference against the
// compared vector's labels (vector its name, "" for inline fields).
func decodeComparisonReference(at string, raw json.RawMessage, labels []string, vector string, schema *encoding.Schema) (ComparisonReference, *errors.CodedError) {
	raw = bytes.TrimSpace(raw)
	shapes := "a literal array (member order), a label map {label: value}, {table, key}, {table, key_field}, {centroid: {filter, by}}, {population: name} or {record: {cohort, keys}}"
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ComparisonReference{}, attributeInvalid(at, "reference_required",
			at+" params.reference is required: "+shapes, map[string]any{"param": "reference"})
	}
	bad := func(msg string) (ComparisonReference, *errors.CodedError) {
		return ComparisonReference{}, attributeInvalid(at, "bad_reference", at+" params.reference "+msg+"; it is "+shapes,
			map[string]any{"param": "reference"})
	}
	switch raw[0] {
	case '[':
		var vals []*float64
		if err := json.Unmarshal(raw, &vals); err != nil {
			return bad("does not decode as an array of numbers: " + err.Error())
		}
		out := make([]float64, len(vals))
		for i, v := range vals {
			if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
				return bad("element " + strconv.Itoa(i) + " is not a finite number")
			}
			out[i] = *v
		}
		if len(out) != len(labels) {
			ce := ReferenceMismatch("", vector, nil, labels, nil, nil)
			ce.Details["reason"] = ReferenceReasonLengthDiffers
			ce.Details["reference_length"] = len(out)
			ce.Details["members"] = len(labels)
			delete(ce.Details, "reference_labels")
			delete(ce.Details, "missing")
			delete(ce.Details, "extra")
			ce.Details["attribute"] = at
			ce.Message = at + " params.reference lists " + strconv.Itoa(len(out)) + " values for " + strconv.Itoa(len(labels)) +
				" members; a literal array is read in member order, one value per member (or use a label map)"
			return ComparisonReference{}, ce
		}
		return ComparisonReference{Form: ReferenceValues, Values: out}, nil
	case '{':
	default:
		return bad("is neither an array nor an object")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return bad("does not decode: " + err.Error())
	}
	// The reserved forms are told from a label map by their operand's
	// shape (a label map's values are numbers): table / population a
	// string, centroid / record an object.
	if t, ok := obj["table"]; ok && len(bytes.TrimSpace(t)) > 0 && bytes.TrimSpace(t)[0] == '"' {
		return decodeTableReference(at, obj, schema)
	}
	if c, ok := obj[ReferenceCentroid]; ok && len(bytes.TrimSpace(c)) > 0 && bytes.TrimSpace(c)[0] == '{' {
		return decodeCentroidReference(at, obj, schema)
	}
	if v, ok := obj[ReferencePopulation]; ok && len(bytes.TrimSpace(v)) > 0 && bytes.TrimSpace(v)[0] == '"' {
		return decodePopulationReference(at, obj)
	}
	if v, ok := obj[ReferenceRecord]; ok && len(bytes.TrimSpace(v)) > 0 && bytes.TrimSpace(v)[0] == '{' {
		return decodeRecordReference(at, obj)
	}
	// A label map: every value a finite number, the label set the
	// vector's.
	byLabel := make(map[string]float64, len(obj))
	mapLabels := make([]string, 0, len(obj))
	for l, v := range obj {
		var f *float64
		if err := json.Unmarshal(v, &f); err != nil || f == nil || math.IsNaN(*f) || math.IsInf(*f, 0) {
			return bad("label " + strconv.Quote(l) + " is not a finite number")
		}
		byLabel[l] = *f
		mapLabels = append(mapLabels, l)
	}
	sort.Strings(mapLabels)
	var missing, extra []string
	values := make([]float64, len(labels))
	for i, l := range labels {
		v, ok := byLabel[l]
		if !ok {
			missing = append(missing, l)
			continue
		}
		values[i] = v
	}
	for _, l := range mapLabels {
		if !slices.Contains(labels, l) {
			extra = append(extra, l)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		ce := ReferenceMismatch("", vector, mapLabels, labels, missing, extra)
		ce.Details["attribute"] = at
		ce.Message = at + " params.reference: " + ce.Message
		return ComparisonReference{}, ce
	}
	return ComparisonReference{Form: ReferenceLabelMap, Values: values}, nil
}

// decodeTableReference decodes the {table, key} / {table, key_field}
// forms (strict keys).
func decodeTableReference(at string, obj map[string]json.RawMessage, schema *encoding.Schema) (ComparisonReference, *errors.CodedError) {
	bad := func(reason, msg string, extra map[string]any) (ComparisonReference, *errors.CodedError) {
		details := map[string]any{"param": "reference"}
		for k, v := range extra {
			details[k] = v
		}
		return ComparisonReference{}, attributeInvalid(at, reason, at+" params.reference "+msg, details)
	}
	str := func(k string) (string, bool, bool) {
		v, ok := obj[k]
		if !ok {
			return "", false, true
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", true, false
		}
		return s, true, true
	}
	for k := range obj {
		if k != "table" && k != "key" && k != "key_field" {
			return bad("bad_reference", "names unknown key "+strconv.Quote(k)+" (a table reference reads table and exactly one of key, key_field)", map[string]any{"key": k})
		}
	}
	table, _, _ := str("table")
	key, hasKey, okKey := str("key")
	field, hasField, okField := str("key_field")
	switch {
	case table == "":
		return bad("bad_reference", "names an empty table", nil)
	case !okKey || !okField:
		return bad("bad_reference", "key and key_field are strings", nil)
	case hasKey == hasField:
		return bad("bad_reference", "sets exactly one of key (one profile) and key_field (each row's own, by a categorical or unsigned-integer column)", nil)
	case hasKey:
		if key == "" {
			return bad("bad_reference", "names an empty key", nil)
		}
		return ComparisonReference{Form: ReferenceTableKey, Table: table, Key: key}, nil
	}
	if field == "" {
		return bad("bad_reference", "names an empty key_field", nil)
	}
	if schema != nil {
		f := schema.Field(field)
		if f == nil {
			return bad("key_field_unknown", "key_field "+strconv.Quote(field)+" is not a field of the cohort", map[string]any{"field": field})
		}
		if !ComparisonKeyFieldAccepts(f.Type) {
			return bad("key_field_type", "key_field "+strconv.Quote(field)+" is "+f.Type.String()+"; it must be a categorical field (its label is the profile key) or an unsigned integer field (u4, u8, u16, u32, u64: its base-10 value is the key)",
				map[string]any{"field": field, "field_type": f.Type.String(), "accepted": ComparisonKeyFieldTypes()})
		}
	}
	return ComparisonReference{Form: ReferenceTableKeyField, Table: table, KeyField: field}, nil
}

// decodeCentroidReference decodes {centroid: {filter?, by?}} (strict
// keys at both levels): filter an inline filterer array (absent: every
// filtered row), by a categorical or unsigned-integer column (the
// key_field rule).
func decodeCentroidReference(at string, obj map[string]json.RawMessage, schema *encoding.Schema) (ComparisonReference, *errors.CodedError) {
	bad := func(reason, msg string, extra map[string]any) (ComparisonReference, *errors.CodedError) {
		details := map[string]any{"param": "reference"}
		for k, v := range extra {
			details[k] = v
		}
		return ComparisonReference{}, attributeInvalid(at, reason, at+" params.reference "+msg, details)
	}
	for k := range obj {
		if k != ReferenceCentroid {
			return bad("bad_reference", "names unknown key "+strconv.Quote(k)+" beside centroid (a centroid reference is {centroid: {filter, by}} alone)", map[string]any{"key": k})
		}
	}
	var inner struct {
		Filter json.RawMessage `json:"filter"`
		By     *string         `json:"by"`
	}
	if err := decodeStrict(obj[ReferenceCentroid], &inner); err != nil {
		return bad("bad_reference", "centroid does not decode: "+err.Error()+" (keys: filter, by)", nil)
	}
	ref := ComparisonReference{Form: ReferenceCentroid}
	if raw := bytes.TrimSpace(inner.Filter); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		var fs []*types.Filterer
		if raw[0] != '[' || decodeStrict(raw, &fs) != nil {
			return bad("bad_reference", "centroid.filter must be an array of filterers", nil)
		}
		if len(fs) == 0 {
			return bad("bad_reference", "centroid.filter is an empty filterer list; omit it to take the centroid of every filtered row", nil)
		}
		for j, f := range fs {
			if f == nil || f.Type == "" {
				return bad("bad_reference", "centroid.filter["+strconv.Itoa(j)+"] needs a filterer type", map[string]any{"index": j})
			}
		}
		ref.Filter, ref.FilterRaw = fs, slices.Clone(json.RawMessage(raw))
	}
	if inner.By != nil {
		by := *inner.By
		if by == "" {
			return bad("bad_reference", "names an empty centroid.by", nil)
		}
		if schema != nil {
			f := schema.Field(by)
			if f == nil {
				return bad("by_unknown", "centroid.by "+strconv.Quote(by)+" is not a field of the cohort", map[string]any{"field": by})
			}
			if !ComparisonKeyFieldAccepts(f.Type) {
				return bad("by_type", "centroid.by "+strconv.Quote(by)+" is "+f.Type.String()+"; it must be a categorical field (one centroid per label) or an unsigned integer field (u4, u8, u16, u32, u64: one per value)",
					map[string]any{"field": by, "field_type": f.Type.String(), "accepted": ComparisonKeyFieldTypes()})
			}
		}
		ref.By = by
	}
	return ref, nil
}

// decodePopulationReference decodes {population: name} (strict keys).
func decodePopulationReference(at string, obj map[string]json.RawMessage) (ComparisonReference, *errors.CodedError) {
	for k := range obj {
		if k != ReferencePopulation {
			return ComparisonReference{}, attributeInvalid(at, "bad_reference",
				at+" params.reference names unknown key "+strconv.Quote(k)+" beside population (a population reference is {population: name} alone)",
				map[string]any{"param": "reference", "key": k})
		}
	}
	var name string
	if err := json.Unmarshal(obj[ReferencePopulation], &name); err != nil || name == "" {
		return ComparisonReference{}, attributeInvalid(at, "bad_reference",
			at+" params.reference.population must name a bound vector fit (a population or a Compose vector fit)",
			map[string]any{"param": "reference"})
	}
	return ComparisonReference{Form: ReferencePopulation, Population: name}, nil
}

// decodeRecordReference decodes {record: {cohort?, keys}} (strict keys
// at every level): keys a non-empty ordered list of {field, value}
// (value text, the point-lookup convention), field names non-empty and
// distinct; cohort, when present, a {filename, data_dir} with a
// filename. Only the shape is judged here — the lookup itself (the
// index, the match, its uniqueness) is the Service's, at run time.
func decodeRecordReference(at string, obj map[string]json.RawMessage) (ComparisonReference, *errors.CodedError) {
	bad := func(msg string, extra map[string]any) (ComparisonReference, *errors.CodedError) {
		details := map[string]any{"param": "reference"}
		for k, v := range extra {
			details[k] = v
		}
		return ComparisonReference{}, attributeInvalid(at, "bad_reference", at+" params.reference "+msg, details)
	}
	for k := range obj {
		if k != ReferenceRecord {
			return bad("names unknown key "+strconv.Quote(k)+" beside record (a record reference is {record: {cohort, keys}} alone)", map[string]any{"key": k})
		}
	}
	var inner recordWire
	if err := decodeStrict(obj[ReferenceRecord], &inner); err != nil {
		return bad("record does not decode: "+err.Error()+" (keys: cohort {filename, data_dir}, keys [{field, value}] — each value text)", nil)
	}
	if len(inner.Keys) == 0 {
		return bad("record.keys is empty; list the key columns of the cohort's sidecar index, in index order, each {field, value}", nil)
	}
	seen := make(map[string]bool, len(inner.Keys))
	for j, k := range inner.Keys {
		if k.Field == "" {
			return bad("record.keys["+strconv.Itoa(j)+"] names an empty field", map[string]any{"index": j})
		}
		if seen[k.Field] {
			return bad("record.keys["+strconv.Itoa(j)+"] repeats field "+strconv.Quote(k.Field), map[string]any{"index": j, "field": k.Field})
		}
		seen[k.Field] = true
	}
	if inner.Cohort != nil && inner.Cohort.Filename == "" {
		return bad("record.cohort names no filename; omit cohort to look the record up in the request's own cohort", nil)
	}
	return ComparisonReference{Form: ReferenceRecord, Cohort: inner.Cohort, Keys: slices.Clone(inner.Keys)}, nil
}

// RecordMembersAbsent is the PULSE_VECTOR_REFERENCE_MISMATCH refusal of
// a {record} reference on slot at whose cohort holds no usable column
// for the members listed in missing (absent, or a type no vector member
// may be: categorical, date, set, …).
func RecordMembersAbsent(at, cohort string, missing []string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_REFERENCE_MISMATCH,
		at+" params.reference: the stored record's cohort "+strconv.Quote(cohort)+" holds no numeric column for member(s) "+strconv.Quote(strings.Join(missing, ", "))+
			"; a record reference is read by member column name, so its cohort must carry every member as an integer, float, packed_bool or vector-element column",
		map[string]any{"reason": ReferenceReasonRecordMembersAbsent, "attribute": at, "slot": at + ".params.reference",
			"cohort": cohort, "missing": slices.Clone(missing)})
}

// PopulationRefusal locates a {population} reference's lookup refusal
// (the runtime's model.Set.LookupVector or predict's
// model.CheckVectorFit) on slot at: an unbound name is
// PULSE_VECTOR_REFERENCE_UNKNOWN reason population_unknown (the bound
// vector fits under details.valid); any other refusal (a name bound as
// a regression: PULSE_MODEL_KIND_MISMATCH) keeps its code. Both carry
// details.attribute, details.population and details.slot.
func PopulationRefusal(at, name string, ce *errors.CodedError) *errors.CodedError {
	if ce.Code == errors.PULSE_MODEL_UNKNOWN {
		valid, _ := ce.Details["available"].([]string)
		if valid == nil {
			valid = []string{}
		}
		ce = errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_REFERENCE_UNKNOWN,
			"population "+strconv.Quote(name)+" is not bound (populations: the request's populations, the registered ones, or a Compose vector fit)",
			map[string]any{"reason": ReferenceReasonPopulationUnknown, "valid": valid})
	}
	if ce.Details == nil {
		ce.Details = map[string]any{}
	}
	ce.Details["attribute"] = at
	ce.Details["population"] = name
	ce.Details["slot"] = at + ".params.reference"
	ce.Message = at + " params.reference: " + ce.Message
	return ce
}

// PopulationAlign aligns a {population} reference's bound fit (its
// labels fitLabels) to the slot's labels by the fit rule (AlignFit):
// perm[i] is the fit's index of labels[i]; unequal label sets are
// PULSE_VECTOR_REFERENCE_MISMATCH labels_differ naming the population.
func PopulationAlign(at, name string, fitLabels, labels []string) ([]int, *errors.CodedError) {
	perm, ce := AlignFit("attribute", at, name, fitLabels, labels)
	if ce != nil {
		ce.Details["population"] = name
		ce.Details["slot"] = at + ".params.reference"
	}
	return perm, ce
}

// ComparisonKeyFieldTypes names the field types a {table, key_field}
// reference accepts: the categorical rungs (the label is the profile
// key) and the unsigned integers (the value's canonical base-10 form —
// no sign, no leading zero — is the key). Floats, dates, datetimes,
// packed_bool, decimal128 and set_* are refused.
func ComparisonKeyFieldTypes() []string {
	return []string{"categorical_u8", "categorical_u16", "categorical_u32", "u4", "u8", "u16", "u32", "u64"}
}

// ComparisonKeyFieldAccepts reports whether ft may be a key_field.
func ComparisonKeyFieldAccepts(ft encoding.FieldType) bool {
	return ft.IsCategorical() || ComparisonKeyFieldInteger(ft)
}

// ComparisonKeyFieldInteger reports whether ft is an unsigned-integer
// key_field, matched by its value's canonical base-10 string.
func ComparisonKeyFieldInteger(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64:
		return true
	}
	return false
}

// ComparisonBinding is a comparison slot's reference against the
// instance's tables: Static the reference in member order for every
// static form; for {table, key_field}, Table and Perm (Perm[i] the
// table index of member i) read each row's profile.
type ComparisonBinding struct {
	Static []float64
	Table  ReferenceTable
	Perm   []int
}

// ComparisonTableIssue judges cfg's table reference against tables:
// PULSE_VECTOR_REFERENCE_UNKNOWN (table_unknown, key_unknown) or
// PULSE_VECTOR_REFERENCE_MISMATCH (labels_differ) naming the slot at.
// A literal or label-map reference raises none. On success the binding
// holds what the rows read (Static is nil in the predict view, whose
// tables carry no values).
func ComparisonTableIssue(at string, cfg ComparisonConfig, tables ReferenceTables) (ComparisonBinding, *errors.CodedError) {
	tag := func(ce *errors.CodedError) *errors.CodedError {
		if ce.Details == nil {
			ce.Details = map[string]any{}
		}
		ce.Details["attribute"] = at
		ce.Details["slot"] = at + ".params.reference"
		ce.Message = at + " params.reference: " + ce.Message
		return ce
	}
	ref := cfg.Reference
	switch ref.Form {
	case ReferenceValues, ReferenceLabelMap:
		return ComparisonBinding{Static: slices.Clone(ref.Values)}, nil
	case ReferenceCentroid, ReferencePopulation, ReferenceRecord:
		// A centroid is folded by the pre-pass, a population bound from
		// the request's fits (PopulationRefusal / PopulationAlign), a
		// record looked up by the Service: no table to judge.
		return ComparisonBinding{}, nil
	}
	t, ce := tables.Table(ref.Table)
	if ce != nil {
		return ComparisonBinding{}, tag(ce)
	}
	if ref.Form == ReferenceTableKey && !t.HasKey(ref.Key) {
		return ComparisonBinding{}, tag(t.KeyUnknown(ref.Table, ref.Key))
	}
	perm, ce := t.Match(ref.Table, cfg.Vector, cfg.Labels)
	if ce != nil {
		return ComparisonBinding{}, tag(ce)
	}
	if ref.Form == ReferenceTableKey {
		v, _ := t.Profile(ref.Key, perm)
		return ComparisonBinding{Static: v}, nil
	}
	return ComparisonBinding{Table: t, Perm: perm}, nil
}

// CanonicalParams is cfg as the params of an inline-fields slot over
// members: the metric written out (a kind default survives the rewrite
// to literal fields), min_valid, the labels the reference aligns to,
// the reference (a literal or label map in member order; a table,
// centroid, population or record form as written), fit_on as written
// and the bound fit's name. Processor.prepare hands it to the
// factory beside the literal member list; ComparisonParams over those
// members decodes it back to cfg.
func (cfg ComparisonConfig) CanonicalParams() json.RawMessage {
	type tableRef struct {
		Table    string `json:"table"`
		Key      string `json:"key,omitempty"`
		KeyField string `json:"key_field,omitempty"`
	}
	type centroid struct {
		Filter json.RawMessage `json:"filter,omitempty"`
		By     string          `json:"by,omitempty"`
	}
	out := struct {
		Metric    string          `json:"metric"`
		MinValid  int             `json:"min_valid"`
		Labels    []string        `json:"labels"`
		Reference any             `json:"reference"`
		FitOn     json.RawMessage `json:"fit_on,omitempty"`
		Fit       string          `json:"fit,omitempty"`
	}{Metric: cfg.Metric.Name(), MinValid: cfg.MinValid, Labels: cfg.Labels, FitOn: cfg.FitOnRaw, Fit: cfg.Fit}
	switch cfg.Reference.Form {
	case ReferenceValues, ReferenceLabelMap:
		out.Reference = cfg.Reference.Values
	case ReferenceCentroid:
		out.Reference = map[string]centroid{ReferenceCentroid: {Filter: cfg.Reference.FilterRaw, By: cfg.Reference.By}}
	case ReferencePopulation:
		out.Reference = map[string]string{ReferencePopulation: cfg.Reference.Population}
	case ReferenceRecord:
		out.Reference = map[string]recordWire{ReferenceRecord: {Cohort: cfg.Reference.Cohort, Keys: cfg.Reference.Keys}}
	default:
		out.Reference = tableRef{Table: cfg.Reference.Table, Key: cfg.Reference.Key, KeyField: cfg.Reference.KeyField}
	}
	b, err := json.Marshal(out)
	if err != nil {
		// Unreachable: finite floats, strings and ints always marshal.
		return nil
	}
	return b
}

// ComparisonSlotParams resolves attribute a's (a comparison slot)
// params against its members and returns them canonical
// (CanonicalParams); ok is false when they do not judge (the
// field-reference pass has refused that request).
func ComparisonSlotParams(a *types.Attribute, members Resolved, schema *encoding.Schema) (json.RawMessage, bool) {
	cfg, err := ComparisonParams(attributePath(0), a.Type, a.Params, members, schema)
	if err != nil {
		return nil, false
	}
	return cfg.CanonicalParams(), true
}

// comparisonReference decodes a comparison slot's params.reference
// loosely into its per-row operands: the table form's key_field, a
// centroid's filter and by, a population's name. ok is false for any
// other slot, a reference that is not an object, or params that do not
// decode (those are refused elsewhere).
func comparisonReference(typ types.AttributeType, a *types.Attribute) (ref struct {
	Table      any             `json:"table"`
	KeyField   string          `json:"key_field"`
	Centroid   json.RawMessage `json:"centroid"`
	Population any             `json:"population"`
}, ok bool) {
	if a == nil || !IsComparisonAttribute(typ) {
		return ref, false
	}
	var p struct {
		Reference json.RawMessage `json:"reference"`
	}
	if paramread.Probe(a.Params, &p) != nil {
		return ref, false
	}
	if raw := bytes.TrimSpace(p.Reference); len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &ref) != nil {
		return ref, false
	}
	return ref, true
}

// comparisonCentroid decodes a comparison slot's centroid operand
// loosely; ok is false when the reference is not a centroid.
func comparisonCentroid(typ types.AttributeType, a *types.Attribute) (c struct {
	Filter json.RawMessage `json:"filter"`
	By     string          `json:"by"`
}, ok bool) {
	ref, ok := comparisonReference(typ, a)
	if !ok {
		return c, false
	}
	if raw := bytes.TrimSpace(ref.Centroid); len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &c) != nil {
		return c, false
	}
	return c, true
}

// ComparisonKeyField returns the column a comparison slot's
// params.reference keys each row by — a {table, key_field}'s key_field
// or a centroid's by — ("" for any other slot or reference form, or
// params that do not decode). Projection and the two-pass planner read
// it: the column must decode beside the members.
func ComparisonKeyField(typ types.AttributeType, a *types.Attribute) string {
	ref, ok := comparisonReference(typ, a)
	if !ok {
		return ""
	}
	if _, isTable := ref.Table.(string); isTable {
		return ref.KeyField
	}
	if c, ok := comparisonCentroid(typ, a); ok {
		return c.By
	}
	return ""
}

// ComparisonCentroid reports whether comparison slot a (route typ)
// compares with a centroid — a reference the pre-pass folds, so the
// slot is two-pass for either operator and any metric
// (ComparisonTwoPass).
func ComparisonCentroid(typ types.AttributeType, a *types.Attribute) bool {
	_, ok := comparisonCentroid(typ, a)
	return ok
}

// ComparisonCentroidFilter returns a centroid reference's decoded
// filter (nil when absent, for any other reference, or when it does not
// decode — refused elsewhere). AttributeFitOn folds it in beside
// fit_on: both are pre-pass predicates over the attribute-step record.
func ComparisonCentroidFilter(typ types.AttributeType, a *types.Attribute) []*types.Filterer {
	c, ok := comparisonCentroid(typ, a)
	if !ok {
		return nil
	}
	raw := bytes.TrimSpace(c.Filter)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var fs []*types.Filterer
	if json.Unmarshal(raw, &fs) != nil {
		return nil
	}
	return slices.DeleteFunc(fs, func(f *types.Filterer) bool { return f == nil })
}

// ComparisonPopulation returns the bound vector fit a comparison slot's
// {population} reference names ("" for any other slot or form).
func ComparisonPopulation(typ types.AttributeType, a *types.Attribute) string {
	ref, ok := comparisonReference(typ, a)
	if !ok {
		return ""
	}
	name, _ := ref.Population.(string)
	return name
}

// ComparisonRecord returns comparison slot a's (route typ) decoded
// {record} reference; ok is false for any other slot or form, or a
// record operand that does not decode (refused elsewhere). The Service
// reads it to resolve every slot's stored record before the processor
// builds.
func ComparisonRecord(typ types.AttributeType, a *types.Attribute) (ComparisonReference, bool) {
	if a == nil || !IsComparisonAttribute(typ) {
		return ComparisonReference{}, false
	}
	var p struct {
		Reference json.RawMessage `json:"reference"`
	}
	if paramread.Probe(a.Params, &p) != nil {
		return ComparisonReference{}, false
	}
	raw := bytes.TrimSpace(p.Reference)
	if len(raw) == 0 || raw[0] != '{' {
		return ComparisonReference{}, false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return ComparisonReference{}, false
	}
	if v, ok := obj[ReferenceRecord]; !ok || len(bytes.TrimSpace(v)) == 0 || bytes.TrimSpace(v)[0] != '{' {
		return ComparisonReference{}, false
	}
	ref, err := decodeRecordReference(attributePath(0), obj)
	if err != nil {
		return ComparisonReference{}, false
	}
	return ref, true
}

// ComparisonTwoPass is the per-slot two-pass predicate of the
// comparison pair: a centroid reference (folded by the pre-pass, either
// operator, any metric), or an ATTR_VEC_DISTANCE over a fitted metric
// (ComparisonFitted) that is not bound to a prebuilt fit (FitBound).
// Every routing site — processing attrRequiresTwoPassIn, predict's
// attrTwoPass, both fused-crosstab facts — reads it.
func ComparisonTwoPass(typ types.AttributeType, a *types.Attribute, specs []types.VectorSpec, schema *encoding.Schema) bool {
	if ComparisonCentroid(typ, a) {
		return true
	}
	return !FitBound(typ, a) && ComparisonFitted(typ, a, specs, schema)
}

// ComparisonFitted reports whether attribute a (route typ) is an
// ATTR_VEC_DISTANCE slot over a fitted metric (std_euclidean,
// mahalanobis) — the per-attribute two-pass predicate. The metric is
// params.metric, else the kind default of the vector a names in specs
// (before Processor.prepare's rewrite writes it out); params that do not
// decode report false (refused elsewhere).
func ComparisonFitted(typ types.AttributeType, a *types.Attribute, specs []types.VectorSpec, schema *encoding.Schema) bool {
	if a == nil || typ != types.ATTR_VEC_DISTANCE {
		return false
	}
	var p struct {
		Metric *string `json:"metric"`
	}
	if raw := bytes.TrimSpace(a.Params); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if json.Unmarshal(raw, &p) != nil {
			return false
		}
	}
	name := ""
	if p.Metric != nil {
		name = *p.Metric
	}
	m, _, err := ResolveKindMetric(metric.ConsumerVecDistance, name, KindIn(specs, schema, a.Vector), metric.Distance)
	return err == nil && m.Fitted()
}

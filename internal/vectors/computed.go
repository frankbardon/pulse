package vectors

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// computed.go lets a vector member name a column the request computes
// itself (sem-contribution E2-S2): a built-in feature output, an
// attribute label or — when windows run over the record rows — a window
// label. The member is read off the record exactly as a materialized
// column would be read off the cohort, so a matrix over computed members
// equals the same matrix over the materialize-then-fit route.
//
// Only a LITERAL fields entry names a computed column (a glob or pattern
// still matches schema fields only, so no existing request changes
// meaning), and only a MAT_* slot reads such a vector: every other
// consumer is refused (ComputedRefusal), as it reads members at a
// pipeline point where the column may not exist yet.

// ComputedColumn is one column a request computes.
type ComputedColumn struct {
	// Slot is the producing slot kind: "feature", "attribute" or
	// "window".
	Slot string
	// Index is the producer's position in its request slot.
	Index int
	// Type is the column's storage type: a nullable f64, save an
	// ATTR_PREVIOUS_STATE output over a categorical / packed_bool state
	// field, which takes that field's type (prevstate.KeySchema).
	Type encoding.FieldType
}

// Computed maps a computed column's name to its producer.
type Computed map[string]ComputedColumn

// ComputedColumns names the columns req computes per record, first
// producer wins: built-in feature outputs (FeatureOutputLabels; an
// extension feature's outputs are unknown before it runs), attribute
// labels (the slot's, else DefaultAttributeLabel) and, when
// WindowsOverRecords(req, schema, cat), window labels (WindowLabel). cat
// is the bound-model catalog of the site (nil: none) — the one
// windows-first decision every request-holding site reads with its
// catalog. A name that is a schema field is left out: the field wins,
// and the walk refuses the shadowing slot. Nil when req computes
// nothing.
func ComputedColumns(req *types.Request, schema *encoding.Schema, cat model.Catalog) Computed {
	return computedColumns(req, schema, WindowsOverRecords(req, schema, cat))
}

// computedColumns is ComputedColumns with the windows-first decision
// given: windows admits every window label.
func computedColumns(req *types.Request, schema *encoding.Schema, windows bool) Computed {
	if req == nil || (len(req.Features) == 0 && len(req.Attributes) == 0 && len(req.Windows) == 0) {
		return nil
	}
	out := Computed{}
	add := func(name string, c ComputedColumn) {
		if name == "" {
			return
		}
		if schema != nil && schema.Field(name) != nil {
			return
		}
		if _, ok := out[name]; !ok {
			out[name] = c
		}
	}
	for i, f := range req.Features {
		if f == nil {
			continue
		}
		for _, l := range FeatureOutputLabels(f, schema) {
			add(l, ComputedColumn{Slot: "feature", Index: i, Type: encoding.FieldTypeF64})
		}
	}
	view := prevstate.KeySchema(req.Attributes, schema)
	for i, a := range req.Attributes {
		if a == nil {
			continue
		}
		label := a.Label
		if label == "" {
			label = DefaultAttributeLabel(a)
		}
		c := ComputedColumn{Slot: "attribute", Index: i, Type: encoding.FieldTypeF64}
		if a.Type == types.ATTR_PREVIOUS_STATE && view != nil && view != schema {
			if vf := view.Field(label); vf != nil {
				c.Type = vf.Type
			}
		}
		add(label, c)
	}
	if windows {
		for i, w := range req.Windows {
			if w != nil {
				add(WindowLabel(w), ComputedColumn{Slot: "window", Index: i, Type: encoding.FieldTypeF64})
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RegressionReads returns the names a regression slot reads off each
// record: target, predictors, unpenalized, covariance cluster /
// order_by, jackknife_by and the free identifiers of its holdout
// predicate, in that order (empties skipped, repeats kept). The slot
// weight is not one — a weight names a cohort field.
func RegressionReads(reg *types.RegressionSpec) []string {
	if reg == nil {
		return nil
	}
	out := make([]string, 0, 2+len(reg.Predictors)+len(reg.Unpenalized))
	add := func(n string) {
		if n != "" {
			out = append(out, n)
		}
	}
	add(reg.Target)
	for _, p := range reg.Predictors {
		add(p)
	}
	for _, p := range reg.Unpenalized {
		add(p)
	}
	if c := reg.Covariance; c != nil {
		add(c.Cluster)
		add(c.OrderBy)
	}
	add(reg.JackknifeBy)
	if reg.Holdout != "" {
		names, _, _ := exprident.Free(reg.Holdout)
		for _, n := range names {
			add(n)
		}
	}
	return out
}

// holdoutReadsWholeEnv reports whether reg's holdout predicate reads
// the whole record ($env) or does not parse, so its names cannot be
// listed: every window label is then copied onto the records.
func holdoutReadsWholeEnv(reg *types.RegressionSpec) bool {
	if reg == nil || reg.Holdout == "" {
		return false
	}
	_, usesEnv, parsed := exprident.Free(reg.Holdout)
	return usesEnv || !parsed
}

// RegressionWindowReads returns the window labels req's regressions
// read (RegressionReads; every window label when a holdout predicate
// reads $env) that are computed record columns
// (ComputedColumns, slot "window": only when WindowsOverRecords over the
// same catalog), in first-read order without repeats; nil when none. The
// engine copies exactly these onto the records before the fit.
func RegressionWindowReads(req *types.Request, schema *encoding.Schema, cat model.Catalog) []string {
	if !WindowsOverRecords(req, schema, cat) || len(req.Regressions) == 0 {
		return nil
	}
	comp := ComputedColumns(req, schema, cat)
	var out []string
	seen := map[string]bool{}
	for _, reg := range req.Regressions {
		reads := RegressionReads(reg)
		if holdoutReadsWholeEnv(reg) {
			for _, w := range req.Windows {
				if w != nil {
					reads = append(reads, WindowLabel(w))
				}
			}
		}
		for _, n := range reads {
			if comp[n].Slot == "window" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// ResolveRequest is Resolve over req.Vectors with req's computed columns
// (ComputedColumns over the site's bound-model catalog cat) admitted as
// literal members — the resolution every request-holding site reads, so
// predict, the walk, projection and the engine agree on a computed
// member.
func ResolveRequest(req *types.Request, schema *encoding.Schema, cat model.Catalog) ([]Resolved, *errors.CodedError) {
	if req == nil {
		return Resolve(nil, schema)
	}
	return resolveIn(req.Vectors, schema, ComputedColumns(req, schema, cat))
}

// resolveListing is ResolveRequest with every window label admitted as
// a computed member, whatever the windows-first decision: the
// resolution an output-column listing (AggregationOutputColumns) reads.
// A schema field still wins and a glob or pattern still matches schema
// fields only, so every vector of a request ResolveRequest admits
// resolves to the same members here.
func resolveListing(req *types.Request, schema *encoding.Schema) ([]Resolved, *errors.CodedError) {
	if req == nil {
		return Resolve(nil, schema)
	}
	return resolveIn(req.Vectors, schema, computedColumns(req, schema, len(req.Windows) > 0))
}

// ComputedRefusal returns the first slot of req, other than a MAT_*
// member vector, that reads a vector with a computed member (resolved
// is ResolveRequest's result, index-aligned with req.Vectors):
// PULSE_VECTOR_INVALID computed_member_unsupported, details slot (the
// consumer path) and member (the first computed member). A test,
// attribute, grouper, aggregation or expression reads its members at a
// pipeline point where the column may not exist yet (an attribute's
// vector input runs inside the attribute pass; a filter before it), so
// it is refused rather than silently fed nulls. Nil when none does.
func ComputedRefusal(req *types.Request, resolved []Resolved) *errors.CodedError {
	if req == nil {
		return nil
	}
	first := map[string]int{}
	for i := range req.Vectors {
		if i < len(resolved) && len(resolved[i].Computed) > 0 {
			first[req.Vectors[i].Name] = i
		}
	}
	if len(first) == 0 {
		return nil
	}
	return nonMatrixConsumerRefusal(req, func(name, slot string, _ consumerKind) *errors.CodedError {
		i, ok := first[name]
		if !ok {
			return nil
		}
		at, member := slotPath(i), resolved[i].Computed[0]
		return invalid(at, name, "computed_member_unsupported",
			"vector "+describe(at, name)+" has computed member "+strconv.Quote(member)+
				" (a feature, attribute or window output of this request), which only a MAT_* slot reads; "+
				slot+" cannot: list the member's source fields, or materialize the column first",
			map[string]any{"slot": slot, "member": member})
	})
}

// MatrixComputedMembers returns, in first-read order, the computed
// columns (ComputedColumns) req's MAT_* slots read: an entry of a
// matrix's vector or inline fields, or a MAT_PARTIAL_CORRELATION
// params.control — always by its literal name, the only way a member
// names a computed column. cat is the site's bound-model catalog
// (ComputedColumns). Nil when none.
func MatrixComputedMembers(req *types.Request, schema *encoding.Schema, cat model.Catalog) []string {
	if req == nil || len(req.Matrices) == 0 {
		return nil
	}
	comp := ComputedColumns(req, schema, cat)
	if len(comp) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(names []string) {
		for _, n := range names {
			if _, ok := comp[n]; ok && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	for i, m := range req.Matrices {
		if m.Vector != "" {
			if j := indexOfVector(req.Vectors, m.Vector); j >= 0 {
				add(req.Vectors[j].Fields)
			}
		} else {
			add(m.Fields)
		}
		if m.Type == types.MAT_PARTIAL_CORRELATION && len(m.Params) > 0 {
			var p struct {
				Control json.RawMessage `json:"control"`
			}
			if paramread.Probe(m.Params, &p) == nil {
				if controls, err := decodeControl(matrixPath(i), p.Control); err == nil {
					add(controls)
				}
			}
		}
	}
	return out
}

// WindowLabel returns a window's output column name: windowparams.Label,
// the one derivation the window engine and predict read too.
func WindowLabel(w *types.Window) string { return windowparams.Label(w) }

// FeatureOutputLabels returns the output column names a built-in feature
// emits (nil when its params or source field name none, or the type is
// not built-in). Mirrors the operator implementations in
// internal/processing/feature; the integration tests round-trip each
// operator end to end.
func FeatureOutputLabels(feat *types.Feature, schema *encoding.Schema) []string {
	switch feat.Type {
	case types.FEAT_LOG:
		return []string{featureSingleLabel(feat, "LOG_")}
	case types.FEAT_SQRT:
		return []string{featureSingleLabel(feat, "SQRT_")}
	case types.FEAT_BUCKETIZE:
		return []string{featureSingleLabel(feat, "BUCKET_")}
	case types.FEAT_FREQUENCY_ENCODE:
		return []string{featureSingleLabel(feat, "FREQ_")}
	case types.FEAT_TARGET_ENCODE:
		return []string{featureSingleLabel(feat, "TARGET_")}
	case types.FEAT_TRAIN_TEST_SPLIT:
		label := feat.Label
		if label == "" {
			label = "split"
		}
		return []string{label}
	case types.FEAT_DATE_FEATURES:
		prefix := feat.Label
		if prefix == "" {
			prefix = feat.Field
		}
		out := []string{
			prefix + "_year",
			prefix + "_month",
			prefix + "_day",
			prefix + "_dow",
			prefix + "_quarter",
		}
		// A `datetime` also carries the wall-clock hour.
		if schema != nil {
			if f := schema.Field(feat.Field); f != nil && f.Type == encoding.FieldTypeDateTime {
				out = append(out, prefix+"_hour")
			}
		}
		return out
	case types.FEAT_POLY:
		prefix := feat.Label
		if prefix == "" {
			prefix = feat.Field + "_poly"
		}
		var p struct {
			Degree int `json:"degree"`
		}
		if len(feat.Params) > 0 {
			_ = paramread.Probe(feat.Params, &p)
		}
		if p.Degree < 2 {
			// Predict surfaces a validation error elsewhere; emit no
			// columns rather than guess.
			return nil
		}
		if p.Degree > 10 {
			p.Degree = 10
		}
		out := make([]string, 0, p.Degree-1)
		for k := 2; k <= p.Degree; k++ {
			out = append(out, prefix+"_"+strconv.Itoa(k))
		}
		return out
	case types.FEAT_ONE_HOT:
		prefix := feat.Label
		if prefix == "" {
			prefix = feat.Field
		}
		// The runtime's level list (newOneHot): dictionary labels, or a
		// packed_bool's "0" / "1".
		values := fieldaccept.Levels(schema.Field(feat.Field))
		if values == nil {
			return nil
		}
		out := make([]string, len(values))
		for i, v := range values {
			out[i] = prefix + "_" + strings.ReplaceAll(v, " ", "_")
		}
		return out
	}
	return nil
}

func featureSingleLabel(feat *types.Feature, prefix string) string {
	if feat.Label != "" {
		return feat.Label
	}
	return prefix + feat.Field
}

package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// materialize_groups.go is the no-execute half of the output slot's
// parent groups (types.MaterializeOutput.Groups): which groups a
// materialize run hands its cohort writer — declared, or inherited from
// the source — which source groups it leaves out and why, and the static
// refusals of a declaration. MaterializePlan — the run's pre-flight and
// its predict — makes the one MaterializeGroupPlan call, and the service
// hands that plan to the run's sink (MaterializeSink.Begin), so the
// three cannot drift.

// Group plan verdicts (descriptor.MaterializeGroupPlan.Verdict) and
// origins.
const (
	MaterializeGroupPlanned      = "planned"
	MaterializeGroupNotInherited = "not_inherited"
	materializeGroupDeclared     = "declared"
	materializeGroupInherited    = "inherited"
)

// MaterializeGroups is the parent-group plan of a materialize run.
type MaterializeGroups struct {
	// Decls are the groups handed to the cohort writer, by written name,
	// in declaration order (CohortBuilderOptions.Groups).
	Decls []types.MaterializeGroup
	// Plan describes every group of Decls, in order, then every source
	// group left out (MaterializeContainer.Groups).
	Plan []descriptor.MaterializeGroupPlan
	// Warnings are one PULSE_MATERIALIZE_GROUP_NOT_INHERITED per source
	// group left out, in source group order — the run's own warnings
	// (fatal under MaterializeOptions.Strict).
	Warnings []*errors.CodedError
	// TooNarrow are the writer's width-screen findings over Decls
	// (PULSE_GROUP_TOO_NARROW), which the run raises from the writer and
	// predict reports as warnings.
	TooNarrow []*errors.CodedError
}

// MaterializeOutputSchema is the flat (ungrouped) schema a materialize
// run hands its cohort writer over layout: the layout's fields in output
// order, each kept vec_* source field's VECTORS entry (dim, kind, scale,
// labels) re-indexed to its output position. schema is the source's.
func MaterializeOutputSchema(schema *encoding.Schema, layout []MaterializeOutputField) *encoding.Schema {
	out := &encoding.Schema{Fields: make([]encoding.Field, len(layout))}
	for i, col := range layout {
		out.Fields[i] = col.Field
		if col.Source >= 0 && col.Field.Type.IsVector() && schema != nil {
			if v, ok := schema.Vector(col.Source); ok {
				v.FieldIndex = i
				out.Vectors = append(out.Vectors, v)
			}
		}
	}
	return out
}

// MaterializeGroupPlan resolves the parent groups a materialize run with
// output block out writes over layout (MaterializeOutputLayout of the
// same request) and schema (the source's schema, its Groups included).
// Nil out — no output block — plans nothing: the run writes ungrouped,
// as it always has.
//
// A present out.Groups is the declaration, by written name: checked as
// the cohort writer checks it (encx.NewGroupEncoder — an unknown name
// PULSE_GROUP_FIELD_UNKNOWN, a field in two groups
// PULSE_GROUP_FIELD_CONFLICT, an empty group or a key naming a
// non-member PULSE_GROUP_DECLARATION_INVALID; details gain path
// output.groups[i]). Absent, each INDEXED source group is inherited
// when every member is written unchanged by some column (the first
// such column, in output order, when a member is written twice): the
// same key and members under their written names. A member no column
// writes, or one every column writing it re-codes (Rebuild: a type
// cast, recode or bucket), leaves the group out with
// PULSE_MATERIALIZE_GROUP_NOT_INHERITED. Constant groups (elided
// constants) are never inherited. Either way the writer's width screen
// (encx.DedupGate.ScreenWidths) runs over the planned groups, so a
// group no wider than its index is reported dropped_too_narrow, as the
// writer drops it. Schema-only; reads no record.
func MaterializeGroupPlan(out *types.MaterializeOutput, schema *encoding.Schema, layout []MaterializeOutputField) (*MaterializeGroups, *errors.CodedError) {
	g := &MaterializeGroups{}
	if out == nil {
		return g, nil
	}
	origin := materializeGroupDeclared
	if out.Groups != nil {
		g.Decls = out.Groups
	} else {
		origin = materializeGroupInherited
		g.inherit(schema, layout)
	}
	if len(g.Decls) == 0 {
		return g, nil
	}
	flat := MaterializeOutputSchema(schema, layout)
	specs := materializeGroupSpecs(g.Decls)
	// NewGroupEncoder is the full declaration check. The width screen
	// after it refuses only a member name the encoder has already
	// refused (a lenient gate never fails on width), so both share one
	// refusal path rather than the screen keeping its own.
	_, err := encx.NewGroupEncoder(flat, specs)
	var (
		screen []encx.GroupViability
		warns  []*errors.CodedError
	)
	if err == nil {
		_, screen, warns, err = encx.DedupGate{}.ScreenWidths(flat, specs)
	}
	if err != nil {
		ce := groupRefusal(err)
		if origin == materializeGroupDeclared {
			ce = withGroupPath(ce)
		}
		return nil, ce
	}
	g.TooNarrow = warns
	planned := make([]descriptor.MaterializeGroupPlan, len(g.Decls))
	for i, d := range g.Decls {
		p := descriptor.MaterializeGroupPlan{
			Label:   specs[i].Label(i),
			Origin:  origin,
			Key:     d.Key,
			Members: d.Members,
			Verdict: MaterializeGroupPlanned,
		}
		if screen[i].Verdict == encx.GroupVerdictDroppedTooNarrow {
			p.Verdict, p.Reason = screen[i].Verdict, screen[i].Reason
		}
		planned[i] = p
	}
	if origin == materializeGroupInherited {
		// inherit recorded each inherited group's source fields at the
		// head of Plan, ahead of the groups left out.
		for i := range planned {
			planned[i].SourceFields = g.Plan[i].SourceFields
		}
		g.Plan = append(planned, g.Plan[len(planned):]...)
	} else {
		g.Plan = planned
	}
	return g, nil
}

// inherit fills Decls with the source groups the layout writes unchanged
// and, for each left out, a not_inherited plan entry and warning. Plan
// holds one stub per inherited group (its SourceFields) ahead of the
// left-out entries; MaterializeGroupPlan completes the stubs.
func (g *MaterializeGroups) inherit(schema *encoding.Schema, layout []MaterializeOutputField) {
	if schema == nil {
		return
	}
	var kept, left []descriptor.MaterializeGroupPlan
	for gi, grp := range schema.Groups {
		if grp.Kind != encoding.GroupKindIndexed {
			continue
		}
		var (
			decl          types.MaterializeGroup
			srcKey, srcMb []string
			fields        []string
			member        string
			change        string
		)
		allKey := true
		for _, m := range grp.Members {
			allKey = allKey && m.Key
		}
		for _, m := range grp.Members {
			name := schema.Fields[m.Field].Name
			fields = append(fields, name)
			if allKey || m.Key {
				srcKey = append(srcKey, name)
			} else {
				srcMb = append(srcMb, name)
			}
			written, how := unchangedColumn(schema.Fields[m.Field], m.Field, layout)
			if how != "" {
				if change == "" {
					member, change = name, how
				}
				continue
			}
			if !allKey && m.Key {
				decl.Key = append(decl.Key, written)
			} else {
				decl.Members = append(decl.Members, written)
			}
		}
		if change == "" {
			g.Decls = append(g.Decls, decl)
			kept = append(kept, descriptor.MaterializeGroupPlan{SourceFields: fields})
			continue
		}
		sp := encx.GroupSpec{Kind: encoding.GroupKindIndexed, Members: append(append([]string(nil), srcKey...), srcMb...)}
		if !allKey {
			sp.Key = srcKey
		}
		label := "source " + sp.Label(gi)
		if allKey {
			srcKey, srcMb = nil, append([]string(nil), sp.Members...)
		}
		left = append(left, descriptor.MaterializeGroupPlan{
			Label:        label,
			Origin:       materializeGroupInherited,
			Key:          srcKey,
			Members:      srcMb,
			SourceFields: fields,
			Verdict:      MaterializeGroupNotInherited,
			Reason:       string(errors.PULSE_MATERIALIZE_GROUP_NOT_INHERITED),
			Member:       member,
			Change:       change,
		})
		g.Warnings = append(g.Warnings, errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_GROUP_NOT_INHERITED,
			"the output does not carry "+label+": member "+strconv.Quote(member)+" is "+change+
				", so its members are written as row fields; declare output.groups to regroup, or [] to write none",
			map[string]any{"group": label, "fields": fields, "member": member, "change": change}))
	}
	g.Plan = append(kept, left...)
}

// unchangedColumn finds the first layout column writing source field
// fi (of type f) unchanged and returns its written name; otherwise how
// the field changes: "dropped" (no column reads it) or, from the first
// column reading it, "bucketed", "recoded" or "retyped".
func unchangedColumn(f encoding.Field, fi int, layout []MaterializeOutputField) (string, string) {
	how := "dropped"
	for _, col := range layout {
		if col.Source != fi {
			continue
		}
		if !col.Rebuild && col.Field.Type == f.Type {
			return col.Field.Name, ""
		}
		if how != "dropped" {
			continue
		}
		switch {
		case col.Bucket != nil:
			how = "bucketed"
		case col.Recode != nil:
			how = "recoded"
		default:
			how = "retyped"
		}
	}
	return "", how
}

// materializeGroupSpecs lowers declarations to the encoder's specs as
// the cohort writer does (io.GroupDecl): members Key ∪ Members, key
// first; Key as declared.
func materializeGroupSpecs(decls []types.MaterializeGroup) []encx.GroupSpec {
	out := make([]encx.GroupSpec, len(decls))
	for i, d := range decls {
		sp := encx.GroupSpec{Kind: encoding.GroupKindIndexed}
		sp.Members = append(append(sp.Members, d.Key...), d.Members...)
		if len(d.Key) > 0 {
			sp.Key = append([]string(nil), d.Key...)
		}
		out[i] = sp
	}
	return out
}

// withGroupPath adds details.path output.groups[i] to a declaration
// refusal, i the (last) group it names.
func withGroupPath(ce *errors.CodedError) *errors.CodedError {
	at := -1
	switch v := ce.Details["group"].(type) {
	case int:
		at = v
	}
	if gs, ok := ce.Details["groups"].([]int); ok && len(gs) > 0 {
		at = gs[len(gs)-1]
	}
	if at < 0 {
		return ce
	}
	details := make(map[string]any, len(ce.Details)+1)
	for k, v := range ce.Details {
		details[k] = v
	}
	details["path"] = "output.groups[" + strconv.Itoa(at) + "]"
	return errors.NewCodedErrorWithDetails(ce.Code, ce.Message, details)
}

// groupRefusal is a group declaration check's refusal as the coded
// error it already is: NewGroupEncoder and DedupGate.ScreenWidths return
// only a bare *errors.CodedError (TestGroupDeclarationRefusalsAreCoded,
// internal/encoding, pins every refusal class), so there is no uncoded
// case to translate. Were one ever added, the assertion fails loudly —
// the facade reports PROCESSING_PANIC — never as a passed plan.
func groupRefusal(err error) *errors.CodedError {
	return err.(*errors.CodedError)
}

// MaterializeGroupRisks is the data-dependent check of a declared keyed
// group: its determined members must hold one value per key tuple on
// the written rows, or the run fails with
// PULSE_GROUP_MEMBER_NOT_CONSTANT and writes nothing. An inherited group
// carries none — its source already held that dependency, and the run
// writes a subset of its rows unchanged. Empty, never nil.
func MaterializeGroupRisks(g *MaterializeGroups) []descriptor.MaterializeRisk {
	risks := []descriptor.MaterializeRisk{}
	if g == nil {
		return risks
	}
	for i, p := range g.Plan {
		if i >= len(g.Decls) || p.Origin != materializeGroupDeclared || p.Verdict != MaterializeGroupPlanned ||
			len(p.Key) == 0 || len(p.Members) == 0 {
			continue
		}
		risks = append(risks, descriptor.MaterializeRisk{
			Column: p.Label,
			Path:   "output.groups[" + strconv.Itoa(i) + "]",
			Code:   string(errors.PULSE_GROUP_MEMBER_NOT_CONSTANT),
			Check:  "every written row sharing a key tuple must agree on each determined member — the run fails at the first row that does not and writes nothing",
		})
	}
	return risks
}

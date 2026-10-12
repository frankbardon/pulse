package processing

import (
	"github.com/frankbardon/pulse/types"
)

// Response.Components.Attributes — one entry per attribute slot whose
// operator declares a ComponentSchema (a built-in implementing
// MetaAttribute, or an extension registered with a ComponentsFunc).
//
// The orchestrator owns the attribute floor {n, n_null}: every arm
// tallies, per filter-passing row the attribute scored, whether the
// output it wrote is present or null (attributeOutputNull — the one
// rule SetAttributeOutput writes by). The operator owns its own keys
// (MetaAttribute.Components, called once after the last row).
// Attributes are cohort-wide on every arm, so a grouped or crosstab
// run carries one entry per slot, never per bucket.
//
// Built only under ComputePlan.Attributes; a slot whose computer is
// not a MetaAttribute gets no tally (attributeFloorFor returns nil),
// so a request with no such slot allocates nothing here.

// MetaAttribute is the optional sibling of AttributeComputer for an
// attribute that reports typed counters on its
// Response.Components.Attributes entry. Components returns ONLY the
// operator-specific keys declared in its descriptor.ComponentSchema —
// never n / n_null, which the orchestrator fills — and is called once,
// after the attribute has scored every row. (nil, nil) emits the floor
// alone.
type MetaAttribute interface {
	Components() (map[string]any, error)
}

// attributeTallyResetter is implemented by an attribute whose own
// counters (its components keys, its Response warning) accumulate in
// Row: the two-pass streaming arm evaluates a row-local stage once per
// prepass layer that needs it AND again on the emission scan, so it
// resets the counters before the emission scan — the one scan every
// other arm's tally equals.
type attributeTallyResetter interface {
	resetTally()
}

// attributeFloor tallies one componented attribute slot's floor.
type attributeFloor struct {
	n, nNull int
}

// observe counts one written output v (nullable: the computer's
// EmitsNull). A nil floor (the slot emits no entry) is a no-op.
func (f *attributeFloor) observe(v float64, nullable bool) {
	if f == nil {
		return
	}
	if attributeOutputNull(v, nullable) {
		f.nNull++
	} else {
		f.n++
	}
}

// attributeFloorFor returns a fresh floor when computer emits a
// Components entry under compute, else nil.
func attributeFloorFor(computer any, compute ComputePlan) *attributeFloor {
	if !compute.Attributes {
		return nil
	}
	if _, ok := computer.(MetaAttribute); !ok {
		return nil
	}
	return &attributeFloor{}
}

// attributeComponents builds the slot's entry from its floor and the
// operator's Components(). A nil floor builds none (ok false).
func attributeComponents(attr *types.Attribute, label string, computer any, f *attributeFloor) (types.AttributeComponents, bool, error) {
	if f == nil {
		return types.AttributeComponents{}, false, nil
	}
	meta, ok := computer.(MetaAttribute)
	if !ok {
		return types.AttributeComponents{}, false, nil
	}
	op, err := meta.Components()
	if err != nil {
		return types.AttributeComponents{}, false, err
	}
	workAttributeComponentBuilds.Add(1)
	entry := types.AttributeComponents{Label: label, Type: attr.Type, N: f.n, NNull: f.nNull}
	if len(op) > 0 {
		entry.Operator = op
	}
	return entry, true, nil
}

// attachAttributeComponents appends entries onto
// resp.Components.Attributes, allocating the shell on first use. No
// entries leaves resp untouched.
func attachAttributeComponents(resp *types.Response, entries []types.AttributeComponents) {
	if len(entries) == 0 {
		return
	}
	if resp.Components == nil {
		resp.Components = &types.ResponseComponents{}
	}
	resp.Components.Attributes = append(resp.Components.Attributes, entries...)
}

// rowLocalAttributeComponents builds the entries of the row-local
// slots on a streaming arm, in declared order.
func rowLocalAttributeComponents(attrs []rowLocalAttrEntry) ([]types.AttributeComponents, error) {
	var out []types.AttributeComponents
	for _, ra := range attrs {
		entry, ok, err := attributeComponents(ra.attr, ra.label, ra.computer, ra.floor)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, entry)
		}
	}
	return out, nil
}

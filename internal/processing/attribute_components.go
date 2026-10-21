package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
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

// MergeableAttributeTally is the optional sibling of RowLocalAttribute
// for a row-local attribute that keeps a per-run tally — its
// Components.Attributes counters and the Response warnings they back
// (ATTR_SCALE_SCORE's out-of-range counts, a reference-form
// ATTR_PROFILE_SIMILARITY's missing-reference and domain counts, an
// extension attribute declaring Mergeable through
// extend.MergeableAttribute). The parallel reducers (ShardWorkers,
// DecodeWorkers) score each partition on its own instance and fold the
// partitions with MergeTally, so the one merged instance's Components()
// and warnings describe the whole cohort exactly as the serial
// instance's do. The tallies are integers (and bounded key sets), so
// the fold is exact and order-free (U51 E3-S2, #456).
//
// other is an instance the SAME factory built from the SAME spec (a
// type mismatch is PROCESSING_INTERNAL); it is not reused afterwards.
// Only the tally folds — the binding (tables, fits, members) is the
// same on every partition. A two-pass attribute (a centroid reference,
// a fit_on fold) never implements it: its pre-pass is cohort-wide.
type MergeableAttributeTally interface {
	RowLocalAttribute
	MergeTally(other RowLocalAttribute) error
}

// MergeAttributeTally folds src's per-run tally into dst, two
// partitions' instances of one attribute slot: MergeTally when dst
// keeps one; nothing for an attribute with no per-run tally. A slot
// emitting Components.Attributes figures (MetaAttribute) without a
// tally fold is PROCESSING_INTERNAL — mergegate admits no such slot to
// the reducers, whose merged figures would describe one partition.
func MergeAttributeTally(dst, src RowLocalAttribute) error {
	if m, ok := dst.(MergeableAttributeTally); ok {
		return m.MergeTally(src)
	}
	if _, ok := dst.(MetaAttribute); ok {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("attribute %T emits components but has no tally merge; it is not eligible for the parallel reducers", dst),
			map[string]any{"computer": fmt.Sprintf("%T", dst)})
	}
	return nil
}

// tallyPartnerMismatch is the PROCESSING_INTERNAL a MergeTally raises
// for a partner of another type.
func tallyPartnerMismatch(t types.AttributeType, other RowLocalAttribute) error {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		fmt.Sprintf("%s: MergeTally partner %T is not the same attribute", t, other),
		map[string]any{"attribute": string(t), "partner": fmt.Sprintf("%T", other)})
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

// AttributeFloor tallies one componented attribute slot's floor. The
// parallel reducers hold one per partition (AttributeFloorFor) and fold
// them with Merge: plain counts, so the fold is order-free and exact.
type AttributeFloor struct {
	n, nNull int
}

// Observe counts one written output v (nullable: the computer's
// EmitsNull). A nil floor (the slot emits no entry) is a no-op.
func (f *AttributeFloor) Observe(v float64, nullable bool) {
	if f == nil {
		return
	}
	if attributeOutputNull(v, nullable) {
		f.nNull++
	} else {
		f.n++
	}
}

// Merge adds other partition's floor into f. Either nil is a no-op
// (both partitions of one slot hold a floor, or neither does).
func (f *AttributeFloor) Merge(other *AttributeFloor) {
	if f == nil || other == nil {
		return
	}
	f.n += other.n
	f.nNull += other.nNull
}

// AttributeFloorFor is attributeFloorFor for the parallel reducers
// (internal/service), which build their attributes outside a Processor.
func AttributeFloorFor(computer any, compute ComputePlan) *AttributeFloor {
	return attributeFloorFor(computer, compute)
}

// AttributeComponentsEntry is attributeComponents for the parallel
// reducers: the slot's entry off its (merged) floor and computer; ok is
// false when the slot emits none (nil floor).
func AttributeComponentsEntry(attr *types.Attribute, label string, computer any, f *AttributeFloor) (types.AttributeComponents, bool, error) {
	return attributeComponents(attr, label, computer, f)
}

// AttachAttributeComponents is attachAttributeComponents for the
// parallel reducers.
func AttachAttributeComponents(resp *types.Response, entries []types.AttributeComponents) {
	attachAttributeComponents(resp, entries)
}

// attributeFloorFor returns a fresh floor when computer emits a
// Components entry under compute, else nil.
func attributeFloorFor(computer any, compute ComputePlan) *AttributeFloor {
	if !compute.Attributes {
		return nil
	}
	if _, ok := computer.(MetaAttribute); !ok {
		return nil
	}
	return &AttributeFloor{}
}

// attributeComponents builds the slot's entry from its floor and the
// operator's Components(). A nil floor builds none (ok false).
func attributeComponents(attr *types.Attribute, label string, computer any, f *AttributeFloor) (types.AttributeComponents, bool, error) {
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

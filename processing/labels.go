package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// LabelResolver translates raw categorical values to display labels
// per a request's LabelBinding slice. It is constructed once per
// Request via BuildLabelResolver and consumed at every output site
// (Sample rows, FacetField counts, AGG_FREQUENCY result keys,
// grouped result keys, Export cells).
//
// Resolver methods are not safe for concurrent use. Each Request
// owns its own resolver; shard-parallel reducers either build one
// resolver per partial and merge warnings at the end, or serialise
// the output stage.
//
// Replace-mode collision behaviour: when two distinct source values
// map to the same label string, the output disambiguates with the
// source value in parentheses ("United States (US)"). Rows-backed
// tables get a pre-pass so both colliding sources render
// symmetrically; function-driven tables detect collisions online,
// so the first source seen renders cleanly and the second renders
// disambiguated (asymmetric). The PULSE_LABEL_COLLISION warning
// names every involved source value so callers can audit.
type LabelResolver struct {
	bindings map[string]*labelBindingState
}

// labelBindingState carries per-field resolver state. Fields are
// pre-allocated lazily on first use to keep zero-binding requests
// cheap.
type labelBindingState struct {
	field          string
	table          LabelTable
	mode           types.LabelMode
	tableName      string
	alwaysDisambig map[string]bool   // source values whose label collides with another source's label
	labelToSource  map[string]string // label → first source seen (online collision detection)
	missCounts     map[string]int
	collisionPairs map[string]map[string]bool // label → set of source values that emit it
}

// ResolverWarning captures a single resolver-side diagnostic. The
// caller folds these into the response envelope after Process / Sample
// / Facet / Export completes.
type ResolverWarning struct {
	Code    errors.Code
	Message string
	Details map[string]any
}

// BuildLabelResolver constructs a LabelResolver from the request
// bindings and the runtime extension registry. Returns (nil, nil)
// when bindings is empty so callers can guard cheaply with a single
// nil check.
//
// Returns PULSE_LABEL_TABLE_UNKNOWN if a binding references a table
// absent from the registry. Other binding-shape issues (unknown
// field, non-categorical field, augment collision) are validated
// upstream by descriptor.ValidateLabels — this constructor trusts
// schema-level checks.
func BuildLabelResolver(bindings []*types.LabelBinding, registry *ExtensionRegistry) (*LabelResolver, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	r := &LabelResolver{bindings: make(map[string]*labelBindingState, len(bindings))}
	for i, b := range bindings {
		if b == nil {
			continue
		}
		tbl, ok := lookupLabelTable(registry, b.Table)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PULSE_LABEL_TABLE_UNKNOWN,
				fmt.Sprintf("labels[%d]: table %q not registered on Service", i, b.Table),
				map[string]any{"index": i, "field": b.Field, "table": b.Table},
			)
		}
		st := &labelBindingState{
			field:          b.Field,
			table:          tbl,
			mode:           b.LabelModeOrDefault(),
			tableName:      b.Table,
			alwaysDisambig: make(map[string]bool),
			labelToSource:  make(map[string]string),
			missCounts:     make(map[string]int),
			collisionPairs: make(map[string]map[string]bool),
		}
		// Pre-pass Rows-backed tables so symmetric disambiguation is
		// possible. Function-driven tables fall back to online
		// detection — see resolveLabel.
		if tbl.Rows != nil {
			inv := make(map[string][]string, len(tbl.Rows))
			for src, lbl := range tbl.Rows {
				inv[lbl] = append(inv[lbl], src)
			}
			for lbl, srcs := range inv {
				if len(srcs) > 1 {
					for _, s := range srcs {
						st.alwaysDisambig[s] = true
					}
					st.collisionPairs[lbl] = make(map[string]bool, len(srcs))
					for _, s := range srcs {
						st.collisionPairs[lbl][s] = true
					}
				}
			}
		}
		r.bindings[b.Field] = st
	}
	if len(r.bindings) == 0 {
		return nil, nil
	}
	return r, nil
}

// Has reports whether the resolver carries any binding for the named
// field. Cheap and nil-safe.
func (r *LabelResolver) Has(field string) bool {
	if r == nil {
		return false
	}
	_, ok := r.bindings[field]
	return ok
}

// Mode returns the binding mode for the named field. Returns the zero
// LabelMode when no binding exists; callers should gate with Has.
func (r *LabelResolver) Mode(field string) types.LabelMode {
	if r == nil {
		return ""
	}
	if st, ok := r.bindings[field]; ok {
		return st.mode
	}
	return ""
}

// AugmentField returns the augment-mode sibling column name for the
// named field, or empty string when the field has no augment binding.
func (r *LabelResolver) AugmentField(field string) string {
	if r == nil {
		return ""
	}
	st, ok := r.bindings[field]
	if !ok || st.mode != types.LabelModeAugment {
		return ""
	}
	return field + "_label"
}

// FieldsWithAugment returns the field names that should emit a
// "<field>_label" sibling column. Stable in iteration order of the
// underlying map (used only for output schema overlay, where stable
// ordering is enforced by caller).
func (r *LabelResolver) FieldsWithAugment() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.bindings))
	for name, st := range r.bindings {
		if st.mode == types.LabelModeAugment {
			out = append(out, name)
		}
	}
	return out
}

// Apply translates a raw categorical value through the binding for
// the named field. Behaviour by mode:
//
//   - Replace: returns (label, sibling="", ok=true) on hit; on miss
//     returns (raw, "", false) and records a miss for the warning
//     summary. The label may be disambiguated as "label (raw)" when
//     two source values collide on the same label.
//   - Augment: returns (raw, label, ok=true) on hit; on miss returns
//     (raw, "", false) and records a miss.
//
// Returns (raw, "", false) when the resolver carries no binding for
// the field. Callers should typically gate with Has before calling.
func (r *LabelResolver) Apply(field, raw string) (out string, sibling string, ok bool) {
	if r == nil {
		return raw, "", false
	}
	st, exists := r.bindings[field]
	if !exists {
		return raw, "", false
	}
	label, hit, err := resolveLabel(st.table, raw)
	if err != nil || !hit {
		st.missCounts[raw]++
		return raw, "", false
	}
	switch st.mode {
	case types.LabelModeAugment:
		return raw, label, true
	case types.LabelModeReplace:
		return st.replaceOutput(raw, label), "", true
	default:
		return raw, "", false
	}
}

// replaceOutput produces the replace-mode display string for the
// (raw, label) pair, applying disambiguation when the label is
// known to collide across multiple source values.
//
// Function-driven tables run online collision detection: the first
// source value to claim a label gets the clean label; subsequent
// sources for the same label get the disambiguated "label (raw)"
// form. Rows-backed tables apply the precomputed alwaysDisambig
// set so both colliding sources render symmetrically.
func (st *labelBindingState) replaceOutput(raw, label string) string {
	if st.alwaysDisambig[raw] {
		st.recordCollision(label, raw)
		return label + " (" + raw + ")"
	}
	if prior, seen := st.labelToSource[label]; seen {
		if prior != raw {
			// Function-driven online collision: mark both, but the
			// already-emitted prior source value renders cleanly.
			// PULSE_LABEL_COLLISION warning carries both source
			// values so the caller knows the asymmetry exists.
			st.alwaysDisambig[raw] = true
			st.recordCollision(label, raw)
			st.recordCollision(label, prior)
			return label + " (" + raw + ")"
		}
		return label
	}
	st.labelToSource[label] = raw
	return label
}

// recordCollision tracks the source value as participating in the
// named label's collision set.
func (st *labelBindingState) recordCollision(label, source string) {
	if st.collisionPairs[label] == nil {
		st.collisionPairs[label] = map[string]bool{}
	}
	st.collisionPairs[label][source] = true
}

// Warnings flushes the per-binding collision and miss summaries into
// envelope-ready warning records. Safe to call once at the end of a
// run; subsequent calls return the same set.
func (r *LabelResolver) Warnings() []ResolverWarning {
	if r == nil || len(r.bindings) == 0 {
		return nil
	}
	var out []ResolverWarning
	for _, st := range r.bindings {
		for label, sources := range st.collisionPairs {
			if len(sources) < 2 {
				continue
			}
			ids := make([]string, 0, len(sources))
			for src := range sources {
				ids = append(ids, src)
			}
			out = append(out, ResolverWarning{
				Code:    errors.PULSE_LABEL_COLLISION,
				Message: fmt.Sprintf("label %q in field %q collides across source values %v", label, st.field, ids),
				Details: map[string]any{
					"field":   st.field,
					"table":   st.tableName,
					"label":   label,
					"sources": ids,
				},
			})
		}
		if len(st.missCounts) > 0 {
			total := 0
			ids := make([]string, 0, len(st.missCounts))
			for src, c := range st.missCounts {
				total += c
				ids = append(ids, src)
			}
			out = append(out, ResolverWarning{
				Code:    errors.PULSE_LABEL_LOOKUP_MISS,
				Message: fmt.Sprintf("field %q: %d row(s) with values not in label table %q", st.field, total, st.tableName),
				Details: map[string]any{
					"field":             st.field,
					"table":             st.tableName,
					"unresolved_count":  total,
					"unresolved_values": ids,
				},
			})
		}
	}
	return out
}

// resolveLabel performs the actual table lookup. Returns the label,
// a hit-bool, and any error surfaced by a function-backed table.
// Errors are not propagated further today; the caller treats them as
// misses for the warning aggregator.
func resolveLabel(tbl LabelTable, key string) (string, bool, error) {
	if tbl.Rows != nil {
		v, ok := tbl.Rows[key]
		return v, ok, nil
	}
	if tbl.Lookup != nil {
		return tbl.Lookup(key)
	}
	return "", false, nil
}

// lookupLabelTable returns the label table registered under name.
// Falls through cleanly on nil registry.
func lookupLabelTable(r *ExtensionRegistry, name string) (LabelTable, bool) {
	if r == nil || r.LabelTables == nil {
		return LabelTable{}, false
	}
	t, ok := r.LabelTables[name]
	return t, ok
}

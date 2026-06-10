package processing

import (
	"sort"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Set-typed groupers — GROUP_SET_VALUE (atomic mask → one bucket per
// unique combination) and GROUP_SET_PER_ELEMENT (per-bit fan-out: one
// row → N buckets, one per selected element).
//
// GROUP_SET_VALUE is a normal single-key StreamingGrouper. PER_ELEMENT
// implements MultiKeyStreamingGrouper so the streaming orchestrator
// can fan a single record into multiple per-key buckets without
// buffering.

// resolveSetGrouperDict returns the field's dictionary, validating
// the field exists and is a set type. Construction with nil schema is
// tolerated for registry tests; runtime callers always pass a schema.
func resolveSetGrouperDict(grp *types.Group, schema *encoding.Schema) (*encoding.Dictionary, error) {
	if schema == nil {
		return nil, nil
	}
	if grp.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(grp.Type)+" requires field")
	}
	f := schema.Field(grp.Field)
	if f == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(grp.Type)+": unknown field "+grp.Field)
	}
	if !f.Type.IsSet() {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(grp.Type)+": field "+grp.Field+" is not a set type")
	}
	return f.Dictionary, nil
}

// --- GROUP_SET_VALUE ------------------------------------------------
//
// Atomic mask = bucket. Key stringified as sorted label list
// (pipe-delimited). Empty mask = "" key, matching how other groupers
// stringify the zero value.

type setValueGrouper struct {
	dict *encoding.Dictionary
}

func newSetValueGrouper(grp *types.Group, schema *encoding.Schema) (Grouper, error) {
	dict, err := resolveSetGrouperDict(grp, schema)
	if err != nil {
		return nil, err
	}
	return &setValueGrouper{dict: dict}, nil
}

func (g *setValueGrouper) KeyForRow(r *Record, field string) (string, bool, error) {
	m, ok := r.SetValue(field)
	if !ok {
		return "", false, nil
	}
	labels := resolveMaskLabels(m, g.dict)
	sort.Strings(labels)
	return strings.Join(labels, "|"), true, nil
}

func (g *setValueGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups := make(map[string][]*Record)
	for _, r := range records {
		key, ok, err := g.KeyForRow(r, field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		groups[key] = append(groups[key], r)
	}
	return groups, nil
}

// --- GROUP_SET_PER_ELEMENT ------------------------------------------
//
// Per-bit fan-out. One row → one bucket per set bit. Empty-mask rows
// contribute to zero buckets (consistent with skipping nulls). The
// same record pointer is shared across all destination buckets — the
// buffered Group() path appends the pointer N times; the streaming
// path calls KeysForRow and the orchestrator drives UpdateRow per
// resulting bucket.

type setPerElementGrouper struct {
	dict *encoding.Dictionary
}

func newSetPerElementGrouper(grp *types.Group, schema *encoding.Schema) (Grouper, error) {
	dict, err := resolveSetGrouperDict(grp, schema)
	if err != nil {
		return nil, err
	}
	return &setPerElementGrouper{dict: dict}, nil
}

func (g *setPerElementGrouper) KeysForRow(r *Record, field string) ([]string, bool, error) {
	m, ok := r.SetValue(field)
	if !ok {
		return nil, false, nil
	}
	labels := resolveMaskLabels(m, g.dict)
	if len(labels) == 0 {
		return nil, false, nil
	}
	return labels, true, nil
}

func (g *setPerElementGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups := make(map[string][]*Record)
	for _, r := range records {
		labels, ok, err := g.KeysForRow(r, field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, label := range labels {
			groups[label] = append(groups[label], r)
		}
	}
	return groups, nil
}

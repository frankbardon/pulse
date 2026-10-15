package service

import (
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/types"
)

// Label NOT-applied reasons (PULSE_LABEL_NOT_APPLIED details.reason).
const (
	// labelReasonNoSurface: the bound field is neither an output column
	// nor a key axis of this response, so no value carried a raw code
	// the binding could translate.
	labelReasonNoSurface = "field_not_in_output"
	// labelReasonAugmentAxis: an augment-mode binding on a crosstab
	// matrix axis — an axis key is a tuple, it has no sibling column
	// for the label to ride beside the raw value.
	labelReasonAugmentAxis = "augment_on_matrix_axis"
)

// labelPass is one response's label application. It translates every
// raw categorical value through the resolver at most once per distinct
// (field, value) beyond the Data rows, so a crosstab axis, an overlay
// key and a group key carry the SAME display string the Data rows do
// and a miss is counted by the row walk alone.
type labelPass struct {
	r *processing.LabelResolver
	// out maps field → raw → display string, for replace-mode hits.
	out map[string]map[string]string
	// missed marks (field, raw) values the resolver already missed on.
	missed map[string]map[string]bool
	// reached records every bound field some response surface carries.
	reached map[string]bool
	// augmentAxis records augment-mode bound fields found on a matrix
	// axis (PULSE_LABEL_NOT_APPLIED, labelReasonAugmentAxis).
	augmentAxis map[string]bool
}

func newLabelPass(r *processing.LabelResolver) *labelPass {
	return &labelPass{
		r:           r,
		out:         map[string]map[string]string{},
		missed:      map[string]map[string]bool{},
		reached:     map[string]bool{},
		augmentAxis: map[string]bool{},
	}
}

// record notes a replace-mode hit made by the Data row walk.
func (p *labelPass) record(field, raw, out string) {
	m := p.out[field]
	if m == nil {
		m = map[string]string{}
		p.out[field] = m
	}
	m[raw] = out
}

// translate is the replace-mode display string for one key element of
// field: the Data walk's answer when it saw the value, else one
// resolver call per distinct value. A non-string element (a numeric
// bin) and an unbound or augment-mode field come back unchanged.
func (p *labelPass) translate(field string, v any) any {
	if !p.r.Has(field) {
		return v
	}
	p.reached[field] = true
	if p.r.Mode(field) != types.LabelModeReplace {
		return v
	}
	raw, ok := rawAsString(v)
	if !ok {
		return v
	}
	if out, ok := p.out[field][raw]; ok {
		return out
	}
	if p.missed[field][raw] {
		return v
	}
	out, _, hit := p.r.Apply(field, raw)
	if !hit {
		if p.missed[field] == nil {
			p.missed[field] = map[string]bool{}
		}
		p.missed[field][raw] = true
		return v
	}
	p.record(field, raw, out)
	return out
}

// tuple relabels one key tuple position by position — fields[i] names
// position i — into a FRESH slice: a host axis and an overlay layer may
// share key slices, so an in-place rewrite would translate a value
// twice. Positions past fields stay as they are.
func (p *labelPass) tuple(k types.AxisKey, fields []string) types.AxisKey {
	if k == nil {
		return nil
	}
	out := make(types.AxisKey, len(k))
	for i, v := range k {
		if i < len(fields) {
			out[i] = p.translate(fields[i], v)
		} else {
			out[i] = v
		}
	}
	return out
}

// axis relabels a whole axis-key list into a fresh list and returns
// the raw → relabelled pairing the overlay keys are re-keyed by.
func (p *labelPass) axis(keys []types.AxisKey, header types.AxisHeader, matrixAxis bool) ([]types.AxisKey, axisMap) {
	if keys == nil {
		return nil, axisMap{}
	}
	if matrixAxis {
		for _, f := range header.Fields {
			if p.r.Has(f) && p.r.Mode(f) == types.LabelModeAugment {
				p.augmentAxis[f] = true
			}
		}
	}
	m := axisMap{pair: map[string]string{}, key: map[string]types.AxisKey{}}
	out := make([]types.AxisKey, len(keys))
	for i, k := range keys {
		out[i] = p.tuple(k, header.Fields)
		m.pair[processing.PairKeyElement(k)] = processing.PairKeyElement(out[i])
		m.key[canonicalAxisKey(k)] = out[i]
	}
	return out, m
}

// axisMap pairs one host axis's raw keys with their relabelled form:
// pair by the pairwise overlays' PAIR element string, key by the
// canonical tuple a SERIES overlay entry repeats.
type axisMap struct {
	pair map[string]string
	key  map[string]types.AxisKey
}

// canonicalAxisKey is a type-qualified identity for a key tuple, so a
// SERIES entry finds the host key it repeats ("1" the string and 1 the
// number never alias).
func canonicalAxisKey(k types.AxisKey) string {
	var b strings.Builder
	for i, v := range k {
		if i > 0 {
			b.WriteByte('\x1f')
		}
		fmt.Fprintf(&b, "%T:%v", v, v)
	}
	return b.String()
}

// isPairHeader reports a pairwise overlay's PAIR axis header.
func isPairHeader(h types.AxisHeader) bool {
	if len(h.Types) == 0 {
		return false
	}
	for _, t := range h.Types {
		if t != "PAIR" {
			return false
		}
	}
	return true
}

// applyLabelSurfaces carries the label bindings past resp.Data onto
// every key surface derived from the same raw values: the crosstab
// matrix axes, every overlay layer's keys (a pairwise PAIR key is
// rebuilt from the relabelled host key it names), and the group-key
// tuples of a grouped run (Tests / PostTests, Matrices, and the
// Components entries keyed to them). Overlays stay read-only siblings
// keyed to host coordinates: only key TEXT changes, never a position or
// a figure, so an overlay coordinate still addresses the same host
// cell. groups is the request's grouper slot list (nil for a crosstab).
func (p *labelPass) applyLabelSurfaces(resp *types.Response, groups []*types.Group) {
	var rows, cols axisMap
	if resp.Crosstab != nil && resp.Crosstab.Matrix != nil {
		m := resp.Crosstab.Matrix
		m.RowKeys, rows = p.axis(m.RowKeys, m.RowHeader, true)
		m.ColumnKeys, cols = p.axis(m.ColumnKeys, m.ColumnHeader, true)
	}
	keyCols := seriespartition.KeyColumns(groups)
	for i := range resp.Overlays {
		p.payload(&resp.Overlays[i].Payload, rows, cols, keyCols)
	}
	if len(keyCols) == 0 {
		return
	}
	for _, t := range resp.Tests {
		if t != nil {
			t.GroupKey = p.tuple(t.GroupKey, keyCols)
		}
	}
	for _, t := range resp.PostTests {
		if t != nil {
			t.GroupKey = p.tuple(t.GroupKey, keyCols)
		}
	}
	for i := range resp.Matrices {
		fields := keyCols
		if h := resp.Matrices[i].GroupHeader; h != nil {
			fields = h.Fields
		}
		resp.Matrices[i].GroupKey = p.tuple(resp.Matrices[i].GroupKey, fields)
	}
	if c := resp.Components; c != nil {
		for i := range c.Aggregations {
			for j := range c.Aggregations[i].Groups {
				g := &c.Aggregations[i].Groups[j]
				g.GroupKey = p.tuple(g.GroupKey, keyCols)
			}
		}
		for i := range c.Matrices {
			c.Matrices[i].GroupKey = p.tuple(c.Matrices[i].GroupKey, keyCols)
		}
	}
}

// payload relabels one overlay payload's keys, its p_adjusted /
// significant_adjusted twins and its named parts.
func (p *labelPass) payload(pl *types.OverlayPayload, rows, cols axisMap, keyCols []string) {
	if pl == nil {
		return
	}
	for _, mx := range []*types.MatrixPayload{pl.Matrix, pl.PAdjusted, pl.SignificantAdjusted} {
		p.overlayMatrix(mx, rows, cols)
	}
	if pl.Series != nil {
		for i := range pl.Series.Entries {
			e := &pl.Series.Entries[i]
			id := canonicalAxisKey(e.Key)
			if k, ok := rows.key[id]; ok {
				e.Key = append(types.AxisKey(nil), k...)
			} else if k, ok := cols.key[id]; ok {
				e.Key = append(types.AxisKey(nil), k...)
			} else if len(keyCols) > 0 {
				e.Key = p.tuple(e.Key, keyCols)
			}
		}
	}
	for _, part := range pl.Parts {
		p.payload(part, rows, cols, keyCols)
	}
}

// overlayMatrix relabels a MATRIX overlay payload's two axes: a PAIR
// axis through the host axis it pairs (row scope → the host rows), any
// other axis by its own header fields, as the host's were.
func (p *labelPass) overlayMatrix(mx *types.MatrixPayload, rows, cols axisMap) {
	if mx == nil {
		return
	}
	if isPairHeader(mx.RowHeader) {
		mx.RowKeys = pairKeys(mx.RowKeys, rows)
	} else {
		mx.RowKeys, _ = p.axis(mx.RowKeys, mx.RowHeader, false)
	}
	if isPairHeader(mx.ColumnHeader) {
		mx.ColumnKeys = pairKeys(mx.ColumnKeys, cols)
	} else {
		mx.ColumnKeys, _ = p.axis(mx.ColumnKeys, mx.ColumnHeader, false)
	}
}

// pairKeys rebuilds each PAIR key's two legs from the relabelled host
// keys they name; a leg the host axis does not list stays as written.
func pairKeys(keys []types.AxisKey, host axisMap) []types.AxisKey {
	if keys == nil {
		return nil
	}
	out := make([]types.AxisKey, len(keys))
	for i, k := range keys {
		nk := make(types.AxisKey, len(k))
		for j, v := range k {
			nk[j] = v
			if s, ok := v.(string); ok {
				if l, ok := host.pair[s]; ok {
					nk[j] = l
				}
			}
		}
		out[i] = nk
	}
	return out
}

// notAppliedWarnings is one PULSE_LABEL_NOT_APPLIED warning per
// caller-authored binding that reached no surface of the response, or
// that reached a crosstab matrix axis in augment mode. A default
// binding the instance injected (Options.AutoLabels) never warns: it
// is offered to every request whose schema carries the field, used or
// not. groups lists the grouper slots, whose key columns count as
// reached even when the run produced no row.
func (p *labelPass) notAppliedWarnings(bindings, auto []*types.LabelBinding, groups []*types.Group) []*types.ResponseWarning {
	for _, c := range seriespartition.KeyColumns(groups) {
		if p.r.Has(c) {
			p.reached[c] = true
		}
	}
	var out []*types.ResponseWarning
	for i, b := range bindings {
		if b == nil || isAutoBinding(b, auto) {
			continue
		}
		reason := ""
		switch {
		case p.augmentAxis[b.Field]:
			reason = labelReasonAugmentAxis
		case !p.reached[b.Field]:
			reason = labelReasonNoSurface
		}
		if reason == "" {
			continue
		}
		out = append(out, labelNotApplied(i, b, reason))
	}
	return out
}

// labelNotApplied builds one PULSE_LABEL_NOT_APPLIED warning.
func labelNotApplied(index int, b *types.LabelBinding, reason string) *types.ResponseWarning {
	msg := fmt.Sprintf("labels[%d]: binding on field %q was not applied: the field is not an output column or key of this response", index, b.Field)
	if reason == labelReasonAugmentAxis {
		msg = fmt.Sprintf("labels[%d]: augment-mode binding on field %q was not applied to the crosstab matrix axis keys (an axis key has no sibling column); use mode \"replace\" or shape \"long\"", index, b.Field)
	}
	return &types.ResponseWarning{
		Code:    string(errors.PULSE_LABEL_NOT_APPLIED),
		Message: msg,
		Details: map[string]any{
			"index":  index,
			"field":  b.Field,
			"table":  b.Table,
			"mode":   string(b.LabelModeOrDefault()),
			"reason": reason,
		},
	}
}

// isAutoBinding reports whether b is one of the instance's default
// bindings (Options.AutoLabels), compared by value: the injected copy
// is a fresh pointer.
func isAutoBinding(b *types.LabelBinding, auto []*types.LabelBinding) bool {
	for _, a := range auto {
		if a != nil && a.Field == b.Field && a.Table == b.Table && a.LabelModeOrDefault() == b.LabelModeOrDefault() {
			return true
		}
	}
	return false
}

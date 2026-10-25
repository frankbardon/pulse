package sem

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// invariance.go is the multi-group equality set (lavaan's group.equal /
// group.partial) and lavaan's per-group identification. Every group's
// copy of the model is specified from the one description (its
// per-group modifiers c(...) read at the group's level); then, as
// lavaan's lavaanify does:
//
//  1. outside the reference group, group_equal loadings frees the
//     specification's marker loadings and group_equal intercepts
//     (without means) frees the unstated latent means;
//  2. a row of a group_equal class (GroupEqualValues) that is not a
//     group_partial row and carries no label is one equality class
//     across groups, keyed by its row ("lhs op rhs"); a labelled row is
//     the label's class (one value wherever the label appears);
//  3. a class holding a fixed member is fixed everywhere at the first
//     fixed member's value (level order) — so equal loadings keep every
//     group's marker at 1, unless group_partial frees it;
//  4. the free members of a class share one joint parameter.

// GroupEqualValues are the accepted group_equal values, lavaan's
// group.equal vocabulary (thresholds needs an ordinal estimator).
func GroupEqualValues() []string {
	return []string{"loadings", "intercepts", "means", "residuals", "residual.covariances",
		"lv.variances", "lv.covariances", "regressions"}
}

// GroupSpecError is a refused multi-group equality spec: Param is the
// knob at fault (group_equal or group_partial), Value the entry.
type GroupSpecError struct {
	Param, Value, Msg string
}

func (e *GroupSpecError) Error() string { return e.Msg }

// Arity is the per-group modifiers' length: the group count a
// description with c(...) lists fits (0: no per-group modifier).
func (m *Model) Arity() int { return m.arity }

// CheckGroupCount refuses per-group modifiers whose length is not the
// group count n.
func (m *Model) CheckGroupCount(n int) error {
	if m.arity > 0 && m.arity != n {
		return fmt.Errorf("per-group modifiers c(...) list %d values, but the fit has %d groups; list one value per group", m.arity, n)
	}
	return nil
}

// specifyEquality validates and records opt's group_equal values and
// group_partial rows on a multi-group model (m is the reference
// specification).
func (m *Model) specifyEquality(opt ModelOptions) error {
	m.equal, m.partial = stringSet{}, stringSet{}
	for _, v := range opt.GroupEqual {
		if v == GroupEqualThresholds {
			if len(m.ordered) == 0 {
				return &GroupSpecError{Param: "group_equal", Value: v,
					Msg: `group_equal "thresholds" needs an ordinal estimator; the ML fit has no thresholds`}
			}
			m.equal.add(v)
			continue
		}
		if !slices.Contains(GroupEqualValues(), v) {
			return &GroupSpecError{Param: "group_equal", Value: v,
				Msg: fmt.Sprintf("group_equal %q is not a lavaan group.equal value (%s)", v, strings.Join(GroupEqualValues(), ", "))}
		}
		m.equal.add(v)
	}
	rows := stringSet{}
	for _, p := range m.params {
		for _, l := range p.locs {
			k := m.lavaanRowKey(l)
			rows.add(k)
			if l.kind == kindCovariance {
				lv, rv := m.rowNames(l)
				rows.add(rv + OpCovariance + lv)
			}
		}
	}
	for _, v := range opt.GroupPartial {
		k := strings.Join(strings.Fields(v), "")
		if m.thresholdRowKey(k) {
			m.partial.add(k)
			continue
		}
		if !rows.has(k) {
			return &GroupSpecError{Param: "group_partial", Value: v,
				Msg: fmt.Sprintf("group_partial %q names no parameter of the model; write a row as lavaan does (f=~x2, x1~1, x1~~x2, y~x)", v)}
		}
		m.partial.add(k)
		if lhs, rhs, ok := strings.Cut(k, OpCovariance); ok {
			m.partial.add(rhs + OpCovariance + lhs)
		}
	}
	return nil
}

// GroupEqualThresholds is the group_equal value holding every ordered
// variable's thresholds equal across groups (an ordinal fit only).
const GroupEqualThresholds = "thresholds"

// thresholdRowKey reports whether k (no spaces) is a threshold row
// "x|tk" of an ordered variable x, k ≥ 1 — a group_partial row an
// ordinal fit frees from group_equal thresholds. Whether the data hold
// a k-th threshold is known only at the fit (FitOrdinalGroups).
func (m *Model) thresholdRowKey(k string) bool {
	v, t, ok := strings.Cut(k, OpThreshold)
	if !ok || !m.ordered.has(v) || len(t) < 2 || t[0] != 't' {
		return false
	}
	n, err := strconv.Atoi(t[1:])
	return err == nil && n >= 1 && strconv.Itoa(n) == t[1:]
}

// thresholdClassKey is threshold k (0-based) of ordered variable v's
// cross-group equality class ("" none): its row under group_equal
// thresholds unless group_partial frees it.
func (m *Model) thresholdClassKey(v string, k int) string {
	row := v + OpThreshold + "t" + strconv.Itoa(k+1)
	if m.equal.has(GroupEqualThresholds) && !m.partial.has(row) {
		return "row:" + row
	}
	return ""
}

// lavaanRowKey is a location's lavaan row label "lhs op rhs" (no
// spaces): f=~x2, y~x, x1~~x2, x1~1.
func (m *Model) lavaanRowKey(l location) string {
	lv, rv := m.rowNames(l)
	switch l.kind {
	case kindLoading:
		return rv + OpMeasurement + lv
	case kindMean:
		return lv + OpIntercept
	case kindCovariance:
		return lv + OpCovariance + rv
	default:
		return lv + OpRegression + rv
	}
}

// equalClass is the group_equal value covering a location ("" none):
// lavaan's classes, observed exogenous (fixed.x) moments excluded.
func (m *Model) equalClass(l location) string {
	lv, rv := m.rowNames(l)
	switch l.kind {
	case kindLoading:
		return "loadings"
	case kindRegression:
		return "regressions"
	case kindMean:
		if m.latent.has(lv) {
			return "means"
		}
		if m.isObsExo(lv) {
			return ""
		}
		return "intercepts"
	}
	switch {
	case m.latent.has(lv) && lv == rv:
		return "lv.variances"
	case m.latent.has(lv):
		return "lv.covariances"
	case m.isObsExo(lv):
		return ""
	case lv == rv:
		return "residuals"
	}
	return "residual.covariances"
}

// classKey is a parameter's cross-group equality class ("" none): its
// label, else its row when group_equal covers it and group_partial
// does not.
func (m *Model) classKey(p *parameter) string {
	if p.user {
		return "label:" + p.name
	}
	l := p.locs[0]
	k := m.lavaanRowKey(l)
	if c := m.equalClass(l); c != "" && m.equal.has(c) && !m.partial.has(k) {
		return "row:" + k
	}
	return ""
}

// groupModels specifies every group's copy of the reference model m:
// the group's per-group modifiers (levels[g]), lavaan's
// non-reference freeing, then each equality class holding a fixed
// member fixed throughout (the steps at the top of the file).
func (m *Model) groupModels(levels []int, ref int) ([]*Model, error) {
	out := make([]*Model, len(levels))
	for g, lvl := range levels {
		o := m.opt
		o.level = lvl
		mg, err := newModel(m.desc, false, o)
		if err != nil {
			return nil, err
		}
		mg.equal, mg.partial = m.equal, m.partial
		if g != ref {
			// lavaan's lav_pt_flat: equal loadings free the markers,
			// equal intercepts (without means) the latent means, and
			// equal thresholds an ordered variable's intercept (its
			// scale factor is FitOrdinalGroups').
			freeLoadings := mg.equal.has("loadings")
			freeMeans := mg.equal.has("intercepts") && !mg.equal.has("means")
			freeOrdered := mg.equal.has(GroupEqualThresholds)
			for _, p := range mg.params {
				if !p.auto {
					continue
				}
				switch l := p.locs[0]; {
				case l.kind == kindLoading && freeLoadings,
					l.kind == kindMean && l.mat == mAlpha && freeMeans && mg.latent.has(mg.Inner[l.i]),
					l.kind == kindMean && l.mat == mNu && freeOrdered && mg.ordered.has(mg.Observed[l.i]):
					p.active, p.hasStart = true, false
				}
			}
		}
		out[g] = mg
	}
	fixed := map[string]float64{}
	for _, mg := range out {
		for _, p := range mg.params {
			if k := mg.classKey(p); k != "" && !p.active && p.hasStart {
				if _, ok := fixed[k]; !ok {
					fixed[k] = p.start
				}
			}
		}
	}
	for _, mg := range out {
		for _, p := range mg.params {
			if v, ok := fixed[mg.classKey(p)]; ok && p.active {
				p.active, p.start, p.hasStart = false, v, true
			}
		}
	}
	return out, nil
}

// GroupFreeRows is how many parameter-table rows carry a free
// parameter across a multi-group fit of model (NewGroupModel) over
// levels groups (the first the reference): every group's free rows, the
// specification's non-reference freeing and equality fixing applied.
// The rows a member multiplicity block corrects, less the :=
// rows.
func GroupFreeRows(model *Model, levels int) (int, error) {
	lv := make([]int, levels)
	for g := range lv {
		lv[g] = g
	}
	models, err := model.groupModels(lv, 0)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, mg := range models {
		for _, e := range SpecifiedRows(mg) {
			if e.Free {
				n++
			}
		}
	}
	return n, nil
}

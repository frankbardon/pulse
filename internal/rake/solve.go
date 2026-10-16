package rake

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Data is every record's margin categories, group and base weight, read
// once by Plan.Load.
type Data struct {
	n int
	// codes[m][i] is record i's category index in margin m, -1 when the
	// record lacks a value for it (missing "base").
	codes [][]int32
	// group[i] is record i's group index into groups (all 0 without by).
	group  []int32
	groups []string
	base   []float64
}

// Len is the number of records read.
func (d *Data) Len() int { return d.n }

// valueKey spells one exact typed cell (pulse.CohortRow conventions) as
// a category key; ok is false for a null.
func valueKey(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case uint64:
		return strconv.FormatUint(x, 10), true
	}
	return "", false
}

func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// tally counts records with one refused condition, keeping the first.
type tally struct {
	rows  int64
	first int64
}

func (t *tally) add(i int64) {
	if t.rows == 0 {
		t.first = i
	}
	t.rows++
}

// Load reads n records through at, which returns record i as exact
// typed values in schema LOGICAL order (CohortReader.RecordAt). It
// refuses an invalid base weight (PULSE_RAKE_INVALID), a null margin or
// by value under missing "refuse" (PULSE_RAKE_MISSING_VALUE) and a
// category without a target (PULSE_RAKE_UNTARGETED_CATEGORY) — after
// the full read, so the details count every offending record.
func (p *Plan) Load(ctx context.Context, n int64, at func(i int64) ([]any, error)) (*Data, error) {
	d := &Data{n: int(n), codes: make([][]int32, len(p.margins)), group: make([]int32, n), base: make([]float64, n)}
	for m := range d.codes {
		d.codes[m] = make([]int32, n)
	}
	missing := make([]tally, len(p.margins))
	var missingBy tally
	type untargeted struct {
		values []string
		tally
	}
	untarg := make([]map[string]*untargeted, len(p.margins))
	var untargOrder [][]string
	groupIdx := map[string]int32{}
	var groupVals []string
	vals := make([]string, 0, 4)
	for i := int64(0); i < n; i++ {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		row, err := at(i)
		if err != nil {
			return nil, err
		}
		for m := range p.margins {
			pm := &p.margins[m]
			vals = vals[:0]
			null := false
			for _, f := range pm.fields {
				k, ok := valueKey(row[f.index])
				if !ok {
					null = true
					break
				}
				vals = append(vals, k)
			}
			if null {
				d.codes[m][i] = -1
				if !p.missingBase {
					missing[m].add(i)
				}
				continue
			}
			key := strings.Join(vals, keySep)
			c, ok := pm.index[key]
			if !ok {
				if untarg[m] == nil {
					untarg[m] = map[string]*untargeted{}
				}
				u := untarg[m][key]
				if u == nil {
					u = &untargeted{values: append([]string(nil), vals...)}
					untarg[m][key] = u
					untargOrder = append(untargOrder, []string{strconv.Itoa(m), key})
				}
				u.add(i)
				continue
			}
			d.codes[m][i] = int32(c)
		}
		if p.by != nil {
			k, ok := valueKey(row[p.by.index])
			if !ok {
				missingBy.add(i)
			} else {
				g, seen := groupIdx[k]
				if !seen {
					g = int32(len(groupVals))
					groupIdx[k] = g
					groupVals = append(groupVals, k)
				}
				d.group[i] = g
			}
		}
		d.base[i] = 1
		if p.base != nil {
			w, present := numeric(row[p.base.index])
			if row[p.base.index] == nil {
				present = false
			}
			if r := weighting.Classify(w, present, types.WeightKindProbability); r != weighting.Valid {
				return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RAKE_INVALID,
					"the base weight is "+r.Key()+" on a record; a base weight must be a finite non-negative number on every record",
					map[string]any{"reason": "base_weight_invalid", "field": "base_weight", "row": i, "value": r.Key()})
			}
			d.base[i] = w
		}
	}
	for m := range p.margins {
		if missing[m].rows > 0 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RAKE_MISSING_VALUE,
				"records have a null value in a margin field and missing is \"refuse\"",
				map[string]any{"margin": m, "field": strings.Join(p.margins[m].fieldNames(), ","), "rows": missing[m].rows, "first_row": missing[m].first})
		}
	}
	if missingBy.rows > 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RAKE_MISSING_VALUE,
			"records have a null by value; a record without a group cannot be raked",
			map[string]any{"field": p.by.name, "rows": missingBy.rows, "first_row": missingBy.first})
	}
	if len(untargOrder) > 0 {
		m, _ := strconv.Atoi(untargOrder[0][0])
		u := untarg[m][untargOrder[0][1]]
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RAKE_UNTARGETED_CATEGORY,
			"records hold a margin category that has no target",
			map[string]any{"margin": m, "fields": p.margins[m].fieldNames(), "values": u.values, "rows": u.rows, "first_row": u.first})
	}
	if p.by == nil {
		d.groups = []string{""}
		return d, nil
	}
	// Groups report in the by field's own order (dictionary position,
	// false before true, numeric), never first appearance.
	order := make([]int32, len(groupVals))
	for g := range order {
		order[g] = int32(g)
	}
	sort.Slice(order, func(a, b int) bool {
		ra, _ := p.by.rank(groupVals[order[a]])
		rb, _ := p.by.rank(groupVals[order[b]])
		return ra < rb
	})
	remap := make([]int32, len(groupVals))
	d.groups = make([]string, len(groupVals))
	for pos, g := range order {
		remap[g] = int32(pos)
		d.groups[pos] = groupVals[g]
	}
	for i := range d.group {
		d.group[i] = remap[d.group[i]]
	}
	return d, nil
}

// Solution is a solved rake: one weight per record plus the report.
type Solution struct {
	// Weights holds record i's output weight.
	Weights  []float64
	Report   Report
	Warnings []*errors.CodedError
}

// Solve rakes every group to the plan's margins: sweeps of iterative
// proportional fitting over the records (each margin in request order
// rescales its categories to target share × the current weight of the
// records it counts), the caps after every sweep, until the largest
// share gap is within tol or max_iter sweeps ran; then each group is
// normalised to its record count (or Total). A category with a positive
// target and no weight in a group is PULSE_RAKE_EMPTY_CELL; a group that
// does not converge adds a PULSE_RAKE_NOT_CONVERGED warning and keeps
// its last sweep.
func (p *Plan) Solve(ctx context.Context, d *Data) (*Solution, error) {
	members := make([][]int, len(d.groups))
	for i := 0; i < d.n; i++ {
		members[d.group[i]] = append(members[d.group[i]], i)
	}
	w := make([]float64, d.n)
	copy(w, d.base)
	sol := &Solution{Weights: w}
	capped := make([]bool, d.n)
	// Observed (base-weighted) shares, per group, before any sweep.
	obsShares := make([][][]float64, len(d.groups))
	obsN := make([][][]int64, len(d.groups))
	for g, rows := range members {
		obsShares[g], obsN[g] = p.shares(d, w, rows)
	}
	for g, rows := range members {
		if err := p.checkEmpty(d, g, obsShares[g]); err != nil {
			return nil, err
		}
		gr, err := p.fit(ctx, d, w, rows, capped)
		if err != nil {
			return nil, err
		}
		gr.Value = d.groups[g]
		gr.Records = int64(len(rows))
		gr.Margins = p.marginReports(d, w, rows, obsShares[g], obsN[g])
		gr.Efficiency = efficiency(w, rows, capped)
		if !gr.Converged {
			details := map[string]any{"iterations": gr.Iterations, "residual": gr.Residual, "tol": p.tol}
			if p.by != nil {
				details["group"] = gr.Value
			}
			sol.Warnings = append(sol.Warnings, errors.NewCodedErrorWithDetails(errors.PULSE_RAKE_NOT_CONVERGED,
				"raking stopped at max_iter before every margin reached its target within tol", details))
		}
		sol.Report.Groups = append(sol.Report.Groups, gr)
	}
	all := make([]int, d.n)
	for i := range all {
		all[i] = i
	}
	r := &sol.Report
	r.Converged = true
	for _, gr := range r.Groups {
		r.Iterations = max(r.Iterations, gr.Iterations)
		r.Residual = math.Max(r.Residual, gr.Residual)
		r.Converged = r.Converged && gr.Converged
	}
	if p.by == nil {
		// One implicit group: its fit IS the report.
		for _, g := range r.Groups {
			r.Margins, r.Efficiency = g.Margins, g.Efficiency
		}
		r.Groups = nil
		return sol, nil
	}
	pooled, pooledN := p.shares(d, d.base, all)
	r.Margins = p.marginReports(d, w, all, pooled, pooledN)
	r.Efficiency = efficiency(w, all, capped)
	return sol, nil
}

// shares returns, per margin and category, the w-weighted share among
// the rows holding a value for the margin, and the record counts.
func (p *Plan) shares(d *Data, w []float64, rows []int) ([][]float64, [][]int64) {
	out := make([][]float64, len(p.margins))
	ns := make([][]int64, len(p.margins))
	for m := range p.margins {
		sums := make([]float64, len(p.margins[m].keys))
		cnt := make([]int64, len(sums))
		total := 0.0
		for _, i := range rows {
			c := d.codes[m][i]
			if c < 0 {
				continue
			}
			sums[c] += w[i]
			cnt[c]++
			total += w[i]
		}
		for c := range sums {
			sums[c] /= total
		}
		out[m], ns[m] = sums, cnt
	}
	return out, ns
}

func (p *Plan) checkEmpty(d *Data, g int, obs [][]float64) error {
	for m := range p.margins {
		pm := &p.margins[m]
		for c, s := range pm.shares {
			if s > 0 && !(obs[m][c] > 0) {
				details := map[string]any{"margin": m, "fields": pm.fieldNames(), "values": pm.keys[c], "target": s}
				if p.by != nil {
					details["group"] = d.groups[g]
				}
				return errors.NewCodedErrorWithDetails(errors.PULSE_RAKE_EMPTY_CELL,
					"a margin category with a positive target holds no weighted record", details)
			}
		}
	}
	return nil
}

// fit runs the sweeps for one group in place and normalises it.
func (p *Plan) fit(ctx context.Context, d *Data, w []float64, rows []int, capped []bool) (GroupReport, error) {
	var gr GroupReport
	sums := make([][]float64, len(p.margins))
	for m := range p.margins {
		sums[m] = make([]float64, len(p.margins[m].keys))
	}
	n := float64(len(rows))
	for it := 1; it <= p.maxIter; it++ {
		if err := ctx.Err(); err != nil {
			return gr, err
		}
		for m := range p.margins {
			pm := &p.margins[m]
			total := p.categorySums(d, m, w, rows, sums[m])
			f := sums[m]
			for c := range f {
				switch {
				case f[c] > 0:
					f[c] = float64(pm.shares[c]*total) / f[c]
				default:
					// Nothing to rescale (an empty zero-target category).
					f[c] = 1
				}
			}
			for _, i := range rows {
				if c := d.codes[m][i]; c >= 0 {
					w[i] = float64(w[i] * f[c])
				}
			}
		}
		if p.minW > 0 || p.maxW > 0 {
			p.applyCaps(w, rows, capped)
		}
		gr.Iterations = it
		gr.Residual = p.residual(d, w, rows, sums)
		if gr.Residual <= p.tol {
			gr.Converged = true
			break
		}
	}
	sum := 0.0
	for _, i := range rows {
		sum += w[i]
	}
	target := n
	if p.total > 0 {
		target = p.total
	}
	scale := target / sum
	for _, i := range rows {
		w[i] = float64(w[i] * scale)
	}
	return gr, nil
}

// capRounds bounds the clip-and-rescale loop of one cap step.
const capRounds = 100

// applyCaps holds every weight of rows within [minW, maxW] × the mean
// weight: rescale to mean 1, clip, and repeat until a rescale moves no
// weight past a cap (the rescale after a clip shifts the mean). capped
// marks the records left at a cap. The scale is reset to Σw = n every
// round, so repeated sweeps never drift toward overflow.
func (p *Plan) applyCaps(w []float64, rows []int, capped []bool) {
	n := float64(len(rows))
	hi := math.Inf(1)
	if p.maxW > 0 {
		hi = p.maxW
	}
	for round := 0; round < capRounds; round++ {
		sum := 0.0
		for _, i := range rows {
			sum += w[i]
		}
		scale := n / sum
		changed := false
		for _, i := range rows {
			x := float64(w[i] * scale)
			switch {
			case x < p.minW:
				x, changed = p.minW, true
			case x > hi:
				x, changed = hi, true
			}
			w[i] = x
		}
		if !changed {
			break
		}
	}
	const rel = 1e-12
	for _, i := range rows {
		capped[i] = (p.minW > 0 && w[i] <= p.minW*(1+rel)) || w[i] >= hi*(1-rel)
	}
}

// categorySums fills sums with margin m's per-category weight over rows
// and returns their total.
func (p *Plan) categorySums(d *Data, m int, w []float64, rows []int, sums []float64) float64 {
	clear(sums)
	total := 0.0
	for _, i := range rows {
		if c := d.codes[m][i]; c >= 0 {
			sums[c] += w[i]
			total += w[i]
		}
	}
	return total
}

// residual is the largest |achieved share − target share| over every
// margin category.
func (p *Plan) residual(d *Data, w []float64, rows []int, scratch [][]float64) float64 {
	worst := 0.0
	for m := range p.margins {
		total := p.categorySums(d, m, w, rows, scratch[m])
		for c, s := range scratch[m] {
			gap := math.Abs(s/total - p.margins[m].shares[c])
			if math.IsNaN(gap) {
				return math.Inf(1)
			}
			worst = math.Max(worst, gap)
		}
	}
	return worst
}

func (p *Plan) marginReports(d *Data, w []float64, rows []int, obs [][]float64, obsN [][]int64) []MarginReport {
	ach, _ := p.shares(d, w, rows)
	out := make([]MarginReport, len(p.margins))
	for m := range p.margins {
		pm := &p.margins[m]
		mr := MarginReport{Fields: pm.fieldNames(), Categories: make([]CategoryReport, len(pm.keys))}
		for _, i := range rows {
			if d.codes[m][i] < 0 {
				mr.NMissing++
			}
		}
		for c := range pm.keys {
			mr.Categories[c] = CategoryReport{
				Values:    pm.keys[c],
				ObservedN: obsN[m][c],
				Observed:  obs[m][c],
				Target:    pm.shares[c],
				Achieved:  ach[m][c],
			}
		}
		out[m] = mr
	}
	return out
}

// efficiency is the Kish weighting-efficiency report over rows' final
// weights.
func efficiency(w []float64, rows []int, capped []bool) Efficiency {
	e := Efficiency{N: int64(len(rows)), WeightMin: math.NaN(), WeightMax: math.NaN()}
	sumSq := 0.0
	for k, i := range rows {
		x := w[i]
		e.SumWeights += x
		sumSq += float64(x * x)
		if k == 0 || x < e.WeightMin {
			e.WeightMin = x
		}
		if k == 0 || x > e.WeightMax {
			e.WeightMax = x
		}
		if capped[i] {
			e.NCapped++
		}
	}
	n := float64(len(rows))
	e.NEff = weighting.KishNEff(e.SumWeights, sumSq)
	e.DeffKish, e.EfficiencyPct, e.WeightRangeRatio, e.WeightCV = math.NaN(), math.NaN(), math.NaN(), math.NaN()
	if e.NEff > 0 {
		e.DeffKish = n / e.NEff
	}
	if n > 0 {
		e.EfficiencyPct = 100 * e.NEff / n
	}
	if e.WeightMin > 0 {
		e.WeightRangeRatio = e.WeightMax / e.WeightMin
	}
	if mean := e.SumWeights / n; n > 0 && mean > 0 {
		ss := 0.0
		for _, i := range rows {
			dv := w[i] - mean
			ss += float64(dv * dv)
		}
		e.WeightCV = math.Sqrt(ss/n) / mean
	}
	return e
}

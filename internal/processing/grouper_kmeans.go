package processing

import (
	"cmp"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/groupfit"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg/metric"
	"github.com/frankbardon/pulse/types"
)

// GROUP_KMEANS — deterministic, seeded k-means over the p members of a
// vector-input grouper slot (types.Group Vector / Fields, resolved onto
// the slot copy by Processor.prepare).
//
// Determinism is the contract: the labels are a function of the
// MULTISET of member tuples (and their weights), never of row order,
// shard order or worker count. Every figure is therefore computed over
// the CANONICAL point set:
//
//  1. Rows with a null (or non-finite) member are skipped (the null
//     bucket, n_null). A row's mass is 1 unweighted, else its frequency
//     weight (ClassFrequencyOnly; an invalid or zero weight carries no
//     mass — the row is still assigned to its nearest centroid, the
//     GROUP_QUANTILE rule).
//  2. The mass-carrying tuples are sorted lexicographically and merged:
//     one distinct point per tuple, its mass the sum (integer counts,
//     so the sum is exact in any order).
//  3. Standardization (default on) z-scores each member by the
//     mass-weighted mean and the ddof = 1 sd over the distinct points
//     (R's scale(); a zero-spread member is centred only).
//  4. n_init starts: start s seeds k-means++ from a math/rand/v2 PCG
//     stream (seed, s) — the first centre drawn by mass, each next by
//     mass·D² — then runs Lloyd iterations (assign every point to its
//     nearest centroid, ties to the lowest index; recompute each
//     centroid as its points' mass-weighted mean; an emptied cluster is
//     re-seeded at the point farthest from its centroid) until the
//     assignment repeats or max_iter passes. The lowest within-SS wins;
//     a tie keeps the earliest start.
//  5. Clusters are numbered cluster_1..cluster_k by descending mass,
//     ties broken by the lexicographic standardized centroid, and the
//     grouped exit emits them in that order (emissionOrder).
//
// Arithmetic is FMA-free (every product is rounded before it is
// summed, the linalg reference-kernel rule), so the labels and figures
// are bit-identical across architectures.

// kmeansSolver names the routine on PULSE_MATRIX_NOT_CONVERGED.
const kmeansSolver = "kmeans_lloyd"

// kmeansLabelPrefix prefixes every cluster key.
const kmeansLabelPrefix = "cluster_"

// kmeansPoint is one distinct canonical point.
type kmeansPoint struct {
	x    []float64 // raw member values
	z    []float64 // clustering-space coordinates
	mass float64
	rows int // rows (any mass) carrying this tuple, mass > 0 only
}

// kmeansFit is a finished clustering.
type kmeansFit struct {
	k          int
	centroids  [][]float64 // clustering space, label order
	original   [][]float64 // original units, label order
	mass       []float64   // label order
	withinSS   []float64   // label order
	totalSS    float64
	iterations int
	converged  bool
	bestInit   int // 1-based
	center     []float64
	scale      []float64
	distinct   int
	// points are the canonical distinct points and pointLabel each
	// one's final cluster (label order), so a mass-carrying row joins
	// exactly the cluster its point was fitted in.
	points     []kmeansPoint
	pointLabel []int
}

type kmeansGrouper struct {
	members []string
	cfg     vectors.KMeansConfig
	key     string
	weight  *types.WeightSpec

	fit      *kmeansFit
	order    *includeFilter
	counts   map[string]int
	finalize bool
}

func newKMeansGrouper(grp *types.Group, schema *encoding.Schema) (Grouper, error) {
	cfg, cerr := vectors.KMeansParams("groups", grp.Params)
	if cerr != nil {
		return nil, cerr
	}
	if _, cerr := groupfit.Parse(grp.Type, grp.Params); cerr != nil {
		return nil, cerr
	}
	members := grp.Fields
	if grp.Vector != "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"GROUP_KMEANS vector "+strconv.Quote(grp.Vector)+" was not resolved before execution",
			map[string]any{"vector": grp.Vector})
	}
	if len(members) > 0 && schema != nil {
		r, err := vectors.ResolveSpec("groups.fields", types.VectorSpec{Fields: members}, schema)
		if err != nil {
			return nil, err
		}
		members = r.Members
	}
	if len(members) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"GROUP_KMEANS sets neither vector nor fields; set exactly one",
			map[string]any{"reason": "no_vector_or_fields"})
	}
	return &kmeansGrouper{
		members: append([]string(nil), members...),
		cfg:     cfg,
		key:     vectors.GroupKeyField(types.GROUP_KMEANS, grp),
		weight:  grp.Weight.Spec(),
	}, nil
}

// kmeansRow is one non-null row's tuple and mass.
type kmeansRow struct {
	rec  *Record
	x    []float64
	mass float64
}

// Group implements Grouper: the field argument is ignored (the members
// come from the slot).
func (g *kmeansGrouper) Group(records []*Record, _ string) (map[string][]*Record, error) {
	g.finalize = true
	g.fit, g.order, g.counts = nil, nil, nil
	rows := make([]kmeansRow, 0, len(records))
	p := len(g.members)
	for _, r := range records {
		x := make([]float64, p)
		ok := true
		for j, m := range g.members {
			v, present := r.NumericValue(m)
			if !present || math.IsNaN(v) || math.IsInf(v, 0) {
				ok = false
				break
			}
			x[j] = v
		}
		if !ok {
			continue
		}
		mass := 1.0
		if g.weight != nil {
			w, reason := readWeight(r, g.weight)
			if reason != weighting.Valid || w <= 0 {
				mass = 0
			} else {
				mass = w
			}
		}
		rows = append(rows, kmeansRow{rec: r, x: x, mass: mass})
	}
	points := canonicalPoints(rows)
	groups := map[string][]*Record{}
	if len(points) == 0 {
		g.fit = &kmeansFit{k: g.cfg.K}
		g.order = &includeFilter{pos: map[string]int{}}
		g.counts = map[string]int{}
		return groups, nil
	}
	if g.cfg.K > len(points) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"GROUP_KMEANS params.k = "+strconv.Itoa(g.cfg.K)+" exceeds the "+strconv.Itoa(len(points))+
				" distinct member tuples the clustered rows carry; lower k",
			map[string]any{"group": g.key, "reason": "k_exceeds_distinct_points", "k": g.cfg.K, "distinct_points": len(points)})
	}
	fit := kmeansRun(points, g.cfg)
	g.fit = fit

	// A mass-carrying row joins its point's fitted cluster; a row with
	// no mass (an invalid or zero weight) joins the nearest centroid in
	// the clustering space.
	labels := make([]string, fit.k)
	g.order = &includeFilter{pos: make(map[string]int, fit.k)}
	for c := range fit.k {
		labels[c] = kmeansLabelPrefix + strconv.Itoa(c+1)
		g.order.pos[labels[c]] = c
	}
	g.counts = make(map[string]int, fit.k)
	z := make([]float64, p)
	for _, row := range rows {
		c := -1
		if row.mass > 0 {
			if i, found := slices.BinarySearchFunc(fit.points, row.x, func(pt kmeansPoint, x []float64) int {
				return compareTuple(pt.x, x)
			}); found {
				c = fit.pointLabel[i]
			}
		}
		if c < 0 {
			for j := range p {
				z[j] = (row.x[j] - fit.center[j]) / fit.scale[j]
			}
			c = nearestCentroid(z, fit.centroids)
		}
		groups[labels[c]] = append(groups[labels[c]], row.rec)
		g.counts[labels[c]]++
	}
	return groups, nil
}

// emissionOrder implements emissionOrderer: clusters emit in label
// (size) order, never alphabetically (cluster_10 after cluster_9).
func (g *kmeansGrouper) emissionOrder() *includeFilter { return g.order }

// warnings returns the grouper's Response-level warnings: the best
// start stopping at max_iter is PULSE_MATRIX_NOT_CONVERGED (solver
// kmeans_lloyd); its labels are the last iterate's.
func (g *kmeansGrouper) warnings() []*types.ResponseWarning {
	if g.fit == nil || g.fit.converged || len(g.fit.centroids) == 0 {
		return nil
	}
	return []*types.ResponseWarning{{
		Code: string(errors.PULSE_MATRIX_NOT_CONVERGED),
		Message: "group " + g.key + ": GROUP_KMEANS's best start did not converge within " + strconv.Itoa(g.cfg.MaxIter) +
			" Lloyd iterations; its clusters are the last iterate, not a fixed point (raise params.max_iter)",
		Details: map[string]any{
			"group":          g.key,
			"solver":         kmeansSolver,
			"iterations":     g.fit.iterations,
			"max_iterations": g.cfg.MaxIter,
			"tolerance":      0.0,
		},
	}}
}

// Components implements MetaGrouper (mergeability none: the fit needs
// every row).
func (g *kmeansGrouper) Components() (map[string]any, error) {
	if !g.finalize || g.fit == nil {
		return nil, nil
	}
	f := g.fit
	buckets := make([]map[string]any, 0, len(f.centroids))
	var within float64
	for c := range f.centroids {
		key := kmeansLabelPrefix + strconv.Itoa(c+1)
		buckets = append(buckets, map[string]any{
			"key":                   key,
			"count":                 g.counts[key],
			"size":                  f.mass[c],
			"within_ss":             f.withinSS[c],
			"centroid":              f.original[c],
			"centroid_standardized": f.centroids[c],
		})
		within += f.withinSS[c]
	}
	between := f.totalSS - within
	ratio := math.NaN()
	if f.totalSS > 0 {
		ratio = between / f.totalSS
	}
	members := append([]string(nil), g.members...)
	return map[string]any{
		"k":                g.cfg.K,
		"seed":             g.cfg.Seed,
		"n_init":           g.cfg.NInit,
		"max_iter":         g.cfg.MaxIter,
		"standardize":      g.cfg.Standardize,
		"members":          members,
		"center":           nonNilFloats(f.center),
		"scale":            nonNilFloats(f.scale),
		"distinct_points":  f.distinct,
		"buckets":          buckets,
		"total_ss":         f.totalSS,
		"within_ss":        within,
		"between_ss":       between,
		"between_ss_ratio": ratio,
		"iterations":       f.iterations,
		"converged":        f.converged,
		"best_init":        f.bestInit,
	}, nil
}

func nonNilFloats(v []float64) []float64 {
	if v == nil {
		return []float64{}
	}
	return v
}

// canonicalPoints sorts the mass-carrying rows' tuples
// lexicographically and merges equal tuples into one distinct point.
func canonicalPoints(rows []kmeansRow) []kmeansPoint {
	idx := make([]int, 0, len(rows))
	for i, r := range rows {
		if r.mass > 0 {
			idx = append(idx, i)
		}
	}
	slices.SortFunc(idx, func(a, b int) int { return compareTuple(rows[a].x, rows[b].x) })
	var out []kmeansPoint
	for _, i := range idx {
		r := rows[i]
		if n := len(out); n > 0 && compareTuple(out[n-1].x, r.x) == 0 {
			out[n-1].mass += r.mass
			out[n-1].rows++
			continue
		}
		out = append(out, kmeansPoint{x: r.x, mass: r.mass, rows: 1})
	}
	return out
}

func compareTuple(a, b []float64) int {
	for j := range a {
		if c := cmp.Compare(a[j], b[j]); c != 0 {
			return c
		}
	}
	return 0
}

// kmeansRun standardizes the canonical points, runs cfg.NInit seeded
// starts and returns the best fit with its clusters relabelled by size.
func kmeansRun(points []kmeansPoint, cfg vectors.KMeansConfig) *kmeansFit {
	p := len(points[0].x)
	n := len(points)
	center := make([]float64, p)
	scale := make([]float64, p)
	var mass float64
	for _, pt := range points {
		mass += pt.mass
	}
	for j := range p {
		scale[j] = 1
	}
	if cfg.Standardize {
		for j := range p {
			var s float64
			for _, pt := range points {
				s += float64(pt.mass * pt.x[j])
			}
			center[j] = s / mass
			var ss float64
			for _, pt := range points {
				d := pt.x[j] - center[j]
				ss += float64(pt.mass * float64(d*d))
			}
			if mass > 1 {
				if sd := math.Sqrt(ss / (mass - 1)); sd > 0 && !math.IsInf(sd, 0) && !math.IsNaN(sd) {
					scale[j] = sd
				}
			}
		}
	}
	for i := range points {
		z := make([]float64, p)
		for j := range p {
			z[j] = (points[i].x[j] - center[j]) / scale[j]
		}
		points[i].z = z
	}

	var best *kmeansStart
	for s := range cfg.NInit {
		st := kmeansStartRun(points, cfg, s)
		if best == nil || st.withinTotal < best.withinTotal {
			best = st
		}
	}

	// Grand mean (clustering space) and the total sum of squares.
	grand := weightedMean(points, nil, -1, p)
	var totalSS float64
	for _, pt := range points {
		totalSS += float64(pt.mass * sqDist(pt.z, grand))
	}

	// Relabel: descending mass, ties by the lexicographic centroid.
	k := cfg.K
	order := make([]int, k)
	for c := range k {
		order[c] = c
	}
	slices.SortStableFunc(order, func(a, b int) int {
		if c := cmp.Compare(best.mass[b], best.mass[a]); c != 0 {
			return c
		}
		return compareTuple(best.centroids[a], best.centroids[b])
	})
	fit := &kmeansFit{
		k: k, totalSS: totalSS, iterations: best.iterations, converged: best.converged,
		bestInit: best.init + 1, center: center, scale: scale, distinct: n,
		points: points, pointLabel: make([]int, n),
	}
	labelOf := make([]int, k)
	for label, c := range order {
		labelOf[c] = label
	}
	for i, c := range best.assign {
		fit.pointLabel[i] = labelOf[c]
	}
	for _, c := range order {
		cz := best.centroids[c]
		orig := make([]float64, p)
		for j := range p {
			orig[j] = float64(cz[j]*scale[j]) + center[j]
		}
		fit.centroids = append(fit.centroids, cz)
		fit.original = append(fit.original, orig)
		fit.mass = append(fit.mass, best.mass[c])
		fit.withinSS = append(fit.withinSS, best.within[c])
	}
	return fit
}

// kmeansStart is one seeded start's converged (or capped) state.
type kmeansStart struct {
	init        int
	centroids   [][]float64
	assign      []int
	mass        []float64
	within      []float64
	withinTotal float64
	iterations  int
	converged   bool
}

// kmeansStartRun runs start s: k-means++ seeding then Lloyd.
func kmeansStartRun(points []kmeansPoint, cfg vectors.KMeansConfig, s int) *kmeansStart {
	k, n := cfg.K, len(points)
	rng := rand.New(rand.NewPCG(uint64(cfg.Seed), uint64(s)))
	centroids := make([][]float64, 0, k)

	// First centre: drawn by mass.
	draw := func(weights []float64) int {
		var total float64
		for _, w := range weights {
			total += w
		}
		u := rng.Float64() * total
		var cum float64
		last := -1
		for i, w := range weights {
			if w <= 0 {
				continue
			}
			cum += w
			last = i
			if u < cum {
				return i
			}
		}
		return last
	}
	w := make([]float64, n)
	for i, pt := range points {
		w[i] = pt.mass
	}
	first := max(draw(w), 0)
	centroids = append(centroids, slices.Clone(points[first].z))
	d2 := make([]float64, n)
	for i, pt := range points {
		d2[i] = sqDist(pt.z, centroids[0])
	}
	for len(centroids) < k {
		for i, pt := range points {
			w[i] = float64(pt.mass * d2[i])
		}
		next := draw(w)
		if next < 0 {
			// Every point already coincides with a centre in the
			// clustering space (distinct raw tuples rounded together):
			// the canonical first point stands in.
			next = 0
		}
		c := slices.Clone(points[next].z)
		centroids = append(centroids, c)
		for i, pt := range points {
			if d := sqDist(pt.z, c); d < d2[i] {
				d2[i] = d
			}
		}
	}

	assign := make([]int, n)
	for i := range assign {
		assign[i] = -1
	}
	st := &kmeansStart{init: s}
	p := len(points[0].z)
	for it := 1; it <= cfg.MaxIter; it++ {
		changed := false
		for i, pt := range points {
			c := nearestCentroid(pt.z, centroids)
			if c != assign[i] {
				assign[i] = c
				changed = true
			}
		}
		st.iterations = it
		if !changed {
			st.converged = true
			break
		}
		for c := range k {
			centroids[c] = weightedMean(points, assign, c, p)
		}
		relocateEmpty(points, assign, centroids)
	}
	st.centroids, st.assign = centroids, assign
	st.mass = make([]float64, k)
	st.within = make([]float64, k)
	for i, pt := range points {
		c := assign[i]
		st.mass[c] += pt.mass
		st.within[c] += float64(pt.mass * sqDist(pt.z, centroids[c]))
	}
	for c := range k {
		st.withinTotal += st.within[c]
	}
	return st
}

// relocateEmpty re-seeds every cluster that lost all its points at the
// point farthest from its own centroid (ties to the lowest point
// index), taking that point into the cluster, so k clusters survive.
func relocateEmpty(points []kmeansPoint, assign []int, centroids [][]float64) {
	k := len(centroids)
	sizes := make([]int, k)
	for _, c := range assign {
		sizes[c]++
	}
	for c := range k {
		if sizes[c] > 0 {
			continue
		}
		far, farD := -1, -1.0
		for i, pt := range points {
			if sizes[assign[i]] <= 1 {
				continue
			}
			if d := sqDist(pt.z, centroids[assign[i]]); d > farD {
				far, farD = i, d
			}
		}
		if far < 0 {
			continue
		}
		sizes[assign[far]]--
		assign[far] = c
		sizes[c] = 1
		centroids[c] = slices.Clone(points[far].z)
	}
}

// weightedMean is the mass-weighted mean of the points assigned to
// cluster c (every point when assign is nil), in canonical order.
func weightedMean(points []kmeansPoint, assign []int, c, p int) []float64 {
	out := make([]float64, p)
	var mass float64
	for i, pt := range points {
		if assign != nil && assign[i] != c {
			continue
		}
		mass += pt.mass
		for j := range p {
			out[j] += float64(pt.mass * pt.z[j])
		}
	}
	if mass > 0 {
		for j := range p {
			out[j] /= mass
		}
	}
	return out
}

// nearestCentroid is the index of the centroid nearest z (squared
// Euclidean), ties to the lowest index.
func nearestCentroid(z []float64, centroids [][]float64) int {
	best, bestD := 0, math.Inf(1)
	for c, cz := range centroids {
		if d := sqDist(z, cz); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

// kmeansMetric is the registry's sq_euclidean, the one metric k-means
// minimises (consumer GROUP_KMEANS accepts the Euclidean family only).
var kmeansMetric = func() metric.Metric {
	m, err := metric.ResolveFor(metric.ConsumerKMeans, "sq_euclidean", metric.Distance)
	if err != nil {
		panic("GROUP_KMEANS metric: " + err.Error())
	}
	return m
}()

// sqDist is the squared Euclidean distance through the linalg/metric
// registry: FMA-free, summed left to right over the members. The
// canonical points and centroids are finite and complete, so the
// registry's pairwise-complete rescaling never fires and the bits are
// exactly the plain sum's (TestKMeans_SqDistIsRegistrySqEuclidean).
func sqDist(a, b []float64) float64 {
	v, _ := kmeansMetric.Eval(metric.Distance, a, b, nil)
	return v
}

var (
	_ MetaGrouper     = (*kmeansGrouper)(nil)
	_ emissionOrderer = (*kmeansGrouper)(nil)
	_ grouperWarner   = (*kmeansGrouper)(nil)
)

// resolveGroupInputs returns req with every vector-input grouper slot
// (groups and both crosstab axes; vectors.ResolveGroups) rewritten for
// its factory: Fields = the resolved literal members, Vector cleared,
// and — on a built-in — Field = its key column (vectors.GroupKeyField,
// computed from the slot as written; an extension grouper keeps its
// Field), so every downstream site that names a grouper's
// column — the Data row key, a crosstab axis header, an overlay's host
// coordinate, a Sort key — reads the key column. req itself is
// returned when no grouper reads a vector, when every such slot is
// already rewritten (Field set; prepare is idempotent) or when
// resolution fails (the field-reference pass has refused that
// request). The caller's request is never mutated.
func (p *Processor) resolveGroupInputs(req *types.Request) *types.Request {
	has := false
	for _, s := range vectors.GroupSlots(req) {
		// A built-in slot is rewritten once (its key Field set); an
		// extension slot keeps its Field, and re-resolving its literal
		// members is a no-op.
		if vectors.HasGroupVectorInput(s.Group) && (s.Group.Field == "" || !vectors.IsVectorGroup(s.Group.Type)) {
			has = true
			break
		}
	}
	if !has {
		return req
	}
	inputs, err := vectors.ResolveGroups(req, p.schema, func(t types.GroupType) types.GroupType {
		if p.exts.isHidden(string(t)) {
			return ""
		}
		return t
	}, func(name string) bool {
		_, ok := p.exts.LookupGrouper(types.GroupType(name))
		return ok
	})
	if err != nil {
		return req
	}
	rewrite := func(gs []*types.Group) []*types.Group {
		if len(gs) == 0 {
			return gs
		}
		out := make([]*types.Group, len(gs))
		for i, g := range gs {
			out[i] = g
			in, ok := inputs[g]
			if !ok {
				continue
			}
			cp := *g
			if !in.Extension {
				cp.Field = vectors.GroupKeyField(types.GROUP_KMEANS, g)
			}
			cp.Vector = ""
			cp.Fields = append([]string(nil), in.Members.Members...)
			out[i] = &cp
		}
		return out
	}
	c := *req
	c.Groups = rewrite(req.Groups)
	if req.Crosstab != nil {
		ct := *req.Crosstab
		ct.Rows = rewrite(req.Crosstab.Rows)
		ct.Columns = rewrite(req.Crosstab.Columns)
		c.Crosstab = &ct
	}
	return &c
}

// wholeSetAxis is one buffered-crosstab axis position whose grouper
// partitions the WHOLE filtered set at once (a data-dependent grouper —
// GROUP_QUANTILE, GROUP_KMEANS — under params.fit "global", the
// default; internal/groupfit): the fitted assignment, its emission
// order and its components, computed once per RunCrosstab so a nested
// position or the per-row-bucket column partition routes records by it
// instead of refitting a subset — the plain-groups rule
// (partitionComposite). Under "within" the position is still fitted
// whole-set — its Components and unstamped warnings, as on plain
// groups — but route is false and PartitionByAxis refits it on each
// outer bucket's records; the outermost composite position (rows[0],
// or columns[0] when there are no rows) has no outer segment, so its
// "within" routes by the whole-set fit exactly as "global" does.
type wholeSetAxis struct {
	// route reports whether PartitionByAxis routes records by keyOf
	// (false: a "within" position refitted per outer bucket).
	route      bool
	keyOf      map[*Record]string
	order      *includeFilter
	components map[string]any
}

// partition routes records by the fitted assignment; a record the fit
// skipped (a null member) joins no bucket.
func (ws *wholeSetAxis) partition(records []*Record) map[string][]*Record {
	out := map[string][]*Record{}
	for _, r := range records {
		if k, ok := ws.keyOf[r]; ok {
			out[k] = append(out[k], r)
		}
	}
	return out
}

// fitWholeSetAxes fits every whole-set axis grouper of spec (both axes,
// every position) over filtered and stores it on the processor for the
// run; it returns the fits' warnings in axis order.
func (p *Processor) fitWholeSetAxes(spec *types.CrosstabSpec, filtered []*Record) ([]*types.ResponseWarning, error) {
	p.wholeSetAxes = nil
	var warns []*types.ResponseWarning
	outermost := true
	for _, axis := range [][]*types.Group{spec.Rows, spec.Columns} {
		for _, grp := range axis {
			first := outermost
			outermost = false
			if grp == nil || !groupfit.Applies(grp.Type) || p.exts.isHidden(string(grp.Type)) {
				continue
			}
			factory, ok := p.exts.LookupGrouper(grp.Type)
			if !ok {
				continue
			}
			grouper, err := factory(grp, p.schema)
			if err != nil {
				return nil, err
			}
			buckets, err := grouper.Group(filtered, grp.Field)
			if err != nil {
				return nil, err
			}
			ws := &wholeSetAxis{route: first || !groupfit.IsWithin(grp), keyOf: make(map[*Record]string, len(filtered))}
			for k, recs := range buckets {
				for _, r := range recs {
					ws.keyOf[r] = k
				}
			}
			if eo, ok := grouper.(emissionOrderer); ok {
				ws.order = eo.emissionOrder()
			}
			if meta, ok := grouper.(MetaGrouper); ok {
				if ws.components, err = meta.Components(); err != nil {
					return nil, err
				}
			}
			warns = append(warns, GrouperWarnings(grouper)...)
			if p.wholeSetAxes == nil {
				p.wholeSetAxes = map[*types.Group]*wholeSetAxis{}
			}
			p.wholeSetAxes[grp] = ws
		}
	}
	return warns, nil
}
